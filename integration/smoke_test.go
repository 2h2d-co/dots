//go:build integration

// Package integration contains end-to-end tests for dots.
package integration

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	_ "modernc.org/sqlite" // Register SQLite driver for integration test database fixtures.
)

var dotsBin string

func TestMain(m *testing.M) {
	tempDir, err := os.MkdirTemp("", "dots-integration-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp dir: %v\n", err)
		os.Exit(1)
	}

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve repo root: %v\n", err)
		os.Exit(1)
	}
	dotsBin = filepath.Join(tempDir, "dots")
	if runtime.GOOS == "windows" {
		dotsBin += ".exe"
	}

	cmd := exec.Command("go", "build", "-o", dotsBin, ".")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+repoRoot)
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build dots: %v\n%s\n", err, output)
		os.Exit(1)
	}

	code := m.Run()
	if err := os.RemoveAll(tempDir); err != nil {
		fmt.Fprintf(os.Stderr, "remove temp dir: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

type testEnv struct {
	t      *testing.T
	home   string
	config string
	state  string
	repo   string
}

type runResult struct {
	stdout string
	stderr string
	code   int
}

func newTestEnv(t *testing.T) testEnv {
	t.Helper()
	root := t.TempDir()
	env := testEnv{
		t:      t,
		home:   filepath.Join(root, "home"),
		config: filepath.Join(root, "config"),
		state:  filepath.Join(root, "state"),
		repo:   filepath.Join(root, "repo"),
	}
	for _, dir := range []string{env.home, env.config, env.state, env.repo} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	return env
}

func (e testEnv) run(args ...string) runResult {
	e.t.Helper()
	return e.runWithEnv(nil, args...)
}

func (e testEnv) runInDir(dir string, args ...string) runResult {
	e.t.Helper()
	return e.runWithOptions(nil, dir, args...)
}

func (e testEnv) runWithEnv(extraEnv map[string]string, args ...string) runResult {
	e.t.Helper()
	return e.runWithOptions(extraEnv, "", args...)
}

func (e testEnv) runWithOptions(extraEnv map[string]string, dir string, args ...string) runResult {
	e.t.Helper()
	cmd := exec.Command(dotsBin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"HOME="+e.home,
		"XDG_CONFIG_HOME="+e.config,
		"XDG_STATE_HOME="+e.state,
	)
	for key, value := range extraEnv {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			e.t.Fatalf("run dots %v: %v", args, err)
		}
	}
	return runResult{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

func (e testEnv) requireRun(args ...string) runResult {
	e.t.Helper()
	result := e.run(args...)
	if result.code != 0 {
		e.t.Fatalf("dots %v exited %d\nstdout:\n%s\nstderr:\n%s", args, result.code, result.stdout, result.stderr)
	}
	return result
}

func TestDotsHelp(t *testing.T) {
	env := newTestEnv(t)
	result := env.requireRun("--help")
	assertContains(t, result.stdout, "minimal copy-based dotfiles manager")
	assertContains(t, result.stdout, "--profile")
}

func TestConfigOverride(t *testing.T) {
	env := newTestEnv(t)
	configA := filepath.Join(env.config, "custom-a.toml")
	configB := filepath.Join(env.config, "custom-b.toml")
	repoA := filepath.Join(filepath.Dir(env.repo), "repo-a")
	repoB := filepath.Join(filepath.Dir(env.repo), "repo-b")
	writeFile(t, filepath.Join(env.home, ".arc"), "a\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".brc"), "b\n", 0o644)

	env.requireRun("--config", configA, "init", repoA, "--profile", "a")
	env.requireRun("--config", configB, "init", repoB, "--profile", "b")
	assertFileMissing(t, filepath.Join(env.config, "dots", "config.toml"))

	result := env.runWithEnv(map[string]string{"DOTS_CONFIG": configA}, "add", filepath.Join(env.home, ".arc"))
	assertExitCode(t, result, 0)
	result = env.runWithEnv(map[string]string{"DOTS_CONFIG": configA}, "--config", configB, "add", filepath.Join(env.home, ".brc"))
	assertExitCode(t, result, 0)

	result = env.runWithEnv(map[string]string{"DOTS_CONFIG": configA}, "list")
	assertExitCode(t, result, 0)
	assertContains(t, result.stdout, ".arc")
	assertNotContains(t, result.stdout, ".brc")

	result = env.runWithEnv(map[string]string{"DOTS_CONFIG": configA}, "--config", configB, "list")
	assertExitCode(t, result, 0)
	assertContains(t, result.stdout, ".brc")
	assertNotContains(t, result.stdout, ".arc")
}

func TestMultipleProfilesInSingleConfig(t *testing.T) {
	env := newTestEnv(t)
	repoA := filepath.Join(filepath.Dir(env.repo), "repo-a")
	repoB := filepath.Join(filepath.Dir(env.repo), "repo-b")
	writeFile(t, filepath.Join(env.home, ".personalrc"), "personal\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".laptoprc"), "laptop\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".workrc"), "work\n", 0o644)

	env.requireRun("init", repoA, "--profile", "personal")
	env.requireRun("init", repoA, "--profile", "laptop")
	env.requireRun("init", repoB, "--profile", "work")

	configPath := filepath.Join(env.config, "dots", "config.toml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	config := string(content)
	assertContains(t, config, "default_profile = \"personal\"")
	assertContains(t, config, "[profiles]")
	assertContains(t, config, "personal = ")
	assertContains(t, config, "laptop = ")
	assertContains(t, config, "work = ")
	assertFileExists(t, filepath.Join(repoA, "personal.db"))
	assertFileExists(t, filepath.Join(repoA, "laptop.db"))
	assertFileExists(t, filepath.Join(repoB, "work.db"))
	assertFileExists(t, filepath.Join(env.state, "dots", "personal.db"))
	assertFileExists(t, filepath.Join(env.state, "dots", "laptop.db"))
	assertFileExists(t, filepath.Join(env.state, "dots", "work.db"))

	env.requireRun("add", filepath.Join(env.home, ".personalrc"))
	env.requireRun("--profile", "laptop", "add", filepath.Join(env.home, ".laptoprc"))
	result := env.runWithEnv(map[string]string{"DOTS_PROFILE": "work"}, "add", filepath.Join(env.home, ".workrc"))
	assertExitCode(t, result, 0)

	result = env.requireRun("list")
	assertContains(t, result.stdout, ".personalrc")
	assertNotContains(t, result.stdout, ".laptoprc")
	assertNotContains(t, result.stdout, ".workrc")

	result = env.requireRun("--profile", "laptop", "list")
	assertContains(t, result.stdout, ".laptoprc")
	assertNotContains(t, result.stdout, ".personalrc")
	assertNotContains(t, result.stdout, ".workrc")

	result = env.runWithEnv(map[string]string{"DOTS_PROFILE": "work"}, "list")
	assertExitCode(t, result, 0)
	assertContains(t, result.stdout, ".workrc")
	assertNotContains(t, result.stdout, ".personalrc")
	assertNotContains(t, result.stdout, ".laptoprc")

	result = env.run("--profile", "missing", "list")
	assertExitCode(t, result, 1)
	assertContains(t, result.stderr, "profile \"missing\" is not configured")

	writeFile(t, filepath.Join(repoB, "README.md"), "repo\n", 0o644)
	result = env.run("add", filepath.Join(repoB, "README.md"))
	assertExitCode(t, result, 1)
	assertContains(t, result.stderr, "refusing to add paths from the dots repo")

	result = env.requireRun("doctor")
	assertContains(t, result.stdout, "Doctor: checking 3 profile(s)")
	assertContains(t, result.stdout, "Profile: personal")
	assertContains(t, result.stdout, "Profile: laptop")
	assertContains(t, result.stdout, "Profile: work")
	assertContains(t, result.stdout, "Doctor: all checked profiles are clean")

	result = env.requireRun("--profile", "work", "doctor")
	assertContains(t, result.stdout, "Doctor: checking 1 profile(s)")
	assertContains(t, result.stdout, "Profile: work")
	assertNotContains(t, result.stdout, "Profile: personal")
	assertNotContains(t, result.stdout, "Profile: laptop")
}

func TestDatabaseMigrationsFromScratchAndAlreadyCurrent(t *testing.T) {
	env := newTestEnv(t)
	env.requireRun("init", env.repo, "--profile", "personal")

	repoDB := filepath.Join(env.repo, "personal.db")
	stateDB := filepath.Join(env.state, "dots", "personal.db")
	assertSQLiteMigrationVersion(t, repoDB, 2)
	assertSQLiteMigrationVersion(t, stateDB, 1)

	env.requireRun("status")
	assertSQLiteMigrationVersion(t, repoDB, 2)
	assertSQLiteMigrationVersion(t, stateDB, 1)
}

func TestDatabaseMigrationsFromLegacyRepoV1(t *testing.T) {
	env := newTestEnv(t)
	writePersonalConfig(t, env)
	if err := os.MkdirAll(filepath.Join(env.repo, "personal"), 0o750); err != nil {
		t.Fatalf("create profile directory: %v", err)
	}
	createLegacyPersonalRepoDB(t, filepath.Join(env.repo, "personal.db"), 1, nil)
	createLegacyPersonalStateDB(t, filepath.Join(env.state, "dots", "personal.db"))

	env.requireRun("status")
	assertSQLiteMigrationVersion(t, filepath.Join(env.repo, "personal.db"), 2)
	assertSQLiteMigrationVersion(t, filepath.Join(env.state, "dots", "personal.db"), 1)
}

func TestDatabaseMigrationsStampLegacyRepoV2WithoutGooseTracking(t *testing.T) {
	env := newTestEnv(t)
	writePersonalConfig(t, env)
	if err := os.MkdirAll(filepath.Join(env.repo, "personal"), 0o750); err != nil {
		t.Fatalf("create profile directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(env.home, ".config", "legacy"), 0o750); err != nil {
		t.Fatalf("create tracked destination directory: %v", err)
	}

	repoDB := filepath.Join(env.repo, "personal.db")
	stateDB := filepath.Join(env.state, "dots", "personal.db")
	createLegacyPersonalRepoDB(t, repoDB, 2, []string{".config/legacy"})
	createLegacyPersonalStateDB(t, stateDB)

	env.requireRun("status")
	assertSQLiteMigrationVersion(t, repoDB, 2)
	assertSQLiteMigrationVersion(t, stateDB, 1)
	withSQLiteDatabase(t, repoDB, func(db *sql.DB) {
		var trackedDir string
		if err := db.QueryRow(`SELECT path FROM tracked_dirs WHERE path = '.config/legacy'`).Scan(&trackedDir); err != nil {
			t.Fatalf("read tracked directory: %v", err)
		}
		if trackedDir != ".config/legacy" {
			t.Fatalf("tracked directory = %q, want .config/legacy", trackedDir)
		}
	})
}

func TestInitRejectsUnsafeMultiProfileConfig(t *testing.T) {
	t.Run("existing profile", func(t *testing.T) {
		env := newTestEnv(t)
		repoB := filepath.Join(filepath.Dir(env.repo), "repo-b")
		env.requireRun("init", env.repo, "--profile", "personal")

		result := env.run("init", repoB, "--profile", "personal")
		assertExitCode(t, result, 1)
		assertContains(t, result.stderr, "profile \"personal\" is already configured")
	})

	t.Run("repo database exists", func(t *testing.T) {
		env := newTestEnv(t)
		repoB := filepath.Join(filepath.Dir(env.repo), "repo-b")
		env.requireRun("init", env.repo, "--profile", "personal")
		writeFile(t, filepath.Join(repoB, "work.db"), "existing\n", 0o600)

		result := env.run("init", repoB, "--profile", "work")
		assertExitCode(t, result, 1)
		assertContains(t, result.stderr, "repo database already exists")
	})

	t.Run("state database exists", func(t *testing.T) {
		env := newTestEnv(t)
		repoB := filepath.Join(filepath.Dir(env.repo), "repo-b")
		env.requireRun("init", env.repo, "--profile", "personal")
		writeFile(t, filepath.Join(env.state, "dots", "work.db"), "existing\n", 0o600)

		result := env.run("init", repoB, "--profile", "work")
		assertExitCode(t, result, 1)
		assertContains(t, result.stderr, "state database already exists")
	})

	t.Run("repo roots overlap", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			existing string
			newRepo  string
		}{
			{name: "new inside existing", existing: filepath.Join("repos", "dotfiles"), newRepo: filepath.Join("repos", "dotfiles", "nested")},
			{name: "existing inside new", existing: filepath.Join("repos", "dotfiles", "nested"), newRepo: filepath.Join("repos", "dotfiles")},
		} {
			t.Run(tc.name, func(t *testing.T) {
				env := newTestEnv(t)
				root := filepath.Dir(env.repo)
				existing := filepath.Join(root, tc.existing)
				newRepo := filepath.Join(root, tc.newRepo)
				env.requireRun("init", existing, "--profile", "personal")

				result := env.run("init", newRepo, "--profile", "work")
				assertExitCode(t, result, 1)
				assertContains(t, result.stderr, "overlaps configured repo")
			})
		}
	})

	t.Run("new repo is tracked home content", func(t *testing.T) {
		env := newTestEnv(t)
		newRepo := filepath.Join(env.home, "dotfiles-b")
		writeFile(t, filepath.Join(newRepo, "tracked.txt"), "tracked\n", 0o644)
		env.requireRun("init", env.repo, "--profile", "personal")
		env.requireRun("add", filepath.Join(newRepo, "tracked.txt"))

		result := env.run("init", newRepo, "--profile", "work")
		assertExitCode(t, result, 1)
		assertContains(t, result.stderr, "already tracked by profile \"personal\"")
		assertFileMissing(t, filepath.Join(env.state, "dots", "work.db"))
	})

	t.Run("new repo is inside tracked directory root", func(t *testing.T) {
		env := newTestEnv(t)
		trackedRoot := filepath.Join(env.home, ".config", "managed")
		newRepo := filepath.Join(trackedRoot, "dotfiles")
		writeFile(t, filepath.Join(trackedRoot, "existing"), "tracked\n", 0o644)
		env.requireRun("init", env.repo, "--profile", "personal")
		env.requireRun("add", trackedRoot)

		result := env.run("init", newRepo, "--profile", "work")
		assertExitCode(t, result, 1)
		assertContains(t, result.stderr, "already tracked by profile \"personal\"")
		assertFileMissing(t, filepath.Join(env.state, "dots", "work.db"))
	})
}

func TestDotsIgnoreFiltersNestedPaths(t *testing.T) {
	env := newTestEnv(t)
	root := filepath.Join(env.home, ".config", "nestedapp")
	writeFile(t, filepath.Join(root, ".dotsignore"), "nested/ignored.txt\ncache/\n*.tmp\n", 0o644)
	writeFile(t, filepath.Join(root, "keep.txt"), "keep\n", 0o644)
	writeFile(t, filepath.Join(root, "nested", "keep.txt"), "nested keep\n", 0o644)
	writeFile(t, filepath.Join(root, "nested", "ignored.txt"), "ignored\n", 0o644)
	writeFile(t, filepath.Join(root, "nested", "deeper", "ignored.tmp"), "ignored tmp\n", 0o644)
	writeFile(t, filepath.Join(root, "cache", "ignored.txt"), "cache\n", 0o644)
	writeFile(t, filepath.Join(root, "nested", "cache", "ignored.txt"), "nested cache\n", 0o644)

	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", root)

	repoRoot := filepath.Join(env.repo, "personal", ".config", "nestedapp")
	assertFileExists(t, filepath.Join(repoRoot, ".dotsignore"))
	assertFileExists(t, filepath.Join(repoRoot, "keep.txt"))
	assertFileExists(t, filepath.Join(repoRoot, "nested", "keep.txt"))
	assertFileMissing(t, filepath.Join(repoRoot, "nested", "ignored.txt"))
	assertFileMissing(t, filepath.Join(repoRoot, "nested", "deeper", "ignored.tmp"))
	assertFileMissing(t, filepath.Join(repoRoot, "cache", "ignored.txt"))
	assertFileMissing(t, filepath.Join(repoRoot, "nested", "cache", "ignored.txt"))

	result := env.requireRun("list")
	assertContains(t, result.stdout, ".config/nestedapp/.dotsignore")
	assertContains(t, result.stdout, ".config/nestedapp/keep.txt")
	assertContains(t, result.stdout, ".config/nestedapp/nested/keep.txt")
	assertNotContains(t, result.stdout, ".config/nestedapp/nested/ignored.txt")
	assertNotContains(t, result.stdout, ".config/nestedapp/nested/deeper/ignored.tmp")
	assertNotContains(t, result.stdout, ".config/nestedapp/cache/ignored.txt")
	assertNotContains(t, result.stdout, ".config/nestedapp/nested/cache/ignored.txt")
}

func TestAddStatusGroupsIndividualPathTrackedRootAndNestedRoot(t *testing.T) {
	env := newTestEnv(t)
	individual := filepath.Join(env.home, ".zshrc")
	root := filepath.Join(env.home, ".config", "trackedapp")
	nestedRoot := filepath.Join(root, "nested")
	writeFile(t, individual, "shell\n", 0o644)
	writeFile(t, filepath.Join(root, ".dotsignore"), "*.tmp\n", 0o644)
	writeFile(t, filepath.Join(root, "initial"), "initial\n", 0o644)
	writeFile(t, filepath.Join(nestedRoot, ".dotsignore"), "ignored-by-nested\n", 0o644)
	writeFile(t, filepath.Join(nestedRoot, "initial"), "nested initial\n", 0o644)

	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", individual)
	env.requireRun("add", root)
	env.requireRun("add", nestedRoot)
	env.requireRun("apply")

	if err := os.Remove(individual); err != nil {
		t.Fatalf("remove individual destination: %v", err)
	}
	rootNew := filepath.Join(root, "new")
	nestedNew := filepath.Join(nestedRoot, "new")
	writeFile(t, rootNew, "new\n", 0o644)
	writeFile(t, filepath.Join(root, "ignored.tmp"), "ignored\n", 0o644)
	writeFile(t, nestedNew, "nested new\n", 0o644)
	writeFile(t, filepath.Join(nestedRoot, "ignored-by-nested"), "nested ignored\n", 0o644)

	result := env.run("status")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "Tracked root: .config/trackedapp")
	assertContains(t, result.stdout, "untracked destination file: .config/trackedapp/new")
	assertContains(t, result.stdout, "Tracked root: .config/trackedapp/nested")
	assertContains(t, result.stdout, "untracked destination file: .config/trackedapp/nested/new")
	assertContains(t, result.stdout, "Individual paths:")
	assertContains(t, result.stdout, "will create: .zshrc")
	assertNotContains(t, result.stdout, ".config/trackedapp/ignored.tmp")
	assertNotContains(t, result.stdout, ".config/trackedapp/nested/ignored-by-nested")
	if count := strings.Count(result.stdout, ".config/trackedapp/nested/new"); count != 1 {
		t.Fatalf("nested root path appeared %d times, want 1\noutput:\n%s", count, result.stdout)
	}

	env.requireRun("add", rootNew)
	env.requireRun("add", nestedNew)
	result = env.run("status")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "Individual paths:")
	assertContains(t, result.stdout, "will create: .zshrc")
	assertNotContains(t, result.stdout, ".config/trackedapp/new")
	assertNotContains(t, result.stdout, ".config/trackedapp/nested/new")
	assertNotContains(t, result.stdout, "Directory drift:")
	assertNotContains(t, result.stdout, "will adopt existing match")
	env.requireRun("apply")
	result = env.requireRun("status")
	assertContains(t, result.stdout, "Clean: no changes")
}

func TestAddDefaultsToCurrentDirectory(t *testing.T) {
	env := newTestEnv(t)
	cwdTarget := filepath.Join(env.home, ".config", "cwdapp")
	writeFile(t, filepath.Join(cwdTarget, "settings.toml"), "enabled = true\n", 0o644)
	writeFile(t, filepath.Join(cwdTarget, "nested", "tool"), "tool\n", 0o755)

	env.requireRun("init", env.repo, "--profile", "personal")
	result := env.runInDir(cwdTarget, "add")
	assertExitCode(t, result, 0)

	assertFileExists(t, filepath.Join(env.repo, "personal", ".config", "cwdapp", "settings.toml"))
	assertFileExists(t, filepath.Join(env.repo, "personal", ".config", "cwdapp", "nested", "tool"))
	result = env.requireRun("list")
	assertContains(t, result.stdout, ".config/cwdapp/settings.toml")
	assertContains(t, result.stdout, ".config/cwdapp/nested/tool")
}

func TestAddFailsBeforeCopyWhenRepoMigrationValidationFails(t *testing.T) {
	env := newTestEnv(t)
	env.requireRun("init", env.repo, "--profile", "personal")
	writeFile(t, filepath.Join(env.home, ".blockedrc"), "blocked\n", 0o644)
	setSQLiteProfileMetadata(t, filepath.Join(env.repo, "personal.db"), "work")

	result := env.run("add", filepath.Join(env.home, ".blockedrc"))
	assertExitCode(t, result, 1)
	assertContains(t, result.stderr, `database belongs to profile "work", not "personal"`)
	assertFileMissing(t, filepath.Join(env.repo, "personal", ".blockedrc"))
}

func TestAddDryRunDoesNotCopyOrTrack(t *testing.T) {
	env := newTestEnv(t)
	target := filepath.Join(env.home, ".config", "dryapp")
	writeFile(t, filepath.Join(target, ".dotsignore"), "ignored\n", 0o644)
	writeFile(t, filepath.Join(target, "keep"), "keep\n", 0o644)
	writeFile(t, filepath.Join(target, "ignored"), "ignored\n", 0o644)

	env.requireRun("init", env.repo, "--profile", "personal")
	result := env.requireRun("add", "--dry-run", target)
	assertContains(t, result.stdout, "Add plan (dry run; no files changed):")
	assertContains(t, result.stdout, "Directory roots:")
	assertContains(t, result.stdout, "  .config/dryapp")
	assertContains(t, result.stdout, "  .config/dryapp/.dotsignore")
	assertContains(t, result.stdout, "  .config/dryapp/keep")
	assertContains(t, result.stdout, "Would track 1 directory root(s)")
	assertContains(t, result.stdout, "Would add 2 file(s) to profile personal")
	assertNotContains(t, result.stdout, ".config/dryapp/ignored")
	assertFileMissing(t, filepath.Join(env.repo, "personal", ".config", "dryapp", "keep"))

	result = env.requireRun("list")
	if strings.TrimSpace(result.stdout) != "" {
		t.Fatalf("list output = %q, want no tracked files after add dry-run", result.stdout)
	}
	result = env.requireRun("status")
	assertContains(t, result.stdout, "Clean: no changes")
}

func TestAddRejectsConfiguredRepoPaths(t *testing.T) {
	env := newTestEnv(t)
	env.repo = filepath.Join(env.home, "dotfiles")
	env.requireRun("init", env.repo, "--profile", "personal")

	repoFile := filepath.Join(env.repo, "README.md")
	profileFile := filepath.Join(env.repo, "personal", ".zshrc")
	writeFile(t, repoFile, "repo\n", 0o644)
	writeFile(t, profileFile, "profile\n", 0o644)

	for _, target := range []string{env.repo, repoFile, filepath.Join(env.repo, "personal"), profileFile} {
		result := env.run("add", target)
		assertExitCode(t, result, 1)
		assertContains(t, result.stderr, "refusing to add paths from the dots repo")
	}

	result := env.runInDir(env.repo, "add")
	assertExitCode(t, result, 1)
	assertContains(t, result.stderr, "refusing to add paths from the dots repo")

	result = env.requireRun("list")
	if strings.TrimSpace(result.stdout) != "" {
		t.Fatalf("list output = %q, want no tracked files", result.stdout)
	}
	assertFileMissing(t, filepath.Join(env.repo, "personal", "dotfiles"))
}

func TestAddRejectsSymlinksAndUnsupportedFileTypes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink and FIFO behavior is only covered on Unix-like targets")
	}
	env := newTestEnv(t)
	env.requireRun("init", env.repo, "--profile", "personal")

	realPath := filepath.Join(env.home, ".realrc")
	linkPath := filepath.Join(env.home, ".linkrc")
	writeFile(t, realPath, "real\n", 0o644)
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	result := env.run("add", linkPath)
	assertExitCode(t, result, 1)
	assertContains(t, result.stderr, "refusing to track symlink")
	assertFileMissing(t, filepath.Join(env.repo, "personal", ".linkrc"))

	fifoPath := filepath.Join(env.home, ".fiforc")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatalf("create FIFO: %v", err)
	}
	result = env.run("add", fifoPath)
	assertExitCode(t, result, 1)
	assertContains(t, result.stderr, "unsupported file type")
	assertFileMissing(t, filepath.Join(env.repo, "personal", ".fiforc"))
}

func TestPathErrorsAndUnusualNames(t *testing.T) {
	env := newTestEnv(t)
	unusualPath := filepath.Join(env.home, ".config", "space dir", "ümlaut", "name #1.txt")
	writeFile(t, unusualPath, "unusual\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")

	result := env.run("add", unusualPath)
	assertExitCode(t, result, 0)
	result = env.requireRun("list")
	assertContains(t, result.stdout, ".config/space dir/ümlaut/name #1.txt")

	if err := os.Remove(unusualPath); err != nil {
		t.Fatalf("remove destination before apply: %v", err)
	}
	env.requireRun("apply")
	assertFileContent(t, unusualPath, "unusual\n")

	outsidePath := filepath.Join(filepath.Dir(env.home), "outside.txt")
	writeFile(t, outsidePath, "outside\n", 0o644)
	result = env.run("add", outsidePath)
	assertExitCode(t, result, 1)
	assertContains(t, result.stderr, "outside home directory")

	result = env.run("add", env.home)
	assertExitCode(t, result, 1)
	assertContains(t, result.stderr, "home directory itself")
}

func TestApplyPreflightReportsDestinationTypeConflict(t *testing.T) {
	env := newTestEnv(t)
	fileA := filepath.Join(env.home, ".config", "types", "a")
	fileB := filepath.Join(env.home, ".config", "types", "b")
	writeFile(t, fileA, "a old\n", 0o644)
	writeFile(t, fileB, "b old\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", filepath.Join(env.home, ".config", "types"))
	env.requireRun("apply")

	writeFile(t, filepath.Join(env.repo, "personal", ".config", "types", "a"), "a new\n", 0o644)
	writeFile(t, filepath.Join(env.repo, "personal", ".config", "types", "b"), "b new\n", 0o644)
	env.requireRun("reindex")
	if err := os.Remove(fileB); err != nil {
		t.Fatalf("remove destination file: %v", err)
	}
	if err := os.MkdirAll(fileB, 0o750); err != nil {
		t.Fatalf("create destination type conflict: %v", err)
	}

	result := env.run("apply")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "destination is not a regular file: .config/types/b")
	assertContains(t, result.stdout, "Apply aborted: destination conflicts found.")
	assertFileContent(t, fileA, "a old\n")
}

func TestLifecycle(t *testing.T) {
	env := newTestEnv(t)
	writeFile(t, filepath.Join(env.home, ".zshrc"), "hello\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".config", "app", "keep"), "keep\n", 0o755)
	writeFile(t, filepath.Join(env.home, ".config", "app", "ignored"), "ignored\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".config", "app", ".dotsignore"), "ignored\n", 0o644)

	result := env.runWithEnv(map[string]string{"DOTS_PROFILE": "personal"}, "init", env.repo)
	assertExitCode(t, result, 0)
	assertFileExists(t, filepath.Join(env.config, "dots", "config.toml"))
	assertFileExists(t, filepath.Join(env.repo, "personal.db"))
	assertFileExists(t, filepath.Join(env.state, "dots", "personal.db"))

	env.requireRun("add", filepath.Join(env.home, ".config", "app"))
	env.requireRun("add", filepath.Join(env.home, ".zshrc"))
	assertFileExists(t, filepath.Join(env.repo, "personal", ".config", "app", ".dotsignore"))
	assertFileExists(t, filepath.Join(env.repo, "personal", ".config", "app", "keep"))
	assertFileMissing(t, filepath.Join(env.repo, "personal", ".config", "app", "ignored"))

	result = env.requireRun("list")
	assertContains(t, result.stdout, ".config/app/.dotsignore")
	assertContains(t, result.stdout, ".config/app/keep")
	assertContains(t, result.stdout, ".zshrc")
	assertNotContains(t, result.stdout, "ignored")

	result = env.requireRun("status")
	assertContains(t, result.stdout, "Clean: no changes")

	result = env.requireRun("apply", "--dry-run")
	assertContains(t, result.stdout, "Apply plan (dry run; no files changed):")
	assertContains(t, result.stdout, "Clean: no changes")

	env.requireRun("apply")
	result = env.requireRun("status")
	assertContains(t, result.stdout, "Clean: no changes")

	writeFile(t, filepath.Join(env.repo, "personal", ".zshrc"), "repo update\n", 0o644)
	result = env.run("status")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "Repo drift:")
	assertContains(t, result.stdout, "profile file changed: .zshrc")

	env.requireRun("reindex")
	result = env.run("status")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "will update: .zshrc")
	env.requireRun("apply")
	assertFileContent(t, filepath.Join(env.home, ".zshrc"), "repo update\n")

	writeFile(t, filepath.Join(env.repo, "personal", ".zshrc"), "forced repo update\n", 0o644)
	env.requireRun("reindex")
	writeFile(t, filepath.Join(env.home, ".zshrc"), "user edit\n", 0o644)
	result = env.run("apply")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "Conflicts:")
	assertContains(t, result.stdout, "destination and profile diverged since last apply: .zshrc")
	assertFileContent(t, filepath.Join(env.home, ".zshrc"), "user edit\n")

	result = env.requireRun("apply", "--force")
	assertContains(t, result.stdout, "Backups written to:")
	assertFileContent(t, filepath.Join(env.home, ".zshrc"), "forced repo update\n")
	assertBackupContains(t, filepath.Join(env.state, "dots", "backups", "personal"), filepath.Join(".zshrc"), "user edit\n")

	env.requireRun("forget", ".config/app/keep")
	result = env.requireRun("list")
	assertNotContains(t, result.stdout, ".config/app/keep")
	assertFileMissing(t, filepath.Join(env.repo, "personal", ".config", "app", "keep"))
	assertFileExists(t, filepath.Join(env.home, ".config", "app", "keep"))
}

func TestSyncCommand(t *testing.T) {
	env := newTestEnv(t)
	trackedRoot := filepath.Join(env.home, ".config", "syncapp")
	writeFile(t, filepath.Join(env.home, ".zshrc"), "base\n", 0o644)
	writeFile(t, filepath.Join(trackedRoot, ".dotsignore"), "ignored\n", 0o644)
	writeFile(t, filepath.Join(trackedRoot, "base"), "base\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", filepath.Join(env.home, ".zshrc"))
	env.requireRun("add", trackedRoot)

	writeFile(t, filepath.Join(env.home, ".zshrc"), "home edit\n", 0o600)
	if err := os.Chmod(filepath.Join(env.home, ".zshrc"), 0o600); err != nil {
		t.Fatalf("chmod edited destination: %v", err)
	}
	writeFile(t, filepath.Join(trackedRoot, "new"), "new\n", 0o644)
	writeFile(t, filepath.Join(trackedRoot, "ignored"), "ignored\n", 0o644)

	result := env.requireRun("sync", "--dry-run")
	assertContains(t, result.stdout, "Sync plan (dry run; no files changed):")
	assertContains(t, result.stdout, "Files to update in repo:")
	assertContains(t, result.stdout, "  .zshrc")
	assertContains(t, result.stdout, "Files to add to repo:")
	assertContains(t, result.stdout, "  .config/syncapp/new")
	assertNotContains(t, result.stdout, ".config/syncapp/ignored")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".zshrc"), "base\n")

	result = env.requireRun("sync")
	assertContains(t, result.stdout, "Sync complete: copied 2 file(s), recorded state for 2 file(s)")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".zshrc"), "home edit\n")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".config", "syncapp", "new"), "new\n")
	info, err := os.Stat(filepath.Join(env.repo, "personal", ".zshrc"))
	if err != nil {
		t.Fatalf("stat synced repo file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("synced repo mode = %o, want 600", info.Mode().Perm())
	}
	result = env.requireRun("status")
	assertContains(t, result.stdout, "Clean: no changes")

	if err := os.Remove(filepath.Join(env.home, ".zshrc")); err != nil {
		t.Fatalf("remove tracked destination: %v", err)
	}
	result = env.requireRun("sync")
	assertContains(t, result.stdout, "dots forget .zshrc")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".zshrc"), "home edit\n")
}

func TestSyncForceBacksUpRepoConflict(t *testing.T) {
	env := newTestEnv(t)
	writeFile(t, filepath.Join(env.home, ".zshrc"), "base\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", filepath.Join(env.home, ".zshrc"))
	writeFile(t, filepath.Join(env.repo, "personal", ".zshrc"), "repo\n", 0o644)
	env.requireRun("reindex")
	writeFile(t, filepath.Join(env.home, ".zshrc"), "home\n", 0o644)

	result := env.run("sync")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "destination and profile diverged since last apply: .zshrc")
	assertContains(t, result.stdout, "Sync aborted: destination conflicts found")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".zshrc"), "repo\n")

	result = env.requireRun("sync", "--force")
	assertContains(t, result.stdout, "Backups written to:")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".zshrc"), "home\n")
	assertBackupContainsOrigin(t, filepath.Join(env.state, "dots", "backups", "personal"), "repo", filepath.Join(".zshrc"), "repo\n")
	result = env.requireRun("status")
	assertContains(t, result.stdout, "Clean: no changes")
}

func TestSyncRefusesWhenRemoteHasChangesToPull(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	env := newTestEnv(t)
	writeFile(t, filepath.Join(env.home, ".gitrc"), "local\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", filepath.Join(env.home, ".gitrc"))

	runGit(t, env.repo, "init", "-b", "main")
	runGit(t, env.repo, "config", "user.email", "dots@example.com")
	runGit(t, env.repo, "config", "user.name", "Dots Test")
	runGit(t, env.repo, "add", ".")
	runGit(t, env.repo, "commit", "-m", "initial")
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", remote)
	runGit(t, env.repo, "remote", "add", "origin", remote)
	runGit(t, env.repo, "push", "-u", "origin", "main")
	runGit(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")

	clone := filepath.Join(t.TempDir(), "clone")
	runGit(t, "", "clone", "--branch", "main", remote, clone)
	runGit(t, clone, "config", "user.email", "dots@example.com")
	runGit(t, clone, "config", "user.name", "Dots Test")
	writeFile(t, filepath.Join(clone, "remote.txt"), "remote\n", 0o644)
	runGit(t, clone, "add", "remote.txt")
	runGit(t, clone, "commit", "-m", "remote change")
	runGit(t, clone, "push")

	writeFile(t, filepath.Join(env.home, ".gitrc"), "home changed\n", 0o644)
	result := env.requireRun("sync", "--dry-run")
	assertContains(t, result.stdout, "Sync plan (dry run; no files changed):")
	assertContains(t, result.stdout, "Files to update in repo:")
	result = env.run("sync")
	assertExitCode(t, result, 1)
	assertContains(t, result.stderr, "pull before sync")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".gitrc"), "local\n")
}

func TestDiffCommand(t *testing.T) {
	env := newTestEnv(t)
	writeFile(t, filepath.Join(env.home, ".zshrc"), "hello\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", filepath.Join(env.home, ".zshrc"))

	result := env.requireRun("diff")
	if result.stdout != "" || result.stderr != "" {
		t.Fatalf("clean diff output = stdout %q stderr %q, want empty", result.stdout, result.stderr)
	}

	writeFile(t, filepath.Join(env.home, ".zshrc"), "user edit\n", 0o644)
	result = env.run("status")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "destination changed since last apply: .zshrc")

	result = env.run("diff")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "diff --git a/.zshrc b/.zshrc")
	assertContains(t, result.stdout, "-user edit")
	assertContains(t, result.stdout, "+hello")
	assertContains(t, result.stderr, "dots apply --force")

	result = env.run("diff", "--sync")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "diff --git a/.zshrc b/.zshrc")
	assertContains(t, result.stdout, "-hello")
	assertContains(t, result.stdout, "+user edit")
	assertNotContains(t, result.stderr, "dots sync --force")

	result = env.runWithEnv(map[string]string{"DOTS_PAGER": "false"}, "diff", "--sync")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "+user edit")

	if err := os.Remove(filepath.Join(env.state, "dots", "personal.db")); err != nil {
		t.Fatalf("remove state db: %v", err)
	}
	result = env.run("diff")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "-user edit")
	assertContains(t, result.stdout, "+hello")
	assertContains(t, result.stderr, "dots apply --force")
	result = env.run("diff", "--sync")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "+user edit")
	assertContains(t, result.stderr, "dots sync --force")
}

func TestDiffTrackedRootCreateDeleteAndRepoDrift(t *testing.T) {
	env := newTestEnv(t)
	root := filepath.Join(env.home, ".config", "app")
	writeFile(t, filepath.Join(root, "base"), "base\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", root)
	writeFile(t, filepath.Join(root, "new"), "new\n", 0o644)

	result := env.run("diff")
	assertExitCode(t, result, 0)
	if result.stdout != "" || result.stderr != "" {
		t.Fatalf("apply-direction tracked-root diff output = stdout %q stderr %q, want empty", result.stdout, result.stderr)
	}
	result = env.run("diff", "--sync")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "diff --git a/.config/app/new b/.config/app/new")
	assertContains(t, result.stdout, "new file mode 100644")
	assertContains(t, result.stdout, "+++ b/.config/app/new")

	env = newTestEnv(t)
	writeFile(t, filepath.Join(env.home, ".zshrc"), "hello\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", filepath.Join(env.home, ".zshrc"))
	if err := os.Remove(filepath.Join(env.home, ".zshrc")); err != nil {
		t.Fatalf("remove tracked destination: %v", err)
	}
	result = env.run("diff")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "--- /dev/null")
	assertContains(t, result.stdout, "+++ b/.zshrc")
	result = env.run("diff", "--sync")
	assertExitCode(t, result, 1)
	if result.stdout != "" {
		t.Fatalf("sync diff for missing destination stdout = %q, want empty", result.stdout)
	}
	assertContains(t, result.stderr, "dots forget .zshrc")

	env = newTestEnv(t)
	writeFile(t, filepath.Join(env.home, ".zshrc"), "hello\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", filepath.Join(env.home, ".zshrc"))
	writeFile(t, filepath.Join(env.repo, "personal", ".zshrc"), "drift\n", 0o644)
	result = env.run("diff")
	assertExitCode(t, result, 1)
	if result.stdout != "" {
		t.Fatalf("repo drift diff stdout = %q, want empty", result.stdout)
	}
	assertContains(t, result.stderr, "Repo drift:")
	assertContains(t, result.stderr, "Diff aborted: profile files differ from the tracking database.")
	assertContains(t, result.stderr, "dots reindex")
}

func TestDiffDivergedPath(t *testing.T) {
	env := newTestEnv(t)
	writeFile(t, filepath.Join(env.home, ".zshrc"), "base\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", filepath.Join(env.home, ".zshrc"))
	writeFile(t, filepath.Join(env.repo, "personal", ".zshrc"), "repo\n", 0o644)
	env.requireRun("reindex")
	writeFile(t, filepath.Join(env.home, ".zshrc"), "home\n", 0o644)

	result := env.run("status")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "destination and profile diverged since last apply: .zshrc")

	result = env.run("diff")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "diff --git a/.zshrc b/.zshrc")
	assertContains(t, result.stdout, "-home")
	assertContains(t, result.stdout, "+repo")
	assertContains(t, result.stderr, "dots apply --force")

	result = env.run("diff", "--sync")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "diff --git a/.zshrc b/.zshrc")
	assertContains(t, result.stdout, "-repo")
	assertContains(t, result.stdout, "+home")
	assertContains(t, result.stderr, "dots sync --force")
}

func TestDiffModeOnlyAndBinaryFiles(t *testing.T) {
	env := newTestEnv(t)
	modePath := filepath.Join(env.home, ".mode")
	writeFile(t, modePath, "same\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", modePath)
	if err := os.Chmod(modePath, 0o600); err != nil {
		t.Fatalf("chmod mode-only destination: %v", err)
	}

	result := env.run("diff")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "diff --git a/.mode b/.mode")
	assertContains(t, result.stdout, "old mode 100600")
	assertContains(t, result.stdout, "new mode 100644")
	assertNotContains(t, result.stdout, "@@")
	assertContains(t, result.stderr, "dots apply --force")

	env = newTestEnv(t)
	binaryPath := filepath.Join(env.home, ".bin")
	writeFile(t, binaryPath, "old\x00binary\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", binaryPath)
	writeFile(t, binaryPath, "new\x00binary\n", 0o644)

	result = env.run("diff")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "diff --git a/.bin b/.bin")
	assertContains(t, result.stdout, "Binary files a/.bin and b/.bin differ")
	assertNotContains(t, result.stdout, "@@")
	assertContains(t, result.stderr, "dots apply --force")
}

func TestDiffDestinationTypeConflictWritesOnlyStderr(t *testing.T) {
	env := newTestEnv(t)
	tracked := filepath.Join(env.home, ".config", "type", "file")
	writeFile(t, tracked, "file\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", tracked)
	if err := os.Remove(tracked); err != nil {
		t.Fatalf("remove tracked file: %v", err)
	}
	if err := os.MkdirAll(tracked, 0o750); err != nil {
		t.Fatalf("create type-conflict directory: %v", err)
	}

	result := env.run("diff")
	assertExitCode(t, result, 1)
	if result.stdout != "" {
		t.Fatalf("type-conflict diff stdout = %q, want empty", result.stdout)
	}
	assertContains(t, result.stderr, "destination is not a regular file: .config/type/file")
}

func TestDiffSyncHonorsDotsIgnoreAndOrdersPatches(t *testing.T) {
	env := newTestEnv(t)
	root := filepath.Join(env.home, ".config", "order")
	writeFile(t, filepath.Join(root, ".dotsignore"), "ignored\n*.tmp\n", 0o644)
	writeFile(t, filepath.Join(root, "base"), "base\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".zshrc"), "repo\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", root)
	env.requireRun("add", filepath.Join(env.home, ".zshrc"))
	writeFile(t, filepath.Join(root, "new"), "new\n", 0o644)
	writeFile(t, filepath.Join(root, "ignored"), "ignored\n", 0o644)
	writeFile(t, filepath.Join(root, "ignored.tmp"), "ignored tmp\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".zshrc"), "home\n", 0o644)

	result := env.run("diff", "--sync")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "diff --git a/.config/order/new b/.config/order/new")
	assertNotContains(t, result.stdout, ".config/order/ignored")
	assertNotContains(t, result.stdout, ".config/order/ignored.tmp")
	assertContains(t, result.stdout, "diff --git a/.zshrc b/.zshrc")
	assertInOrder(t, result.stdout, "diff --git a/.config/order/new b/.config/order/new", "diff --git a/.zshrc b/.zshrc")
}

func TestApplyPreflightPreventsPartialApply(t *testing.T) {
	env := newTestEnv(t)
	writeFile(t, filepath.Join(env.home, ".config", "two", "a"), "a old\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".config", "two", "b"), "b old\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", filepath.Join(env.home, ".config", "two"))
	env.requireRun("apply")

	writeFile(t, filepath.Join(env.repo, "personal", ".config", "two", "a"), "a new\n", 0o644)
	writeFile(t, filepath.Join(env.repo, "personal", ".config", "two", "b"), "b new\n", 0o644)
	env.requireRun("reindex")
	writeFile(t, filepath.Join(env.home, ".config", "two", "b"), "b user\n", 0o644)

	result := env.run("apply")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "destination and profile diverged since last apply: .config/two/b")
	assertFileContent(t, filepath.Join(env.home, ".config", "two", "a"), "a old\n")
	assertFileContent(t, filepath.Join(env.home, ".config", "two", "b"), "b user\n")
}

func TestProfileOverridesAndDoctor(t *testing.T) {
	env := newTestEnv(t)
	writeFile(t, filepath.Join(env.home, ".personalrc"), "personal\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".workrc"), "work\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", filepath.Join(env.home, ".personalrc"))
	env.requireRun("apply")

	env.requireRun("init", env.repo, "--profile", "work")
	writeFile(t, filepath.Join(env.repo, "work", ".workrc"), "work\n", 0o644)
	env.requireRun("--profile", "work", "reindex")

	result := env.runWithEnv(map[string]string{"DOTS_PROFILE": "work"}, "list")
	assertExitCode(t, result, 0)
	assertContains(t, result.stdout, ".workrc")
	assertNotContains(t, result.stdout, ".personalrc")

	result = env.runWithEnv(map[string]string{"DOTS_PROFILE": "work"}, "--profile", "personal", "list")
	assertExitCode(t, result, 0)
	assertContains(t, result.stdout, ".personalrc")
	assertNotContains(t, result.stdout, ".workrc")

	result = env.run("doctor")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "Profile: personal")
	assertContains(t, result.stdout, "Profile: work")
	assertContains(t, result.stdout, "will adopt existing match: .workrc")

	result = env.runWithEnv(map[string]string{"DOTS_PROFILE": "work"}, "doctor")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "Profile: work")
	assertNotContains(t, result.stdout, "Profile: personal")
}

func TestReindexRefusesWhenRemoteHasChangesToPull(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	env := newTestEnv(t)
	writeFile(t, filepath.Join(env.home, ".gitrc"), "local\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", filepath.Join(env.home, ".gitrc"))

	runGit(t, env.repo, "init", "-b", "main")
	runGit(t, env.repo, "config", "user.email", "dots@example.com")
	runGit(t, env.repo, "config", "user.name", "Dots Test")
	runGit(t, env.repo, "add", ".")
	runGit(t, env.repo, "commit", "-m", "initial")
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", remote)
	runGit(t, env.repo, "remote", "add", "origin", remote)
	runGit(t, env.repo, "push", "-u", "origin", "main")
	runGit(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")

	clone := filepath.Join(t.TempDir(), "clone")
	runGit(t, "", "clone", "--branch", "main", remote, clone)
	runGit(t, clone, "config", "user.email", "dots@example.com")
	runGit(t, clone, "config", "user.name", "Dots Test")
	writeFile(t, filepath.Join(clone, "remote.txt"), "remote\n", 0o644)
	runGit(t, clone, "add", "remote.txt")
	runGit(t, clone, "commit", "-m", "remote change")
	runGit(t, clone, "push")

	writeFile(t, filepath.Join(env.repo, "personal", ".gitrc"), "local changed\n", 0o644)
	result := env.run("reindex")
	assertExitCode(t, result, 1)
	assertContains(t, result.stderr, "pull before reindex")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	if dir != "" {
		cmd.Dir = dir
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func writePersonalConfig(t *testing.T, env testEnv) {
	t.Helper()
	content := fmt.Sprintf("default_profile = \"personal\"\n\n[profiles]\npersonal = %q\n", env.repo)
	writeFile(t, filepath.Join(env.config, "dots", "config.toml"), content, 0o600)
}

func createLegacyPersonalRepoDB(t *testing.T, path string, schemaVersion int, trackedDirs []string) {
	t.Helper()
	withSQLiteDatabase(t, path, func(db *sql.DB) {
		execSQL := func(query string, args ...any) {
			t.Helper()
			if _, err := db.Exec(query, args...); err != nil {
				t.Fatalf("execute legacy repo SQL: %v", err)
			}
		}

		execSQL(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
		execSQL(`INSERT INTO meta (key, value) VALUES ('profile', 'personal')`)
		execSQL(`INSERT INTO meta (key, value) VALUES ('schema_version', ?)`, fmt.Sprintf("%d", schemaVersion))
		execSQL(`CREATE TABLE files (
			path TEXT PRIMARY KEY,
			sha256 TEXT NOT NULL,
			mode INTEGER NOT NULL,
			size INTEGER NOT NULL,
			updated_at TEXT NOT NULL
		)`)
		switch schemaVersion {
		case 1:
			return
		case 2:
			execSQL(`CREATE TABLE tracked_dirs (
				path TEXT PRIMARY KEY,
				updated_at TEXT NOT NULL
			)`)
			for _, trackedDir := range trackedDirs {
				execSQL(`INSERT INTO tracked_dirs (path, updated_at) VALUES (?, 'legacy')`, trackedDir)
			}
		default:
			t.Fatalf("unsupported legacy repo schema version: %d", schemaVersion)
		}
	})
}

func createLegacyPersonalStateDB(t *testing.T, path string) {
	t.Helper()
	withSQLiteDatabase(t, path, func(db *sql.DB) {
		execSQL := func(query string, args ...any) {
			t.Helper()
			if _, err := db.Exec(query, args...); err != nil {
				t.Fatalf("execute legacy state SQL: %v", err)
			}
		}

		execSQL(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
		execSQL(`INSERT INTO meta (key, value) VALUES ('profile', 'personal')`)
		execSQL(`INSERT INTO meta (key, value) VALUES ('schema_version', '1')`)
		execSQL(`CREATE TABLE files (
			path TEXT PRIMARY KEY,
			sha256 TEXT NOT NULL,
			repo_sha256 TEXT NOT NULL,
			mode INTEGER NOT NULL,
			size INTEGER NOT NULL,
			applied_at TEXT NOT NULL
		)`)
	})
}

func assertSQLiteMigrationVersion(t *testing.T, path string, want int64) {
	t.Helper()
	withSQLiteDatabase(t, path, func(db *sql.DB) {
		var got int64
		if err := db.QueryRow(`SELECT MAX(version_id) FROM dots_schema_migrations`).Scan(&got); err != nil {
			t.Fatalf("read migration version from %s: %v", path, err)
		}
		if got != want {
			t.Fatalf("migration version for %s = %d, want %d", path, got, want)
		}
	})
}

func withSQLiteDatabase(t *testing.T, path string, fn func(*sql.DB)) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create sqlite parent directory: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite database: %v", err)
		}
	}()
	fn(db)
}

func writeFile(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create parent for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func setSQLiteProfileMetadata(t *testing.T, path, profile string) {
	t.Helper()
	withSQLiteDatabase(t, path, func(db *sql.DB) {
		if _, err := db.Exec(`UPDATE meta SET value = ? WHERE key = 'profile'`, profile); err != nil {
			t.Fatalf("update profile metadata: %v", err)
		}
	})
}

func assertExitCode(t *testing.T, result runResult, want int) {
	t.Helper()
	if result.code != want {
		t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", result.code, want, result.stdout, result.stderr)
	}
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output missing %q\noutput:\n%s", want, got)
	}
}

func assertNotContains(t *testing.T, got, want string) {
	t.Helper()
	if strings.Contains(got, want) {
		t.Fatalf("output unexpectedly contains %q\noutput:\n%s", want, got)
	}
}

func assertInOrder(t *testing.T, got, first, second string) {
	t.Helper()
	firstIndex := strings.Index(got, first)
	secondIndex := strings.Index(got, second)
	if firstIndex < 0 || secondIndex < 0 || firstIndex >= secondIndex {
		t.Fatalf("output does not contain %q before %q\noutput:\n%s", first, second, got)
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be missing, stat err = %v", path, err)
	}
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(content) != want {
		t.Fatalf("%s content = %q, want %q", path, content, want)
	}
}

func assertBackupContains(t *testing.T, backupRoot, relPath, want string) {
	t.Helper()
	assertBackupContainsOrigin(t, backupRoot, "", relPath, want)
}

func assertBackupContainsOrigin(t *testing.T, backupRoot, origin, relPath, want string) {
	t.Helper()
	encoded := base64.RawURLEncoding.EncodeToString([]byte(filepath.ToSlash(relPath)))
	found := false
	if err := filepath.WalkDir(backupRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.ToSlash(path) == filepath.ToSlash(backupRoot) {
			return nil
		}
		slashPath := filepath.ToSlash(path)
		if !strings.HasSuffix(slashPath, "/"+encoded+"/payload") {
			return nil
		}
		if origin != "" && !strings.Contains(slashPath, "/"+origin+"/") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found = found || string(content) == want
		return nil
	}); err != nil {
		t.Fatalf("walk backups: %v", err)
	}
	if !found {
		t.Fatalf("backup %s under %s with origin %q and content %q not found", relPath, backupRoot, origin, want)
	}
}

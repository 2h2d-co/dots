//go:build integration

// Package integration contains end-to-end tests for dots.
package integration

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
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

	result = env.run("doctor")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "Doctor: checking 3 profile(s)")
	assertContains(t, result.stdout, "Profile: personal")
	assertContains(t, result.stdout, "Profile: laptop")
	assertContains(t, result.stdout, "Profile: work")

	result = env.run("--profile", "work", "doctor")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "Doctor: checking 1 profile(s)")
	assertContains(t, result.stdout, "Profile: work")
	assertNotContains(t, result.stdout, "Profile: personal")
	assertNotContains(t, result.stdout, "Profile: laptop")
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
	assertContains(t, result.stdout, "Tracked root: .config/trackedapp")
	assertContains(t, result.stdout, "will adopt existing match: .config/trackedapp/new")
	assertContains(t, result.stdout, "Tracked root: .config/trackedapp/nested")
	assertContains(t, result.stdout, "will adopt existing match: .config/trackedapp/nested/new")
	assertContains(t, result.stdout, "Individual paths:")
	assertContains(t, result.stdout, "will create: .zshrc")
	assertNotContains(t, result.stdout, "Directory drift:")
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

	result = env.run("status")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "Pending changes:")
	assertContains(t, result.stdout, "will adopt existing match: .zshrc")

	result = env.requireRun("apply", "--dry-run")
	assertContains(t, result.stdout, "Apply plan (dry run; no files changed):")
	assertContains(t, result.stdout, "will adopt existing match")

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
	assertContains(t, result.stdout, "destination changed since last apply: .zshrc")
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
	assertContains(t, result.stdout, "destination changed since last apply: .config/two/b")
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

func writeFile(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create parent for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
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
	found := false
	if err := filepath.WalkDir(backupRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.ToSlash(path) == filepath.ToSlash(backupRoot) {
			return nil
		}
		if strings.HasSuffix(filepath.ToSlash(path), "/"+filepath.ToSlash(relPath)) {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			found = found || string(content) == want
		}
		return nil
	}); err != nil {
		t.Fatalf("walk backups: %v", err)
	}
	if !found {
		t.Fatalf("backup %s under %s with content %q not found", relPath, backupRoot, want)
	}
}

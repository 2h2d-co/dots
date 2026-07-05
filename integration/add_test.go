//go:build integration

// Package integration contains end-to-end tests for dots.
package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestDotsIgnoreFiltersNestedPaths(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestNPMRCSecretsAreScrubbedAndComparedCanonically(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	token1 := "npm_" + "a1b2c3d4e5f6g7h8i9j0klmnopqrstuvwx12"
	token2 := "npm_" + "z9y8x7w6v5u4t3s2r1q0ponmlkjihgfedcba"
	npmrcPath := filepath.Join(env.home, ".config", "npm", "npmrc")

	env.requireRun("init", env.repo, "--profile", "personal")
	writeFile(t, npmrcPath, "registry=https://registry.npmjs.org/\n//registry.npmjs.org/:_authToken="+token1+"\n", 0o600)
	env.requireRun("add", npmrcPath)
	assertFileContent(t, filepath.Join(env.repo, "personal", ".config", "npm", "npmrc"), "registry=https://registry.npmjs.org/\n\n")

	writeFile(t, npmrcPath, "registry=https://registry.npmjs.org/\n//registry.npmjs.org/:_authToken="+token2+"\n", 0o600)
	result := env.requireRun("status")
	assertContains(t, result.stdout, "Clean: no changes")

	writeFile(t, npmrcPath, "registry=https://custom.example/\n//registry.npmjs.org/:_authToken="+token2+"\n", 0o600)
	result = env.run("diff", "--sync", "--no-pager")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "+registry=https://custom.example/")
	assertNotContains(t, result.stdout, token1)
	assertNotContains(t, result.stdout, token2)

	env.requireRun("sync")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".config", "npm", "npmrc"), "registry=https://custom.example/\n\n")
}

func TestAddRejectsConfiguredRepoPaths(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

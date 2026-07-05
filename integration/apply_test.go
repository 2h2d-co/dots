//go:build integration

// Package integration contains end-to-end tests for dots.
package integration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyPreflightReportsDestinationTypeConflict(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestApplyPreflightPreventsPartialApply(t *testing.T) {
	t.Parallel()
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

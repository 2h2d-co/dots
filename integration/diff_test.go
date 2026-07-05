//go:build integration

// Package integration contains end-to-end tests for dots.
package integration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiffCommand(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

//go:build integration

// Package integration contains end-to-end tests for dots.
package integration

import (
	"path/filepath"
	"testing"
)

func TestScopedApplyAndDiffAcceptSelectedRepoDrift(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	appRoot := filepath.Join(env.home, ".config", "scoped-app")
	otherRoot := filepath.Join(env.home, ".config", "scoped-other")
	writeFile(t, filepath.Join(appRoot, "config"), "app base\n", 0o644)
	writeFile(t, filepath.Join(otherRoot, "config"), "other base\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", appRoot)
	env.requireRun("add", otherRoot)

	writeFile(t, filepath.Join(env.repo, "personal", ".config", "scoped-app", "config"), "app repo\n", 0o644)
	writeFile(t, filepath.Join(env.repo, "personal", ".config", "scoped-other", "config"), "other repo\n", 0o644)

	result := env.run("diff", filepath.Join(".config", "scoped-app"))
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "diff --git a/.config/scoped-app/config b/.config/scoped-app/config")
	assertContains(t, result.stdout, "+app repo")
	assertNotContains(t, result.stdout, "scoped-other")
	if result.stderr != "" {
		t.Fatalf("scoped repo drift diff stderr = %q, want empty", result.stderr)
	}

	result = env.requireRun("apply", filepath.Join(".config", "scoped-app"))
	assertContains(t, result.stdout, "Apply complete: copied 1 file(s), left 0 matching file(s) untouched")
	assertFileContent(t, filepath.Join(appRoot, "config"), "app repo\n")
	assertFileContent(t, filepath.Join(otherRoot, "config"), "other base\n")

	result = env.run("status")
	assertExitCode(t, result, 1)
	assertNotContains(t, result.stdout, "scoped-app")
	assertContains(t, result.stdout, "profile file changed: .config/scoped-other/config")
}

func TestScopedApplyForceOnlySelectedConflict(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	root := filepath.Join(env.home, ".config", "scoped-conflict")
	writeFile(t, filepath.Join(root, "a"), "a base\n", 0o644)
	writeFile(t, filepath.Join(root, "b"), "b base\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", root)
	writeFile(t, filepath.Join(env.repo, "personal", ".config", "scoped-conflict", "a"), "a repo\n", 0o644)
	writeFile(t, filepath.Join(env.repo, "personal", ".config", "scoped-conflict", "b"), "b repo\n", 0o644)
	env.requireRun("reindex")
	writeFile(t, filepath.Join(root, "a"), "a home\n", 0o644)
	writeFile(t, filepath.Join(root, "b"), "b home\n", 0o644)

	result := env.requireRun("apply", "--force", filepath.Join(".config", "scoped-conflict", "a"))
	assertContains(t, result.stdout, "Backups written to:")
	assertFileContent(t, filepath.Join(root, "a"), "a repo\n")
	assertFileContent(t, filepath.Join(root, "b"), "b home\n")
	assertBackupContainsOrigin(t, filepath.Join(env.state, "dots", "backups", "personal"), "home", filepath.Join(".config", "scoped-conflict", "a"), "a home\n")
}

func TestScopedSyncAndDiffHandleSelectedRepoDrift(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	root := filepath.Join(env.home, ".config", "scoped-sync")
	writeFile(t, filepath.Join(root, "drift"), "base\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".outsiderc"), "outside base\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", root)
	env.requireRun("add", filepath.Join(env.home, ".outsiderc"))
	writeFile(t, filepath.Join(env.repo, "personal", ".config", "scoped-sync", "drift"), "repo drift\n", 0o644)
	writeFile(t, filepath.Join(env.repo, "personal", ".outsiderc"), "outside repo drift\n", 0o644)
	writeFile(t, filepath.Join(root, "drift"), "home drift\n", 0o644)

	result := env.run("diff", "--sync", filepath.Join(".config", "scoped-sync", "drift"))
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "diff --git a/.config/scoped-sync/drift b/.config/scoped-sync/drift")
	assertContains(t, result.stdout, "-repo drift")
	assertContains(t, result.stdout, "+home drift")
	assertContains(t, result.stderr, "dots sync --force")
	assertNotContains(t, result.stdout, ".outsiderc")

	result = env.run("sync", filepath.Join(".config", "scoped-sync", "drift"))
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "profile file changed: .config/scoped-sync/drift")
	assertContains(t, result.stdout, "Sync aborted: destination conflicts found")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".config", "scoped-sync", "drift"), "repo drift\n")

	result = env.requireRun("sync", "--force", filepath.Join(".config", "scoped-sync", "drift"))
	assertContains(t, result.stdout, "Backups written to:")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".config", "scoped-sync", "drift"), "home drift\n")
	assertBackupContainsOrigin(t, filepath.Join(env.state, "dots", "backups", "personal"), "repo", filepath.Join(".config", "scoped-sync", "drift"), "repo drift\n")

	result = env.run("status")
	assertExitCode(t, result, 1)
	assertNotContains(t, result.stdout, "scoped-sync")
	assertContains(t, result.stdout, "profile file changed: .outsiderc")
}

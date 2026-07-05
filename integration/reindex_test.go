//go:build integration

// Package integration contains end-to-end tests for dots.
package integration

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestReindexRefusesWhenRemoteHasChangesToPull(t *testing.T) {
	t.Parallel()
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

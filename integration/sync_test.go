//go:build integration

// Package integration contains end-to-end tests for dots.
package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestSyncCommand(t *testing.T) {
	t.Parallel()
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

func TestSyncConflictAbortIsAllOrNothingAndForceBacksUpRepo(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	writeFile(t, filepath.Join(env.home, ".changedrc"), "changed base\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".conflictrc"), "conflict base\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", filepath.Join(env.home, ".changedrc"))
	env.requireRun("add", filepath.Join(env.home, ".conflictrc"))
	writeFile(t, filepath.Join(env.repo, "personal", ".conflictrc"), "repo conflict\n", 0o644)
	env.requireRun("reindex")
	writeFile(t, filepath.Join(env.home, ".changedrc"), "home changed\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".conflictrc"), "home conflict\n", 0o644)

	repoDigestBefore := repoDBFilesDigest(t, env)
	stateDigestBefore := stateDBFilesDigest(t, env)
	result := env.run("sync")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "destination and profile diverged since last apply: .conflictrc")
	assertContains(t, result.stdout, "Sync aborted: destination conflicts found")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".changedrc"), "changed base\n")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".conflictrc"), "repo conflict\n")
	if got := repoDBFilesDigest(t, env); got != repoDigestBefore {
		t.Fatalf("repo DB changed after aborted sync\nbefore:\n%s\nafter:\n%s", repoDigestBefore, got)
	}
	if got := stateDBFilesDigest(t, env); got != stateDigestBefore {
		t.Fatalf("state DB changed after aborted sync\nbefore:\n%s\nafter:\n%s", stateDigestBefore, got)
	}
	assertFileMissing(t, filepath.Join(env.state, "dots", "backups", "personal"))

	result = env.requireRun("sync", "--force")
	assertContains(t, result.stdout, "Backups written to:")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".changedrc"), "home changed\n")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".conflictrc"), "home conflict\n")
	assertBackupContainsOrigin(t, filepath.Join(env.state, "dots", "backups", "personal"), "repo", filepath.Join(".conflictrc"), "repo conflict\n")
	result = env.requireRun("status")
	assertContains(t, result.stdout, "Clean: no changes")
}

func TestSyncIngestsUnmanagedDestinationWithForce(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	writeFile(t, filepath.Join(env.home, ".managedrc"), "base\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", filepath.Join(env.home, ".managedrc"))
	deleteStateRecord(t, env, ".managedrc")
	writeFile(t, filepath.Join(env.home, ".managedrc"), "home managed\n", 0o644)

	repoDigestBefore := repoDBFilesDigest(t, env)
	stateDigestBefore := stateDBFilesDigest(t, env)
	result := env.run("sync")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "unmanaged destination differs: .managedrc")
	assertContains(t, result.stdout, "Sync aborted: destination conflicts found")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".managedrc"), "base\n")
	if got := repoDBFilesDigest(t, env); got != repoDigestBefore {
		t.Fatalf("repo DB changed after unmanaged destination abort\nbefore:\n%s\nafter:\n%s", repoDigestBefore, got)
	}
	if got := stateDBFilesDigest(t, env); got != stateDigestBefore {
		t.Fatalf("state DB changed after unmanaged destination abort\nbefore:\n%s\nafter:\n%s", stateDigestBefore, got)
	}

	result = env.requireRun("sync", "--force")
	assertContains(t, result.stdout, "Backups written to:")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".managedrc"), "home managed\n")
	assertBackupContainsOrigin(t, filepath.Join(env.state, "dots", "backups", "personal"), "repo", filepath.Join(".managedrc"), "base\n")
	result = env.requireRun("status")
	assertContains(t, result.stdout, "Clean: no changes")
}

func TestSyncRefreshesStateOnlyMatches(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	writeFile(t, filepath.Join(env.home, ".adoptrc"), "base\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", filepath.Join(env.home, ".adoptrc"))
	deleteStateRecord(t, env, ".adoptrc")
	repoDigestBefore := repoDBFilesDigest(t, env)

	result := env.run("status")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "will adopt existing match: .adoptrc")
	result = env.requireRun("sync")
	assertContains(t, result.stdout, "Sync complete: copied 0 file(s), recorded state for 1 file(s)")
	if got := repoDBFilesDigest(t, env); got != repoDigestBefore {
		t.Fatalf("repo DB changed during state-only sync\nbefore:\n%s\nafter:\n%s", repoDigestBefore, got)
	}
	result = env.requireRun("status")
	assertContains(t, result.stdout, "Clean: no changes")
}

func TestSyncRefusesRepoDriftBeforeMutation(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	writeFile(t, filepath.Join(env.home, ".changedrc"), "changed base\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".driftrc"), "drift base\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", filepath.Join(env.home, ".changedrc"))
	env.requireRun("add", filepath.Join(env.home, ".driftrc"))
	writeFile(t, filepath.Join(env.repo, "personal", ".driftrc"), "repo drift\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".changedrc"), "home changed\n", 0o644)
	repoDigestBefore := repoDBFilesDigest(t, env)
	stateDigestBefore := stateDBFilesDigest(t, env)

	result := env.run("sync")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "Repo drift:")
	assertContains(t, result.stdout, "profile file changed: .driftrc")
	assertContains(t, result.stdout, "Sync aborted: profile repo files changed since dots last indexed them.")
	assertContains(t, result.stdout, "dots reindex")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".changedrc"), "changed base\n")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".driftrc"), "repo drift\n")
	if got := repoDBFilesDigest(t, env); got != repoDigestBefore {
		t.Fatalf("repo DB changed after repo drift refusal\nbefore:\n%s\nafter:\n%s", repoDigestBefore, got)
	}
	if got := stateDBFilesDigest(t, env); got != stateDigestBefore {
		t.Fatalf("state DB changed after repo drift refusal\nbefore:\n%s\nafter:\n%s", stateDigestBefore, got)
	}
	assertFileMissing(t, filepath.Join(env.state, "dots", "backups", "personal"))
}

func TestDiffSyncMatchesSyncMutationSet(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	plainRoot := filepath.Join(env.home, ".config", "plain-sync")
	writeFile(t, filepath.Join(plainRoot, "base"), "base\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".plainrc"), "plain base\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", plainRoot)
	env.requireRun("add", filepath.Join(env.home, ".plainrc"))
	writeFile(t, filepath.Join(plainRoot, "new"), "new\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".plainrc"), "plain home\n", 0o644)

	result := env.run("diff", "--sync")
	assertExitCode(t, result, 1)
	plainPatchPaths := diffSyncPatchPaths(t, result.stdout)
	assertStringSlicesEqual(t, plainPatchPaths, []string{".config/plain-sync/new", ".plainrc"})
	plainBefore := profileFileSnapshot(t, env)
	env.requireRun("sync")
	plainChanged := changedProfilePaths(plainBefore, profileFileSnapshot(t, env))
	assertStringSlicesEqual(t, plainChanged, plainPatchPaths)
	result = env.requireRun("status")
	assertContains(t, result.stdout, "Clean: no changes")

	env = newTestEnv(t)
	forceRoot := filepath.Join(env.home, ".config", "force-sync")
	writeFile(t, filepath.Join(forceRoot, "base"), "base\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".plainrc"), "plain base\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".divergedrc"), "diverged base\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".managedrc"), "managed base\n", 0o644)
	env.requireRun("init", env.repo, "--profile", "personal")
	env.requireRun("add", forceRoot)
	env.requireRun("add", filepath.Join(env.home, ".plainrc"))
	env.requireRun("add", filepath.Join(env.home, ".divergedrc"))
	env.requireRun("add", filepath.Join(env.home, ".managedrc"))
	writeFile(t, filepath.Join(env.repo, "personal", ".divergedrc"), "repo diverged\n", 0o644)
	env.requireRun("reindex")
	deleteStateRecord(t, env, ".managedrc")
	writeFile(t, filepath.Join(forceRoot, "new"), "new\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".plainrc"), "plain home\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".divergedrc"), "home diverged\n", 0o644)
	writeFile(t, filepath.Join(env.home, ".managedrc"), "home managed\n", 0o644)

	result = env.run("diff", "--sync")
	assertExitCode(t, result, 1)
	assertContains(t, result.stderr, "plain sync refuses .divergedrc")
	assertContains(t, result.stderr, "plain sync refuses .managedrc")
	forcePatchPaths := diffSyncPatchPaths(t, result.stdout)
	assertStringSlicesEqual(t, forcePatchPaths, []string{".config/force-sync/new", ".divergedrc", ".managedrc", ".plainrc"})
	forceBefore := profileFileSnapshot(t, env)
	env.requireRun("sync", "--force")
	forceChanged := changedProfilePaths(forceBefore, profileFileSnapshot(t, env))
	assertStringSlicesEqual(t, forceChanged, forcePatchPaths)
	result = env.requireRun("status")
	assertContains(t, result.stdout, "Clean: no changes")
}

func TestSyncRefusesWhenRemoteHasChangesToPull(t *testing.T) {
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

	writeFile(t, filepath.Join(env.home, ".gitrc"), "home changed\n", 0o644)
	result := env.requireRun("sync", "--dry-run")
	assertContains(t, result.stdout, "Sync plan (dry run; no files changed):")
	assertContains(t, result.stdout, "Files to update in repo:")
	result = env.run("sync")
	assertExitCode(t, result, 1)
	assertContains(t, result.stderr, "pull before sync")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".gitrc"), "local\n")
}

func profileFileSnapshot(t *testing.T, env testEnv) map[string]string {
	t.Helper()
	root := filepath.Join(env.repo, "personal")
	snapshot := make(map[string]string)
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot[filepath.ToSlash(rel)] = fmt.Sprintf("%o\x00%s", info.Mode().Perm(), content)
		return nil
	}); err != nil {
		t.Fatalf("snapshot profile files: %v", err)
	}
	return snapshot
}

func changedProfilePaths(before, after map[string]string) []string {
	changed := make([]string, 0)
	seen := make(map[string]struct{})
	for path, afterValue := range after {
		seen[path] = struct{}{}
		if before[path] != afterValue {
			changed = append(changed, path)
		}
	}
	for path := range before {
		if _, ok := seen[path]; !ok {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return changed
}

func diffSyncPatchPaths(t *testing.T, patch string) []string {
	t.Helper()
	var paths []string
	for _, line := range strings.Split(patch, "\n") {
		rest, ok := strings.CutPrefix(line, "diff --git a/")
		if !ok {
			continue
		}
		path, _, ok := strings.Cut(rest, " b/")
		if !ok {
			t.Fatalf("unexpected diff header: %s", line)
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func assertStringSlicesEqual(t *testing.T, got []string, want []string) {
	t.Helper()
	sortedGot := append([]string(nil), got...)
	sortedWant := append([]string(nil), want...)
	sort.Strings(sortedGot)
	sort.Strings(sortedWant)
	if strings.Join(sortedGot, "\n") != strings.Join(sortedWant, "\n") {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

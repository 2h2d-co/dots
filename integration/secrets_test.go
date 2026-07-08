//go:build integration

// Package integration contains end-to-end tests for dots.
package integration

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNPMRCSecretsAreScrubbedAndComparedCanonically(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	npmValue1 := "npm_" + strings.Join([]string{"a1b2c3d4", "e5f6g7h8", "i9j0klmn", "opqrstuv", "wx12"}, "")
	npmValue2 := "npm_" + strings.Join([]string{"z9y8x7w6", "v5u4t3s2", "r1q0ponm", "lkjihgfe", "dcba"}, "")
	npmrcPath := filepath.Join(env.home, ".config", "npm", "npmrc")

	env.requireRun("init", env.repo, "--profile", "personal")
	writeFile(t, npmrcPath, "registry=https://registry.npmjs.org/\n//registry.npmjs.org/:_authToken="+npmValue1+"\n", 0o600)
	env.requireRun("add", npmrcPath)
	assertFileContent(t, filepath.Join(env.repo, "personal", ".config", "npm", "npmrc"), "registry=https://registry.npmjs.org/\n\n")

	writeFile(t, npmrcPath, "registry=https://registry.npmjs.org/\n//registry.npmjs.org/:_authToken="+npmValue2+"\n", 0o600)
	result := env.requireRun("status")
	assertContains(t, result.stdout, "Status: clean")

	writeFile(t, npmrcPath, "registry=https://custom.example/\n//registry.npmjs.org/:_authToken="+npmValue2+"\n", 0o600)
	result = env.run("diff", "--sync", "--no-pager")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "+registry=https://custom.example/")
	assertNotContains(t, result.stdout, npmValue1)
	assertNotContains(t, result.stdout, npmValue2)
	assertNotContains(t, result.stderr, npmValue1)
	assertNotContains(t, result.stderr, npmValue2)

	syncResult := env.requireRun("sync")
	assertNotContains(t, syncResult.stdout, npmValue1)
	assertNotContains(t, syncResult.stdout, npmValue2)
	assertFileContent(t, filepath.Join(env.repo, "personal", ".config", "npm", "npmrc"), "registry=https://custom.example/\n\n")
}

func TestNPMAuthTokenIsScrubbedInAnyFileEndToEnd(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	npmValue1 := "npm_" + strings.Join([]string{"a1b2c3d4", "e5f6g7h8", "i9j0klmn", "opqrstuv", "wx12"}, "")
	npmValue2 := "npm_" + strings.Join([]string{"z9y8x7w6", "v5u4t3s2", "r1q0ponm", "lkjihgfe", "dcba"}, "")
	configPath := filepath.Join(env.home, ".config", "arbitrary", "settings.conf")
	repoPath := filepath.Join(env.repo, "personal", ".config", "arbitrary", "settings.conf")

	env.requireRun("init", env.repo, "--profile", "personal")
	writeFile(t, configPath, "before\ntoken="+npmValue1+"\nafter\n", 0o600)
	env.requireRun("add", configPath)
	assertFileContent(t, repoPath, "before\n\nafter\n")

	writeFile(t, configPath, "before\ntoken="+npmValue2+"\nafter\n", 0o600)
	result := env.requireRun("status")
	assertContains(t, result.stdout, "Status: clean")

	writeFile(t, configPath, "before edited\ntoken="+npmValue2+"\nafter\n", 0o600)
	result = env.run("diff", "--sync", "--no-pager")
	assertExitCode(t, result, 1)
	assertContains(t, result.stdout, "+before edited")
	assertNotContains(t, result.stdout, npmValue1)
	assertNotContains(t, result.stdout, npmValue2)
	assertNotContains(t, result.stderr, npmValue1)
	assertNotContains(t, result.stderr, npmValue2)

	syncResult := env.requireRun("sync")
	assertNotContains(t, syncResult.stdout, npmValue1)
	assertNotContains(t, syncResult.stdout, npmValue2)
	assertFileContent(t, repoPath, "before edited\n\nafter\n")
}

func TestNPMRCGenericAuthLinesAreScrubbedEndToEnd(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	passwordValue := strings.Join([]string{"YWJjZGVm", "Z2hpamts", "bW5vcHFy", "c3R1dnd4", "eXo="}, "")
	authValue := strings.Join([]string{"bWFkZXVw", "dXNlcjpt", "YWRldXBw", "YXNzd29y", "ZA=="}, "")
	npmrcPath := filepath.Join(env.home, ".npmrc")

	env.requireRun("init", env.repo, "--profile", "personal")
	writeFile(t, npmrcPath, "//registry.npmjs.org/:_password="+passwordValue+"\n"+
		"//registry.npmjs.org/:_auth="+authValue+"\n"+
		"//registry.npmjs.org/:username=madeupuser\n", 0o600)
	env.requireRun("add", npmrcPath)
	assertFileContent(t, filepath.Join(env.repo, "personal", ".npmrc"), "\n\n//registry.npmjs.org/:username=madeupuser\n")
}

func TestSecretScrubbingPreservesLocalTokenOnApply(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	npmValue1 := "npm_" + strings.Join([]string{"a1b2c3d4", "e5f6g7h8", "i9j0klmn", "opqrstuv", "wx12"}, "")
	npmValue2 := "npm_" + strings.Join([]string{"z9y8x7w6", "v5u4t3s2", "r1q0ponm", "lkjihgfe", "dcba"}, "")
	trackedPath := filepath.Join(".config", "applysecrets", "credentials")
	homePath := filepath.Join(env.home, trackedPath)

	env.requireRun("init", env.repo, "--profile", "personal")
	writeFile(t, homePath, "enabled=true\ntoken="+npmValue1+"\n", 0o600)
	env.requireRun("add", homePath)
	assertFileContent(t, filepath.Join(env.repo, "personal", trackedPath), "enabled=true\n\n")

	writeFile(t, homePath, "enabled=true\ntoken="+npmValue2+"\n", 0o600)
	deleteStateRecord(t, env, trackedPath)
	result := env.requireRun("apply")
	assertContains(t, result.stdout, "Apply complete: copied 0 file(s)")
	assertFileContent(t, homePath, "enabled=true\ntoken="+npmValue2+"\n")

	result = env.requireRun("status")
	assertContains(t, result.stdout, "Status: clean")
}

func TestSecretScanBlocksUnsupportedAddWithoutLeaking(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	genericValue := strings.Join([]string{"a1b2c3d4", "e5f6g7h8", "i9j0klmn", "opqrstuv", "wx12"}, "")
	configPath := filepath.Join(env.home, ".config", "blocked", "settings")

	env.requireRun("init", env.repo, "--profile", "personal")
	writeFile(t, configPath, "api"+"_key="+genericValue+"\n", 0o600)
	result := env.run("add", configPath)
	assertExitCode(t, result, 1)
	assertContains(t, result.stderr, "secret scan blocked")
	assertContains(t, result.stderr, "generic-api-key")
	assertNotContains(t, result.stdout, genericValue)
	assertNotContains(t, result.stderr, genericValue)
	assertFileMissing(t, filepath.Join(env.repo, "personal", ".config", "blocked", "settings"))
}

func TestSecretScanBlocksUnsupportedSyncWithoutPartialWrites(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	genericValue := strings.Join([]string{"a1b2c3d4", "e5f6g7h8", "i9j0klmn", "opqrstuv", "wx12"}, "")
	safePath := filepath.Join(env.home, ".safe")
	blockedPath := filepath.Join(env.home, ".blocked")

	env.requireRun("init", env.repo, "--profile", "personal")
	writeFile(t, safePath, "safe base\n", 0o600)
	writeFile(t, blockedPath, "blocked base\n", 0o600)
	env.requireRun("add", safePath)
	env.requireRun("add", blockedPath)

	writeFile(t, safePath, "safe changed\n", 0o600)
	writeFile(t, blockedPath, "api"+"_key="+genericValue+"\n", 0o600)
	repoDigestBefore := repoDBFilesDigest(t, env)
	stateDigestBefore := stateDBFilesDigest(t, env)
	result := env.run("sync")
	assertExitCode(t, result, 1)
	assertContains(t, result.stderr, "secret scan blocked")
	assertContains(t, result.stderr, "generic-api-key")
	assertNotContains(t, result.stdout, genericValue)
	assertNotContains(t, result.stderr, genericValue)
	assertFileContent(t, filepath.Join(env.repo, "personal", ".safe"), "safe base\n")
	assertFileContent(t, filepath.Join(env.repo, "personal", ".blocked"), "blocked base\n")
	if got := repoDBFilesDigest(t, env); got != repoDigestBefore {
		t.Fatalf("repo DB changed after blocked sync\nbefore:\n%s\nafter:\n%s", repoDigestBefore, got)
	}
	if got := stateDBFilesDigest(t, env); got != stateDigestBefore {
		t.Fatalf("state DB changed after blocked sync\nbefore:\n%s\nafter:\n%s", stateDigestBefore, got)
	}
}

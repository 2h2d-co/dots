//go:build integration

// Package integration contains end-to-end tests for dots.
package integration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDotsHelp(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	result := env.requireRun("--help")
	assertContains(t, result.stdout, "minimal copy-based dotfiles manager")
	assertContains(t, result.stdout, "--profile")
}

func TestConfigOverride(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestInitRejectsUnsafeMultiProfileConfig(t *testing.T) {
	t.Parallel()
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

func TestProfileOverridesAndDoctor(t *testing.T) {
	t.Parallel()
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

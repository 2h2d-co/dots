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

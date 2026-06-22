package dots

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCleanTrackedPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr string
	}{
		{name: "plain", raw: "foo/bar", want: "foo/bar"},
		{name: "leading dot", raw: "./foo/bar", want: "foo/bar"},
		{name: "cleaned", raw: "foo/../bar", want: "bar"},
		{name: "spaces and unicode", raw: ".config/My App/üñîçødé file", want: ".config/My App/üñîçødé file"},
		{name: "empty", raw: "", wantErr: "path is required"},
		{name: "dot", raw: ".", wantErr: "path is required"},
		{name: "parent", raw: "../secret", wantErr: "invalid tracked path"},
		{name: "absolute", raw: "/tmp/secret", wantErr: "invalid tracked path"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := cleanTrackedPath(tc.raw)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("cleanTrackedPath(%q) error = %v, want containing %q", tc.raw, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("cleanTrackedPath(%q) error = %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("cleanTrackedPath(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestHomeRelativePath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	home := filepath.Join(root, "home")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{home, outside} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}

	got, err := homeRelativePath(home, filepath.Join(home, ".config", "app"))
	if err != nil {
		t.Fatalf("homeRelativePath() error = %v", err)
	}
	if got != ".config/app" {
		t.Fatalf("homeRelativePath() = %q, want .config/app", got)
	}

	if _, err := homeRelativePath(home, home); err == nil || !strings.Contains(err.Error(), "home directory itself") {
		t.Fatalf("homeRelativePath(home) error = %v, want home-directory refusal", err)
	}
	if _, err := homeRelativePath(home, filepath.Join(outside, "file")); err == nil || !strings.Contains(err.Error(), "outside home directory") {
		t.Fatalf("homeRelativePath(outside) error = %v, want outside-home refusal", err)
	}
}

func TestHomeRelativePathWithSymlinkedHomePrefix(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup is not portable on Windows")
	}

	root := t.TempDir()
	realRoot := filepath.Join(root, "real")
	realHome := filepath.Join(realRoot, "home")
	if err := os.MkdirAll(realHome, 0o750); err != nil {
		t.Fatalf("create real home: %v", err)
	}
	aliasRoot := filepath.Join(root, "alias")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Skipf("create symlinked home prefix: %v", err)
	}

	got, err := homeRelativePath(filepath.Join(aliasRoot, "home"), filepath.Join(realHome, ".config", "app"))
	if err != nil {
		t.Fatalf("homeRelativePath() error = %v", err)
	}
	if got != ".config/app" {
		t.Fatalf("homeRelativePath() = %q, want .config/app", got)
	}
}

func TestTrackedPathFromArg(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	rt := &Runtime{Home: home}

	got, err := trackedPathFromArg(rt, ".config/app")
	if err != nil {
		t.Fatalf("trackedPathFromArg(relative) error = %v", err)
	}
	if got != ".config/app" {
		t.Fatalf("trackedPathFromArg(relative) = %q, want .config/app", got)
	}

	got, err = trackedPathFromArg(rt, filepath.Join(home, ".zshrc"))
	if err != nil {
		t.Fatalf("trackedPathFromArg(abs) error = %v", err)
	}
	if got != ".zshrc" {
		t.Fatalf("trackedPathFromArg(abs) = %q, want .zshrc", got)
	}

	if _, err := trackedPathFromArg(rt, ""); err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("trackedPathFromArg(empty) error = %v, want required-path error", err)
	}
}

func TestHashFileAndFileRecord(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, ".config", "tool")
	writeUnitFile(t, path, "content\n", 0o755)

	gotHash, err := hashFile(path)
	if err != nil {
		t.Fatalf("hashFile() error = %v", err)
	}
	wantHash := sha256Hex("content\n")
	if gotHash != wantHash {
		t.Fatalf("hashFile() = %q, want %q", gotHash, wantHash)
	}

	record, err := fileRecord(root, ".config/tool")
	if err != nil {
		t.Fatalf("fileRecord() error = %v", err)
	}
	if record.Path != ".config/tool" || record.SHA256 != wantHash || record.Mode != 0o755 || record.Size != int64(len("content\n")) {
		t.Fatalf("fileRecord() = %+v, want path .config/tool hash %s mode 755 size %d", record, wantHash, len("content\n"))
	}
}

func TestFileModeFromRecord(t *testing.T) {
	t.Parallel()

	mode, err := fileModeFromRecord(0o755)
	if err != nil {
		t.Fatalf("fileModeFromRecord(0755) error = %v", err)
	}
	if mode != 0o755 {
		t.Fatalf("fileModeFromRecord(0755) = %o, want 0755", mode)
	}
	if _, err := fileModeFromRecord(0o1000); err == nil || !strings.Contains(err.Error(), "invalid file mode") {
		t.Fatalf("fileModeFromRecord(01000) error = %v, want invalid mode", err)
	}
}

func writeUnitFile(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create parent for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

func sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

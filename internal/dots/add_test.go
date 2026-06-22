package dots

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddRejectsConfiguredRepoPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	home := filepath.Join(root, "home")
	repo := filepath.Join(home, "dotfiles")
	rt := &Runtime{Home: home, Repo: repo, Profile: "personal"}
	for _, dir := range []string{home, profileDir(rt)} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	repoFile := filepath.Join(repo, "README.md")
	profileFile := filepath.Join(profileDir(rt), ".zshrc")
	writeUnitFile(t, repoFile, "repo\n", 0o644)
	writeUnitFile(t, profileFile, "profile\n", 0o644)

	for _, target := range []string{repo, repoFile, profileDir(rt), profileFile} {
		t.Run(target, func(t *testing.T) {
			_, err := addPath(rt, target)
			if err == nil || !strings.Contains(err.Error(), "refusing to add paths from the dots repo") {
				t.Fatalf("addPath(%q) error = %v, want repo-path refusal", target, err)
			}
		})
	}
}

func TestAddStillAcceptsHomePathsWhenRepoIsUnderHome(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	home := filepath.Join(root, "home")
	repo := filepath.Join(home, "dotfiles")
	rt := &Runtime{Home: home, Repo: repo, Profile: "personal"}
	if err := os.MkdirAll(profileDir(rt), 0o750); err != nil {
		t.Fatalf("create profile dir: %v", err)
	}
	source := filepath.Join(home, ".zshrc")
	writeUnitFile(t, source, "shell\n", 0o644)

	records, err := addPath(rt, source)
	if err != nil {
		t.Fatalf("addPath(home file) error = %v", err)
	}
	assertFileRecords(t, records, []FileRecord{testFileRecord(".zshrc", "shell\n")})
	copiedRecord, err := fileRecord(profileDir(rt), ".zshrc")
	if err != nil {
		t.Fatalf("fileRecord(copied file) error = %v", err)
	}
	assertFileRecords(t, []FileRecord{copiedRecord}, []FileRecord{testFileRecord(".zshrc", "shell\n")})
}

func TestPathInsideOrEqual(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(root, "child"), 0o750); err != nil {
		t.Fatalf("create root: %v", err)
	}
	siblingWithPrefix := filepath.Join(filepath.Dir(root), "repository", "file")

	cases := []struct {
		name   string
		target string
		want   bool
	}{
		{name: "same path", target: root, want: true},
		{name: "child", target: filepath.Join(root, "child"), want: true},
		{name: "nested missing child", target: filepath.Join(root, "child", "missing"), want: true},
		{name: "sibling with same prefix", target: siblingWithPrefix, want: false},
		{name: "parent", target: filepath.Dir(root), want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pathInsideOrEqual(root, tc.target)
			if err != nil {
				t.Fatalf("pathInsideOrEqual() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("pathInsideOrEqual(%q, %q) = %v, want %v", root, tc.target, got, tc.want)
			}
		})
	}
}

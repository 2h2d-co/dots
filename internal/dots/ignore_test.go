package dots

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotsIgnoreAndMatcher(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), ".dotsignore")
	content := "\n# comment\n/cache/\nnested/ignored.txt\n*.tmp\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write .dotsignore: %v", err)
	}

	matcher, err := loadDotsIgnore(path)
	if err != nil {
		t.Fatalf("loadDotsIgnore() error = %v", err)
	}

	cases := []struct {
		name  string
		rel   string
		isDir bool
		want  bool
	}{
		{name: "dotsignore itself is kept", rel: ".dotsignore", want: false},
		{name: "root directory pattern", rel: "cache", isDir: true, want: true},
		{name: "root directory child", rel: "cache/file", want: true},
		{name: "nested directory matched by basename when walking directory", rel: "nested/cache", isDir: true, want: true},
		{name: "explicit nested file", rel: "nested/ignored.txt", want: true},
		{name: "nested glob", rel: "nested/deeper/file.tmp", want: true},
		{name: "included nested file", rel: "nested/keep.txt", want: false},
		{name: "included root file", rel: "keep.txt", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := matcher.ignored(tc.rel, tc.isDir)
			if got != tc.want {
				t.Fatalf("matcher.ignored(%q, %v) = %v, want %v", tc.rel, tc.isDir, got, tc.want)
			}
		})
	}
}

func TestLoadDotsIgnoreMissingFile(t *testing.T) {
	t.Parallel()

	matcher, err := loadDotsIgnore(filepath.Join(t.TempDir(), ".dotsignore"))
	if err != nil {
		t.Fatalf("loadDotsIgnore(missing) error = %v", err)
	}
	if matcher.ignored("anything", false) {
		t.Fatal("missing .dotsignore should not ignore paths")
	}
}

package dots

import (
	"bytes"
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

func TestCollectAddPlanDoesNotCopy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "repo")
	rt := &Runtime{Home: home, Repo: repo, Profile: "personal"}
	sourceRoot := filepath.Join(home, ".config", "dryapp")
	writeUnitFile(t, filepath.Join(sourceRoot, ".dotsignore"), "ignored\n", 0o644)
	writeUnitFile(t, filepath.Join(sourceRoot, "keep"), "keep\n", 0o644)
	writeUnitFile(t, filepath.Join(sourceRoot, "ignored"), "ignored\n", 0o644)

	plan, err := collectAddPlan(rt, sourceRoot)
	if err != nil {
		t.Fatalf("collectAddPlan() error = %v", err)
	}
	records := make([]FileRecord, 0, len(plan.Items))
	for _, item := range plan.Items {
		records = append(records, item.Record)
	}
	assertFileRecords(t, records, []FileRecord{
		testFileRecord(".config/dryapp/.dotsignore", "ignored\n"),
		testFileRecord(".config/dryapp/keep", "keep\n"),
	})
	assertTrackedDirs(t, plan.TrackedDirs, []TrackedDirRecord{{Path: ".config/dryapp"}})
	if _, err := os.Stat(filepath.Join(repo, "personal", ".config", "dryapp", "keep")); !os.IsNotExist(err) {
		t.Fatalf("dry-run plan copied repo file, stat err = %v", err)
	}

	var out bytes.Buffer
	if err := writeAddPlan(&out, rt, plan); err != nil {
		t.Fatalf("writeAddPlan() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Add plan (dry run; no files changed):",
		"Directory roots:",
		"  .config/dryapp",
		"  .config/dryapp/.dotsignore",
		"  .config/dryapp/keep",
		"Would track 1 directory root(s)",
		"Would add 2 file(s) to profile personal",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("dry-run output missing %q\noutput:\n%s", want, got)
		}
	}
	if strings.Contains(got, ".config/dryapp/ignored") {
		t.Fatalf("dry-run output includes ignored path\noutput:\n%s", got)
	}
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

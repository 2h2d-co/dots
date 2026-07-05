package dots

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderDiffEntries(t *testing.T) {
	t.Parallel()

	entries := []diffEntry{
		{
			Path:       "bin/tool",
			NewContent: []byte("echo hi\n"),
			NewMode:    0o755,
		},
		{
			Path:       "mode-only",
			OldContent: []byte("same\n"),
			NewContent: []byte("same\n"),
			OldMode:    0o600,
			NewMode:    0o644,
			OldExists:  true,
		},
		{
			Path:       "notes",
			OldContent: []byte("old"),
			NewContent: []byte("new\n"),
			OldMode:    0o644,
			NewMode:    0o644,
			OldExists:  true,
		},
		{
			Path:       "blob",
			OldContent: []byte("old\x00blob"),
			NewContent: []byte("new\x00blob"),
			OldMode:    0o644,
			NewMode:    0o644,
			OldExists:  true,
		},
	}
	patch, err := renderDiffEntries(entries)
	if err != nil {
		t.Fatalf("renderDiffEntries() error = %v", err)
	}
	for _, want := range []string{
		"diff --git a/bin/tool b/bin/tool\n",
		"new file mode 100755\n",
		"--- /dev/null\n+++ b/bin/tool\n",
		"+echo hi\n",
		"diff --git a/mode-only b/mode-only\nold mode 100600\nnew mode 100644\n",
		"diff --git a/notes b/notes\n--- a/notes\n+++ b/notes\n",
		"-old\n\\ No newline at end of file\n+new\n",
		"diff --git a/blob b/blob\nBinary files a/blob and b/blob differ\n",
	} {
		if !strings.Contains(patch, want) {
			t.Fatalf("patch missing %q\npatch:\n%s", want, patch)
		}
	}
	modeStart := strings.Index(patch, "diff --git a/mode-only")
	nextStart := strings.Index(patch[modeStart+1:], "diff --git ")
	modeSegment := patch[modeStart:]
	if nextStart >= 0 {
		modeSegment = patch[modeStart : modeStart+1+nextStart]
	}
	if strings.Contains(modeSegment, "@@") {
		t.Fatalf("mode-only entry unexpectedly contains a hunk\npatch:\n%s", patch)
	}
}

func TestRenderDiffEntriesOmitsIdenticalEntry(t *testing.T) {
	t.Parallel()

	patch, err := renderDiffEntries([]diffEntry{{
		Path:       "same",
		OldContent: []byte("same\n"),
		NewContent: []byte("same\n"),
		OldMode:    0o644,
		NewMode:    0o644,
		OldExists:  true,
	}})
	if err != nil {
		t.Fatalf("renderDiffEntries() error = %v", err)
	}
	if patch != "" {
		t.Fatalf("renderDiffEntries() = %q, want empty", patch)
	}
}

func TestBuildApplyDiffPlanIncludesForceConflicts(t *testing.T) {
	t.Parallel()

	rt := newStatusTestRuntime(t)
	update := writeRepoTrackedFile(t, rt, "update", "repo\n")
	conflict := writeRepoTrackedFile(t, rt, "conflict", "repo conflict\n")
	writeDestinationFile(t, rt, "update", "home\n")
	writeDestinationFile(t, rt, "conflict", "home conflict\n")
	report := statusReport{
		Pending:  []statusItem{{Kind: kindPendingUpdate, Path: "update"}},
		Conflict: []statusItem{{Kind: kindConflictChanged, Path: "conflict"}},
	}

	plan, err := buildApplyDiffPlan(rt, report, []FileRecord{update, conflict})
	if err != nil {
		t.Fatalf("buildApplyDiffPlan() error = %v", err)
	}
	assertDiffEntryPaths(t, plan.Entries, []string{"conflict", "update"})
	assertDiffNoteContains(t, plan.Notes, "conflict", "dots apply --force")
}

func TestBuildSyncDiffPlanIncludesDestinationWinsAndTrackedRootCreates(t *testing.T) {
	t.Parallel()

	rt := newStatusTestRuntime(t)
	changed := writeRepoTrackedFile(t, rt, "changed", "repo\n")
	managed := writeRepoTrackedFile(t, rt, "managed", "repo managed\n")
	writeDestinationFile(t, rt, "changed", "home\n")
	writeDestinationFile(t, rt, "managed", "home managed\n")
	writeDestinationFile(t, rt, ".config/app/new", "new\n")
	report := statusReport{
		Directory: []statusItem{{Kind: kindDirectoryUntracked, Path: ".config/app/new"}},
		Pending:   []statusItem{{Kind: kindPendingCreate, Path: "missing"}},
		Conflict: []statusItem{
			{Kind: kindConflictChanged, Path: "changed"},
			{Kind: kindConflictManaged, Path: "managed"},
		},
	}

	plan, err := buildSyncDiffPlan(rt, report, []FileRecord{changed, managed})
	if err != nil {
		t.Fatalf("buildSyncDiffPlan() error = %v", err)
	}
	assertDiffEntryPaths(t, plan.Entries, []string{".config/app/new", "changed", "managed"})
	assertNoDiffNote(t, plan.Notes, "changed")
	assertDiffNoteContains(t, plan.Notes, "managed", "dots sync --force")
	assertDiffNoteContains(t, plan.Notes, "missing", "dots forget missing")
}

func TestDiffPagerResolutionAndGate(t *testing.T) {
	t.Setenv(pagerEnv, "")

	if got := resolveDiffPager(false, " env pager ", "config pager"); got != "env pager" {
		t.Fatalf("resolveDiffPager(env) = %q, want env pager", got)
	}
	if got := resolveDiffPager(false, "  ", " config pager "); got != "config pager" {
		t.Fatalf("resolveDiffPager(config) = %q, want config pager", got)
	}
	if got := resolveDiffPager(true, "env pager", "config pager"); got != "" {
		t.Fatalf("resolveDiffPager(no pager) = %q, want empty", got)
	}

	rt := &Runtime{Pager: "config pager"}
	var out bytes.Buffer
	called := false
	err := writeDiffPatch(&out, "patch", rt, diffOptions{
		stdoutIsTerminal: func() bool { return false },
		runPager: func(_ string, _ string) error {
			called = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("writeDiffPatch(non-tty) error = %v", err)
	}
	if called || out.String() != "patch" {
		t.Fatalf("non-tty output = %q, pager called = %v; want raw patch", out.String(), called)
	}

	out.Reset()
	called = false
	err = writeDiffPatch(&out, "patch", rt, diffOptions{
		stdoutIsTerminal: func() bool { return true },
		runPager: func(pager string, input string) error {
			called = true
			if pager != "config pager" || input != "patch" {
				t.Fatalf("pager args = %q, %q", pager, input)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("writeDiffPatch(tty) error = %v", err)
	}
	if !called || out.String() != "" {
		t.Fatalf("tty output = %q, pager called = %v; want pager only", out.String(), called)
	}

	called = false
	err = writeDiffPatch(&out, "", rt, diffOptions{
		stdoutIsTerminal: func() bool { return true },
		runPager: func(_ string, _ string) error {
			called = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("writeDiffPatch(empty) error = %v", err)
	}
	if called {
		t.Fatal("empty patch spawned pager")
	}
}

func TestRunShellPagerAllowsEarlyExit(t *testing.T) {
	if err := runShellPager("true", strings.Repeat("x", 1<<20)); err != nil {
		t.Fatalf("runShellPager(true) error = %v", err)
	}
}

func assertDiffEntryPaths(t *testing.T, entries []diffEntry, want []string) {
	t.Helper()
	if len(entries) != len(want) {
		t.Fatalf("diff entry count = %d, want %d; entries = %+v", len(entries), len(want), entries)
	}
	for i := range want {
		if entries[i].Path != want[i] {
			t.Fatalf("entry[%d].Path = %q, want %q; entries = %+v", i, entries[i].Path, want[i], entries)
		}
	}
}

func assertDiffNoteContains(t *testing.T, notes []diffNote, path string, want string) {
	t.Helper()
	for _, note := range notes {
		if note.Path == path && strings.Contains(note.Text, want) {
			return
		}
	}
	t.Fatalf("diff note for %s containing %q not found in %+v", path, want, notes)
}

func assertNoDiffNote(t *testing.T, notes []diffNote, path string) {
	t.Helper()
	for _, note := range notes {
		if note.Path == path {
			t.Fatalf("unexpected diff note for %s in %+v", path, notes)
		}
	}
}

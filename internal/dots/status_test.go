package dots

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeStatusClean(t *testing.T) {
	t.Parallel()

	rt := newStatusTestRuntime(t)
	record := writeRepoTrackedFile(t, rt, ".zshrc", "shell\n")
	writeDestinationFile(t, rt, ".zshrc", "shell\n")
	populateStatusDatabases(t, rt, []FileRecord{record}, []FileRecord{record})

	report, records, err := analyzeStatus(rt)
	if err != nil {
		t.Fatalf("analyzeStatus() error = %v", err)
	}
	if len(records) != 1 || records[0] != record {
		t.Fatalf("records = %+v, want %+v", records, []FileRecord{record})
	}
	if report.dirty() {
		t.Fatalf("report should be clean: %+v", report)
	}
	if report.TrackedFiles != 1 {
		t.Fatalf("TrackedFiles = %d, want 1", report.TrackedFiles)
	}
}

func TestAnalyzeStatusClassifiesChanges(t *testing.T) {
	t.Parallel()

	rt := newStatusTestRuntime(t)
	repoRecords := []FileRecord{
		testFileRecord("repo-missing", "missing\n"),
		testFileRecord("repo-modified", "old\n"),
		writeRepoTrackedFile(t, rt, "pending-create", "create\n"),
		writeRepoTrackedFile(t, rt, "pending-adopt", "adopt\n"),
		writeRepoTrackedFile(t, rt, "conflict-unmanaged", "repo\n"),
		writeRepoTrackedFile(t, rt, "pending-update", "new\n"),
		writeRepoTrackedFile(t, rt, "conflict-changed", "old\n"),
		writeRepoTrackedFile(t, rt, "conflict-diverged", "new\n"),
		writeRepoTrackedFile(t, rt, "conflict-type", "file\n"),
		writeRepoTrackedFile(t, rt, "pending-state", "new\n"),
	}
	writeUnitFile(t, repoFilePath(rt, "repo-modified"), "current\n", 0o644)
	writeUnitFile(t, repoFilePath(rt, "repo-untracked"), "untracked\n", 0o644)

	writeDestinationFile(t, rt, "pending-adopt", "adopt\n")
	writeDestinationFile(t, rt, "conflict-unmanaged", "different\n")
	writeDestinationFile(t, rt, "pending-update", "old\n")
	writeDestinationFile(t, rt, "conflict-changed", "user\n")
	writeDestinationFile(t, rt, "conflict-diverged", "user\n")
	if err := os.MkdirAll(destinationPath(rt, "conflict-type"), 0o750); err != nil {
		t.Fatalf("create destination directory conflict: %v", err)
	}
	writeDestinationFile(t, rt, "pending-state", "new\n")

	stateRecords := []FileRecord{
		testFileRecord("pending-update", "old\n"),
		testFileRecord("conflict-changed", "old\n"),
		testFileRecord("conflict-diverged", "old\n"),
		testFileRecord("pending-state", "old\n"),
		testFileRecord("stale-state", "stale\n"),
	}
	populateStatusDatabases(t, rt, repoRecords, stateRecords)

	report, _, err := analyzeStatus(rt)
	if err != nil {
		t.Fatalf("analyzeStatus() error = %v", err)
	}
	if !report.dirty() {
		t.Fatal("report should be dirty")
	}
	if !report.hasRepoDrift() {
		t.Fatalf("report should have repo drift: %+v", report)
	}
	if !report.hasConflicts() {
		t.Fatalf("report should have conflicts: %+v", report)
	}

	assertStatusItem(t, report.Repo, kindRepoMissing, "repo-missing")
	assertStatusItem(t, report.Repo, kindRepoModified, "repo-modified")
	assertStatusItem(t, report.Repo, kindRepoUntracked, "repo-untracked")
	assertStatusItem(t, report.Pending, kindPendingCreate, "pending-create")
	assertStatusItem(t, report.Pending, kindPendingAdopt, "pending-adopt")
	assertStatusItem(t, report.Pending, kindPendingUpdate, "pending-update")
	assertStatusItem(t, report.Pending, kindPendingState, "pending-state")
	assertStatusItem(t, report.Conflict, kindConflictManaged, "conflict-unmanaged")
	assertStatusItem(t, report.Conflict, kindConflictChanged, "conflict-changed")
	assertStatusItem(t, report.Conflict, kindConflictDiverged, "conflict-diverged")
	assertStatusItem(t, report.Conflict, kindConflictType, "conflict-type")
	assertStatusItem(t, report.State, kindStaleState, "stale-state")
}

func TestAnalyzeStatusSkipsGitIgnoredUntrackedProfileFiles(t *testing.T) {
	t.Parallel()

	rt := newStatusTestRuntime(t)
	tracked := writeRepoTrackedFile(t, rt, ".config/app/tracked.pyc", "old\n")
	writeUnitFile(t, filepath.Join(rt.Repo, ".gitignore"), "__pycache__/\n*.py[cod]\n", 0o644)
	writeUnitFile(t, repoFilePath(rt, ".config/app/tracked.pyc"), "new\n", 0o644)
	writeUnitFile(t, repoFilePath(rt, ".config/app/__pycache__/module.cache"), "ignored directory\n", 0o644)
	writeUnitFile(t, repoFilePath(rt, ".config/app/script.pyc"), "ignored file\n", 0o644)
	writeUnitFile(t, repoFilePath(rt, ".config/app/new.txt"), "visible\n", 0o644)
	populateStatusDatabases(t, rt, []FileRecord{tracked}, nil)

	report, _, err := analyzeStatus(rt)
	if err != nil {
		t.Fatalf("analyzeStatus() error = %v", err)
	}

	assertStatusItem(t, report.Repo, kindRepoModified, ".config/app/tracked.pyc")
	assertStatusItem(t, report.Repo, kindRepoUntracked, ".config/app/new.txt")
	assertNoStatusItem(t, report.Repo, kindRepoUntracked, ".config/app/__pycache__/module.cache")
	assertNoStatusItem(t, report.Repo, kindRepoUntracked, ".config/app/script.pyc")
}

func TestAnalyzeStatusReportsUntrackedFilesInTrackedDirectories(t *testing.T) {
	t.Parallel()

	rt := newStatusTestRuntime(t)
	repoRecords := []FileRecord{
		writeRepoTrackedFile(t, rt, ".config/app/.dotsignore", "ignored\ncache/\n*.tmp\n"),
		writeRepoTrackedFile(t, rt, ".config/app/keep", "keep\n"),
	}
	writeDestinationFile(t, rt, ".config/app/.dotsignore", "ignored\ncache/\n*.tmp\n")
	writeDestinationFile(t, rt, ".config/app/keep", "keep\n")
	writeDestinationFile(t, rt, ".config/app/new", "new\n")
	writeDestinationFile(t, rt, ".config/app/nested/new", "nested\n")
	writeDestinationFile(t, rt, ".config/app/ignored", "ignored\n")
	writeDestinationFile(t, rt, ".config/app/cache/ignored", "cache\n")
	writeDestinationFile(t, rt, ".config/app/ignored.tmp", "tmp\n")
	populateStatusDatabases(t, rt, repoRecords, repoRecords)
	populateTrackedDirs(t, rt, []TrackedDirRecord{{Path: ".config/app"}})

	report, _, err := analyzeStatus(rt)
	if err != nil {
		t.Fatalf("analyzeStatus() error = %v", err)
	}

	assertStatusItem(t, report.Directory, kindDirectoryUntracked, ".config/app/nested/new")
	assertStatusItem(t, report.Directory, kindDirectoryUntracked, ".config/app/new")
	assertNoStatusItem(t, report.Directory, kindDirectoryUntracked, ".config/app/ignored")
	assertNoStatusItem(t, report.Directory, kindDirectoryUntracked, ".config/app/cache/ignored")
	assertNoStatusItem(t, report.Directory, kindDirectoryUntracked, ".config/app/ignored.tmp")
}

func TestWriteStatusReportCleanSummary(t *testing.T) {
	t.Parallel()

	report := statusReport{
		Profile:      "personal",
		TrackedFiles: 42,
		TrackedDirs: []TrackedDirRecord{
			{Path: ".config/git"},
			{Path: ".config/mise"},
			{Path: ".config/npm"},
			{Path: ".config/zellij"},
			{Path: ".ssh"},
		},
	}

	var out bytes.Buffer
	if err := writeStatusReport(&out, report); err != nil {
		t.Fatalf("writeStatusReport() error = %v", err)
	}
	want := "Profile: personal\n" +
		"Status: clean\n" +
		"\n" +
		"Checked:\n" +
		"  Repo index: current\n" +
		"  Home destinations: current\n" +
		"  Tracked roots: no untracked files\n" +
		"  Apply state: current\n" +
		"\n" +
		"Tracked:\n" +
		"  Files: 42\n" +
		"  Roots: 5\n"
	if out.String() != want {
		t.Fatalf("writeStatusReport() = %q, want %q", out.String(), want)
	}
}

func TestWriteStatusReportCleanSummaryWithNoTrackedRoots(t *testing.T) {
	t.Parallel()

	report := statusReport{Profile: "personal", TrackedFiles: 1}

	var out bytes.Buffer
	if err := writeStatusReport(&out, report); err != nil {
		t.Fatalf("writeStatusReport() error = %v", err)
	}
	want := "Profile: personal\n" +
		"Status: clean\n" +
		"\n" +
		"Checked:\n" +
		"  Repo index: current\n" +
		"  Home destinations: current\n" +
		"  Tracked roots: none configured\n" +
		"  Apply state: current\n" +
		"\n" +
		"Tracked:\n" +
		"  Files: 1\n" +
		"  Roots: 0\n"
	if out.String() != want {
		t.Fatalf("writeStatusReport() = %q, want %q", out.String(), want)
	}
}

func TestWriteStatusReportGroupsByMostSpecificTrackedRoot(t *testing.T) {
	t.Parallel()

	report := statusReport{
		Profile: "personal",
		TrackedDirs: []TrackedDirRecord{
			{Path: ".config/nvim"},
			{Path: ".config"},
		},
		Directory: []statusItem{
			{Kind: kindDirectoryUntracked, Path: ".config/nvim/lua/new"},
			{Kind: kindDirectoryUntracked, Path: ".config/alacritty/new"},
		},
		Pending: []statusItem{{Kind: kindPendingCreate, Path: ".zshrc"}},
	}
	report.sort()

	var out bytes.Buffer
	if err := writeStatusReport(&out, report); err != nil {
		t.Fatalf("writeStatusReport() error = %v", err)
	}
	want := "Profile: personal\n" +
		"Status: changes require attention\n" +
		"\n" +
		"Tracked root: .config\n" +
		"  Directory drift:\n" +
		"    untracked destination file: .config/alacritty/new\n" +
		"\n" +
		"Tracked root: .config/nvim\n" +
		"  Directory drift:\n" +
		"    untracked destination file: .config/nvim/lua/new\n" +
		"\n" +
		"Individual paths:\n" +
		"  Pending changes:\n" +
		"    will create: .zshrc\n"
	if out.String() != want {
		t.Fatalf("writeStatusReport() = %q, want %q", out.String(), want)
	}
}

func TestAnalyzeStatusReturnsDestinationPermissionError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission errors are not reliable when running as root")
	}

	rt := newStatusTestRuntime(t)
	record := writeRepoTrackedFile(t, rt, "locked/file", "repo\n")
	lockedDir := filepath.Join(rt.Home, "locked")
	if err := os.MkdirAll(lockedDir, 0o750); err != nil {
		t.Fatalf("create locked directory: %v", err)
	}
	if err := os.Chmod(lockedDir, 0); err != nil {
		t.Fatalf("lock destination directory: %v", err)
	}
	populateStatusDatabases(t, rt, []FileRecord{record}, nil)

	_, _, err := analyzeStatus(rt)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("analyzeStatus() error = %v, want permission error", err)
	}
}

func newStatusTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	root := t.TempDir()
	rt := &Runtime{
		Repo:     filepath.Join(root, "repo"),
		Profile:  "personal",
		Home:     filepath.Join(root, "home"),
		StateDir: filepath.Join(root, "state"),
	}
	for _, dir := range []string{profileDir(rt), rt.Home, rt.StateDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	return rt
}

func writeRepoTrackedFile(t *testing.T, rt *Runtime, trackedPath string, content string) FileRecord {
	t.Helper()
	writeUnitFile(t, repoFilePath(rt, trackedPath), content, 0o644)
	return testFileRecord(trackedPath, content)
}

func writeDestinationFile(t *testing.T, rt *Runtime, trackedPath string, content string) {
	t.Helper()
	writeUnitFile(t, destinationPath(rt, trackedPath), content, 0o644)
}

func populateStatusDatabases(t *testing.T, rt *Runtime, repoRecords []FileRecord, stateRecords []FileRecord) {
	t.Helper()
	repoDB, err := openRepoDB(rt.Repo, rt.Profile)
	if err != nil {
		t.Fatalf("openRepoDB() error = %v", err)
	}
	stateDB, err := openStateDB(rt.StateDir, rt.Profile)
	if err != nil {
		t.Fatalf("openStateDB() error = %v", errors.Join(err, repoDB.Close()))
	}
	if err := replaceRepoRecords(repoDB, repoRecords); err != nil {
		t.Fatalf("replaceRepoRecords() error = %v", errors.Join(err, repoDB.Close(), stateDB.Close()))
	}
	if err := replaceStateRecords(stateDB, stateRecords); err != nil {
		t.Fatalf("replaceStateRecords() error = %v", errors.Join(err, repoDB.Close(), stateDB.Close()))
	}
	if err := errors.Join(repoDB.Close(), stateDB.Close()); err != nil {
		t.Fatalf("close status databases: %v", err)
	}
}

func populateTrackedDirs(t *testing.T, rt *Runtime, dirs []TrackedDirRecord) {
	t.Helper()
	repoDB, err := openRepoDB(rt.Repo, rt.Profile)
	if err != nil {
		t.Fatalf("openRepoDB() error = %v", err)
	}
	if err := upsertTrackedDirs(repoDB, dirs); err != nil {
		t.Fatalf("upsertTrackedDirs() error = %v", errors.Join(err, repoDB.Close()))
	}
	if err := repoDB.Close(); err != nil {
		t.Fatalf("close repo database: %v", err)
	}
}

func testFileRecord(path string, content string) FileRecord {
	return FileRecord{
		Path:   path,
		SHA256: sha256Hex(content),
		Mode:   0o644,
		Size:   int64(len(content)),
	}
}

func assertStatusItem(t *testing.T, items []statusItem, kind statusKind, path string) {
	t.Helper()
	for _, item := range items {
		if item.Kind == kind && item.Path == path {
			return
		}
	}
	t.Fatalf("status item %s: %s not found in %+v", kind, path, items)
}

func assertNoStatusItem(t *testing.T, items []statusItem, kind statusKind, path string) {
	t.Helper()
	for _, item := range items {
		if item.Kind == kind && item.Path == path {
			t.Fatalf("status item %s: %s unexpectedly found in %+v", kind, path, items)
		}
	}
}

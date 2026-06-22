package dots

import (
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
		writeRepoTrackedFile(t, rt, "conflict-changed", "new\n"),
		writeRepoTrackedFile(t, rt, "conflict-type", "file\n"),
		writeRepoTrackedFile(t, rt, "pending-state", "new\n"),
	}
	writeUnitFile(t, repoFilePath(rt, "repo-modified"), "current\n", 0o644)
	writeUnitFile(t, repoFilePath(rt, "repo-untracked"), "untracked\n", 0o644)

	writeDestinationFile(t, rt, "pending-adopt", "adopt\n")
	writeDestinationFile(t, rt, "conflict-unmanaged", "different\n")
	writeDestinationFile(t, rt, "pending-update", "old\n")
	writeDestinationFile(t, rt, "conflict-changed", "user\n")
	if err := os.MkdirAll(destinationPath(rt, "conflict-type"), 0o750); err != nil {
		t.Fatalf("create destination directory conflict: %v", err)
	}
	writeDestinationFile(t, rt, "pending-state", "new\n")

	stateRecords := []FileRecord{
		testFileRecord("pending-update", "old\n"),
		testFileRecord("conflict-changed", "old\n"),
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
	assertStatusItem(t, report.Conflict, kindConflictType, "conflict-type")
	assertStatusItem(t, report.State, kindStaleState, "stale-state")
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

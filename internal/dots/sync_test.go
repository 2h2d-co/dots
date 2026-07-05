package dots

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSyncPlanMapping(t *testing.T) {
	t.Parallel()

	records := []FileRecord{
		testFileRecord("changed", "repo\n"),
		testFileRecord("diverged", "repo\n"),
		testFileRecord("managed", "repo\n"),
		testFileRecord("adopt", "repo\n"),
		testFileRecord("state", "repo\n"),
		testFileRecord("update", "repo\n"),
	}
	report := statusReport{
		Conflict: []statusItem{
			{Kind: kindConflictDiverged, Path: "diverged"},
			{Kind: kindConflictChanged, Path: "changed"},
			{Kind: kindConflictManaged, Path: "managed"},
			{Kind: kindConflictType, Path: "type"},
		},
		Directory: []statusItem{
			{Kind: kindDirectoryRootConflict, Path: ".config/root"},
			{Kind: kindDirectoryUntracked, Path: ".config/root/new"},
			{Kind: kindDirectoryUnsupported, Path: ".config/root/socket"},
		},
		Pending: []statusItem{
			{Kind: kindPendingAdopt, Path: "adopt"},
			{Kind: kindPendingState, Path: "state"},
			{Kind: kindPendingCreate, Path: "missing"},
			{Kind: kindPendingUpdate, Path: "update"},
		},
		State: []statusItem{{Kind: kindStaleState, Path: "stale"}},
	}

	plain, err := buildSyncPlan(report, records, false)
	if err != nil {
		t.Fatalf("buildSyncPlan(plain) error = %v", err)
	}
	assertSyncItemPaths(t, plain.Updates, []string{"changed"})
	assertSyncItemPaths(t, plain.Adds, []string{".config/root/new"})
	assertSyncItemPaths(t, plain.StateOnly, []string{"adopt", "state"})
	assertSyncItemPaths(t, plain.Conflicts, []string{"diverged", "managed"})
	assertSyncItemPaths(t, plain.Omitted, []string{"stale", "update"})
	assertSyncNoteContains(t, plain.Notes, "missing", "dots forget missing")
	assertSyncNoteContains(t, plain.Notes, "type", "destination is not a regular file")
	assertSyncNoteContains(t, plain.Notes, ".config/root", "tracked directory is not a directory")
	assertSyncNoteContains(t, plain.Notes, ".config/root/socket", "untracked destination is not regular")

	forced, err := buildSyncPlan(report, records, true)
	if err != nil {
		t.Fatalf("buildSyncPlan(force) error = %v", err)
	}
	assertSyncItemPaths(t, forced.Updates, []string{"changed", "diverged", "managed"})
	if !forced.Updates[1].RequiresForce || !forced.Updates[2].RequiresForce {
		t.Fatalf("forced conflict updates = %+v, want RequiresForce", forced.Updates)
	}
	assertSyncItemPaths(t, forced.Conflicts, nil)
}

func TestSyncProfileCopiesDestinationAddsNewFilesAndRefreshesState(t *testing.T) {
	t.Parallel()

	rt := newStatusTestRuntime(t)
	changed := writeRepoTrackedFile(t, rt, "changed", "old\n")
	base := writeRepoTrackedFile(t, rt, ".config/app/base", "base\n")
	adopt := writeRepoTrackedFile(t, rt, "adopt", "adopt\n")
	writeDestinationFile(t, rt, "changed", "old\n")
	writeDestinationFile(t, rt, ".config/app/base", "base\n")
	writeDestinationFile(t, rt, "adopt", "adopt\n")
	populateStatusDatabases(t, rt, []FileRecord{changed, base, adopt}, []FileRecord{changed, base})
	populateTrackedDirs(t, rt, []TrackedDirRecord{{Path: ".config/app"}})

	writeUnitFile(t, destinationPath(rt, "changed"), "home\n", 0o600)
	writeDestinationFile(t, rt, ".config/app/new", "new\n")

	var out bytes.Buffer
	if err := syncProfile(rt, syncOptions{}, &out); err != nil {
		t.Fatalf("syncProfile() error = %v", err)
	}
	if !strings.Contains(out.String(), "copied 2 file(s), recorded state for 3 file(s)") {
		t.Fatalf("sync output = %q, want copy/state summary", out.String())
	}

	assertFileContent(t, repoFilePath(rt, "changed"), "home\n")
	assertFileContent(t, repoFilePath(rt, ".config/app/new"), "new\n")
	changedRecord, err := fileRecord(profileDir(rt), "changed")
	if err != nil {
		t.Fatalf("fileRecord(changed) error = %v", err)
	}
	if changedRecord.Mode != 0o600 {
		t.Fatalf("synced mode = %o, want 600", changedRecord.Mode)
	}
	newRecord, err := fileRecord(profileDir(rt), ".config/app/new")
	if err != nil {
		t.Fatalf("fileRecord(new) error = %v", err)
	}

	repoDB, err := openRepoDB(rt.Repo, rt.Profile)
	if err != nil {
		t.Fatalf("openRepoDB() error = %v", err)
	}
	repoRecords, err := listRepoRecords(repoDB)
	if err != nil {
		t.Fatalf("listRepoRecords() error = %v", errors.Join(err, repoDB.Close()))
	}
	if err := repoDB.Close(); err != nil {
		t.Fatalf("close repo database: %v", err)
	}
	assertFileRecords(t, repoRecords, []FileRecord{base, newRecord, adopt, changedRecord})

	stateDB, err := openStateDB(rt.StateDir, rt.Profile)
	if err != nil {
		t.Fatalf("openStateDB() error = %v", err)
	}
	stateRecords, err := listStateRecords(stateDB)
	if err != nil {
		t.Fatalf("listStateRecords() error = %v", errors.Join(err, stateDB.Close()))
	}
	if err := stateDB.Close(); err != nil {
		t.Fatalf("close state database: %v", err)
	}
	assertStateRecords(t, stateRecords, []StateRecord{
		{Path: ".config/app/base", SHA256: base.SHA256, RepoSHA: base.SHA256, Mode: base.Mode, Size: base.Size},
		{Path: ".config/app/new", SHA256: newRecord.SHA256, RepoSHA: newRecord.SHA256, Mode: newRecord.Mode, Size: newRecord.Size},
		{Path: "adopt", SHA256: adopt.SHA256, RepoSHA: adopt.SHA256, Mode: adopt.Mode, Size: adopt.Size},
		{Path: "changed", SHA256: changedRecord.SHA256, RepoSHA: changedRecord.SHA256, Mode: changedRecord.Mode, Size: changedRecord.Size},
	})

	report, _, err := analyzeStatus(rt)
	if err != nil {
		t.Fatalf("analyzeStatus() error = %v", err)
	}
	if report.dirty() {
		t.Fatalf("status after sync should be clean: %+v", report)
	}
}

func TestSyncProfileConflictAbortLeavesFilesUntouched(t *testing.T) {
	t.Parallel()

	rt := newStatusTestRuntime(t)
	state := writeRepoTrackedFile(t, rt, ".zshrc", "base\n")
	writeDestinationFile(t, rt, ".zshrc", "home\n")
	writeUnitFile(t, repoFilePath(rt, ".zshrc"), "repo\n", 0o644)
	repoRecord, err := fileRecord(profileDir(rt), ".zshrc")
	if err != nil {
		t.Fatalf("fileRecord(repo) error = %v", err)
	}
	populateStatusDatabases(t, rt, []FileRecord{repoRecord}, []FileRecord{state})

	var out bytes.Buffer
	err = syncProfile(rt, syncOptions{}, &out)
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || !exitErr.Silent {
		t.Fatalf("syncProfile() error = %v, want silent exit 1", err)
	}
	if !strings.Contains(out.String(), "Sync aborted: destination conflicts found") {
		t.Fatalf("sync output = %q, want abort", out.String())
	}
	assertFileContent(t, repoFilePath(rt, ".zshrc"), "repo\n")
	assertFileContent(t, destinationPath(rt, ".zshrc"), "home\n")
	assertFileMissing(t, filepath.Join(rt.StateDir, "backups"))
}

func TestSyncProfileForceBacksUpRepoAndTakesDestination(t *testing.T) {
	t.Parallel()

	rt := newStatusTestRuntime(t)
	state := writeRepoTrackedFile(t, rt, ".zshrc", "base\n")
	writeDestinationFile(t, rt, ".zshrc", "home\n")
	writeUnitFile(t, repoFilePath(rt, ".zshrc"), "repo\n", 0o644)
	repoRecord, err := fileRecord(profileDir(rt), ".zshrc")
	if err != nil {
		t.Fatalf("fileRecord(repo) error = %v", err)
	}
	populateStatusDatabases(t, rt, []FileRecord{repoRecord}, []FileRecord{state})

	var out bytes.Buffer
	if err := syncProfile(rt, syncOptions{Force: true}, &out); err != nil {
		t.Fatalf("syncProfile(force) error = %v", err)
	}
	if !strings.Contains(out.String(), "Backups written to:") {
		t.Fatalf("sync output = %q, want backup path", out.String())
	}
	assertFileContent(t, repoFilePath(rt, ".zshrc"), "home\n")
	assertRepoBackupPayload(t, rt, ".zshrc", "repo\n")

	syncedRecord, err := fileRecord(profileDir(rt), ".zshrc")
	if err != nil {
		t.Fatalf("fileRecord(synced) error = %v", err)
	}
	repoDB, err := openRepoDB(rt.Repo, rt.Profile)
	if err != nil {
		t.Fatalf("openRepoDB() error = %v", err)
	}
	repoRecords, err := listRepoRecords(repoDB)
	if err != nil {
		t.Fatalf("listRepoRecords() error = %v", errors.Join(err, repoDB.Close()))
	}
	if err := repoDB.Close(); err != nil {
		t.Fatalf("close repo database: %v", err)
	}
	assertFileRecords(t, repoRecords, []FileRecord{syncedRecord})
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()
	gotHash, err := hashFile(path)
	if err != nil {
		t.Fatalf("hash %s: %v", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if gotHash != sha256Hex(want) || info.Size() != int64(len(want)) {
		t.Fatalf("%s content hash/size = %s/%d, want %s/%d", path, gotHash, info.Size(), sha256Hex(want), len(want))
	}
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be missing, stat err = %v", path, err)
	}
}

func assertSyncItemPaths(t *testing.T, items []syncPlanItem, want []string) {
	t.Helper()
	if len(items) != len(want) {
		t.Fatalf("sync item count = %d, want %d; items = %+v", len(items), len(want), items)
	}
	for i := range want {
		if items[i].Path != want[i] {
			t.Fatalf("item[%d].Path = %q, want %q; items = %+v", i, items[i].Path, want[i], items)
		}
	}
}

func assertSyncNoteContains(t *testing.T, notes []syncPlanNote, path string, want string) {
	t.Helper()
	for _, note := range notes {
		if note.Path == path && strings.Contains(note.Text, want) {
			return
		}
	}
	t.Fatalf("sync note for %s containing %q not found in %+v", path, want, notes)
}

func assertRepoBackupPayload(t *testing.T, rt *Runtime, trackedPath string, want string) {
	t.Helper()
	sets, err := os.ReadDir(filepath.Join(rt.StateDir, "backups", rt.Profile))
	if err != nil {
		t.Fatalf("read backup sets: %v", err)
	}
	if len(sets) != 1 {
		t.Fatalf("backup set count = %d, want 1", len(sets))
	}
	entry := base64.RawURLEncoding.EncodeToString([]byte(trackedPath))
	payload := filepath.Join(rt.StateDir, "backups", rt.Profile, sets[0].Name(), backupOriginRepo, entry, backupPayload)
	gotHash, err := hashFile(payload)
	if err != nil {
		t.Fatalf("hash repo backup payload: %v", err)
	}
	info, err := os.Stat(payload)
	if err != nil {
		t.Fatalf("stat repo backup payload: %v", err)
	}
	if gotHash != sha256Hex(want) || info.Size() != int64(len(want)) {
		t.Fatalf("repo backup hash/size = %s/%d, want %s/%d", gotHash, info.Size(), sha256Hex(want), len(want))
	}
}

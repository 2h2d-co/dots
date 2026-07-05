package dots

import (
	"database/sql"
	"strings"
	"testing"
)

func TestRepoDBCataloging(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	if err := ensureRepoDB(repo, "personal"); err != nil {
		t.Fatalf("ensureRepoDB() error = %v", err)
	}
	db, err := openRepoDB(repo, "personal")
	if err != nil {
		t.Fatalf("openRepoDB() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var profile string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'profile'`).Scan(&profile); err != nil {
		t.Fatalf("read profile metadata: %v", err)
	}
	if profile != "personal" {
		t.Fatalf("profile metadata = %q, want personal", profile)
	}
	assertGooseVersion(t, db, 2)

	if err := upsertTrackedDirs(db, []TrackedDirRecord{{Path: "dir/app"}, {Path: "dir"}}); err != nil {
		t.Fatalf("upsertTrackedDirs() error = %v", err)
	}
	dirs, err := listTrackedDirs(db)
	if err != nil {
		t.Fatalf("listTrackedDirs() error = %v", err)
	}
	assertTrackedDirs(t, dirs, []TrackedDirRecord{{Path: "dir"}, {Path: "dir/app"}})

	initial := []FileRecord{
		{Path: "b", SHA256: "sha-b", Mode: 0o644, Size: 2},
		{Path: "a", SHA256: "sha-a", Mode: 0o755, Size: 1},
	}
	if err := upsertRepoRecords(db, initial); err != nil {
		t.Fatalf("upsertRepoRecords(initial) error = %v", err)
	}

	records, err := listRepoRecords(db)
	if err != nil {
		t.Fatalf("listRepoRecords() error = %v", err)
	}
	assertFileRecords(t, records, []FileRecord{
		{Path: "a", SHA256: "sha-a", Mode: 0o755, Size: 1},
		{Path: "b", SHA256: "sha-b", Mode: 0o644, Size: 2},
	})

	if err := upsertRepoRecords(db, []FileRecord{{Path: "a", SHA256: "sha-a2", Mode: 0o600, Size: 3}}); err != nil {
		t.Fatalf("upsertRepoRecords(update) error = %v", err)
	}
	records, err = listRepoRecords(db)
	if err != nil {
		t.Fatalf("listRepoRecords(updated) error = %v", err)
	}
	assertFileRecords(t, records, []FileRecord{
		{Path: "a", SHA256: "sha-a2", Mode: 0o600, Size: 3},
		{Path: "b", SHA256: "sha-b", Mode: 0o644, Size: 2},
	})

	if err := replaceRepoRecords(db, []FileRecord{{Path: "c", SHA256: "sha-c", Mode: 0o700, Size: 4}}); err != nil {
		t.Fatalf("replaceRepoRecords() error = %v", err)
	}
	records, err = listRepoRecords(db)
	if err != nil {
		t.Fatalf("listRepoRecords(replaced) error = %v", err)
	}
	assertFileRecords(t, records, []FileRecord{{Path: "c", SHA256: "sha-c", Mode: 0o700, Size: 4}})
	dirs, err = listTrackedDirs(db)
	if err != nil {
		t.Fatalf("listTrackedDirs(after replace records) error = %v", err)
	}
	assertTrackedDirs(t, dirs, []TrackedDirRecord{{Path: "dir"}, {Path: "dir/app"}})
}

func TestStateDBCatalogingAndForget(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	state := t.TempDir()
	repoDB, err := openRepoDB(repo, "personal")
	if err != nil {
		t.Fatalf("openRepoDB() error = %v", err)
	}
	defer func() { _ = repoDB.Close() }()
	stateDB, err := openStateDB(state, "personal")
	if err != nil {
		t.Fatalf("openStateDB() error = %v", err)
	}
	defer func() { _ = stateDB.Close() }()
	assertGooseVersion(t, stateDB, 1)

	records := []FileRecord{
		{Path: "dir/file", SHA256: "sha-file", Mode: 0o644, Size: 4},
		{Path: "dir/child/file", SHA256: "sha-child", Mode: 0o600, Size: 5},
		{Path: "keep", SHA256: "sha-keep", Mode: 0o755, Size: 6},
	}
	if err := replaceRepoRecords(repoDB, records); err != nil {
		t.Fatalf("replaceRepoRecords() error = %v", err)
	}
	if err := replaceStateRecords(stateDB, records); err != nil {
		t.Fatalf("replaceStateRecords() error = %v", err)
	}
	if err := upsertTrackedDirs(repoDB, []TrackedDirRecord{{Path: "dir"}, {Path: "keepdir"}}); err != nil {
		t.Fatalf("upsertTrackedDirs() error = %v", err)
	}

	stateRecords, err := listStateRecords(stateDB)
	if err != nil {
		t.Fatalf("listStateRecords() error = %v", err)
	}
	assertStateRecords(t, stateRecords, []StateRecord{
		{Path: "dir/child/file", SHA256: "sha-child", RepoSHA: "sha-child", Mode: 0o600, Size: 5},
		{Path: "dir/file", SHA256: "sha-file", RepoSHA: "sha-file", Mode: 0o644, Size: 4},
		{Path: "keep", SHA256: "sha-keep", RepoSHA: "sha-keep", Mode: 0o755, Size: 6},
	})

	if err := forgetRecords(repoDB, stateDB, []string{"dir"}); err != nil {
		t.Fatalf("forgetRecords() error = %v", err)
	}
	repoRecords, err := listRepoRecords(repoDB)
	if err != nil {
		t.Fatalf("listRepoRecords(after forget) error = %v", err)
	}
	assertFileRecords(t, repoRecords, []FileRecord{{Path: "keep", SHA256: "sha-keep", Mode: 0o755, Size: 6}})
	stateRecords, err = listStateRecords(stateDB)
	if err != nil {
		t.Fatalf("listStateRecords(after forget) error = %v", err)
	}
	assertStateRecords(t, stateRecords, []StateRecord{{Path: "keep", SHA256: "sha-keep", RepoSHA: "sha-keep", Mode: 0o755, Size: 6}})
	dirs, err := listTrackedDirs(repoDB)
	if err != nil {
		t.Fatalf("listTrackedDirs(after forget) error = %v", err)
	}
	assertTrackedDirs(t, dirs, []TrackedDirRecord{{Path: "keepdir"}})
}

func TestUpsertStateRecordsRefreshesSelectedPaths(t *testing.T) {
	t.Parallel()

	state := t.TempDir()
	stateDB, err := openStateDB(state, "personal")
	if err != nil {
		t.Fatalf("openStateDB() error = %v", err)
	}
	defer func() { _ = stateDB.Close() }()

	initial := []FileRecord{
		{Path: "a", SHA256: "sha-a", Mode: 0o644, Size: 1},
		{Path: "b", SHA256: "sha-b", Mode: 0o755, Size: 2},
	}
	if err := replaceStateRecords(stateDB, initial); err != nil {
		t.Fatalf("replaceStateRecords() error = %v", err)
	}
	if err := upsertStateRecords(stateDB, []FileRecord{{Path: "a", SHA256: "sha-a2", Mode: 0o600, Size: 3}}); err != nil {
		t.Fatalf("upsertStateRecords() error = %v", err)
	}

	stateRecords, err := listStateRecords(stateDB)
	if err != nil {
		t.Fatalf("listStateRecords() error = %v", err)
	}
	assertStateRecords(t, stateRecords, []StateRecord{
		{Path: "a", SHA256: "sha-a2", RepoSHA: "sha-a2", Mode: 0o600, Size: 3},
		{Path: "b", SHA256: "sha-b", RepoSHA: "sha-b", Mode: 0o755, Size: 2},
	})
}

func TestOpenDBRejectsMismatchedProfileMetadata(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	if err := ensureRepoDB(repo, "personal"); err != nil {
		t.Fatalf("ensureRepoDB() error = %v", err)
	}
	setProfileMetadata(t, repoDBPath(repo, "personal"), "work")
	if db, err := openRepoDB(repo, "personal"); err == nil {
		_ = db.Close()
		t.Fatal("openRepoDB() error = nil, want profile mismatch")
	} else if !strings.Contains(err.Error(), `database belongs to profile "work", not "personal"`) {
		t.Fatalf("openRepoDB() error = %v, want profile mismatch", err)
	}

	state := t.TempDir()
	if err := ensureStateDB(state, "personal"); err != nil {
		t.Fatalf("ensureStateDB() error = %v", err)
	}
	setProfileMetadata(t, stateDBPath(state, "personal"), "work")
	if db, err := openStateDB(state, "personal"); err == nil {
		_ = db.Close()
		t.Fatal("openStateDB() error = nil, want profile mismatch")
	} else if !strings.Contains(err.Error(), `database belongs to profile "work", not "personal"`) {
		t.Fatalf("openStateDB() error = %v, want profile mismatch", err)
	}
}

func assertGooseVersion(t *testing.T, db *sql.DB, want int64) {
	t.Helper()

	var got int64
	if err := db.QueryRow(`SELECT MAX(version_id) FROM dots_schema_migrations`).Scan(&got); err != nil {
		t.Fatalf("read goose migration version: %v", err)
	}
	if got != want {
		t.Fatalf("goose migration version = %d, want %d", got, want)
	}
}

func setProfileMetadata(t *testing.T, path, profile string) {
	t.Helper()

	db, err := openSQLite(path)
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite database: %v", err)
		}
	}()

	if _, err := db.Exec(`UPDATE meta SET value = ? WHERE key = 'profile'`, profile); err != nil {
		t.Fatalf("update profile metadata: %v", err)
	}
}

func assertFileRecords(t *testing.T, got []FileRecord, want []FileRecord) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("record count = %d, want %d; records = %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("record[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func assertTrackedDirs(t *testing.T, got []TrackedDirRecord, want []TrackedDirRecord) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("tracked directory count = %d, want %d; dirs = %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tracked dir[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func assertStateRecords(t *testing.T, got []StateRecord, want []StateRecord) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("state record count = %d, want %d; records = %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("state record[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

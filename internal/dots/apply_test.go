package dots

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestApplyAdoptsMatchingDestinationWithoutTouchingFile(t *testing.T) {
	t.Parallel()

	rt := newStatusTestRuntime(t)
	record := writeRepoTrackedFile(t, rt, ".ignore", "ignore\n")
	writeDestinationFile(t, rt, ".ignore", "ignore\n")
	destination := destinationPath(rt, ".ignore")
	originalTime := time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(destination, originalTime, originalTime); err != nil {
		t.Fatalf("set destination timestamp: %v", err)
	}
	before, err := os.Stat(destination)
	if err != nil {
		t.Fatalf("stat destination before apply: %v", err)
	}
	populateStatusDatabases(t, rt, []FileRecord{record}, nil)

	var out bytes.Buffer
	if err := applyProfile(rt, applyOptions{}, &out); err != nil {
		t.Fatalf("applyProfile() error = %v", err)
	}

	after, err := os.Stat(destination)
	if err != nil {
		t.Fatalf("stat destination after apply: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("destination modtime = %s, want unchanged %s", after.ModTime(), before.ModTime())
	}
	if after.Mode().Perm() != before.Mode().Perm() || after.Size() != before.Size() {
		t.Fatalf("destination metadata = mode %o size %d, want mode %o size %d", after.Mode().Perm(), after.Size(), before.Mode().Perm(), before.Size())
	}
	if !strings.Contains(out.String(), "copied 0 file(s), left 1 matching file(s) untouched") {
		t.Fatalf("apply output = %q, want matching-file skip summary", out.String())
	}

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
	assertStateRecords(t, stateRecords, []StateRecord{{
		Path:    record.Path,
		SHA256:  record.SHA256,
		RepoSHA: record.SHA256,
		Mode:    record.Mode,
		Size:    record.Size,
	}})
}

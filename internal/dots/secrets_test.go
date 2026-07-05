package dots

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

const fakeNPMToken = "npm_" + "a1b2c3d4e5f6g7h8i9j0klmnopqrstuvwx12"

func TestCanonicalizeHomeContentScrubsNPMRCTokenLine(t *testing.T) {
	t.Parallel()

	content := []byte("registry=https://registry.npmjs.org/\n" +
		"//registry.npmjs.org/:_authToken=" + fakeNPMToken + "\n" +
		"//registry.npmjs.org/:_authToken=${NPM_TOKEN}\n")

	got, err := canonicalizeHomeContent(".config/npm/npmrc", content, true)
	if err != nil {
		t.Fatalf("canonicalizeHomeContent() error = %v", err)
	}
	want := "registry=https://registry.npmjs.org/\n\n//registry.npmjs.org/:_authToken=${NPM_TOKEN}\n"
	if string(got) != want {
		t.Fatalf("canonical content = %q, want %q", string(got), want)
	}
	if strings.Contains(string(got), fakeNPMToken) {
		t.Fatal("canonical content retained npm token")
	}
}

func TestCanonicalizeHomeContentScrubsGenericNPMRCAuthLines(t *testing.T) {
	t.Parallel()

	content := []byte("//registry.npmjs.org/:_password=" + "YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXo=" + "\n" +
		"//registry.npmjs.org/:_auth=" + "bWFkZXVwdXNlcjptYWRldXBwYXNzd29yZA==" + "\n" +
		"//registry.npmjs.org/:username=madeupuser\n")

	got, err := canonicalizeHomeContent("npmrc", content, true)
	if err != nil {
		t.Fatalf("canonicalizeHomeContent() error = %v", err)
	}
	want := "\n\n//registry.npmjs.org/:username=madeupuser\n"
	if string(got) != want {
		t.Fatalf("canonical content = %q, want %q", string(got), want)
	}
}

func TestCanonicalizeHomeContentPreservesLineTerminators(t *testing.T) {
	t.Parallel()

	content := []byte("before\r\n//registry.npmjs.org/:_authToken=" + fakeNPMToken + "\r\nafter")
	got, err := canonicalizeHomeContent(".npmrc", content, true)
	if err != nil {
		t.Fatalf("canonicalizeHomeContent() error = %v", err)
	}
	want := []byte("before\r\n\r\nafter")
	if !bytes.Equal(got, want) {
		t.Fatalf("canonical content = %q, want %q", got, want)
	}
}

func TestCanonicalizeHomeContentScrubsNPMTokenLineInAnyFile(t *testing.T) {
	t.Parallel()

	content := []byte("before\ntoken=" + fakeNPMToken + "\nafter\n")
	got, err := canonicalizeHomeContent(".config/app/config", content, true)
	if err != nil {
		t.Fatalf("canonicalizeHomeContent() error = %v", err)
	}
	want := "before\n\nafter\n"
	if string(got) != want {
		t.Fatalf("canonical content = %q, want %q", string(got), want)
	}
}

func TestCanonicalizeHomeContentBlocksUnsupportedFindings(t *testing.T) {
	t.Parallel()

	genericValue := "a1b2c3d4" + "e5f6g7h8" + "i9j0klmn" + "opqrstuv" + "wx12"
	content := []byte("api_key=" + genericValue + "\n")
	_, err := canonicalizeHomeContent(".config/app/config", content, true)
	var scanErr secretScanError
	if !errors.As(err, &scanErr) {
		t.Fatalf("canonicalizeHomeContent() error = %v, want secretScanError", err)
	}
	if len(scanErr.Findings) != 1 || scanErr.Findings[0].RuleID != gitleaksRuleGenericAPIKey {
		t.Fatalf("findings = %+v, want generic API key finding", scanErr.Findings)
	}
	if strings.Contains(err.Error(), genericValue) {
		t.Fatal("secret scan error included raw token")
	}
}

func TestAnalyzeStatusComparesCanonicalNPMRCContent(t *testing.T) {
	t.Parallel()

	rt := newStatusTestRuntime(t)
	repoContent := "registry=https://registry.npmjs.org/\n\n"
	record := writeRepoTrackedFile(t, rt, ".config/npm/npmrc", repoContent)
	writeDestinationFile(t, rt, ".config/npm/npmrc", "registry=https://registry.npmjs.org/\n//registry.npmjs.org/:_authToken="+fakeNPMToken+"\n")
	populateStatusDatabases(t, rt, []FileRecord{record}, []FileRecord{record})

	report, _, err := analyzeStatus(rt)
	if err != nil {
		t.Fatalf("analyzeStatus() error = %v", err)
	}
	if report.dirty() {
		t.Fatalf("report should be clean when only npm token differs: %+v", report)
	}
}

func TestAddPathWritesCanonicalNPMRCContent(t *testing.T) {
	t.Parallel()

	rt := newStatusTestRuntime(t)
	writeDestinationFile(t, rt, ".npmrc", "registry=https://registry.npmjs.org/\n//registry.npmjs.org/:_authToken="+fakeNPMToken+"\n")

	records, err := addPath(rt, destinationPath(rt, ".npmrc"))
	if err != nil {
		t.Fatalf("addPath() error = %v", err)
	}
	wantContent := "registry=https://registry.npmjs.org/\n\n"
	assertFileContent(t, repoFilePath(rt, ".npmrc"), wantContent)
	assertFileRecords(t, records, []FileRecord{testFileRecord(".npmrc", wantContent)})
}

func TestSyncProfileSecretScanAbortLeavesFilesUntouched(t *testing.T) {
	t.Parallel()

	rt := newStatusTestRuntime(t)
	safeRecord := writeRepoTrackedFile(t, rt, "a-safe", "old safe\n")
	blockedRecord := writeRepoTrackedFile(t, rt, "z-blocked", "old blocked\n")
	writeDestinationFile(t, rt, "a-safe", "new safe\n")
	genericValue := "a1b2c3d4" + "e5f6g7h8" + "i9j0klmn" + "opqrstuv" + "wx12"
	writeDestinationFile(t, rt, "z-blocked", "api"+"_key="+genericValue+"\n")
	populateStatusDatabases(t, rt, []FileRecord{safeRecord, blockedRecord}, []FileRecord{safeRecord, blockedRecord})

	var out bytes.Buffer
	err := syncProfile(rt, syncOptions{}, &out)
	var scanErr secretScanError
	if !errors.As(err, &scanErr) {
		t.Fatalf("syncProfile() error = %v, want secretScanError", err)
	}
	assertFileContent(t, repoFilePath(rt, "a-safe"), "old safe\n")
	assertFileContent(t, repoFilePath(rt, "z-blocked"), "old blocked\n")
}

func TestBuildSyncDiffPlanUsesCanonicalDestinationContent(t *testing.T) {
	t.Parallel()

	rt := newStatusTestRuntime(t)
	record := writeRepoTrackedFile(t, rt, ".npmrc", "registry=https://old.example/\n\n")
	writeDestinationFile(t, rt, ".npmrc", "registry=https://new.example/\n//registry.npmjs.org/:_authToken="+fakeNPMToken+"\n")
	report := statusReport{Conflict: []statusItem{{Kind: kindConflictChanged, Path: ".npmrc"}}}

	plan, err := buildSyncDiffPlan(rt, report, []FileRecord{record})
	if err != nil {
		t.Fatalf("buildSyncDiffPlan() error = %v", err)
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("diff entries = %+v, want one entry", plan.Entries)
	}
	got := string(plan.Entries[0].NewContent)
	want := "registry=https://new.example/\n\n"
	if got != want {
		t.Fatalf("sync diff new content = %q, want %q", got, want)
	}
	if strings.Contains(got, fakeNPMToken) {
		t.Fatal("sync diff retained npm token")
	}
}

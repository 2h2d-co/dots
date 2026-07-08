package dots

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	udiff "github.com/aymanbagabas/go-udiff"
)

const binaryProbeBytes = 8000

type diffOptions struct {
	Sync             bool
	NoPager          bool
	Paths            []string
	stdoutIsTerminal func() bool
	runPager         pagerRunner
}

type diffPlan struct {
	Entries []diffEntry
	Notes   []diffNote
}

type diffEntry struct {
	Path       string
	OldContent []byte
	NewContent []byte
	OldMode    int64
	NewMode    int64
	OldExists  bool
}

type diffNote struct {
	Path string
	Text string
}

func diffProfile(rt *Runtime, opts diffOptions, out, errOut io.Writer) error {
	scope, err := newPathScope(rt, opts.Paths)
	if err != nil {
		return err
	}
	var report statusReport
	var records []FileRecord
	if opts.Sync {
		report, records, err = analyzeSyncStatus(rt, scope)
	} else {
		report, records, _, err = analyzeApplyStatus(rt, scope)
	}
	if err != nil {
		return err
	}
	if report.hasRepoDrift() && (!scope.active() || !opts.Sync) {
		if err := writeStatusReport(errOut, report); err != nil {
			return err
		}
		if err := writeRepoDriftRefusal(errOut, "Diff"); err != nil {
			return err
		}
		return ExitError{Code: 1, Silent: true}
	}

	plan, err := buildDiffPlan(rt, opts.Sync, report, records)
	if err != nil {
		return err
	}
	patch, err := renderDiffEntries(plan.Entries)
	if err != nil {
		return err
	}
	if err := writeDiffNotes(errOut, plan.Notes); err != nil {
		return err
	}
	if err := writeDiffPatch(out, patch, rt, opts); err != nil {
		return err
	}
	if patch != "" || len(plan.Notes) > 0 {
		return ExitError{Code: 1, Silent: true}
	}
	return nil
}

func buildDiffPlan(rt *Runtime, syncDirection bool, report statusReport, records []FileRecord) (diffPlan, error) {
	if syncDirection {
		return buildSyncDiffPlan(rt, report, records)
	}
	return buildApplyDiffPlan(rt, report, records)
}

func buildApplyDiffPlan(rt *Runtime, report statusReport, records []FileRecord) (diffPlan, error) {
	plan := diffPlan{}
	recordsByPath := fileRecordMap(records)
	for _, item := range report.Pending {
		record, ok := recordsByPath[item.Path]
		if !ok {
			return diffPlan{}, fmt.Errorf("tracked record missing for %s", item.Path)
		}
		switch item.Kind {
		case kindPendingCreate:
			newContent, err := readDiffFile(repoFilePath(rt, item.Path))
			if err != nil {
				return diffPlan{}, err
			}
			plan.Entries = append(plan.Entries, diffEntry{
				Path:       item.Path,
				NewContent: newContent,
				NewMode:    record.Mode,
			})
		case kindPendingUpdate:
			entry, err := applyUpdateDiffEntry(rt, item.Path, record)
			if err != nil {
				return diffPlan{}, err
			}
			plan.Entries = append(plan.Entries, entry)
		case kindPendingAdopt, kindPendingState:
			continue
		}
	}
	for _, item := range report.Conflict {
		switch item.Kind {
		case kindConflictChanged, kindConflictDiverged, kindConflictManaged:
			record, ok := recordsByPath[item.Path]
			if !ok {
				return diffPlan{}, fmt.Errorf("tracked record missing for %s", item.Path)
			}
			entry, err := applyUpdateDiffEntry(rt, item.Path, record)
			if err != nil {
				return diffPlan{}, err
			}
			plan.Entries = append(plan.Entries, entry)
			plan.Notes = append(plan.Notes, diffNote{Path: item.Path, Text: fmt.Sprintf("diff: plain apply refuses %s; use `dots apply --force` to back up and overwrite it", item.Path)})
		case kindConflictType:
			plan.Notes = append(plan.Notes, diffNote{Path: item.Path, Text: conflictTypeDiffNote(item)})
		}
	}
	plan.sort()
	return plan, nil
}

func buildSyncDiffPlan(rt *Runtime, report statusReport, records []FileRecord) (diffPlan, error) {
	syncPlan, err := buildSyncPlan(report, records, true)
	if err != nil {
		return diffPlan{}, err
	}
	if err := addRepoDriftToSyncPlan(rt, report, true, &syncPlan); err != nil {
		return diffPlan{}, err
	}
	syncPlan.sort()
	plan := diffPlan{}
	for _, item := range syncPlan.Updates {
		entry, err := syncUpdateDiffEntry(rt, item.Path, item.Record)
		if err != nil {
			return diffPlan{}, err
		}
		plan.Entries = append(plan.Entries, entry)
		if item.RequiresForce {
			plan.Notes = append(plan.Notes, diffNote{Path: item.Path, Text: fmt.Sprintf("diff: plain sync refuses %s; `dots sync --force` would copy the home file into the repo", item.Path)})
		}
	}
	for _, item := range syncPlan.Adds {
		newContent, newMode, err := readCanonicalDiffFile(item.Path, destinationPath(rt, item.Path))
		if err != nil {
			return diffPlan{}, err
		}
		plan.Entries = append(plan.Entries, diffEntry{
			Path:       item.Path,
			NewContent: newContent,
			NewMode:    newMode,
		})
	}
	for _, note := range syncPlan.Notes {
		plan.Notes = append(plan.Notes, diffNote{Path: note.Path, Text: syncDiffNoteText(note)})
	}
	plan.sort()
	return plan, nil
}

func applyUpdateDiffEntry(rt *Runtime, trackedPath string, record FileRecord) (diffEntry, error) {
	oldContent, oldMode, err := readCanonicalDiffFile(trackedPath, destinationPath(rt, trackedPath))
	if err != nil {
		return diffEntry{}, err
	}
	newContent, err := readDiffFile(repoFilePath(rt, trackedPath))
	if err != nil {
		return diffEntry{}, err
	}
	return diffEntry{
		Path:       trackedPath,
		OldContent: oldContent,
		NewContent: newContent,
		OldMode:    oldMode,
		NewMode:    record.Mode,
		OldExists:  true,
	}, nil
}

func syncUpdateDiffEntry(rt *Runtime, trackedPath string, record FileRecord) (diffEntry, error) {
	oldContent, err := readDiffFile(repoFilePath(rt, trackedPath))
	if err != nil {
		return diffEntry{}, err
	}
	newContent, newMode, err := readCanonicalDiffFile(trackedPath, destinationPath(rt, trackedPath))
	if err != nil {
		return diffEntry{}, err
	}
	return diffEntry{
		Path:       trackedPath,
		OldContent: oldContent,
		NewContent: newContent,
		OldMode:    record.Mode,
		NewMode:    newMode,
		OldExists:  true,
	}, nil
}

func conflictTypeDiffNote(item statusItem) string {
	if item.Detail == "" {
		return fmt.Sprintf("diff: destination is not a regular file: %s", item.Path)
	}
	return fmt.Sprintf("diff: destination is not a regular file: %s (%s)", item.Path, item.Detail)
}

func syncDiffNoteText(note syncPlanNote) string {
	switch note.Kind {
	case kindPendingCreate:
		return fmt.Sprintf("diff: %s is missing from $HOME; sync will not delete repo files, use `dots forget %s` if tracking should stop", note.Path, note.Path)
	case kindConflictType:
		if note.Detail != "" {
			return fmt.Sprintf("diff: destination is not a regular file: %s (%s)", note.Path, note.Detail)
		}
		return fmt.Sprintf("diff: destination is not a regular file: %s", note.Path)
	case kindDirectoryUnsupported:
		return fmt.Sprintf("diff: untracked destination is not regular: %s", note.Path)
	case kindDirectoryRootConflict:
		return fmt.Sprintf("diff: tracked directory is not a directory: %s", note.Path)
	default:
		return "diff: " + note.Text
	}
}

func readCanonicalDiffFile(trackedPath, filePath string) ([]byte, int64, error) {
	file, err := readCanonicalHomeFile(trackedPath, filePath, true)
	if err != nil {
		return nil, 0, err
	}
	return file.Content, file.Record.Mode, nil
}

func readDiffFile(path string) ([]byte, error) {
	cleaned := filepath.Clean(path)
	info, err := os.Lstat(cleaned)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", cleaned, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("unsupported file type at %s", cleaned)
	}
	file, err := os.Open(cleaned)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", cleaned, err)
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", cleaned, err)
	}
	return content, nil
}

func renderDiffEntries(entries []diffEntry) (string, error) {
	if len(entries) == 0 {
		return "", nil
	}
	var out bytes.Buffer
	for _, entry := range entries {
		rendered, err := renderDiffEntry(entry)
		if err != nil {
			return "", err
		}
		out.WriteString(rendered)
	}
	return out.String(), nil
}

func renderDiffEntry(entry diffEntry) (string, error) {
	oldLabel := "a/" + entry.Path
	if !entry.OldExists {
		oldLabel = "/dev/null"
	}
	newLabel := "b/" + entry.Path
	oldMode, err := gitFileMode(entry.OldMode)
	if err != nil && entry.OldExists {
		return "", err
	}
	newMode, err := gitFileMode(entry.NewMode)
	if err != nil {
		return "", err
	}
	contentChanged := !bytes.Equal(entry.OldContent, entry.NewContent)
	modeChanged := entry.OldExists && entry.OldMode != entry.NewMode
	created := !entry.OldExists
	if !contentChanged && !modeChanged && !created {
		return "", nil
	}

	var out bytes.Buffer
	fmt.Fprintf(&out, "diff --git a/%s b/%s\n", entry.Path, entry.Path)
	if created {
		fmt.Fprintf(&out, "new file mode %s\n", newMode)
	} else if modeChanged {
		fmt.Fprintf(&out, "old mode %s\n", oldMode)
		fmt.Fprintf(&out, "new mode %s\n", newMode)
	}
	if !contentChanged {
		return out.String(), nil
	}
	if isBinaryContent(entry.OldContent) || isBinaryContent(entry.NewContent) {
		fmt.Fprintf(&out, "Binary files %s and %s differ\n", oldLabel, newLabel)
		return out.String(), nil
	}
	out.WriteString(udiff.Unified(oldLabel, newLabel, string(entry.OldContent), string(entry.NewContent)))
	return out.String(), nil
}

func gitFileMode(mode int64) (string, error) {
	if _, err := fileModeFromRecord(mode); err != nil {
		return "", err
	}
	return fmt.Sprintf("100%03o", mode), nil
}

func isBinaryContent(content []byte) bool {
	limit := len(content)
	if limit > binaryProbeBytes {
		limit = binaryProbeBytes
	}
	return bytes.IndexByte(content[:limit], 0) >= 0
}

func writeDiffNotes(out io.Writer, notes []diffNote) error {
	for _, note := range notes {
		if _, err := fmt.Fprintln(out, note.Text); err != nil {
			return err
		}
	}
	return nil
}

func writeDiffPatch(out io.Writer, patch string, rt *Runtime, opts diffOptions) error {
	pager := resolveDiffPager(opts.NoPager, os.Getenv(pagerEnv), rt.Pager)
	terminalCheck := opts.stdoutIsTerminal
	if terminalCheck == nil {
		terminalCheck = stdoutIsTerminal
	}
	runner := opts.runPager
	if runner == nil {
		runner = runShellPager
	}
	if pager != "" && patch != "" && terminalCheck() {
		return runner(pager, patch)
	}
	_, err := io.WriteString(out, patch)
	return err
}

func (p *diffPlan) sort() {
	sort.Slice(p.Entries, func(i, j int) bool {
		return p.Entries[i].Path < p.Entries[j].Path
	})
	sort.Slice(p.Notes, func(i, j int) bool {
		if p.Notes[i].Path == p.Notes[j].Path {
			return p.Notes[i].Text < p.Notes[j].Text
		}
		return p.Notes[i].Path < p.Notes[j].Path
	})
}

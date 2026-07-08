package dots

import (
	"errors"
	"fmt"
	"io"
	"sort"
)

type syncOptions struct {
	DryRun bool
	Force  bool
	Paths  []string
}

type syncPlan struct {
	Updates   []syncPlanItem
	Adds      []syncPlanItem
	StateOnly []syncPlanItem
	Conflicts []syncPlanItem
	Notes     []syncPlanNote
	Omitted   []syncPlanItem
}

type syncPlanItem struct {
	Path          string
	Kind          statusKind
	Record        FileRecord
	RequiresForce bool
}

type syncPlanNote struct {
	Path   string
	Kind   statusKind
	Detail string
	Text   string
}

type syncPreparedWrite struct {
	Path          string
	Content       []byte
	Record        FileRecord
	RequiresForce bool
}

func syncProfile(rt *Runtime, opts syncOptions, out io.Writer) error {
	scope, err := newPathScope(rt, opts.Paths)
	if err != nil {
		return err
	}
	report, records, err := analyzeSyncStatus(rt, scope)
	if err != nil {
		return err
	}
	if report.hasRepoDrift() && !scope.active() {
		if err := writeStatusReport(out, report); err != nil {
			return err
		}
		if err := writeRepoDriftRefusal(out, "Sync"); err != nil {
			return err
		}
		return ExitError{Code: 1, Silent: true}
	}

	plan, err := buildSyncPlan(report, records, opts.Force)
	if err != nil {
		return err
	}
	if scope.active() {
		if err := addRepoDriftToSyncPlan(rt, report, opts.Force, &plan); err != nil {
			return err
		}
		plan.sort()
	}
	if opts.DryRun {
		if err := writeSyncPlan(out, report, plan, opts); err != nil {
			return err
		}
		if len(plan.Conflicts) > 0 && !opts.Force {
			return ExitError{Code: 1, Silent: true}
		}
		_, err := prepareSyncWrites(rt, plan)
		return err
	}

	if len(plan.Conflicts) > 0 && !opts.Force {
		if err := writeStatusReport(out, report); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out, "Sync aborted: destination conflicts found. Resolve the conflicts, or re-run with --force to back up conflicting repo files and take the destination side."); err != nil {
			return err
		}
		return ExitError{Code: 1, Silent: true}
	}

	if err := ensureNothingToPull(rt.Repo, "sync"); err != nil {
		return err
	}
	preparedWrites, err := prepareSyncWrites(rt, plan)
	if err != nil {
		return err
	}

	repoDB, err := openRepoDB(rt.Repo, rt.Profile)
	if err != nil {
		return err
	}
	stateDB, err := openStateDB(rt.StateDir, rt.Profile)
	if err != nil {
		return errors.Join(err, repoDB.Close())
	}

	var backups *backupSetWriter
	if opts.Force {
		backups = newBackupSetWriter(rt)
	}

	copiedRecords := make([]FileRecord, 0, len(preparedWrites))
	for _, item := range preparedWrites {
		record, err := syncWritePreparedToRepo(rt, item, backups)
		if err != nil {
			return errors.Join(err, repoDB.Close(), stateDB.Close())
		}
		copiedRecords = append(copiedRecords, record)
	}
	sortFileRecords(copiedRecords)

	stateRecords := make([]FileRecord, 0, len(copiedRecords)+len(plan.StateOnly))
	stateRecords = append(stateRecords, copiedRecords...)
	for _, item := range plan.StateOnly {
		stateRecords = append(stateRecords, item.Record)
	}
	sortFileRecords(stateRecords)

	if err := upsertRepoRecords(repoDB, copiedRecords); err != nil {
		return errors.Join(err, repoDB.Close(), stateDB.Close())
	}
	if err := upsertStateRecords(stateDB, stateRecords); err != nil {
		return errors.Join(err, repoDB.Close(), stateDB.Close())
	}
	if err := errors.Join(repoDB.Close(), stateDB.Close()); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(out, "Sync complete: copied %d file(s), recorded state for %d file(s), left %d file(s) untouched for profile %s\n", len(copiedRecords), len(stateRecords), plan.untouchedCount(), rt.Profile); err != nil {
		return err
	}
	if backups.Written() {
		if _, err := fmt.Fprintf(out, "Backups written to: %s\n", backups.Path()); err != nil {
			return err
		}
	}
	return writeSyncNotes(out, plan.Notes)
}

func buildSyncPlan(report statusReport, records []FileRecord, force bool) (syncPlan, error) {
	plan := syncPlan{}
	recordsByPath := fileRecordMap(records)
	recordFor := func(item statusItem) (FileRecord, error) {
		record, ok := recordsByPath[item.Path]
		if !ok {
			return FileRecord{}, fmt.Errorf("tracked record missing for %s", item.Path)
		}
		return record, nil
	}

	for _, item := range report.Conflict {
		switch item.Kind {
		case kindConflictChanged:
			record, err := recordFor(item)
			if err != nil {
				return syncPlan{}, err
			}
			plan.Updates = append(plan.Updates, syncPlanItem{Path: item.Path, Kind: item.Kind, Record: record})
		case kindConflictDiverged, kindConflictManaged:
			record, err := recordFor(item)
			if err != nil {
				return syncPlan{}, err
			}
			planItem := syncPlanItem{Path: item.Path, Kind: item.Kind, Record: record, RequiresForce: true}
			if force {
				plan.Updates = append(plan.Updates, planItem)
			} else {
				plan.Conflicts = append(plan.Conflicts, planItem)
			}
		case kindConflictType:
			plan.Notes = append(plan.Notes, syncPlanNote{Path: item.Path, Kind: item.Kind, Detail: item.Detail, Text: syncNoteText(item)})
		}
	}

	for _, item := range report.Directory {
		switch item.Kind {
		case kindDirectoryUntracked:
			plan.Adds = append(plan.Adds, syncPlanItem{Path: item.Path, Kind: item.Kind})
		case kindDirectoryUnsupported, kindDirectoryRootConflict:
			plan.Notes = append(plan.Notes, syncPlanNote{Path: item.Path, Kind: item.Kind, Detail: item.Detail, Text: syncNoteText(item)})
		}
	}

	for _, item := range report.Pending {
		switch item.Kind {
		case kindPendingAdopt, kindPendingState:
			record, err := recordFor(item)
			if err != nil {
				return syncPlan{}, err
			}
			plan.StateOnly = append(plan.StateOnly, syncPlanItem{Path: item.Path, Kind: item.Kind, Record: record})
		case kindPendingCreate:
			plan.Notes = append(plan.Notes, syncPlanNote{Path: item.Path, Kind: item.Kind, Detail: item.Detail, Text: syncNoteText(item)})
		case kindPendingUpdate:
			record, err := recordFor(item)
			if err != nil {
				return syncPlan{}, err
			}
			plan.Omitted = append(plan.Omitted, syncPlanItem{Path: item.Path, Kind: item.Kind, Record: record})
		}
	}

	for _, item := range report.State {
		if item.Kind == kindStaleState {
			plan.Omitted = append(plan.Omitted, syncPlanItem{Path: item.Path, Kind: item.Kind})
		}
	}

	plan.sort()
	return plan, nil
}

func addRepoDriftToSyncPlan(rt *Runtime, report statusReport, force bool, plan *syncPlan) error {
	driftPaths := make(map[string]struct{})
	for _, item := range report.Repo {
		driftPaths[item.Path] = struct{}{}
	}
	removeSyncPlanPaths(plan, driftPaths)

	for _, item := range report.Repo {
		switch item.Kind {
		case kindRepoModified, kindRepoUntracked:
			if _, _, err := destinationCanonicalFingerprint(item.Path, destinationPath(rt, item.Path)); err != nil {
				plan.Notes = append(plan.Notes, syncPlanNote{Path: item.Path, Kind: item.Kind, Detail: err.Error(), Text: syncNoteText(item)})
				continue
			}
			record, err := fileRecord(profileDir(rt), item.Path)
			if err != nil {
				return err
			}
			planItem := syncPlanItem{Path: item.Path, Kind: item.Kind, Record: record, RequiresForce: true}
			if force {
				plan.Updates = append(plan.Updates, planItem)
			} else {
				plan.Conflicts = append(plan.Conflicts, planItem)
			}
		case kindRepoMissing:
			if _, _, err := destinationCanonicalFingerprint(item.Path, destinationPath(rt, item.Path)); err != nil {
				plan.Notes = append(plan.Notes, syncPlanNote{Path: item.Path, Kind: item.Kind, Detail: item.Detail, Text: syncNoteText(item)})
				continue
			}
			plan.Adds = append(plan.Adds, syncPlanItem{Path: item.Path, Kind: item.Kind})
		case kindRepoUnsupported:
			plan.Notes = append(plan.Notes, syncPlanNote{Path: item.Path, Kind: item.Kind, Detail: item.Detail, Text: syncNoteText(item)})
		}
	}
	return nil
}

func removeSyncPlanPaths(plan *syncPlan, paths map[string]struct{}) {
	plan.Updates = filterSyncPlanItems(plan.Updates, paths)
	plan.Adds = filterSyncPlanItems(plan.Adds, paths)
	plan.StateOnly = filterSyncPlanItems(plan.StateOnly, paths)
	plan.Conflicts = filterSyncPlanItems(plan.Conflicts, paths)
	plan.Omitted = filterSyncPlanItems(plan.Omitted, paths)
	plan.Notes = filterSyncPlanNotes(plan.Notes, paths)
}

func filterSyncPlanItems(items []syncPlanItem, paths map[string]struct{}) []syncPlanItem {
	if len(paths) == 0 {
		return items
	}
	filtered := items[:0]
	for _, item := range items {
		if _, ok := paths[item.Path]; !ok {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func filterSyncPlanNotes(notes []syncPlanNote, paths map[string]struct{}) []syncPlanNote {
	if len(paths) == 0 {
		return notes
	}
	filtered := notes[:0]
	for _, note := range notes {
		if _, ok := paths[note.Path]; !ok {
			filtered = append(filtered, note)
		}
	}
	return filtered
}

func prepareSyncWrites(rt *Runtime, plan syncPlan) ([]syncPreparedWrite, error) {
	items := make([]syncPlanItem, 0, len(plan.Updates)+len(plan.Adds))
	items = append(items, plan.Updates...)
	items = append(items, plan.Adds...)
	writes := make([]syncPreparedWrite, 0, len(items))
	for _, item := range items {
		file, err := readCanonicalHomeFile(item.Path, destinationPath(rt, item.Path), true)
		if err != nil {
			return nil, err
		}
		writes = append(writes, syncPreparedWrite{
			Path:          item.Path,
			Content:       file.Content,
			Record:        file.Record,
			RequiresForce: item.RequiresForce,
		})
	}
	return writes, nil
}

func syncWritePreparedToRepo(rt *Runtime, item syncPreparedWrite, backups *backupSetWriter) (FileRecord, error) {
	repoPath := repoFilePath(rt, item.Path)
	if item.RequiresForce {
		if backups == nil {
			return FileRecord{}, errors.New("backup writer is required for forced sync")
		}
		if err := backupRepoFile(repoPath, backups, item.Path); err != nil {
			return FileRecord{}, err
		}
	}
	if err := writeFileBytes(repoPath, item.Content, item.Record.Mode); err != nil {
		return FileRecord{}, err
	}
	return item.Record, nil
}

func writeSyncPlan(out io.Writer, report statusReport, plan syncPlan, opts syncOptions) error {
	if _, err := fmt.Fprintln(out, "Sync plan (dry run; no files changed):"); err != nil {
		return err
	}
	if err := writeStatusReport(out, report); err != nil {
		return err
	}
	if opts.Force && plan.hasForceUpdates() {
		if _, err := fmt.Fprintln(out, "Force enabled: conflicting repo paths would be backed up before overwrite."); err != nil {
			return err
		}
	}
	if err := writeSyncPlanItems(out, "Files to update in repo", plan.Updates); err != nil {
		return err
	}
	if err := writeSyncPlanItems(out, "Files to add to repo", plan.Adds); err != nil {
		return err
	}
	if err := writeSyncPlanItems(out, "State-only refreshes", plan.StateOnly); err != nil {
		return err
	}
	if err := writeSyncPlanItems(out, "Conflicts", plan.Conflicts); err != nil {
		return err
	}
	return writeSyncNotes(out, plan.Notes)
}

func writeSyncPlanItems(out io.Writer, title string, items []syncPlanItem) error {
	if len(items) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(out, "%s:\n", title); err != nil {
		return err
	}
	for _, item := range items {
		suffix := ""
		if item.RequiresForce {
			suffix = " (requires --force; repo backup)"
		}
		if _, err := fmt.Fprintf(out, "  %s%s\n", item.Path, suffix); err != nil {
			return err
		}
	}
	return nil
}

func writeSyncNotes(out io.Writer, notes []syncPlanNote) error {
	if len(notes) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(out, "Notes:"); err != nil {
		return err
	}
	for _, note := range notes {
		if _, err := fmt.Fprintf(out, "  %s\n", note.Text); err != nil {
			return err
		}
	}
	return nil
}

func syncNoteText(item statusItem) string {
	switch item.Kind {
	case kindPendingCreate:
		return fmt.Sprintf("%s is missing from $HOME; sync will not delete repo files, use `dots forget %s` if tracking should stop", item.Path, item.Path)
	case kindRepoModified, kindRepoUntracked:
		return fmt.Sprintf("%s has profile repo drift but $HOME has no regular file to sync", item.Path)
	case kindRepoMissing:
		return fmt.Sprintf("%s is missing from the profile repo and cannot be restored because $HOME has no regular file to sync", item.Path)
	case kindRepoUnsupported:
		return fmt.Sprintf("profile repo path is not a regular file: %s", item.Path)
	case kindConflictType:
		if item.Detail != "" {
			return fmt.Sprintf("destination is not a regular file: %s (%s)", item.Path, item.Detail)
		}
		return fmt.Sprintf("destination is not a regular file: %s", item.Path)
	case kindDirectoryUnsupported:
		return fmt.Sprintf("untracked destination is not regular: %s", item.Path)
	case kindDirectoryRootConflict:
		return fmt.Sprintf("tracked directory is not a directory: %s", item.Path)
	default:
		return fmt.Sprintf("%s: %s", item.Kind, item.Path)
	}
}

func (p *syncPlan) sort() {
	sortSyncItems(p.Updates)
	sortSyncItems(p.Adds)
	sortSyncItems(p.StateOnly)
	sortSyncItems(p.Conflicts)
	sortSyncItems(p.Omitted)
	sort.Slice(p.Notes, func(i, j int) bool {
		if p.Notes[i].Path == p.Notes[j].Path {
			return p.Notes[i].Kind < p.Notes[j].Kind
		}
		return p.Notes[i].Path < p.Notes[j].Path
	})
}

func sortSyncItems(items []syncPlanItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Path == items[j].Path {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Path < items[j].Path
	})
}

func (p syncPlan) hasForceUpdates() bool {
	for _, item := range p.Updates {
		if item.RequiresForce {
			return true
		}
	}
	return false
}

func (p syncPlan) untouchedCount() int {
	return len(p.Conflicts) + len(p.Notes) + len(p.Omitted)
}

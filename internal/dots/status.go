package dots

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

type statusKind string

const (
	kindRepoMissing     statusKind = "profile file missing"
	kindRepoModified    statusKind = "profile file changed"
	kindRepoUnsupported statusKind = "profile file is not regular"
	kindRepoUntracked   statusKind = "untracked profile file"
	kindPendingCreate   statusKind = "will create"
	kindPendingUpdate   statusKind = "will update"
	kindPendingAdopt    statusKind = "will adopt existing match"
	kindPendingState    statusKind = "will refresh apply state"
	kindConflictChanged statusKind = "destination changed since last apply"
	kindConflictManaged statusKind = "unmanaged destination differs"
	kindConflictType    statusKind = "destination is not a regular file"
	kindStaleState      statusKind = "stale apply state"
)

type statusItem struct {
	Kind   statusKind
	Path   string
	Detail string
}

type statusReport struct {
	Profile  string
	Repo     []statusItem
	Pending  []statusItem
	Conflict []statusItem
	State    []statusItem
}

func analyzeStatus(rt *Runtime) (statusReport, []FileRecord, error) {
	repoDB, err := openRepoDB(rt.Repo, rt.Profile)
	if err != nil {
		return statusReport{}, nil, err
	}
	defer func() { _ = repoDB.Close() }()
	stateDB, err := openStateDB(rt.StateDir, rt.Profile)
	if err != nil {
		return statusReport{}, nil, err
	}
	defer func() { _ = stateDB.Close() }()

	return analyzeStatusWithDB(rt, repoDB, stateDB)
}

func analyzeStatusWithDB(rt *Runtime, repoDB, stateDB *sql.DB) (statusReport, []FileRecord, error) {
	records, err := listRepoRecords(repoDB)
	if err != nil {
		return statusReport{}, nil, err
	}
	stateRecords, err := listStateRecords(stateDB)
	if err != nil {
		return statusReport{}, nil, err
	}

	report := statusReport{Profile: rt.Profile}
	repoRecords := fileRecordMap(records)
	stateRecordsByPath := stateRecordMap(stateRecords)

	for _, record := range records {
		current, err := fileRecord(profileDir(rt), record.Path)
		if err != nil {
			classifyRepoRecordError(&report, record.Path, err)
			continue
		}
		if current.SHA256 != record.SHA256 || current.Mode != record.Mode || current.Size != record.Size {
			report.Repo = append(report.Repo, statusItem{Kind: kindRepoModified, Path: record.Path})
		}
	}

	if err := filepath.WalkDir(profileDir(rt), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == profileDir(rt) {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(profileDir(rt), path)
		if err != nil {
			return err
		}
		trackedPath, err := cleanTrackedPath(rel)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			report.Repo = append(report.Repo, statusItem{Kind: kindRepoUnsupported, Path: trackedPath})
			return nil
		}
		if _, ok := repoRecords[trackedPath]; !ok {
			report.Repo = append(report.Repo, statusItem{Kind: kindRepoUntracked, Path: trackedPath})
		}
		return nil
	}); err != nil {
		return statusReport{}, nil, fmt.Errorf("scan profile directory: %w", err)
	}

	for _, record := range records {
		stateRecord, hasState := stateRecordsByPath[record.Path]
		dest := destinationPath(rt, record.Path)
		destSHA, destMode, err := destinationFingerprint(dest)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				report.Pending = append(report.Pending, statusItem{Kind: kindPendingCreate, Path: record.Path})
				continue
			}
			if errors.Is(err, os.ErrPermission) {
				return statusReport{}, nil, err
			}
			report.Conflict = append(report.Conflict, statusItem{Kind: kindConflictType, Path: record.Path, Detail: err.Error()})
			continue
		}

		destMatchesRepo := destSHA == record.SHA256 && destMode == record.Mode
		if !hasState {
			if destMatchesRepo {
				report.Pending = append(report.Pending, statusItem{Kind: kindPendingAdopt, Path: record.Path})
			} else {
				report.Conflict = append(report.Conflict, statusItem{Kind: kindConflictManaged, Path: record.Path})
			}
			continue
		}

		destMatchesState := destSHA == stateRecord.SHA256 && destMode == stateRecord.Mode
		stateMatchesRepo := stateRecord.RepoSHA == record.SHA256 && stateRecord.Mode == record.Mode
		switch {
		case destMatchesRepo && stateMatchesRepo:
			continue
		case destMatchesRepo:
			report.Pending = append(report.Pending, statusItem{Kind: kindPendingState, Path: record.Path})
		case destMatchesState:
			report.Pending = append(report.Pending, statusItem{Kind: kindPendingUpdate, Path: record.Path})
		default:
			report.Conflict = append(report.Conflict, statusItem{Kind: kindConflictChanged, Path: record.Path})
		}
	}

	for _, stateRecord := range stateRecords {
		if _, ok := repoRecords[stateRecord.Path]; !ok {
			report.State = append(report.State, statusItem{Kind: kindStaleState, Path: stateRecord.Path})
		}
	}

	report.sort()
	return report, records, nil
}

func classifyRepoRecordError(report *statusReport, trackedPath string, err error) {
	if errors.Is(err, os.ErrNotExist) {
		report.Repo = append(report.Repo, statusItem{Kind: kindRepoMissing, Path: trackedPath})
		return
	}
	report.Repo = append(report.Repo, statusItem{Kind: kindRepoUnsupported, Path: trackedPath, Detail: err.Error()})
}

func (r *statusReport) sort() {
	sortItems(r.Repo)
	sortItems(r.Pending)
	sortItems(r.Conflict)
	sortItems(r.State)
}

func sortItems(items []statusItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind == items[j].Kind {
			return items[i].Path < items[j].Path
		}
		return items[i].Kind < items[j].Kind
	})
}

func (r statusReport) dirty() bool {
	return len(r.Repo) > 0 || len(r.Pending) > 0 || len(r.Conflict) > 0 || len(r.State) > 0
}

func (r statusReport) hasRepoDrift() bool {
	return len(r.Repo) > 0
}

func (r statusReport) hasConflicts() bool {
	return len(r.Conflict) > 0
}

func writeStatusReport(out io.Writer, report statusReport) error {
	if _, err := fmt.Fprintf(out, "Profile: %s\n", report.Profile); err != nil {
		return err
	}
	if report.dirty() {
		if _, err := fmt.Fprintln(out, "Status: changes require attention"); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(out, "Status: clean"); err != nil {
		return err
	}
	if err := writeStatusSection(out, "Repo drift", report.Repo); err != nil {
		return err
	}
	if err := writeStatusSection(out, "Pending changes", report.Pending); err != nil {
		return err
	}
	if err := writeStatusSection(out, "Conflicts", report.Conflict); err != nil {
		return err
	}
	if err := writeStatusSection(out, "Applied state", report.State); err != nil {
		return err
	}
	if !report.dirty() {
		_, err := fmt.Fprintln(out, "Clean: no changes")
		return err
	}
	return nil
}

func writeStatusSection(out io.Writer, title string, items []statusItem) error {
	if len(items) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(out, "%s:\n", title); err != nil {
		return err
	}
	for _, item := range items {
		if item.Detail == "" {
			if _, err := fmt.Fprintf(out, "  %s: %s\n", item.Kind, item.Path); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(out, "  %s: %s (%s)\n", item.Kind, item.Path, item.Detail); err != nil {
				return err
			}
		}
	}
	return nil
}

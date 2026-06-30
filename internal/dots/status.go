package dots

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
)

type statusKind string

const (
	kindRepoMissing           statusKind = "profile file missing"
	kindRepoModified          statusKind = "profile file changed"
	kindRepoUnsupported       statusKind = "profile file is not regular"
	kindRepoUntracked         statusKind = "untracked profile file"
	kindPendingCreate         statusKind = "will create"
	kindPendingUpdate         statusKind = "will update"
	kindPendingAdopt          statusKind = "will adopt existing match"
	kindPendingState          statusKind = "will refresh apply state"
	kindConflictChanged       statusKind = "destination changed since last apply"
	kindConflictManaged       statusKind = "unmanaged destination differs"
	kindConflictType          statusKind = "destination is not a regular file"
	kindDirectoryUntracked    statusKind = "untracked destination file"
	kindDirectoryUnsupported  statusKind = "untracked destination is not regular"
	kindDirectoryRootConflict statusKind = "tracked directory is not a directory"
	kindStaleState            statusKind = "stale apply state"
)

type statusItem struct {
	Kind   statusKind
	Path   string
	Detail string
}

type statusReport struct {
	Profile     string
	TrackedDirs []TrackedDirRecord
	Repo        []statusItem
	Directory   []statusItem
	Pending     []statusItem
	Conflict    []statusItem
	State       []statusItem
}

type statusGroup struct {
	Title     string
	Repo      []statusItem
	Directory []statusItem
	Pending   []statusItem
	Conflict  []statusItem
	State     []statusItem
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
	trackedDirs, err := listTrackedDirs(repoDB)
	if err != nil {
		return statusReport{}, nil, err
	}
	stateRecords, err := listStateRecords(stateDB)
	if err != nil {
		return statusReport{}, nil, err
	}

	report := statusReport{Profile: rt.Profile, TrackedDirs: trackedDirs}
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

	profileRoot := profileDir(rt)
	if err := filepath.WalkDir(profileRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == profileRoot {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(profileRoot, path)
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

	if err := scanTrackedDirs(rt, trackedDirs, repoRecords, &report); err != nil {
		return statusReport{}, nil, err
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

func scanTrackedDirs(rt *Runtime, trackedDirs []TrackedDirRecord, repoRecords map[string]FileRecord, report *statusReport) error {
	reported := make(map[string]struct{})
	for _, dir := range trackedDirs {
		destinationRoot := destinationPath(rt, dir.Path)
		info, err := os.Lstat(destinationRoot)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("stat tracked directory %s: %w", destinationRoot, err)
		}
		if !info.IsDir() {
			key := string(kindDirectoryRootConflict) + "\x00" + dir.Path
			if _, ok := reported[key]; !ok {
				report.Directory = append(report.Directory, statusItem{Kind: kindDirectoryRootConflict, Path: dir.Path})
				reported[key] = struct{}{}
			}
			continue
		}

		matcher, err := loadDotsIgnore(repoFilePath(rt, path.Join(dir.Path, ".dotsignore")))
		if err != nil {
			return fmt.Errorf("load tracked directory .dotsignore for %s: %w", dir.Path, err)
		}
		if err := filepath.WalkDir(destinationRoot, func(current string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if current == destinationRoot {
				return nil
			}
			rel, err := filepath.Rel(destinationRoot, current)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			trackedPath, err := cleanTrackedPath(path.Join(dir.Path, rel))
			if err != nil {
				return err
			}
			if isNestedTrackedRoot(dir.Path, trackedPath, trackedDirs) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if matcher.ignored(rel, entry.IsDir()) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			if _, ok := repoRecords[trackedPath]; ok {
				return nil
			}
			item := statusItem{Kind: kindDirectoryUntracked, Path: trackedPath}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				item.Kind = kindDirectoryUnsupported
			}
			key := string(item.Kind) + "\x00" + item.Path
			if _, ok := reported[key]; !ok {
				report.Directory = append(report.Directory, item)
				reported[key] = struct{}{}
			}
			return nil
		}); err != nil {
			return fmt.Errorf("scan tracked directory %s: %w", destinationRoot, err)
		}
	}
	return nil
}

func isNestedTrackedRoot(currentRoot, trackedPath string, trackedDirs []TrackedDirRecord) bool {
	for _, dir := range trackedDirs {
		if dir.Path != currentRoot && dir.Path == trackedPath && trackedPathInsideRoot(currentRoot, trackedPath) {
			return true
		}
	}
	return false
}

func classifyRepoRecordError(report *statusReport, trackedPath string, err error) {
	if errors.Is(err, os.ErrNotExist) {
		report.Repo = append(report.Repo, statusItem{Kind: kindRepoMissing, Path: trackedPath})
		return
	}
	report.Repo = append(report.Repo, statusItem{Kind: kindRepoUnsupported, Path: trackedPath, Detail: err.Error()})
}

func (r *statusReport) sort() {
	sortTrackedDirs(r.TrackedDirs)
	sortItems(r.Repo)
	sortItems(r.Directory)
	sortItems(r.Pending)
	sortItems(r.Conflict)
	sortItems(r.State)
}

func sortTrackedDirs(dirs []TrackedDirRecord) {
	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i].Path < dirs[j].Path
	})
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
	return len(r.Repo) > 0 || len(r.Directory) > 0 || len(r.Pending) > 0 || len(r.Conflict) > 0 || len(r.State) > 0
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
	if !report.dirty() {
		_, err := fmt.Fprintln(out, "Clean: no changes")
		return err
	}
	if len(report.TrackedDirs) > 0 {
		return writeGroupedStatusSections(out, report)
	}
	return writeFlatStatusSections(out, report)
}

func writeFlatStatusSections(out io.Writer, report statusReport) error {
	if err := writeStatusSection(out, "Repo drift", report.Repo); err != nil {
		return err
	}
	if err := writeStatusSection(out, "Directory drift", report.Directory); err != nil {
		return err
	}
	if err := writeStatusSection(out, "Pending changes", report.Pending); err != nil {
		return err
	}
	if err := writeStatusSection(out, "Conflicts", report.Conflict); err != nil {
		return err
	}
	return writeStatusSection(out, "Applied state", report.State)
}

func writeGroupedStatusSections(out io.Writer, report statusReport) error {
	rootGroups, individual := groupStatusItems(report)
	for _, group := range rootGroups {
		if err := writeStatusGroup(out, group); err != nil {
			return err
		}
	}
	if individual.dirty() {
		return writeStatusGroup(out, individual)
	}
	return nil
}

func groupStatusItems(report statusReport) ([]statusGroup, statusGroup) {
	groupsByRoot := make(map[string]*statusGroup, len(report.TrackedDirs))
	rootGroups := make([]*statusGroup, 0, len(report.TrackedDirs))
	for _, dir := range report.TrackedDirs {
		group := &statusGroup{Title: "Tracked root: " + dir.Path}
		groupsByRoot[dir.Path] = group
		rootGroups = append(rootGroups, group)
	}
	individual := statusGroup{Title: "Individual paths:"}
	groupForPath := func(trackedPath string) *statusGroup {
		root, ok := trackedRootForPath(report.TrackedDirs, trackedPath)
		if !ok {
			return &individual
		}
		return groupsByRoot[root]
	}

	for _, item := range report.Repo {
		group := groupForPath(item.Path)
		group.Repo = append(group.Repo, item)
	}
	for _, item := range report.Directory {
		group := groupForPath(item.Path)
		group.Directory = append(group.Directory, item)
	}
	for _, item := range report.Pending {
		group := groupForPath(item.Path)
		group.Pending = append(group.Pending, item)
	}
	for _, item := range report.Conflict {
		group := groupForPath(item.Path)
		group.Conflict = append(group.Conflict, item)
	}
	for _, item := range report.State {
		group := groupForPath(item.Path)
		group.State = append(group.State, item)
	}

	groups := make([]statusGroup, 0, len(rootGroups))
	for _, group := range rootGroups {
		if group.dirty() {
			groups = append(groups, *group)
		}
	}
	return groups, individual
}

func trackedRootForPath(trackedDirs []TrackedDirRecord, trackedPath string) (string, bool) {
	best := ""
	for _, dir := range trackedDirs {
		if trackedPathInsideRoot(dir.Path, trackedPath) && len(dir.Path) > len(best) {
			best = dir.Path
		}
	}
	return best, best != ""
}

func (g statusGroup) dirty() bool {
	return len(g.Repo) > 0 || len(g.Directory) > 0 || len(g.Pending) > 0 || len(g.Conflict) > 0 || len(g.State) > 0
}

func writeStatusGroup(out io.Writer, group statusGroup) error {
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, group.Title); err != nil {
		return err
	}
	if err := writeIndentedStatusSection(out, "Repo drift", group.Repo, "  ", "    "); err != nil {
		return err
	}
	if err := writeIndentedStatusSection(out, "Directory drift", group.Directory, "  ", "    "); err != nil {
		return err
	}
	if err := writeIndentedStatusSection(out, "Pending changes", group.Pending, "  ", "    "); err != nil {
		return err
	}
	if err := writeIndentedStatusSection(out, "Conflicts", group.Conflict, "  ", "    "); err != nil {
		return err
	}
	return writeIndentedStatusSection(out, "Applied state", group.State, "  ", "    ")
}

func writeStatusSection(out io.Writer, title string, items []statusItem) error {
	return writeIndentedStatusSection(out, title, items, "", "  ")
}

func writeIndentedStatusSection(out io.Writer, title string, items []statusItem, sectionIndent, itemIndent string) error {
	if len(items) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(out, "%s%s:\n", sectionIndent, title); err != nil {
		return err
	}
	for _, item := range items {
		if item.Detail == "" {
			if _, err := fmt.Fprintf(out, "%s%s: %s\n", itemIndent, item.Kind, item.Path); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(out, "%s%s: %s (%s)\n", itemIndent, item.Kind, item.Path, item.Detail); err != nil {
				return err
			}
		}
	}
	return nil
}

package dots

import (
	"fmt"
	"sort"
)

type pathScope struct {
	paths []string
}

func newPathScope(rt *Runtime, args []string) (pathScope, error) {
	if len(args) == 0 {
		return pathScope{}, nil
	}
	paths := make([]string, 0, len(args))
	seen := make(map[string]struct{}, len(args))
	for _, arg := range args {
		trackedPath, err := trackedPathFromArg(rt, arg)
		if err != nil {
			return pathScope{}, err
		}
		if _, ok := seen[trackedPath]; ok {
			continue
		}
		paths = append(paths, trackedPath)
		seen[trackedPath] = struct{}{}
	}
	sort.Strings(paths)
	return pathScope{paths: paths}, nil
}

func (s pathScope) active() bool {
	return len(s.paths) > 0
}

func (s pathScope) contains(trackedPath string) bool {
	if !s.active() {
		return true
	}
	for _, scopePath := range s.paths {
		if trackedPathInsideRoot(scopePath, trackedPath) {
			return true
		}
	}
	return false
}

func (s pathScope) filterReport(report statusReport) statusReport {
	if !s.active() {
		return report
	}
	filtered := statusReport{Profile: report.Profile, TrackedFiles: report.TrackedFiles, TrackedDirs: report.TrackedDirs}
	filtered.Repo = s.filterStatusItems(report.Repo)
	filtered.Directory = s.filterStatusItems(report.Directory)
	filtered.Pending = s.filterStatusItems(report.Pending)
	filtered.Conflict = s.filterStatusItems(report.Conflict)
	filtered.State = s.filterStatusItems(report.State)
	return filtered
}

func (s pathScope) filterStatusItems(items []statusItem) []statusItem {
	if !s.active() {
		return items
	}
	filtered := make([]statusItem, 0, len(items))
	for _, item := range items {
		if s.contains(item.Path) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (s pathScope) filterRecords(records []FileRecord) []FileRecord {
	if !s.active() {
		return records
	}
	filtered := make([]FileRecord, 0, len(records))
	for _, record := range records {
		if s.contains(record.Path) {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func validatePathScope(scope pathScope, inputs statusInputs, report statusReport) error {
	if !scope.active() {
		return nil
	}
	for _, scopePath := range scope.paths {
		if scopePathMatchesInputs(scopePath, inputs) || scopePathMatchesReport(scopePath, report) {
			continue
		}
		return fmt.Errorf("path is not tracked and has no pending status: %s", scopePath)
	}
	return nil
}

func scopePathMatchesInputs(scopePath string, inputs statusInputs) bool {
	for _, record := range inputs.Records {
		if trackedPathInsideRoot(scopePath, record.Path) {
			return true
		}
	}
	for _, dir := range inputs.TrackedDirs {
		if trackedPathInsideRoot(scopePath, dir.Path) || trackedPathInsideRoot(dir.Path, scopePath) {
			return true
		}
	}
	for _, stateRecord := range inputs.StateRecords {
		if trackedPathInsideRoot(scopePath, stateRecord.Path) {
			return true
		}
	}
	return false
}

func scopePathMatchesReport(scopePath string, report statusReport) bool {
	for _, items := range [][]statusItem{report.Repo, report.Directory, report.Pending, report.Conflict, report.State} {
		for _, item := range items {
			if trackedPathInsideRoot(scopePath, item.Path) {
				return true
			}
		}
	}
	return false
}

func mergeFileRecords(base, updates []FileRecord) []FileRecord {
	if len(updates) == 0 {
		return base
	}
	byPath := fileRecordMap(base)
	for _, update := range updates {
		byPath[update.Path] = update
	}
	merged := make([]FileRecord, 0, len(byPath))
	for _, record := range byPath {
		merged = append(merged, record)
	}
	sortFileRecords(merged)
	return merged
}

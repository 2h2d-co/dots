package dots

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

type addPlanItem struct {
	Source string
	Record FileRecord
}

func addPath(rt *Runtime, target string) ([]FileRecord, error) {
	plan, err := collectAddPlan(rt, target)
	if err != nil {
		return nil, err
	}
	return executeAddPlan(rt, plan)
}

func collectAddPlan(rt *Runtime, target string) ([]addPlanItem, error) {
	expanded, err := expandPath(target)
	if err != nil {
		return nil, err
	}
	absTarget, err := filepath.Abs(expanded)
	if err != nil {
		return nil, fmt.Errorf("resolve target path: %w", err)
	}
	if err := rejectRepoTarget(rt, absTarget); err != nil {
		return nil, err
	}
	info, err := os.Lstat(absTarget)
	if err != nil {
		return nil, fmt.Errorf("stat target %s: %w", absTarget, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing to track symlink: %s", absTarget)
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return nil, fmt.Errorf("unsupported file type: %s", absTarget)
	}

	if !info.IsDir() {
		trackedPath, err := homeRelativePath(rt.Home, absTarget)
		if err != nil {
			return nil, err
		}
		item, err := newAddPlanItem(absTarget, trackedPath, info)
		if err != nil {
			return nil, err
		}
		return []addPlanItem{item}, nil
	}

	if _, err := homeRelativePath(rt.Home, absTarget); err != nil {
		return nil, err
	}

	matcher, err := loadDotsIgnore(filepath.Join(absTarget, ".dotsignore"))
	if err != nil {
		return nil, fmt.Errorf("load .dotsignore: %w", err)
	}

	var plan []addPlanItem
	if err := filepath.WalkDir(absTarget, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == absTarget {
			return nil
		}
		relFromRoot, err := filepath.Rel(absTarget, path)
		if err != nil {
			return err
		}
		relFromRoot = filepath.ToSlash(relFromRoot)
		if matcher.ignored(relFromRoot, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to track symlink: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type: %s", path)
		}
		trackedPath, err := homeRelativePath(rt.Home, path)
		if err != nil {
			return err
		}
		item, err := newAddPlanItem(path, trackedPath, info)
		if err != nil {
			return err
		}
		plan = append(plan, item)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("add directory %s: %w", absTarget, err)
	}
	sort.Slice(plan, func(i, j int) bool {
		return plan[i].Record.Path < plan[j].Record.Path
	})
	return plan, nil
}

func rejectRepoTarget(rt *Runtime, target string) error {
	repos := rt.ConfiguredRepos
	if len(repos) == 0 {
		repos = []string{rt.Repo}
	}
	for _, repo := range repos {
		insideRepo, err := pathInsideOrEqual(repo, target)
		if err != nil {
			return err
		}
		if insideRepo {
			return fmt.Errorf("refusing to add paths from the dots repo %s: %s", repo, target)
		}
	}
	return nil
}

func newAddPlanItem(src, trackedPath string, info fs.FileInfo) (addPlanItem, error) {
	hash, err := hashFile(src)
	if err != nil {
		return addPlanItem{}, err
	}
	return addPlanItem{
		Source: src,
		Record: FileRecord{
			Path:   trackedPath,
			SHA256: hash,
			Mode:   int64(info.Mode().Perm()),
			Size:   info.Size(),
		},
	}, nil
}

func executeAddPlan(rt *Runtime, plan []addPlanItem) ([]FileRecord, error) {
	records := make([]FileRecord, 0, len(plan))
	for _, item := range plan {
		if err := copyFile(item.Source, repoFilePath(rt, item.Record.Path), item.Record.Mode); err != nil {
			return nil, err
		}
		record, err := fileRecord(profileDir(rt), item.Record.Path)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	sortFileRecords(records)
	return records, nil
}

func writeAddPlan(out io.Writer, rt *Runtime, plan []addPlanItem) error {
	if _, err := fmt.Fprintln(out, "Add plan (dry run; no files changed):"); err != nil {
		return err
	}
	for _, item := range plan {
		if _, err := fmt.Fprintf(out, "  %s\n", item.Record.Path); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(out, "Would add %d file(s) to profile %s\n", len(plan), rt.Profile)
	return err
}

func targetOrCurrent(args []string) (string, error) {
	if len(args) > 1 {
		return "", errors.New("accepts at most one path")
	}
	if len(args) == 1 {
		return args[0], nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current directory: %w", err)
	}
	return cwd, nil
}

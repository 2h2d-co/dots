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

type addPlan struct {
	Items       []addPlanItem
	TrackedDirs []TrackedDirRecord
}

type addPlanItem struct {
	Source string
	Record FileRecord
}

func addPath(rt *Runtime, target string) ([]FileRecord, error) {
	plan, err := collectAddPlan(rt, target)
	if err != nil {
		return nil, err
	}
	return executeAddPlan(rt, plan.Items)
}

func collectAddPlan(rt *Runtime, target string) (addPlan, error) {
	expanded, err := expandPath(target)
	if err != nil {
		return addPlan{}, err
	}
	absTarget, err := filepath.Abs(expanded)
	if err != nil {
		return addPlan{}, fmt.Errorf("resolve target path: %w", err)
	}
	if err := rejectRepoTarget(rt, absTarget); err != nil {
		return addPlan{}, err
	}
	info, err := os.Lstat(absTarget)
	if err != nil {
		return addPlan{}, fmt.Errorf("stat target %s: %w", absTarget, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return addPlan{}, fmt.Errorf("refusing to track symlink: %s", absTarget)
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return addPlan{}, fmt.Errorf("unsupported file type: %s", absTarget)
	}

	if !info.IsDir() {
		trackedPath, err := homeRelativePath(rt.Home, absTarget)
		if err != nil {
			return addPlan{}, err
		}
		item, err := newAddPlanItem(absTarget, trackedPath, info)
		if err != nil {
			return addPlan{}, err
		}
		return addPlan{Items: []addPlanItem{item}}, nil
	}

	trackedRoot, err := homeRelativePath(rt.Home, absTarget)
	if err != nil {
		return addPlan{}, err
	}

	matcher, err := loadDotsIgnore(filepath.Join(absTarget, ".dotsignore"))
	if err != nil {
		return addPlan{}, fmt.Errorf("load .dotsignore: %w", err)
	}

	var items []addPlanItem
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
		items = append(items, item)
		return nil
	}); err != nil {
		return addPlan{}, fmt.Errorf("add directory %s: %w", absTarget, err)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Record.Path < items[j].Record.Path
	})
	return addPlan{
		Items:       items,
		TrackedDirs: []TrackedDirRecord{{Path: trackedRoot}},
	}, nil
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

func writeAddPlan(out io.Writer, rt *Runtime, plan addPlan) error {
	if _, err := fmt.Fprintln(out, "Add plan (dry run; no files changed):"); err != nil {
		return err
	}
	if len(plan.TrackedDirs) > 0 {
		if _, err := fmt.Fprintln(out, "Directory roots:"); err != nil {
			return err
		}
		for _, dir := range plan.TrackedDirs {
			if _, err := fmt.Fprintf(out, "  %s\n", dir.Path); err != nil {
				return err
			}
		}
	}
	if len(plan.Items) > 0 && len(plan.TrackedDirs) > 0 {
		if _, err := fmt.Fprintln(out, "Files:"); err != nil {
			return err
		}
	}
	for _, item := range plan.Items {
		if _, err := fmt.Fprintf(out, "  %s\n", item.Record.Path); err != nil {
			return err
		}
	}
	if len(plan.TrackedDirs) > 0 {
		if _, err := fmt.Fprintf(out, "Would track %d directory root(s)\n", len(plan.TrackedDirs)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(out, "Would add %d file(s) to profile %s\n", len(plan.Items), rt.Profile)
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

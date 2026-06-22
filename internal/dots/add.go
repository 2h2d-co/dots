package dots

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func addPath(rt *Runtime, target string) ([]FileRecord, error) {
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
		return copyOneTrackedFile(rt, absTarget, trackedPath, info)
	}

	rootTrackedPath, err := homeRelativePath(rt.Home, absTarget)
	if err != nil {
		return nil, err
	}
	_ = rootTrackedPath

	matcher, err := loadDotsIgnore(filepath.Join(absTarget, ".dotsignore"))
	if err != nil {
		return nil, fmt.Errorf("load .dotsignore: %w", err)
	}

	var records []FileRecord
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
		record, err := copyTrackedFile(rt, path, trackedPath, info)
		if err != nil {
			return err
		}
		records = append(records, record)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("add directory %s: %w", absTarget, err)
	}
	sortFileRecords(records)
	return records, nil
}

func rejectRepoTarget(rt *Runtime, target string) error {
	insideRepo, err := pathInsideOrEqual(rt.Repo, target)
	if err != nil {
		return err
	}
	if insideRepo {
		return fmt.Errorf("refusing to add paths from the dots repo: %s", target)
	}
	return nil
}

func copyOneTrackedFile(rt *Runtime, src, trackedPath string, info fs.FileInfo) ([]FileRecord, error) {
	record, err := copyTrackedFile(rt, src, trackedPath, info)
	if err != nil {
		return nil, err
	}
	return []FileRecord{record}, nil
}

func copyTrackedFile(rt *Runtime, src, trackedPath string, info fs.FileInfo) (FileRecord, error) {
	dst := repoFilePath(rt, trackedPath)
	if err := copyFileFromInfo(src, dst, info); err != nil {
		return FileRecord{}, err
	}
	record, err := fileRecord(profileDir(rt), trackedPath)
	if err != nil {
		return FileRecord{}, err
	}
	return record, nil
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

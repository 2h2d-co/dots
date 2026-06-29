package dots

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func profileDir(rt *Runtime) string {
	return filepath.Join(rt.Repo, rt.Profile)
}

func repoFilePath(rt *Runtime, trackedPath string) string {
	return filepath.Join(profileDir(rt), filepath.FromSlash(trackedPath))
}

func destinationPath(rt *Runtime, trackedPath string) string {
	return filepath.Join(rt.Home, filepath.FromSlash(trackedPath))
}

func homeRelativePath(home, target string) (string, error) {
	absTarget, err := comparablePath(target)
	if err != nil {
		return "", fmt.Errorf("resolve target path: %w", err)
	}
	absHome, err := comparablePath(home)
	if err != nil {
		return "", fmt.Errorf("resolve home path: %w", err)
	}
	rel, err := filepath.Rel(absHome, absTarget)
	if err != nil {
		return "", fmt.Errorf("resolve home-relative path: %w", err)
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." {
		return "", errors.New("refusing to track the home directory itself")
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("target %s is outside home directory %s", absTarget, absHome)
	}
	return cleanTrackedPath(rel)
}

func comparablePath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absPath)
	if err == nil {
		return resolved, nil
	}

	current := absPath
	missing := []string{}
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return absPath, nil
		}
		missing = append([]string{filepath.Base(current)}, missing...)
		current = parent
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			parts := append([]string{resolved}, missing...)
			return filepath.Join(parts...), nil
		}
	}
}

func pathInsideOrEqual(root, target string) (bool, error) {
	comparableRoot, err := comparablePath(root)
	if err != nil {
		return false, fmt.Errorf("resolve root path: %w", err)
	}
	comparableTarget, err := comparablePath(target)
	if err != nil {
		return false, fmt.Errorf("resolve target path: %w", err)
	}
	rel, err := filepath.Rel(comparableRoot, comparableTarget)
	if err != nil {
		return false, fmt.Errorf("compare paths: %w", err)
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../")), nil
}

func trackedPathFromArg(rt *Runtime, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("path is required")
	}
	if strings.HasPrefix(raw, "~") || filepath.IsAbs(raw) {
		expanded, err := expandPath(raw)
		if err != nil {
			return "", err
		}
		return homeRelativePath(rt.Home, expanded)
	}
	return cleanTrackedPath(raw)
}

func fileRecord(root, trackedPath string) (FileRecord, error) {
	path := filepath.Join(root, filepath.FromSlash(trackedPath))
	info, err := os.Lstat(path)
	if err != nil {
		return FileRecord{}, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return FileRecord{}, fmt.Errorf("unsupported file type at %s", path)
	}
	hash, err := hashFile(path)
	if err != nil {
		return FileRecord{}, err
	}
	return FileRecord{
		Path:   trackedPath,
		SHA256: hash,
		Mode:   int64(info.Mode().Perm()),
		Size:   info.Size(),
	}, nil
}

func destinationFingerprint(path string) (sha string, mode int64, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("unsupported file type at %s", path)
	}
	hash, err := hashFile(path)
	if err != nil {
		return "", 0, err
	}
	return hash, int64(info.Mode().Perm()), nil
}

func hashFile(path string) (string, error) {
	cleaned := filepath.Clean(path)
	file, err := os.Open(cleaned)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", cleaned, err)
	}
	defer func() { _ = file.Close() }()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("hash %s: %w", cleaned, err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func copyFile(src, dst string, mode int64) error {
	cleanedSrc := filepath.Clean(src)
	srcFile, err := os.Open(cleanedSrc)
	if err != nil {
		return fmt.Errorf("open source %s: %w", cleanedSrc, err)
	}
	defer func() { _ = srcFile.Close() }()

	fileMode, err := fileModeFromRecord(mode)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return fmt.Errorf("create destination directory for %s: %w", dst, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".dots-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", dst, err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := io.Copy(tmp, srcFile); err != nil {
		return errors.Join(fmt.Errorf("copy %s to %s: %w", cleanedSrc, dst, err), tmp.Close())
	}
	if err := tmp.Chmod(fileMode); err != nil {
		return errors.Join(fmt.Errorf("set mode on %s: %w", tmpPath, err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file for %s: %w", dst, err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("install %s: %w", dst, err)
	}
	return nil
}

func fileModeFromRecord(mode int64) (fs.FileMode, error) {
	if mode < 0 || mode > 0o777 {
		return 0, fmt.Errorf("invalid file mode %o", mode)
	}
	return fs.FileMode(mode).Perm(), nil
}

func collectProfileRecords(rt *Runtime) ([]FileRecord, error) {
	root := profileDir(rt)
	var records []FileRecord
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("stat profile directory %s: %w", root, err)
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		trackedPath, err := cleanTrackedPath(rel)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type in profile: %s", trackedPath)
		}
		record, err := fileRecord(root, trackedPath)
		if err != nil {
			return err
		}
		records = append(records, record)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("scan profile directory: %w", err)
	}
	sortFileRecords(records)
	return records, nil
}

func removeRepoPath(rt *Runtime, trackedPath string) error {
	path := repoFilePath(rt, trackedPath)
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove repo path %s: %w", path, err)
	}
	return pruneEmptyDirs(profileDir(rt), filepath.Dir(path))
}

func pruneEmptyDirs(root, start string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	current, err := filepath.Abs(start)
	if err != nil {
		return err
	}
	for current != root {
		if !strings.HasPrefix(current, root+string(os.PathSeparator)) {
			return nil
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if len(entries) > 0 {
			return nil
		}
		if err := os.Remove(current); err != nil {
			return err
		}
		current = filepath.Dir(current)
	}
	return nil
}

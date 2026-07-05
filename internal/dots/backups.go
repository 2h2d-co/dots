package dots

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	backupOriginHome = "home"
	backupOriginRepo = "repo"
	backupPayload    = "payload"
)

type backupSetWriter struct {
	baseDir   string
	root      string
	allocated bool
}

func newBackupSetWriter(rt *Runtime) *backupSetWriter {
	return &backupSetWriter{baseDir: filepath.Join(rt.StateDir, "backups", rt.Profile)}
}

func (w *backupSetWriter) backupPath(origin, trackedPath string) (string, error) {
	if origin != backupOriginHome && origin != backupOriginRepo {
		return "", fmt.Errorf("invalid backup origin %q", origin)
	}
	cleaned, err := cleanTrackedPath(trackedPath)
	if err != nil {
		return "", err
	}
	if err := w.ensureAllocated(); err != nil {
		return "", err
	}
	entry := base64.RawURLEncoding.EncodeToString([]byte(cleaned))
	return filepath.Join(w.root, origin, entry, backupPayload), nil
}

func (w *backupSetWriter) ensureAllocated() error {
	if w.allocated {
		return nil
	}
	_, root, err := allocateBackupSet(w.baseDir, time.Now().UTC())
	if err != nil {
		return err
	}
	w.root = root
	w.allocated = true
	return nil
}

func (w *backupSetWriter) Written() bool {
	return w != nil && w.allocated
}

func (w *backupSetWriter) Path() string {
	if w == nil {
		return ""
	}
	return w.root
}

func allocateBackupSet(baseDir string, now time.Time) (string, string, error) {
	if err := os.MkdirAll(baseDir, 0o750); err != nil {
		return "", "", fmt.Errorf("create backup profile directory: %w", err)
	}
	day := now.Format("20060102")
	index, err := nextBackupSetIndex(baseDir, day)
	if err != nil {
		return "", "", err
	}
	for {
		id := fmt.Sprintf("%s.%d", day, index)
		root := filepath.Join(baseDir, id)
		err := os.Mkdir(root, 0o750)
		if err == nil {
			return id, root, nil
		}
		if !os.IsExist(err) {
			return "", "", fmt.Errorf("create backup set %s: %w", root, err)
		}
		index++
	}
}

func nextBackupSetIndex(baseDir, day string) (int, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, fmt.Errorf("read backup profile directory: %w", err)
	}
	prefix := day + "."
	maxIndex := 0
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		index, err := strconv.Atoi(strings.TrimPrefix(entry.Name(), prefix))
		if err == nil && index > maxIndex {
			maxIndex = index
		}
	}
	return maxIndex + 1, nil
}

func backupRepoFile(path string, backups *backupSetWriter, trackedPath string) error {
	if backups == nil {
		return fmt.Errorf("backup writer is required for repo backup")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat repo file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported repo file type at %s", path)
	}
	backupPath, err := backups.backupPath(backupOriginRepo, trackedPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o750); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	if err := os.Rename(path, backupPath); err != nil {
		return fmt.Errorf("backup %s to %s: %w", path, backupPath, err)
	}
	return nil
}

//go:build integration

// Package integration contains end-to-end tests for dots.
package integration

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

import _ "modernc.org/sqlite" // Register SQLite driver for integration test database fixtures.

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	if dir != "" {
		cmd.Dir = dir
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func writePersonalConfig(t *testing.T, env testEnv) {
	t.Helper()
	content := fmt.Sprintf("default_profile = \"personal\"\n\n[profiles]\npersonal = %q\n", env.repo)
	writeFile(t, filepath.Join(env.config, "dots", "config.toml"), content, 0o600)
}

func createLegacyPersonalRepoDB(t *testing.T, path string, schemaVersion int, trackedDirs []string) {
	t.Helper()
	withSQLiteDatabase(t, path, func(db *sql.DB) {
		execSQL := func(query string, args ...any) {
			t.Helper()
			if _, err := db.Exec(query, args...); err != nil {
				t.Fatalf("execute legacy repo SQL: %v", err)
			}
		}

		execSQL(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
		execSQL(`INSERT INTO meta (key, value) VALUES ('profile', 'personal')`)
		execSQL(`INSERT INTO meta (key, value) VALUES ('schema_version', ?)`, fmt.Sprintf("%d", schemaVersion))
		execSQL(`CREATE TABLE files (
			path TEXT PRIMARY KEY,
			sha256 TEXT NOT NULL,
			mode INTEGER NOT NULL,
			size INTEGER NOT NULL,
			updated_at TEXT NOT NULL
		)`)
		switch schemaVersion {
		case 1:
			return
		case 2:
			execSQL(`CREATE TABLE tracked_dirs (
				path TEXT PRIMARY KEY,
				updated_at TEXT NOT NULL
			)`)
			for _, trackedDir := range trackedDirs {
				execSQL(`INSERT INTO tracked_dirs (path, updated_at) VALUES (?, 'legacy')`, trackedDir)
			}
		default:
			t.Fatalf("unsupported legacy repo schema version: %d", schemaVersion)
		}
	})
}

func createLegacyPersonalStateDB(t *testing.T, path string) {
	t.Helper()
	withSQLiteDatabase(t, path, func(db *sql.DB) {
		execSQL := func(query string, args ...any) {
			t.Helper()
			if _, err := db.Exec(query, args...); err != nil {
				t.Fatalf("execute legacy state SQL: %v", err)
			}
		}

		execSQL(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
		execSQL(`INSERT INTO meta (key, value) VALUES ('profile', 'personal')`)
		execSQL(`INSERT INTO meta (key, value) VALUES ('schema_version', '1')`)
		execSQL(`CREATE TABLE files (
			path TEXT PRIMARY KEY,
			sha256 TEXT NOT NULL,
			repo_sha256 TEXT NOT NULL,
			mode INTEGER NOT NULL,
			size INTEGER NOT NULL,
			applied_at TEXT NOT NULL
		)`)
	})
}

func assertSQLiteMigrationVersion(t *testing.T, path string, want int64) {
	t.Helper()
	withSQLiteDatabase(t, path, func(db *sql.DB) {
		var got int64
		if err := db.QueryRow(`SELECT MAX(version_id) FROM dots_schema_migrations`).Scan(&got); err != nil {
			t.Fatalf("read migration version from %s: %v", path, err)
		}
		if got != want {
			t.Fatalf("migration version for %s = %d, want %d", path, got, want)
		}
	})
}

func withSQLiteDatabase(t *testing.T, path string, fn func(*sql.DB)) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create sqlite parent directory: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite database: %v", err)
		}
	}()
	fn(db)
}

func writeFile(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create parent for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func setSQLiteProfileMetadata(t *testing.T, path, profile string) {
	t.Helper()
	withSQLiteDatabase(t, path, func(db *sql.DB) {
		if _, err := db.Exec(`UPDATE meta SET value = ? WHERE key = 'profile'`, profile); err != nil {
			t.Fatalf("update profile metadata: %v", err)
		}
	})
}

func assertExitCode(t *testing.T, result runResult, want int) {
	t.Helper()
	if result.code != want {
		t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", result.code, want, result.stdout, result.stderr)
	}
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output missing %q\noutput:\n%s", want, got)
	}
}

func assertNotContains(t *testing.T, got, want string) {
	t.Helper()
	if strings.Contains(got, want) {
		t.Fatalf("output unexpectedly contains %q\noutput:\n%s", want, got)
	}
}

func assertInOrder(t *testing.T, got, first, second string) {
	t.Helper()
	firstIndex := strings.Index(got, first)
	secondIndex := strings.Index(got, second)
	if firstIndex < 0 || secondIndex < 0 || firstIndex >= secondIndex {
		t.Fatalf("output does not contain %q before %q\noutput:\n%s", first, second, got)
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be missing, stat err = %v", path, err)
	}
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(content) != want {
		t.Fatalf("%s content = %q, want %q", path, content, want)
	}
}

func assertBackupContains(t *testing.T, backupRoot, relPath, want string) {
	t.Helper()
	assertBackupContainsOrigin(t, backupRoot, "", relPath, want)
}

func assertBackupContainsOrigin(t *testing.T, backupRoot, origin, relPath, want string) {
	t.Helper()
	encoded := base64.RawURLEncoding.EncodeToString([]byte(filepath.ToSlash(relPath)))
	found := false
	if err := filepath.WalkDir(backupRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.ToSlash(path) == filepath.ToSlash(backupRoot) {
			return nil
		}
		slashPath := filepath.ToSlash(path)
		if !strings.HasSuffix(slashPath, "/"+encoded+"/payload") {
			return nil
		}
		if origin != "" && !strings.Contains(slashPath, "/"+origin+"/") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found = found || string(content) == want
		return nil
	}); err != nil {
		t.Fatalf("walk backups: %v", err)
	}
	if !found {
		t.Fatalf("backup %s under %s with origin %q and content %q not found", relPath, backupRoot, origin, want)
	}
}

func deleteStateRecord(t *testing.T, env testEnv, trackedPath string) {
	t.Helper()
	withSQLiteDatabase(t, filepath.Join(env.state, "dots", "personal.db"), func(db *sql.DB) {
		if _, err := db.Exec(`DELETE FROM files WHERE path = ?`, filepath.ToSlash(trackedPath)); err != nil {
			t.Fatalf("delete state record: %v", err)
		}
	})
}

func repoDBFilesDigest(t *testing.T, env testEnv) string {
	t.Helper()
	var digest strings.Builder
	withSQLiteDatabase(t, filepath.Join(env.repo, "personal.db"), func(db *sql.DB) {
		rows, err := db.Query(`SELECT path, sha256, mode, size FROM files ORDER BY path`)
		if err != nil {
			t.Fatalf("query repo records: %v", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var path string
			var sha string
			var mode int64
			var size int64
			if err := rows.Scan(&path, &sha, &mode, &size); err != nil {
				t.Fatalf("scan repo record: %v", err)
			}
			fmt.Fprintf(&digest, "%s\t%s\t%d\t%d\n", path, sha, mode, size)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate repo records: %v", err)
		}
	})
	return digest.String()
}

func stateDBFilesDigest(t *testing.T, env testEnv) string {
	t.Helper()
	var digest strings.Builder
	withSQLiteDatabase(t, filepath.Join(env.state, "dots", "personal.db"), func(db *sql.DB) {
		rows, err := db.Query(`SELECT path, sha256, repo_sha256, mode, size FROM files ORDER BY path`)
		if err != nil {
			t.Fatalf("query state records: %v", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var path string
			var sha string
			var repoSHA string
			var mode int64
			var size int64
			if err := rows.Scan(&path, &sha, &repoSHA, &mode, &size); err != nil {
				t.Fatalf("scan state record: %v", err)
			}
			fmt.Fprintf(&digest, "%s\t%s\t%s\t%d\t%d\n", path, sha, repoSHA, mode, size)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate state records: %v", err)
		}
	})
	return digest.String()
}

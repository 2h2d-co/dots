package dots

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Register the SQLite database/sql driver.
)

const sqliteDriver = "sqlite"

// FileRecord describes one tracked regular file.
type FileRecord struct {
	Path   string
	SHA256 string
	Mode   int64
	Size   int64
}

// StateRecord describes one applied regular file.
type StateRecord struct {
	Path    string
	SHA256  string
	RepoSHA string
	Mode    int64
	Size    int64
}

func repoDBPath(repo, profile string) string {
	return filepath.Join(repo, profile+".db")
}

func stateDBPath(stateDir, profile string) string {
	return filepath.Join(stateDir, profile+".db")
}

func ensureRepoDB(repo, profile string) error {
	db, err := openSQLite(repoDBPath(repo, profile))
	if err != nil {
		return err
	}
	return errors.Join(migrateRepoDB(db, profile), db.Close())
}

func ensureStateDB(stateDir, profile string) error {
	db, err := openSQLite(stateDBPath(stateDir, profile))
	if err != nil {
		return err
	}
	return errors.Join(migrateStateDB(db, profile), db.Close())
}

func openRepoDB(repo, profile string) (*sql.DB, error) {
	db, err := openSQLite(repoDBPath(repo, profile))
	if err != nil {
		return nil, err
	}
	if err := migrateRepoDB(db, profile); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return db, nil
}

func openStateDB(stateDir, profile string) (*sql.DB, error) {
	db, err := openSQLite(stateDBPath(stateDir, profile))
	if err != nil {
		return nil, err
	}
	if err := migrateStateDB(db, profile); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return db, nil
}

func openSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open(sqliteDriver, path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database %s: %w", path, err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return nil, errors.Join(fmt.Errorf("configure sqlite database %s: %w", path, err), db.Close())
	}
	return db, nil
}

func migrateRepoDB(db *sql.DB, profile string) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS files (
			path TEXT PRIMARY KEY,
			sha256 TEXT NOT NULL,
			mode INTEGER NOT NULL,
			size INTEGER NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("migrate repo database: %w", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES ('schema_version', '1') ON CONFLICT(key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("record repo schema version: %w", err)
	}
	if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES ('profile', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, profile); err != nil {
		return fmt.Errorf("record repo profile: %w", err)
	}
	return nil
}

func migrateStateDB(db *sql.DB, profile string) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS files (
			path TEXT PRIMARY KEY,
			sha256 TEXT NOT NULL,
			repo_sha256 TEXT NOT NULL,
			mode INTEGER NOT NULL,
			size INTEGER NOT NULL,
			applied_at TEXT NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("migrate state database: %w", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES ('schema_version', '1') ON CONFLICT(key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("record state schema version: %w", err)
	}
	if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES ('profile', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, profile); err != nil {
		return fmt.Errorf("record state profile: %w", err)
	}
	return nil
}

func listRepoRecords(db *sql.DB) ([]FileRecord, error) {
	rows, err := db.Query(`SELECT path, sha256, mode, size FROM files ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("list tracked files: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var records []FileRecord
	for rows.Next() {
		var record FileRecord
		if err := rows.Scan(&record.Path, &record.SHA256, &record.Mode, &record.Size); err != nil {
			return nil, fmt.Errorf("scan tracked file: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tracked files: %w", err)
	}
	return records, nil
}

func listStateRecords(db *sql.DB) ([]StateRecord, error) {
	rows, err := db.Query(`SELECT path, sha256, repo_sha256, mode, size FROM files ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("list applied files: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var records []StateRecord
	for rows.Next() {
		var record StateRecord
		if err := rows.Scan(&record.Path, &record.SHA256, &record.RepoSHA, &record.Mode, &record.Size); err != nil {
			return nil, fmt.Errorf("scan applied file: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list applied files: %w", err)
	}
	return records, nil
}

func upsertRepoRecords(db *sql.DB, records []FileRecord) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin repo update: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	statement, err := tx.Prepare(`INSERT INTO files (path, sha256, mode, size, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			sha256 = excluded.sha256,
			mode = excluded.mode,
			size = excluded.size,
			updated_at = excluded.updated_at`)
	if err != nil {
		return fmt.Errorf("prepare repo update: %w", err)
	}
	defer func() { _ = statement.Close() }()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, record := range records {
		if _, err := statement.Exec(record.Path, record.SHA256, record.Mode, record.Size, now); err != nil {
			return fmt.Errorf("update tracked file %s: %w", record.Path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit repo update: %w", err)
	}
	return nil
}

func replaceRepoRecords(db *sql.DB, records []FileRecord) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin repo replacement: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	if _, err := tx.Exec(`DELETE FROM files`); err != nil {
		return fmt.Errorf("clear tracked files: %w", err)
	}
	statement, err := tx.Prepare(`INSERT INTO files (path, sha256, mode, size, updated_at) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare repo replacement: %w", err)
	}
	defer func() { _ = statement.Close() }()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, record := range records {
		if _, err := statement.Exec(record.Path, record.SHA256, record.Mode, record.Size, now); err != nil {
			return fmt.Errorf("insert tracked file %s: %w", record.Path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit repo replacement: %w", err)
	}
	return nil
}

func replaceStateRecords(db *sql.DB, records []FileRecord) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin state replacement: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	if _, err := tx.Exec(`DELETE FROM files`); err != nil {
		return fmt.Errorf("clear applied files: %w", err)
	}
	statement, err := tx.Prepare(`INSERT INTO files (path, sha256, repo_sha256, mode, size, applied_at) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare state replacement: %w", err)
	}
	defer func() { _ = statement.Close() }()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, record := range records {
		if _, err := statement.Exec(record.Path, record.SHA256, record.SHA256, record.Mode, record.Size, now); err != nil {
			return fmt.Errorf("insert applied file %s: %w", record.Path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit state replacement: %w", err)
	}
	return nil
}

func forgetRecords(repoDB, stateDB *sql.DB, paths []string) error {
	if err := deleteMatchingRecords(repoDB, paths); err != nil {
		return err
	}
	if err := deleteMatchingRecords(stateDB, paths); err != nil {
		return err
	}
	return nil
}

func deleteMatchingRecords(db *sql.DB, paths []string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin delete records: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	for _, p := range paths {
		if _, err := tx.Exec(`DELETE FROM files WHERE path = ? OR path LIKE ?`, p, p+"/%"); err != nil {
			return fmt.Errorf("delete records for %s: %w", p, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete records: %w", err)
	}
	return nil
}

func fileRecordMap(records []FileRecord) map[string]FileRecord {
	result := make(map[string]FileRecord, len(records))
	for _, record := range records {
		result[record.Path] = record
	}
	return result
}

func stateRecordMap(records []StateRecord) map[string]StateRecord {
	result := make(map[string]StateRecord, len(records))
	for _, record := range records {
		result[record.Path] = record
	}
	return result
}

func sortFileRecords(records []FileRecord) {
	sort.Slice(records, func(i, j int) bool {
		return records[i].Path < records[j].Path
	})
}

func cleanTrackedPath(raw string) (string, error) {
	p := filepath.ToSlash(filepath.Clean(raw))
	p = strings.TrimPrefix(p, "./")
	if p == "." || p == "" {
		return "", errors.New("path is required")
	}
	if strings.HasPrefix(p, "../") || p == ".." || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("invalid tracked path %q", raw)
	}
	return p, nil
}

func rollbackUnlessCommitted(tx *sql.Tx) {
	_ = tx.Rollback()
}

package dots

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // Register the SQLite database/sql driver.
)

const (
	sqliteDriver          = "sqlite"
	schemaMigrationsTable = "dots_schema_migrations"
)

//go:embed migrations/repo/*.sql migrations/state/*.sql
var migrationFiles embed.FS

// FileRecord describes one tracked regular file.
type FileRecord struct {
	Path   string
	SHA256 string
	Mode   int64
	Size   int64
}

// TrackedDirRecord describes one tracked directory root.
type TrackedDirRecord struct {
	Path string
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
	if err := runMigrations(db, "migrations/repo"); err != nil {
		return fmt.Errorf("migrate repo database: %w", err)
	}
	if err := ensureProfileMetadata(db, profile); err != nil {
		return fmt.Errorf("validate repo profile metadata: %w", err)
	}
	return nil
}

func migrateStateDB(db *sql.DB, profile string) error {
	if err := runMigrations(db, "migrations/state"); err != nil {
		return fmt.Errorf("migrate state database: %w", err)
	}
	if err := ensureProfileMetadata(db, profile); err != nil {
		return fmt.Errorf("validate state profile metadata: %w", err)
	}
	return nil
}

func runMigrations(db *sql.DB, path string) error {
	migrations, err := fs.Sub(migrationFiles, path)
	if err != nil {
		return fmt.Errorf("load sqlite migrations %s: %w", path, err)
	}
	provider, err := goose.NewProvider(
		goose.DialectSQLite3,
		db,
		migrations,
		goose.WithTableName(schemaMigrationsTable),
		goose.WithLogger(goose.NopLogger()),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return fmt.Errorf("prepare sqlite migrations %s: %w", path, err)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		return fmt.Errorf("run sqlite migrations %s: %w", path, err)
	}
	return nil
}

func ensureProfileMetadata(db *sql.DB, profile string) error {
	var existing string
	err := db.QueryRow(`SELECT value FROM meta WHERE key = 'profile'`).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES ('profile', ?)`, profile); err != nil {
			return fmt.Errorf("record profile metadata: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read profile metadata: %w", err)
	}
	if existing != profile {
		return fmt.Errorf("database belongs to profile %q, not %q", existing, profile)
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

func listTrackedDirs(db *sql.DB) ([]TrackedDirRecord, error) {
	rows, err := db.Query(`SELECT path FROM tracked_dirs ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("list tracked directories: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var dirs []TrackedDirRecord
	for rows.Next() {
		var dir TrackedDirRecord
		if err := rows.Scan(&dir.Path); err != nil {
			return nil, fmt.Errorf("scan tracked directory: %w", err)
		}
		dirs = append(dirs, dir)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tracked directories: %w", err)
	}
	return dirs, nil
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

func upsertTrackedDirs(db *sql.DB, dirs []TrackedDirRecord) error {
	if len(dirs) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tracked directory update: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	statement, err := tx.Prepare(`INSERT INTO tracked_dirs (path, updated_at)
		VALUES (?, ?)
		ON CONFLICT(path) DO UPDATE SET
			updated_at = excluded.updated_at`)
	if err != nil {
		return fmt.Errorf("prepare tracked directory update: %w", err)
	}
	defer func() { _ = statement.Close() }()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, dir := range dirs {
		if _, err := statement.Exec(dir.Path, now); err != nil {
			return fmt.Errorf("update tracked directory %s: %w", dir.Path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tracked directory update: %w", err)
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

func upsertStateRecords(db *sql.DB, records []FileRecord) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin state update: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	statement, err := tx.Prepare(`INSERT INTO files (path, sha256, repo_sha256, mode, size, applied_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			sha256 = excluded.sha256,
			repo_sha256 = excluded.repo_sha256,
			mode = excluded.mode,
			size = excluded.size,
			applied_at = excluded.applied_at`)
	if err != nil {
		return fmt.Errorf("prepare state update: %w", err)
	}
	defer func() { _ = statement.Close() }()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, record := range records {
		if _, err := statement.Exec(record.Path, record.SHA256, record.SHA256, record.Mode, record.Size, now); err != nil {
			return fmt.Errorf("update applied file %s: %w", record.Path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit state update: %w", err)
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
	if err := deleteMatchingTrackedDirs(repoDB, paths); err != nil {
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

func deleteMatchingTrackedDirs(db *sql.DB, paths []string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin delete tracked directories: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	for _, p := range paths {
		if _, err := tx.Exec(`DELETE FROM tracked_dirs WHERE path = ? OR path LIKE ?`, p, p+"/%"); err != nil {
			return fmt.Errorf("delete tracked directories for %s: %w", p, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete tracked directories: %w", err)
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

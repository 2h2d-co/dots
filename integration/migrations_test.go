//go:build integration

// Package integration contains end-to-end tests for dots.
package integration

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseMigrationsFromScratchAndAlreadyCurrent(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.requireRun("init", env.repo, "--profile", "personal")

	repoDB := filepath.Join(env.repo, "personal.db")
	stateDB := filepath.Join(env.state, "dots", "personal.db")
	assertSQLiteMigrationVersion(t, repoDB, 2)
	assertSQLiteMigrationVersion(t, stateDB, 1)

	env.requireRun("status")
	assertSQLiteMigrationVersion(t, repoDB, 2)
	assertSQLiteMigrationVersion(t, stateDB, 1)
}

func TestDatabaseMigrationsFromLegacyRepoV1(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	writePersonalConfig(t, env)
	if err := os.MkdirAll(filepath.Join(env.repo, "personal"), 0o750); err != nil {
		t.Fatalf("create profile directory: %v", err)
	}
	createLegacyPersonalRepoDB(t, filepath.Join(env.repo, "personal.db"), 1, nil)
	createLegacyPersonalStateDB(t, filepath.Join(env.state, "dots", "personal.db"))

	env.requireRun("status")
	assertSQLiteMigrationVersion(t, filepath.Join(env.repo, "personal.db"), 2)
	assertSQLiteMigrationVersion(t, filepath.Join(env.state, "dots", "personal.db"), 1)
}

func TestDatabaseMigrationsStampLegacyRepoV2WithoutGooseTracking(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	writePersonalConfig(t, env)
	if err := os.MkdirAll(filepath.Join(env.repo, "personal"), 0o750); err != nil {
		t.Fatalf("create profile directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(env.home, ".config", "legacy"), 0o750); err != nil {
		t.Fatalf("create tracked destination directory: %v", err)
	}

	repoDB := filepath.Join(env.repo, "personal.db")
	stateDB := filepath.Join(env.state, "dots", "personal.db")
	createLegacyPersonalRepoDB(t, repoDB, 2, []string{".config/legacy"})
	createLegacyPersonalStateDB(t, stateDB)

	env.requireRun("status")
	assertSQLiteMigrationVersion(t, repoDB, 2)
	assertSQLiteMigrationVersion(t, stateDB, 1)
	withSQLiteDatabase(t, repoDB, func(db *sql.DB) {
		var trackedDir string
		if err := db.QueryRow(`SELECT path FROM tracked_dirs WHERE path = '.config/legacy'`).Scan(&trackedDir); err != nil {
			t.Fatalf("read tracked directory: %v", err)
		}
		if trackedDir != ".config/legacy" {
			t.Fatalf("tracked directory = %q, want .config/legacy", trackedDir)
		}
	})
}

-- +goose Up
CREATE TABLE IF NOT EXISTS tracked_dirs (
	path TEXT PRIMARY KEY,
	updated_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE tracked_dirs;

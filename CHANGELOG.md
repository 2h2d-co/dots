# Changelog

All notable changes to `dots` will be documented in this file.

This project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) style sections and semantic versioning.

## Unreleased

## [0.0.2] - 2026-07-05

### Added

- Added `dots add --dry-run` to preview files and directory roots without copying files or updating the profile database.
- Added tracked directory roots so directory adds report future new destination files in status, including support for nested tracked roots.

### Changed

- Changed config from single `repo`/`profile` keys to `default_profile` plus a `[profiles]` table so one config can manage multiple profile/repo pairs. Migrate from:

  ```toml
  repo = "~/dotfiles"
  profile = "personal"
  ```

  to:

  ```toml
  default_profile = "personal"

  [profiles]
  personal = "~/dotfiles"
  ```

- Changed `dots init` to add a new configured profile to an existing config, while refusing duplicate profile names and pre-existing profile repo/state databases.
- Changed `dots doctor` to check all configured profiles across configured repos unless a profile override is set.
- Changed `dots apply` to leave matching destination files untouched and only refresh applied-state records for those paths.
- Changed repo and state database setup to use embedded `pressly/goose` SQLite migrations and reject databases with mismatched profile metadata.
- Changed the release workflow to attest `checksums.txt` alongside release archives.

### Security

- Refuse `dots add` paths from any configured dots repo, reject nested configured repos, and reject initializing a repo path already tracked as home content by another configured profile.

## [0.0.1] - 2026-06-22

### Added

- Added the initial `dots` CLI with `init`, `add`, `apply`, `status`, `doctor`, `list`, `reindex`, `forget`, completion generation, manpage generation, and version output.
- Added copy-only profile management with top-level profile directories in the configured repo.
- Added per-profile SQLite repo catalogs with tracked paths, file modes, sizes, and SHA-256 sums.
- Added per-profile applied-state databases under `${XDG_STATE_HOME:-$HOME/.local/state}/dots/{profile}.db`.
- Added config loading from `$XDG_CONFIG_HOME/dots/config.toml`, falling back to `~/.config/dots/config.toml`.
- Added `DOTS_CONFIG`/`--config` and `DOTS_PROFILE`/`--profile` overrides.
- Added `.dotsignore` support for `dots add`; the top-level ignore file in an added directory filters nested paths and is itself tracked.
- Added full apply preflight checks, dry-run planning, conflict detection, and `--force` backups before overwrite.
- Added status and doctor reports for repo drift, pending changes, destination conflicts, and stale applied state.
- Added read-only git upstream freshness checks before `dots reindex` rewrites the repo catalog.
- Added generated manpages and shell completion support.

### Security

- Added safety checks for refusing symlinks, unsupported file types, paths outside `$HOME`, and paths inside the configured dots repo.

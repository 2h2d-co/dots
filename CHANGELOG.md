# Changelog

All notable changes to `dots` will be documented in this file.

This project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) style sections and semantic versioning.

## Unreleased

_No unreleased changes._

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

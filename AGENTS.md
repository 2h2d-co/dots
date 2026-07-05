# dots CLI Project Instructions

`dots` is a Go-based, minimal, copy-only dotfiles manager: a deliberately limited and lightweight alternative to `chezmoi`.

## Conventions

- Format commit messages according to [Conventional Commits](https://www.conventionalcommits.org/).
- Maintain `CHANGELOG.md` using the [Keep a Changelog](https://keepachangelog.com/) style.
- Add changelog entries for changes whose commit would be `feat:` or `fix:`; keep entries under `Unreleased` until a release is made.
- Release commits should do the following:
  - update the project version;
  - move `Unreleased` changelog entries into the new release section;
  - commit with `release: vX.Y.Z` as the commit message;
  - tag the release with the matching `vX.Y.Z` tag.

## Core Constraints

- Global config only: `$XDG_CONFIG_HOME/dots/config.toml`, falling back to `~/.config/dots/config.toml`.
- `--config PATH` and `DOTS_CONFIG=PATH` override the config path.
- `--profile PROFILE` and `DOTS_PROFILE=PROFILE` override the active profile.
- Copies only. Do not add symlink-based behavior.
- Profiles are top-level directories in the configured repo.
- Each profile has a top-level SQLite `.db` file that catalogs tracked files and SHA-256 sums.
- Last applied state lives under `${XDG_STATE_HOME:-$HOME/.local/state}/dots/{profile}.db`.
- `dots status` compares the profile repo DB, last applied DB, current repo contents, and current destination files.
- `dots doctor` checks drift for all profiles in the repo.
- `dots reindex` refuses to rewrite the repo DB when a configured git upstream has changes to pull, and it must not fetch, pull, push, commit, or otherwise manage git state.
- `dots add [PATH]` defaults to the current directory, refuses paths inside the configured dots repo, and honors a `.dotsignore` file located in an added directory. `.dotsignore` is copied into the repo.
- Preserve regular file contents, relative paths, and executable bits.
- Reject symlinks, sockets, devices, and FIFOs.

## Command scope

Maintain integration-test coverage for this command surface:

- `dots init REPO --profile PROFILE`
- `dots add [PATH]`
- `dots apply --dry-run`
- `dots apply --force` with backups before overwrite
- `dots status`
- `dots doctor`
- `dots list`
- `dots reindex`
- `dots forget PATH...`
- `dots --version`

Do not add archive, URL, git/git submodule, template, secret, package-management, hook, TUI, or profile-inheritance support unless the product scope changes.

## Linting/security expectations

Follow the same baseline as `cage`:

- No `nolint`.
- No `#nosec`.
- Do not lower linter thresholds or add suppressions just to get green builds.
- GitHub Actions should pin third-party actions by full commit SHA and use `persist-credentials: false`.
- Respect XDG variables in tests so integration tests never touch a developer's real dotfiles, config, or state.

Recommended validation:

```sh
go mod verify
test -z "$(gofmt -l .)"
go test -race -mod=readonly ./...
go test -v -count=1 -tags=integration ./integration
go vet ./...
mise run lint
goreleaser check
```

If mise config is untrusted in a non-interactive harness, run commands with:

```sh
export MISE_TRUSTED_CONFIG_PATHS=$PWD
```

## Documentation/metadata

- License: MIT, copyright `Two Humans and Two Dogs LLC (2h2d.co)`.
- Repo URL: `https://github.com/2h2d-co/dots`.
- Keep README, `examples/config.toml`, command help, shell completions, and manpage support aligned with CLI behavior.

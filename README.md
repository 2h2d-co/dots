# dots

`dots` is a minimal, copy-based dotfiles manager.

It is intended to be a deliberately small competitor to established dotfile managers such as `chezmoi`: profiles, copied files, SQLite-backed tracking state, and safety-first apply/status workflows without templates, archives, URL fetching, or git/submodule automation.

Repository: <https://github.com/2h2d-co/dots>

## Quick start

```sh
# Create config, a repo profile directory, and tracking databases.
dots init ~/dotfiles --profile personal

# Track a file or directory under $HOME. With no PATH, add uses the current directory.
dots add ~/.zshrc
dots add ~/.config/nvim

# Preview and apply tracked files back into $HOME.
dots status
dots apply --dry-run
dots apply
```

## Commands

- `dots init REPO --profile PROFILE`: initialize config, one profile directory, and SQLite tracking databases.
- `dots add [PATH]`: copy a file or directory from `$HOME` into the active profile and update the profile database. `PATH` defaults to the current directory. Paths inside the configured dots repo are refused.
- `dots apply [--dry-run] [--force]`: copy tracked profile files back to `$HOME` after a full preflight check. `--force` backs up conflicting destinations before overwriting.
- `dots status`: show profile drift, pending changes, destination conflicts, and stale applied state for the active profile.
- `dots doctor`: run status checks for all profiles, or only the overridden profile when `--profile` or `DOTS_PROFILE` is set.
- `dots list`: list tracked files in the active profile.
- `dots reindex`: rebuild the active profile database from current profile files. If the repo has a configured git upstream, reindex refuses to run when the upstream has changes that are not reflected locally.
- `dots forget PATH...`: stop tracking paths without deleting destination files from `$HOME`.
- `dots completion SHELL`: generate shell completion scripts.
- `dots man DIR`: generate man pages.
- `dots --version`: print the version.

## Config and state

- Config lives at `$XDG_CONFIG_HOME/dots/config.toml`, falling back to `~/.config/dots/config.toml`.
- `DOTS_CONFIG=PATH` and `--config PATH` override the config path; the flag wins over the environment variable.
- `DOTS_PROFILE=PROFILE` and `--profile PROFILE` override the active profile; the flag wins over the environment variable and config profile.
- Profiles are top-level folders in the configured repo directory.
- Each profile has a top-level SQLite DB in the repo that catalogs tracked files, file modes, sizes, and SHA-256 sums.
- Last applied state is tracked in `${XDG_STATE_HOME:-$HOME/.local/state}/dots/{profile}.db`.

## File handling

- `dots` always copies regular files; it never creates symlinks.
- Symlinks, other unsupported file types, and paths inside the configured dots repo are rejected.
- `.dotsignore` in an added directory excludes matching paths from copy/tracking and is itself copied.
- Ignore patterns from that top-level `.dotsignore` apply to nested paths under the added directory.
- Nested `.dotsignore` files are treated as regular files when they are not ignored; they do not add more ignore rules.

## Safety and exits

- `dots apply` checks the complete profile before changing any destination file.
- Without `--force`, apply exits without changing files when it finds destination conflicts.
- With `--force`, apply moves conflicting destinations into `${XDG_STATE_HOME:-$HOME/.local/state}/dots/backups/{profile}/...` before overwriting.
- `dots status` and `dots doctor` exit `0` when clean and `1` when drift, pending changes, conflicts, or stale state need attention.

## Excluded functionality

`dots` does not support archives, URL fetching, git/submodule management beyond read-only reindex freshness checks, templates, hooks, secrets, package management, TUI workflows, or profile inheritance.

## Requirements

- Go 1.26+

## Development

```sh
mise install
make test
make build
```

With mise shell integration active, `dots:local` is an alias for `go run .`.

## License

MIT. See [LICENSE](LICENSE).

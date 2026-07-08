# dots

`dots` is a minimal, copy-based dotfiles manager.

It is intended to be a deliberately small competitor to established dotfile managers such as `chezmoi`: profiles, copied files, SQLite-backed tracking state, and safety-first apply/status workflows without templates, archives, URL fetching, or git/submodule automation.

Repository: <https://github.com/2h2d-co/dots>

## Quick start

```sh
# Create config, a repo profile directory, and tracking databases.
dots init ~/dotfiles --profile personal

# Add more configured profiles to the same config when needed.
dots init ~/work-dotfiles --profile work

# Track a file or directory root under $HOME. With no PATH, add uses the current directory.
dots add ~/.zshrc
dots add --dry-run ~/.config/nvim
dots add ~/.config/nvim

# Preview and apply tracked files back into $HOME.
dots status
dots diff
dots diff | hunk patch -
dots apply --dry-run
dots apply
# Limit apply/diff/sync to one tracked root or file when resolving a focused change.
dots diff ~/.config/git
dots apply ~/.config/git/config

# After editing files in $HOME, record allowed destination changes back into the profile.
dots diff --sync
dots diff --sync ~/.config/mise/mise.lock
dots sync --dry-run
dots sync
```

## Commands

- `dots init REPO --profile PROFILE`: initialize config or add one configured profile, then create its profile directory and SQLite tracking databases.
- `dots add [--dry-run] [PATH]`: copy a file or directory from `$HOME` into the active profile and update the profile and applied-state databases for the added files. Directory adds also record the directory as a tracked root, so future new files under it appear in status. Tracked directory roots may be nested. `PATH` defaults to the current directory. Paths inside any configured dots repo are refused. Home-to-repo content is scanned with pinned Gitleaks rules; supported npm auth token findings and npmrc auth lines are scrubbed before writing, and remaining findings abort before changes. `--dry-run` lists the files and directory roots that would be added without copying files or updating the database.
- `dots apply [--dry-run] [--force] [PATH...]`: apply tracked profile files back to `$HOME` after a preflight check. `PATH` arguments limit apply to matching tracked files or tracked-root subtrees. Scoped apply accepts selected profile repo drift as the current tracked repo state before applying it. Destinations whose scrubbed canonical content already matches the profile are left untouched and only recorded in applied state, preserving local-only credentials. `--force` backs up conflicting destinations before overwriting.
- `dots sync [--dry-run] [--force] [PATH...]`: copy destination changes and new files under tracked roots back into the active profile, refreshing applied state for files it records. `PATH` arguments limit sync to matching tracked files, tracked-root subtrees, or new destination files under tracked roots. Scoped sync can resolve selected profile repo drift by backing up conflicting repo files with `--force` and taking the destination side. Home-to-repo content is scanned with pinned Gitleaks rules; supported npm auth token findings and npmrc auth lines are scrubbed before writing, and remaining findings abort before changes. Plain sync refuses destination/profile conflicts; `--force` backs up conflicting repo files and takes the destination side.
- `dots diff [--sync] [--no-pager] [PATH...]`: print a git-style unified diff for what `dots apply --force` would change. `PATH` arguments limit diff to matching tracked files, tracked-root subtrees, or new destination files under tracked roots. Scoped apply-direction diff reads selected profile repo drift as the current repo side; scoped `--sync` diff previews taking the destination side for selected repo drift. Destination-side content is canonicalized with the same secret scrubbing used by add and sync so patches do not expose scrubbed local credentials. `--sync` previews what `dots sync --force` would change. Patch text goes to stdout; notes and refusals go to stderr so output can be piped to tools such as `hunk`.
- `dots status`: show profile drift, tracked-directory drift, pending changes, destination conflicts, and stale applied state for the active profile. Supported secret-scrubbed paths are compared by canonical content, so local-only npm auth token changes do not create drift.
- `dots doctor`: run status checks for all configured profiles, or only the overridden profile when `--profile` or `DOTS_PROFILE` is set.
- `dots list`: list tracked files in the active profile.
- `dots reindex`: rebuild the active profile database from current profile files. If the repo has a configured git upstream, reindex refuses to run when the upstream has changes that are not reflected locally.
- `dots forget PATH...`: stop tracking paths without deleting destination files from `$HOME`.
- `dots completion SHELL`: generate shell completion scripts.
- `dots man DIR`: generate man pages.
- `dots --version`: print the version.

## Config and state

- Config lives at `$XDG_CONFIG_HOME/dots/config.toml`, falling back to `~/.config/dots/config.toml`.
- `DOTS_CONFIG=PATH` and `--config PATH` override the config path; the flag wins over the environment variable.
- Config uses one `default_profile` and one `[profiles]` table mapping profile names to repo paths:

  ```toml
  default_profile = "personal"
  # pager = "hunk patch --pager -"

  [profiles]
  personal = "~/dotfiles"
  work = "~/work-dotfiles"
  ```

- `DOTS_PROFILE=PROFILE` and `--profile PROFILE` override the active profile; the flag wins over the environment variable and `default_profile`.
- `DOTS_PAGER=COMMAND` overrides the optional `pager` config key for `dots diff`; `dots diff --no-pager` disables paging. The pager is used only when stdout is a terminal and the diff is non-empty.
- Profiles are top-level folders in their configured repo directory.
- Each profile has a top-level SQLite DB in the repo that catalogs tracked files, tracked directory roots, file modes, sizes, and SHA-256 sums.
- Last applied state is tracked in `${XDG_STATE_HOME:-$HOME/.local/state}/dots/{profile}.db`.
- `dots add` refuses paths inside any repo configured in the active config.
- `dots add` records applied state for added files because the home file and repo copy match at add time.

## File handling

- `dots` always copies regular files; it never creates symlinks.
- Symlinks, other unsupported file types, and paths inside any configured dots repo are rejected.
- `.dotsignore` in an added directory excludes matching paths from copy/tracking and is itself copied.
- Ignore patterns from that top-level `.dotsignore` apply to nested paths under the added directory.
- New destination files under a tracked directory root are reported by `dots status` until they are synced, added directly, or ignored.
- Git-ignored untracked profile files are omitted from `dots status`; tracked profile files are still checked even when they match `.gitignore` rules.
- Tracked directory roots may be nested; status output groups paths by the most specific tracked root, with directly tracked files shown under `Individual paths`.
- Nested `.dotsignore` files are treated as regular files when they are not ignored; they do not add more ignore rules.
- `dots sync` never deletes profile files for missing destinations; use `dots forget` explicitly when tracking should stop.
- Home-to-repo copies are scanned with pinned Gitleaks default rules. Supported npm auth token findings blank the entire finding line in any file; npmrc `_authToken`, `_auth`, and `_password` lines can also be blanked when stock Gitleaks flags them. Unsupported remaining findings block the copy without printing raw secrets.
- Local npm auth token changes are compared through the scrubbed canonical view, so token value churn does not create status drift or force apply to overwrite the local credential.

## Safety and exits

- `dots apply`, `dots sync`, and `dots diff` accept optional `PATH` scopes. A scope can be a tracked file, a tracked root, a subtree under a tracked root, or an absolute/home-relative spelling of one.
- `dots apply` checks the selected scope before changing any destination file; without `PATH` arguments it checks the complete profile.
- Destination files that already match the profile are left untouched; apply only refreshes the applied-state database for those paths.
- Without `--force`, apply exits without changing files when it finds destination conflicts.
- With `--force`, apply moves conflicting destinations into `${XDG_STATE_HOME:-$HOME/.local/state}/dots/backups/{profile}/{SET}/home/{ENTRY}/payload` before overwriting. Sync uses the same backup sets under `repo/{ENTRY}/payload` for conflicting repo files.
- `dots sync` checks the selected scope before changing repo files; without `PATH` arguments it checks the complete profile. Without `--force`, it exits without changing files when destination/profile conflicts need an explicit side choice. Real sync runs also refuse when a configured git upstream has changes to pull; `--dry-run` stays local-only.
- Unscoped apply/sync/diff refuse profile repo drift. Scoped apply and apply-direction diff accept selected changed/untracked profile files as the repo side. Scoped sync and `diff --sync` can operate on selected repo drift; `sync --force PATH` backs up changed repo files and takes the destination side.
- `dots diff` is read-only. It exits `0` when it has no patchable differences or notes, and `1` when it prints a patch, emits an excluded-conflict note, or refuses because profile repo files drifted from the tracking database.
- `dots status` and `dots doctor` exit `0` when clean and `1` when drift, directory drift, pending changes, conflicts, or stale state need attention.

## Excluded functionality

`dots` does not support archives, URL fetching, git/submodule management beyond read-only reindex freshness checks, templates, hooks, secret storage/restoration, package management, TUI workflows, or profile inheritance.

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

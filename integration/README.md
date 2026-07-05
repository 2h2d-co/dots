# dots integration tests

The integration suite should exercise `dots` end to end without touching real user dotfiles, config, state, or caches. Tests must use temporary HOME/XDG directories and `DOTS_CONFIG`/`--config` overrides.

## Run

```sh
go test -v -count=1 -tags=integration ./integration

# Run top-level integration tests in parallel.
go test -v -count=1 -tags=integration -parallel=8 ./integration
```

## Scope

The suite covers:

- initialization of config, repo, active profile, profile DB, and state directories
- multiple configured profiles in one config, including profiles that share a repo and profiles with distinct repos
- init rejection for duplicate profiles, pre-existing profile databases, overlapping repo roots, and new repos already tracked as home content
- add with dry-run, current-directory default, explicit target directory, individual files, tracked directory roots, and nested tracked directory roots
- add rejection for symlinks, unsupported file types, paths outside `$HOME`, and paths inside any configured dots repo
- `.dotsignore` filtering while copying `.dotsignore` itself
- profile-scoped tracking under top-level repo profile folders
- repo profile DB SHA-256 cataloging
- copy-only apply with dry-run, conflict detection, destination type conflicts, force overwrite, and backups
- copy-only sync with dry-run, state refreshes, tracked-root additions, conflict aborts, force repo backups, repo-drift refusal, and diff-preview parity
- last-applied state DB updates
- status exit codes, drift reporting, grouped tracked-root output, individual path output, and new files under tracked directory roots
- doctor drift checks across all configured profiles and profile overrides
- list/reindex/forget behavior
- reindex and sync refusal when a configured git upstream has changes to pull
- unusual path names with spaces and Unicode

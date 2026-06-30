package dots

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func (a *App) newInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init REPO",
		Short: "Initialize dots config and a configured profile",
		Long:  "Initialize or extend dots config, the dotfiles repository, one profile directory, and SQLite tracking databases. A profile is required via --profile or DOTS_PROFILE.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := a.resolveProfileOverride()
			if err := validateProfile(profile); err != nil {
				return err
			}
			configPath, err := a.resolveConfigPath()
			if err != nil {
				return err
			}
			repo, err := resolveRepoPath(args[0])
			if err != nil {
				return err
			}
			home, err := resolveHomeDir()
			if err != nil {
				return err
			}
			stateDir, err := defaultStateDir()
			if err != nil {
				return err
			}

			cfg := Config{
				DefaultProfile: profile,
				Profiles: map[string]string{
					profile: repo,
				},
			}
			configExists := false
			if _, err := os.Stat(configPath); err == nil {
				configExists = true
				cfg, err = loadConfig(configPath)
				if err != nil {
					return err
				}
				if err := validateConfig(cfg); err != nil {
					return err
				}
				if _, ok := cfg.Profiles[profile]; ok {
					return fmt.Errorf("profile %q is already configured", profile)
				}
				profiles, _, err := resolveConfiguredProfiles(cfg)
				if err != nil {
					return err
				}
				if err := rejectOverlappingConfiguredRepos(repo, profiles); err != nil {
					return err
				}
				if err := rejectTrackedRepoDestination(repo, home, profiles); err != nil {
					return err
				}
				cfg.Profiles[profile] = repo
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("check config %s: %w", configPath, err)
			}

			if _, err := os.Stat(repoDBPath(repo, profile)); err == nil {
				return fmt.Errorf("repo database already exists: %s", repoDBPath(repo, profile))
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("check repo database: %w", err)
			}
			if _, err := os.Stat(stateDBPath(stateDir, profile)); err == nil {
				return fmt.Errorf("state database already exists: %s", stateDBPath(stateDir, profile))
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("check state database: %w", err)
			}

			if err := os.MkdirAll(filepath.Join(repo, profile), 0o750); err != nil {
				return fmt.Errorf("create profile directory: %w", err)
			}
			if err := os.MkdirAll(stateDir, 0o750); err != nil {
				return fmt.Errorf("create state directory: %w", err)
			}
			if err := ensureRepoDB(repo, profile); err != nil {
				return err
			}
			if err := ensureStateDB(stateDir, profile); err != nil {
				return err
			}
			if configExists {
				err = replaceConfig(configPath, cfg)
			} else {
				err = createConfig(configPath, cfg)
			}
			if err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Initialized profile %s in %s\n", profile, repo)
			return err
		},
	}
}

func rejectOverlappingConfiguredRepos(repo string, profiles map[string]RuntimeProfile) error {
	for _, profile := range runtimeProfileNames(profiles) {
		configuredRepo := profiles[profile].Repo
		sameRepo, err := pathsEqual(repo, configuredRepo)
		if err != nil {
			return err
		}
		if sameRepo {
			continue
		}

		newInsideExisting, err := pathInsideOrEqual(configuredRepo, repo)
		if err != nil {
			return err
		}
		existingInsideNew, err := pathInsideOrEqual(repo, configuredRepo)
		if err != nil {
			return err
		}
		if newInsideExisting || existingInsideNew {
			return fmt.Errorf("repo %s overlaps configured repo for profile %q: %s", repo, profile, configuredRepo)
		}
	}
	return nil
}

func rejectTrackedRepoDestination(repo, home string, profiles map[string]RuntimeProfile) error {
	insideHome, err := pathInsideOrEqual(home, repo)
	if err != nil {
		return err
	}
	if !insideHome {
		return nil
	}

	repoRoot, err := homeRelativeRepoRoot(home, repo)
	if err != nil {
		return err
	}
	for _, profile := range runtimeProfileNames(profiles) {
		dbPath := repoDBPath(profiles[profile].Repo, profile)
		if _, err := os.Stat(dbPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("check repo database %s: %w", dbPath, err)
		}

		repoDB, err := openRepoDB(profiles[profile].Repo, profile)
		if err != nil {
			return err
		}
		records, err := listRepoRecords(repoDB)
		if err != nil {
			return errors.Join(err, repoDB.Close())
		}
		trackedDirs, err := listTrackedDirs(repoDB)
		if err != nil {
			return errors.Join(err, repoDB.Close())
		}
		if err := repoDB.Close(); err != nil {
			return err
		}

		for _, record := range records {
			if trackedPathInsideRoot(repoRoot, record.Path) {
				return fmt.Errorf("repo %s is already tracked by profile %q as %s", repo, profile, record.Path)
			}
		}
		for _, dir := range trackedDirs {
			if trackedPathInsideRoot(repoRoot, dir.Path) || trackedPathInsideRoot(dir.Path, repoRoot) {
				return fmt.Errorf("repo %s is already tracked by profile %q as %s", repo, profile, dir.Path)
			}
		}
	}
	return nil
}

func pathsEqual(a, b string) (bool, error) {
	comparableA, err := comparablePath(a)
	if err != nil {
		return false, fmt.Errorf("resolve path %s: %w", a, err)
	}
	comparableB, err := comparablePath(b)
	if err != nil {
		return false, fmt.Errorf("resolve path %s: %w", b, err)
	}
	return comparableA == comparableB, nil
}

func homeRelativeRepoRoot(home, repo string) (string, error) {
	absRepo, err := comparablePath(repo)
	if err != nil {
		return "", fmt.Errorf("resolve repo path: %w", err)
	}
	absHome, err := comparablePath(home)
	if err != nil {
		return "", fmt.Errorf("resolve home path: %w", err)
	}
	rel, err := filepath.Rel(absHome, absRepo)
	if err != nil {
		return "", fmt.Errorf("resolve home-relative repo path: %w", err)
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." {
		return "", nil
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("repo %s is outside home directory %s", absRepo, absHome)
	}
	return cleanTrackedPath(rel)
}

func trackedPathInsideRoot(root, trackedPath string) bool {
	if root == "" {
		return true
	}
	return trackedPath == root || strings.HasPrefix(trackedPath, root+"/")
}

func (a *App) newAddCommand() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "add [PATH]",
		Short: "Copy a file or directory into the active profile",
		Long:  "Copy a file or directory from the home directory into the active profile and update the profile tracking database. Directory adds also record the directory as a tracked root so future new files under it appear in status. Tracked directory roots may be nested. PATH defaults to the current directory. Paths inside any configured dots repo are refused. --dry-run lists the files and directory roots that would be added without copying files or updating the database.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := targetOrCurrent(args)
			if err != nil {
				return err
			}
			rt, err := a.resolveRuntime()
			if err != nil {
				return err
			}
			plan, err := collectAddPlan(rt, target)
			if err != nil {
				return err
			}
			if dryRun {
				return writeAddPlan(cmd.OutOrStdout(), rt, plan)
			}
			records, err := executeAddPlan(rt, plan.Items)
			if err != nil {
				return err
			}
			repoDB, err := openRepoDB(rt.Repo, rt.Profile)
			if err != nil {
				return err
			}
			if err := upsertRepoRecords(repoDB, records); err != nil {
				return errors.Join(err, repoDB.Close())
			}
			if err := upsertTrackedDirs(repoDB, plan.TrackedDirs); err != nil {
				return errors.Join(err, repoDB.Close())
			}
			if err := repoDB.Close(); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Added %d file(s) to profile %s\n", len(records), rt.Profile); err != nil {
				return err
			}
			if len(plan.TrackedDirs) > 0 {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Tracked %d directory root(s) in profile %s\n", len(plan.TrackedDirs), rt.Profile)
				return err
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be added without changing files")
	return cmd
}

func (a *App) newApplyCommand() *cobra.Command {
	var opts applyOptions
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Preview or apply tracked files to the home directory",
		Long:  "Preview tracked files from the active profile or apply needed changes to the home directory. Apply always performs a full preflight check before changing files; destinations that already match the profile are left untouched and only recorded in applied state. --force backs up conflicting destinations before overwriting them.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := a.resolveRuntime()
			if err != nil {
				return err
			}
			return applyProfile(rt, opts, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "show what would be applied without changing files")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "back up and overwrite conflicting destination files")
	return cmd
}

func (a *App) newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show profile drift, directory drift, pending changes, and conflicts",
		Long:  "Compare the active profile database, profile files, tracked directory roots, applied-state database, and home-directory destination files. When tracked roots are present, status output groups paths by the most specific tracked root and reports directly tracked files under Individual paths. A clean status exits 0; drift, pending changes, conflicts, directory drift, or stale state exit 1.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := a.resolveRuntime()
			if err != nil {
				return err
			}
			report, _, err := analyzeStatus(rt)
			if err != nil {
				return err
			}
			if err := writeStatusReport(cmd.OutOrStdout(), report); err != nil {
				return err
			}
			if report.dirty() {
				return ExitError{Code: 1, Silent: true}
			}
			return nil
		},
	}
}

func (a *App) newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check one or all profiles for issues",
		Long:  "Run status checks for all configured profiles, or only the overridden profile when --profile or DOTS_PROFILE is set. Doctor exits 0 when every checked profile is clean and 1 when any checked profile needs attention.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := a.resolveRuntime()
			if err != nil {
				return err
			}
			profiles := a.doctorRuntimes(rt)
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Doctor: checking %d profile(s)\n", len(profiles)); err != nil {
				return err
			}
			dirty := false
			for _, profileRuntime := range profiles {
				if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
					return err
				}
				report, _, err := analyzeStatus(&profileRuntime)
				if err != nil {
					return err
				}
				if err := writeStatusReport(cmd.OutOrStdout(), report); err != nil {
					return err
				}
				dirty = dirty || report.dirty()
			}
			if dirty {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Doctor: issues found"); err != nil {
					return err
				}
				return ExitError{Code: 1, Silent: true}
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Doctor: all checked profiles are clean")
			return err
		},
	}
}

func (a *App) doctorRuntimes(rt *Runtime) []Runtime {
	if a.resolveProfileOverride() != "" {
		return []Runtime{*rt}
	}

	profiles := make([]Runtime, 0, len(rt.ConfiguredProfiles))
	for _, profile := range runtimeProfileNames(rt.ConfiguredProfiles) {
		profileRuntime := *rt
		profileRuntime.Profile = profile
		profileRuntime.Repo = rt.ConfiguredProfiles[profile].Repo
		profiles = append(profiles, profileRuntime)
	}
	return profiles
}

func (a *App) newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List tracked files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := a.resolveRuntime()
			if err != nil {
				return err
			}
			repoDB, err := openRepoDB(rt.Repo, rt.Profile)
			if err != nil {
				return err
			}
			records, err := listRepoRecords(repoDB)
			if err != nil {
				return errors.Join(err, repoDB.Close())
			}
			if err := repoDB.Close(); err != nil {
				return err
			}
			for _, record := range records {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), record.Path); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func (a *App) newReindexCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "reindex",
		Short: "Rebuild the active profile database from repo files",
		Long:  "Rebuild the active profile database from the current profile files. If the repo has a configured git upstream, reindex refuses to run unless there is nothing to pull from the remote.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := a.resolveRuntime()
			if err != nil {
				return err
			}
			if err := ensureNothingToPull(rt.Repo); err != nil {
				return err
			}
			records, err := collectProfileRecords(rt)
			if err != nil {
				return err
			}
			repoDB, err := openRepoDB(rt.Repo, rt.Profile)
			if err != nil {
				return err
			}
			if err := replaceRepoRecords(repoDB, records); err != nil {
				return errors.Join(err, repoDB.Close())
			}
			if err := repoDB.Close(); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Reindexed %d file(s) for profile %s\n", len(records), rt.Profile)
			return err
		},
	}
}

func (a *App) newForgetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "forget PATH...",
		Short: "Stop tracking files without deleting destination files",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := a.resolveRuntime()
			if err != nil {
				return err
			}
			paths := make([]string, 0, len(args))
			for _, arg := range args {
				trackedPath, err := trackedPathFromArg(rt, arg)
				if err != nil {
					return err
				}
				paths = append(paths, trackedPath)
			}
			repoDB, err := openRepoDB(rt.Repo, rt.Profile)
			if err != nil {
				return err
			}
			stateDB, err := openStateDB(rt.StateDir, rt.Profile)
			if err != nil {
				return errors.Join(err, repoDB.Close())
			}

			for _, trackedPath := range paths {
				if err := removeRepoPath(rt, trackedPath); err != nil {
					return errors.Join(err, repoDB.Close(), stateDB.Close())
				}
			}
			if err := forgetRecords(repoDB, stateDB, paths); err != nil {
				return errors.Join(err, repoDB.Close(), stateDB.Close())
			}
			if err := errors.Join(repoDB.Close(), stateDB.Close()); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Forgot %d path(s) from profile %s\n", len(paths), rt.Profile)
			return err
		},
	}
}

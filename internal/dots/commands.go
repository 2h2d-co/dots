package dots

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
)

func (a *App) newInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init REPO",
		Short: "Initialize dots config and a profile",
		Long:  "Initialize dots config, the dotfiles repository, one profile directory, and SQLite tracking databases. A profile is required via --profile or DOTS_PROFILE.",
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
			if _, err := os.Stat(configPath); err == nil {
				return fmt.Errorf("config already exists: %s", configPath)
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("check config %s: %w", configPath, err)
			}
			repo, err := expandPath(args[0])
			if err != nil {
				return err
			}
			repo, err = filepath.Abs(repo)
			if err != nil {
				return fmt.Errorf("resolve repo path: %w", err)
			}
			stateDir, err := defaultStateDir()
			if err != nil {
				return err
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
			if err := writeConfig(configPath, Config{Repo: repo, Profile: profile}); err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Initialized profile %s in %s\n", profile, repo)
			return err
		},
	}
}

func (a *App) newAddCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "add [PATH]",
		Short: "Copy a file or directory into the active profile",
		Long:  "Copy a file or directory from the home directory into the active profile and update the profile tracking database. PATH defaults to the current directory. Paths inside the configured dots repo are refused.",
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
			records, err := addPath(rt, target)
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
			if err := repoDB.Close(); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Added %d file(s) to profile %s\n", len(records), rt.Profile)
			return err
		},
	}
}

func (a *App) newApplyCommand() *cobra.Command {
	var opts applyOptions
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Preview or copy tracked files to the home directory",
		Long:  "Preview or copy tracked files from the active profile to the home directory. Apply always performs a full preflight check before changing files; --force backs up conflicting destinations before overwriting them.",
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
		Short: "Show profile drift, pending changes, and conflicts",
		Long:  "Compare the active profile database, profile files, applied-state database, and home-directory destination files. A clean status exits 0; drift, pending changes, conflicts, or stale state exit 1.",
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
		Long:  "Run status checks for all profiles in the repo, or only the overridden profile when --profile or DOTS_PROFILE is set. Doctor exits 0 when every checked profile is clean and 1 when any checked profile needs attention.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := a.resolveRuntime()
			if err != nil {
				return err
			}
			profiles, err := a.doctorProfiles(rt)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Doctor: checking %d profile(s)\n", len(profiles)); err != nil {
				return err
			}
			dirty := false
			for _, profile := range profiles {
				if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
					return err
				}
				profileRuntime := *rt
				profileRuntime.Profile = profile
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

func (a *App) doctorProfiles(rt *Runtime) ([]string, error) {
	if a.resolveProfileOverride() != "" {
		return []string{rt.Profile}, nil
	}
	entries, err := os.ReadDir(rt.Repo)
	if err != nil {
		return nil, fmt.Errorf("read repo directory: %w", err)
	}
	var profiles []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		profile := entry.Name()
		if err := validateProfile(profile); err != nil {
			continue
		}
		if _, err := os.Stat(repoDBPath(rt.Repo, profile)); err == nil {
			profiles = append(profiles, profile)
		}
	}
	if len(profiles) == 0 {
		profiles = append(profiles, rt.Profile)
	}
	sort.Strings(profiles)
	return profiles, nil
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

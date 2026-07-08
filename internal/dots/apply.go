package dots

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type applyOptions struct {
	DryRun bool
	Force  bool
	Paths  []string
}

func applyProfile(rt *Runtime, opts applyOptions, out io.Writer) error {
	scope, err := newPathScope(rt, opts.Paths)
	if err != nil {
		return err
	}
	report, records, repoUpdates, err := analyzeApplyStatus(rt, scope)
	if err != nil {
		return err
	}
	if report.hasRepoDrift() {
		if err := writeStatusReport(out, report); err != nil {
			return err
		}
		if err := writeRepoDriftRefusal(out, "Apply"); err != nil {
			return err
		}
		return ExitError{Code: 1, Silent: true}
	}
	if opts.DryRun {
		if err := writeApplyPlan(out, report, opts); err != nil {
			return err
		}
		if report.hasConflicts() && !opts.Force {
			return ExitError{Code: 1, Silent: true}
		}
		return nil
	}

	if report.hasConflicts() && !opts.Force {
		if err := writeStatusReport(out, report); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out, "Apply aborted: destination conflicts found."); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out, "Resolve the conflicts, or re-run with --force to back up and overwrite conflicting destinations."); err != nil {
			return err
		}
		return ExitError{Code: 1, Silent: true}
	}

	var backups *backupSetWriter
	if opts.Force {
		backups = newBackupSetWriter(rt)
	}

	writePaths := applyWritePaths(report, opts)
	copied := 0
	for _, record := range records {
		if _, ok := writePaths[record.Path]; !ok {
			continue
		}
		src := repoFilePath(rt, record.Path)
		dst := destinationPath(rt, record.Path)
		if opts.Force {
			if err := backupExistingDestination(dst, backups, record.Path, record); err != nil {
				return err
			}
		}
		if err := copyFile(src, dst, record.Mode); err != nil {
			return err
		}
		copied++
	}

	stateDB, err := openStateDB(rt.StateDir, rt.Profile)
	if err != nil {
		return err
	}
	if scope.active() {
		repoDB, err := openRepoDB(rt.Repo, rt.Profile)
		if err != nil {
			return errors.Join(err, stateDB.Close())
		}
		if err := upsertRepoRecords(repoDB, repoUpdates); err != nil {
			return errors.Join(err, repoDB.Close(), stateDB.Close())
		}
		if err := upsertStateRecords(stateDB, records); err != nil {
			return errors.Join(err, repoDB.Close(), stateDB.Close())
		}
		if err := errors.Join(repoDB.Close(), stateDB.Close()); err != nil {
			return err
		}
	} else {
		if err := replaceStateRecords(stateDB, records); err != nil {
			return errors.Join(err, stateDB.Close())
		}
		if err := stateDB.Close(); err != nil {
			return err
		}
	}

	untouched := len(records) - copied
	if _, err := fmt.Fprintf(out, "Apply complete: copied %d file(s), left %d matching file(s) untouched for profile %s\n", copied, untouched, rt.Profile); err != nil {
		return err
	}
	if backups.Written() {
		if _, err := fmt.Fprintf(out, "Backups written to: %s\n", backups.Path()); err != nil {
			return err
		}
	}
	return nil
}

func writeRepoDriftRefusal(out io.Writer, operation string) error {
	if _, err := fmt.Fprintf(out, "%s aborted: profile repo files changed since dots last indexed them.\n", operation); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out, "Run `dots reindex` to accept the current repo files as tracked state, or restore the repo files if those changes were unintended.")
	return err
}

func applyWritePaths(report statusReport, opts applyOptions) map[string]struct{} {
	paths := make(map[string]struct{})
	for _, item := range report.Pending {
		switch item.Kind {
		case kindPendingCreate, kindPendingUpdate:
			paths[item.Path] = struct{}{}
		case kindPendingAdopt, kindPendingState:
			continue
		}
	}
	if opts.Force {
		for _, item := range report.Conflict {
			paths[item.Path] = struct{}{}
		}
	}
	return paths
}

func writeApplyPlan(out io.Writer, report statusReport, opts applyOptions) error {
	if _, err := fmt.Fprintln(out, "Apply plan (dry run; no files changed):"); err != nil {
		return err
	}
	if err := writeStatusReport(out, report); err != nil {
		return err
	}
	if report.hasConflicts() && opts.Force {
		_, err := fmt.Fprintln(out, "Force enabled: conflicting destination paths would be backed up before overwrite.")
		return err
	}
	return nil
}

func backupExistingDestination(dst string, backups *backupSetWriter, trackedPath string, record FileRecord) error {
	info, err := os.Lstat(dst)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat destination %s: %w", dst, err)
	}
	if info.Mode().IsRegular() {
		sha, mode, err := destinationFingerprint(dst)
		if err != nil {
			return err
		}
		if sha == record.SHA256 && mode == record.Mode {
			return nil
		}
	}
	if backups == nil {
		return errors.New("backup writer is required for forced apply")
	}
	backupPath, err := backups.backupPath(backupOriginHome, trackedPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o750); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	if err := os.Rename(dst, backupPath); err != nil {
		return fmt.Errorf("backup %s to %s: %w", dst, backupPath, err)
	}
	return nil
}

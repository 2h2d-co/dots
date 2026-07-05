package dots

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

const (
	pagerCommandEnv = "DOTS_SELECTED_PAGER"
	pagerShell      = `/bin/sh`
	pagerScript     = `eval "$DOTS_SELECTED_PAGER"`
)

type pagerRunner func(pager, input string) error

func resolveDiffPager(noPager bool, envValue, configValue string) string {
	if noPager {
		return ""
	}
	if pager := strings.TrimSpace(envValue); pager != "" {
		return pager
	}
	return strings.TrimSpace(configValue)
}

func stdoutIsTerminal() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func runShellPager(pager, input string) error {
	cmd := exec.Command(pagerShell, "-c", pagerScript)
	cmd.Env = append(os.Environ(), pagerCommandEnv+"="+pager)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open pager stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start pager: %w", err)
	}

	_, writeErr := io.WriteString(stdin, input)
	closeErr := stdin.Close()
	waitErr := cmd.Wait()
	if writeErr != nil && !isBrokenPipe(writeErr) {
		return fmt.Errorf("write pager input: %w", writeErr)
	}
	if closeErr != nil && !isBrokenPipe(closeErr) {
		return fmt.Errorf("close pager input: %w", closeErr)
	}
	if waitErr != nil {
		return fmt.Errorf("pager exited: %w", waitErr)
	}
	return nil
}

func isBrokenPipe(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrClosedPipe)
}

//go:build !windows

package themida

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// configureProcess puts the command in its own process group and installs a
// cancellation handler that kills the whole group. Themida runs under Wine,
// which spawns helper processes; signalling the group ensures none are left
// behind when the context is cancelled. A group that has already exited (ESRCH)
// is reported as a completed process rather than an error.
func configureProcess(_ context.Context, cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}

		if err != nil {
			return fmt.Errorf("kill command process group: %w", err)
		}

		return nil
	}
}

package themida

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// processKillTimeout bounds the taskkill cancellation attempt so a stuck
// taskkill cannot hang shutdown indefinitely before the direct kill fallback.
const processKillTimeout = 10 * time.Second

// configureProcess hides the protector's console window and installs a
// cancellation handler that terminates the whole process tree with taskkill /T,
// since the Themida protector may spawn child processes. The handler runs on a
// detached, time-bounded context so it still completes after the caller's
// context is cancelled, and falls back to killing the root process directly if
// taskkill fails.
func configureProcess(ctx context.Context, cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Cancel = func() error {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), processKillTimeout)
		defer cancel()

		// #nosec G204 -- The PID is from the process we started; taskkill is invoked without a shell.
		kill := exec.CommandContext(
			ctx,
			"taskkill",
			"/PID",
			strconv.Itoa(cmd.Process.Pid),
			"/T",
			"/F",
		)

		kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

		err := kill.Run()
		if err != nil {
			return cmd.Process.Kill()
		}

		return nil
	}
}

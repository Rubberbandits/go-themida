package themida

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

// CommandExecutor runs the prepared protector invocation, letting an application
// with its own logging or process registry replace the package's default runner
// (see [Runner.Execute]).
//
// The command is unstarted: the implementation must run it and wait for it to
// finish. ctx is the job context and must be honored for cancellation. step is a
// short human-readable name for the step, suitable for error wrapping. capture,
// when non-nil, must receive the command's stdout only. allowedExitCodes lists
// non-zero exit codes to treat as success, and apply only to normal process
// exits, never to cancellation or startup failures. A nil CommandExecutor uses
// the package's context-aware process runner.
type CommandExecutor func(
	ctx context.Context, step string, cmd *exec.Cmd, capture io.Writer, allowedExitCodes ...int,
) error

// processWaitDelay bounds how long Run waits for inherited output handles to
// close after the process exits, so a tool that leaves handles open cannot block
// the runner indefinitely.
const processWaitDelay = 2 * time.Second

// execute runs one protection step with the default runner, or delegates to a
// configured [Runner.Execute]. It short-circuits if the context is already done,
// streams output through the runner's writers plus a bounded diagnostic tail,
// and, on failure, wraps the error with the step name and that tail.
func (r *Runner) execute(
	ctx context.Context, step string, cmd *exec.Cmd, capture io.Writer, allowed ...int,
) error {
	{
		err := ctx.Err()
		if err != nil {
			return fmt.Errorf("%s: %w", step, err)
		}
	}

	if r.Execute != nil {
		return r.Execute(ctx, step, cmd, capture, allowed...)
	}

	tail := &diagnosticTail{}
	r.attachStreams(cmd, capture, tail)

	err := commandResult(ctx, cmd.Run())
	if err == nil || allowedExit(ctx, err, allowed) {
		return nil
	}

	return fmt.Errorf("%s: %w: %s", step, err, tail.String())
}

// attachStreams wires the command's stdout and stderr to the diagnostic tail,
// the runner's optional Stdout/Stderr, and the optional stdout capture, all
// serialized through one mutex so interleaved writes from both streams stay
// intact.
func (r *Runner) attachStreams(cmd *exec.Cmd, capture io.Writer, tail *diagnosticTail) {
	stdout := []io.Writer{tail}
	stderr := []io.Writer{tail}

	if r.Stdout != nil {
		stdout = append(stdout, r.Stdout)
	}

	if r.Stderr != nil {
		stderr = append(stderr, r.Stderr)
	}

	if capture != nil {
		stdout = append(stdout, capture)
	}

	var streams sync.Mutex

	cmd.Stdout = &streamWriter{mu: &streams, writer: io.MultiWriter(stdout...)}
	cmd.Stderr = &streamWriter{mu: &streams, writer: io.MultiWriter(stderr...)}
}

// commandResult normalizes a completed Run: a cancelled context takes priority
// over the raw error, and a WaitDelay expiry (a tool exiting cleanly while
// leaving output handles open) is treated as success.
func commandResult(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("command cancelled: %w", ctx.Err())
	}
	// A successful tool may leave inherited output handles open; bound pipe draining.
	if errors.Is(err, exec.ErrWaitDelay) {
		return nil
	}

	return err
}

// allowedExit reports whether err is a normal process exit (not a cancellation)
// whose exit code is in allowed.
func allowedExit(ctx context.Context, err error, allowed []int) bool {
	var exitErr *exec.ExitError

	return ctx.Err() == nil && errors.As(err, &exitErr) &&
		slices.Contains(allowed, exitErr.ExitCode())
}

// newCommand builds a context-bound command with the shared wait delay and the
// platform-specific process configuration (window hiding and process-tree
// cancellation) applied. No shell is involved.
func newCommand(ctx context.Context, executable string, args ...string) *exec.Cmd {
	// #nosec G204 -- The caller explicitly chooses the executable and arguments; no shell is used.
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.WaitDelay = processWaitDelay
	configureProcess(ctx, cmd)

	return cmd
}

// diagnosticTail is a thread-safe io.Writer that retains only the last 64 KiB
// written, providing a bounded excerpt of a tool's output to include in error
// messages.
type diagnosticTail struct {
	mu   sync.Mutex
	data []byte
}

// Write appends to the tail, discarding the oldest bytes so the retained data
// never exceeds the limit. It never returns an error and reports the full input
// length as written.
func (t *diagnosticTail) Write(data []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	const limit = 64 * 1024

	size := len(data)
	if size >= limit {
		t.data = append(t.data[:0], data[size-limit:]...)
	} else {
		if len(t.data)+size > limit {
			t.data = t.data[len(t.data)+size-limit:]
		}

		t.data = append(t.data, data...)
	}

	return size, nil
}

// String returns the retained output with surrounding whitespace trimmed.
func (t *diagnosticTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	return strings.TrimSpace(string(t.data))
}

// streamWriter serializes writes to an underlying writer through a shared mutex,
// so stdout and stderr delivered to the same destination do not interleave
// mid-write.
type streamWriter struct {
	mu     *sync.Mutex
	writer io.Writer
}

// Write forwards data to the underlying writer under the shared lock.
func (w *streamWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	size, err := w.writer.Write(data)
	if err != nil {
		return size, fmt.Errorf("stream command output: %w", err)
	}

	return size, nil
}

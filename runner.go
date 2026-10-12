package themida

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// defaultCleanupTimeout bounds Wine shutdown when CleanupTimeout is zero.
	defaultCleanupTimeout = 15 * time.Second
	// protectFlag, inputFlag, and outputFlag are the Themida command-line flags
	// for protecting a binary with explicit input and output paths.
	protectFlag = "/protect"
	inputFlag   = "/inputfile"
	outputFlag  = "/outputfile"
)

// Runner applies Themida protection to binaries, either natively on Windows or
// through Wine on another OS. It is configured once and reused: calls to
// [Runner.Protect] on one Runner are serialized through their cleanup, so a Wine
// prefix, installed license, and temporary files from one call are fully torn
// down before the next begins.
//
// Do not copy a Runner after first use, and do not mutate its configuration
// while a call is in progress. Separate runners that share a Themida
// installation, a Wine prefix, or an output path must be coordinated by the
// caller.
type Runner struct {
	// Path is a Themida executable or installation directory. An explicit
	// executable path takes precedence over Arch.
	Path string
	// Arch selects Themida.exe (Arch32) or Themida64.exe (Arch64) when Path
	// is a directory. Zero defaults to Arch64.
	Arch Arch
	// License contains the license bytes to install as TMLicenseA1.dat beside the
	// executable for this call. Any existing license is backed up and restored
	// after execution and environment teardown. Nil requires an existing license
	// and leaves it untouched; a non-nil empty license is rejected.
	License []byte
	// Environment runs the protector through a compatibility layer such as Wine,
	// translating paths and the command and tearing down afterward. Nil runs the
	// executable directly (normally on Windows). See [Environment].
	Environment Environment
	// Env is the complete subprocess environment; nil inherits os.Environ().
	Env []string
	// Stdout receives the protector's standard output, in addition to the
	// internal diagnostic tail. Nil discards it.
	Stdout io.Writer
	// Stderr receives the protector's standard error, in addition to the
	// internal diagnostic tail. Nil discards it.
	Stderr io.Writer
	// Execute, when set, replaces the default process runner for the protector
	// invocation (see [CommandExecutor]). It owns streaming and logging in that
	// mode. An Environment runs its own helper commands separately.
	Execute CommandExecutor
	// CleanupTimeout bounds Wine shutdown, even when the job is cancelled.
	// Zero selects 15 seconds.
	CleanupTimeout time.Duration
	// once lazily initializes the serialization slot on first use.
	once sync.Once
	// slot is a capacity-one channel that serializes Protect calls.
	slot chan struct{}
}

// Protection describes one protection request: which project to apply, which
// binary to protect, and where to write the result. All paths are host paths
// resolved against the caller's current directory.
//
// The project source is one of ProjectFile, Project, or Options alone;
// ProjectFile and Project are mutually exclusive. InputFile and OutputFile are
// always required.
type Protection struct {
	// ProjectFile is the path to a .tmd project to protect with. It is mutually
	// exclusive with Project.
	ProjectFile string
	// Project is an already-parsed project to protect with. The runner clones it,
	// so the caller's value is never mutated. It is mutually exclusive with
	// ProjectFile.
	Project *Project
	// ProjectDirectory anchors relative resources for in-memory/settings-only
	// projects. File overrides always remain beside the original project.
	ProjectDirectory string
	// Options are settings applied over the chosen project before protection.
	// Supplied alone, they form a project from scratch.
	Options Settings
	// InputFile is the binary to protect.
	InputFile string
	// OutputFile is where the protected binary is written.
	OutputFile string
	// DeleteOutput removes the generated file after returning its bytes and on
	// execution failure. A pre-existing output is removed before protection.
	DeleteOutput bool
}

// protectionFiles holds the resolved absolute paths for one run: the protector
// executable, its installation directory (the working directory and license
// location), and the input and output binaries.
type protectionFiles struct {
	executable   string
	installation string
	input        string
	output       string
}

// protectionResult accumulates the protected bytes and any error, including
// errors joined from deferred cleanup steps.
type protectionResult struct {
	binary []byte
	err    error
}

// Protect protects request.InputFile and returns the newly generated binary. It
// runs the protector with /protect and explicit input and output paths.
//
// A nil context uses context.Background(). Calls are serialized: Protect waits
// for its turn (honoring cancellation while waiting) and, before returning,
// completes Wine shutdown, license restoration, and temporary-project cleanup so
// the next call starts clean. It never returns stale or empty output; a cleanup
// failure rejects the binary while preserving any execution error.
//
//nolint:contextcheck // Nil contexts select a documented uncancelled fallback; shutdown retains caller values.
func (r *Runner) Protect(ctx context.Context, request Protection) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	r.once.Do(func() { r.slot = make(chan struct{}, 1) })

	select {
	case r.slot <- struct{}{}:
		defer func() { <-r.slot }()
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for Themida runner: %w", ctx.Err())
	}

	err := ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("start Themida protection: %w", err)
	}

	result := r.protect(ctx, request)

	return result.binary, result.err
}

// validate checks the runner's own configuration: a path is required, Arch (if
// set) must be 32 or 64, and CleanupTimeout must not be negative.
func (r *Runner) validate() error {
	if r.Path == "" {
		return fmt.Errorf("%w: executable or installation path is required", ErrInvalidProtection)
	}

	if r.Arch != 0 && r.Arch != Arch32 && r.Arch != Arch64 {
		return fmt.Errorf(
			"%w: unsupported architecture %d: use 32 or 64",
			ErrInvalidProtection,
			r.Arch,
		)
	}

	if r.CleanupTimeout < 0 {
		return fmt.Errorf("%w: cleanup timeout must not be negative", ErrInvalidProtection)
	}

	return nil
}

// protect performs one serialized protection run: it resolves files, prepares
// the temporary project and license, configures Wine, then protects and reads
// the output. Cleanups for the project, license, and Wine prefix are deferred in
// an order that keeps the license installed until Wine has shut down.
func (r *Runner) protect(ctx context.Context, request Protection) *protectionResult {
	result := &protectionResult{}

	files, err := r.resolveFiles(request)
	if err != nil {
		return result.fail(err)
	}

	projectPath, closeProject, err := prepareProtectionProject(request)
	if err != nil {
		return result.fail(err)
	}
	defer result.cleanup("clean up Themida project", closeProject)

	closeLicense, err := prepareLicense(files.installation, r.License)
	if err != nil {
		return result.fail(err)
	}
	// Registered before environment teardown so the license remains until it finishes.
	defer result.cleanup("clean up Themida license", closeLicense)

	env, teardown, err := r.setupEnvironment(ctx)
	if err != nil {
		return result.fail(err)
	}

	if teardown != nil {
		defer result.cleanup(
			"clean up protection environment",
			func() error { return r.teardown(ctx, teardown) },
		)
	}

	r.protectOutput(ctx, files, projectPath, request.DeleteOutput, env, result)

	return result
}

// setupEnvironment resolves the subprocess environment and a teardown. With no
// Environment it is the inherited environment and a nil teardown; otherwise the
// Environment's Setup provides both.
func (r *Runner) setupEnvironment(
	ctx context.Context,
) ([]string, func(context.Context) error, error) {
	base := r.Env
	if base == nil {
		base = os.Environ()
	}

	if r.Environment == nil {
		return base, nil, nil
	}

	env, teardown, err := r.Environment.Setup(ctx, base)
	if err != nil {
		return nil, nil, fmt.Errorf("set up protection environment: %w", err)
	}

	return env, teardown, nil
}

// teardown runs the environment's teardown within CleanupTimeout. It uses a
// detached context so teardown still completes after the caller's context is
// cancelled, while retaining the caller's context values.
func (r *Runner) teardown(ctx context.Context, teardown func(context.Context) error) error {
	timeout := r.CleanupTimeout
	if timeout == 0 {
		timeout = defaultCleanupTimeout
	}
	// Retain caller values, but allow teardown to finish after caller cancellation.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	return teardown(cleanupCtx)
}

// resolveFiles validates the runner and request and resolves the absolute
// executable, installation, input, and output paths. When Path is a directory,
// the executable is chosen from Arch; when Path is an .exe, the installation is
// its directory.
func (r *Runner) resolveFiles(request Protection) (protectionFiles, error) {
	{
		err := r.validate()
		if err != nil {
			return protectionFiles{}, err
		}
	}

	if request.InputFile == "" || request.OutputFile == "" {
		return protectionFiles{}, fmt.Errorf(
			"%w: input and output files are required",
			ErrInvalidProtection,
		)
	}

	input, err := absolutePath(request.InputFile)
	if err != nil {
		return protectionFiles{}, err
	}

	output, err := absolutePath(request.OutputFile)
	if err != nil {
		return protectionFiles{}, err
	}

	installation, err := absolutePath(r.Path)
	if err != nil {
		return protectionFiles{}, err
	}

	executable := installation
	if !strings.EqualFold(filepath.Ext(executable), ".exe") {
		executable = filepath.Join(installation, architectureExecutable(r.Arch))
	} else {
		installation = filepath.Dir(executable)
	}

	files := protectionFiles{
		executable:   executable,
		installation: installation,
		input:        input,
		output:       output,
	}
	{
		err := files.validate(request.ProjectFile)
		if err != nil {
			return protectionFiles{}, err
		}
	}

	return files, nil
}

// validate rejects unsafe aliasing between the resolved paths: the input and
// output must differ from each other and from the license file, the input must
// be a nonempty regular file, and the project file (when given) must differ from
// both the license and the output.
func (f protectionFiles) validate(projectFile string) error {
	licensePath := filepath.Join(f.installation, licenseFilename)
	if sameFile(f.input, licensePath) || sameFile(f.output, licensePath) {
		return fmt.Errorf(
			"%w: input and output must differ from the Themida license",
			ErrInvalidProtection,
		)
	}

	if sameFile(f.input, f.output) {
		return fmt.Errorf("%w: input and output files must differ", ErrInvalidProtection)
	}
	{
		err := validateBinaryFile(f.input)
		if err != nil {
			return fmt.Errorf("read Themida input: %w", err)
		}
	}

	if projectFile == "" {
		return nil
	}

	base, err := absolutePath(projectFile)
	if err != nil {
		return err
	}

	if sameFile(base, licensePath) {
		return fmt.Errorf("%w: project must differ from the Themida license", ErrInvalidProtection)
	}

	if sameFile(base, f.output) {
		return fmt.Errorf("%w: output must differ from the project file", ErrInvalidProtection)
	}

	return nil
}

// validateBinaryFile requires that path is an existing, nonempty regular file,
// used to reject a missing or empty input and to confirm the output was
// produced.
func validateBinaryFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect binary %q: %w", path, err)
	}

	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("%w: %q must be a nonempty regular file", ErrInvalidProtection, path)
	}

	return nil
}

// protectOutput builds and runs the protection command, recording the result.
// It removes any previous output first so stale output can never be returned,
// optionally schedules the new output for deletion, and reads the generated
// binary only after a successful run.
func (r *Runner) protectOutput(
	ctx context.Context, files protectionFiles, projectPath string, deleteOutput bool,
	env []string, result *protectionResult,
) {
	cmd, err := r.protectionCommand(ctx, files, projectPath, env)
	if err != nil {
		result.fail(err)

		return
	}
	{
		err := removeFile(files.output)
		if err != nil {
			result.fail(fmt.Errorf("remove previous Themida output: %w", err))

			return
		}
	}

	if deleteOutput {
		defer result.cleanup(
			"remove Themida output",
			func() error { return removeFile(files.output) },
		)
	}
	{
		err := r.execute(ctx, "Themida protection", cmd, nil)
		if err != nil {
			result.fail(err)

			return
		}
	}

	result.binary, result.err = readProtectionOutput(files.output)
}

// protectionCommand assembles the protector invocation. When an Environment is
// set, the project, input, and output paths are translated and the command is
// wrapped (for example the protector becomes the first argument to a loader).
// The command runs in the installation directory with the configured environment.
func (r *Runner) protectionCommand(
	ctx context.Context, files protectionFiles, projectPath string, env []string,
) (*exec.Cmd, error) {
	paths := []string{projectPath, files.input, files.output}
	executable := files.executable

	if r.Environment != nil {
		for index, path := range paths {
			converted, err := r.Environment.TranslatePath(ctx, path, env)
			if err != nil {
				return nil, fmt.Errorf("translate protection path %q: %w", path, err)
			}

			paths[index] = converted
		}
	}

	args := []string{protectFlag, paths[0], inputFlag, paths[1], outputFlag, paths[2]}

	if r.Environment != nil {
		wrapped, prefix, err := r.Environment.Command(executable)
		if err != nil {
			return nil, fmt.Errorf("build protection command: %w", err)
		}

		executable = wrapped

		args = append(append([]string{}, prefix...), args...)
	}

	cmd := newCommand(ctx, executable, args...)
	cmd.Dir, cmd.Env = files.installation, env

	return cmd, nil
}

// readProtectionOutput reads the protected binary, requiring a nonempty regular
// file so a missing or empty output is reported as an error rather than returned
// as a result.
func readProtectionOutput(path string) ([]byte, error) {
	{
		err := validateBinaryFile(path)
		if err != nil {
			return nil, fmt.Errorf("read Themida output: %w", err)
		}
	}
	// #nosec G304 -- Protection paths are explicitly selected by the caller.
	binary, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Themida output: %w", err)
	}

	if len(binary) == 0 {
		return nil, fmt.Errorf("%w: output is empty", ErrInvalidProtection)
	}

	return binary, nil
}

// fail clears any binary and sets err, returning the result for convenient
// early return.
func (p *protectionResult) fail(err error) *protectionResult {
	p.binary, p.err = nil, err

	return p
}

// cleanup runs a deferred cleanup step and, on failure, joins its error onto the
// result and discards the binary, so a run whose cleanup failed never returns a
// binary as if it had succeeded.
func (p *protectionResult) cleanup(step string, cleanup func() error) {
	err := cleanup()
	if err != nil {
		p.binary = nil
		p.err = errors.Join(p.err, fmt.Errorf("%s: %w", step, err))
	}
}

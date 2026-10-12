//nolint:testpackage // White-box tests exercise internal parser and execution behavior.
package themida

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func assertLicenseRestored(t *testing.T, path string, original []byte, info os.FileInfo) {
	t.Helper()

	// #nosec G304 -- The caller supplies a license fixture in its private test directory.
	data, err := os.ReadFile(path)
	if original == nil {
		requireTestf(t, errors.Is(err, os.ErrNotExist), "temporary license remains: %v", err)
	} else {
		requireNoError(t, err)
		requireTestf(t, bytes.Equal(data, original), "original license bytes were not restored")

		restored, err := os.Stat(path)
		requireNoError(t, err)
		requireTestf(t, os.SameFile(info, restored), "original license identity changed")
		requireTestf(
			t,
			info.ModTime().Equal(restored.ModTime()),
			"original license modification time changed",
		)
		requireTestf(t, info.Mode() == restored.Mode(), "original license permissions changed")
	}

	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".themida-license-*"))
	requireNoError(t, err)
	requireTestf(t, len(files) == 0, "license temporary files remain: %v", files)
}

func TestRunnerLicenseLifecycle(t *testing.T) {
	t.Parallel()

	for _, useWine := range []bool{false, true} {
		for _, mode := range []string{"existing", "replace", "create"} {
			t.Run(
				map[bool]string{false: "native/", true: "wine/"}[useWine]+mode,
				func(t *testing.T) {
					t.Parallel()
					checkLicenseLifecycle(t, useWine, mode)
				},
			)
		}
	}
}

func TestRunnerLicenseRestoredOnFailure(t *testing.T) {
	t.Parallel()

	for _, originalExists := range []bool{false, true} {
		for _, mode := range []string{
			testCommandFailure, testPathFailure, testNoOutput, "server-fail", testStartup, "cancel", "cleanup-timeout",
		} {
			t.Run(
				map[bool]string{false: "create/", true: "replace/"}[originalExists]+mode,
				func(t *testing.T) {
					t.Parallel()
					checkLicenseFailure(t, originalExists, mode)
				},
			)
		}
	}
}

func TestRunnerRequiresLicenseAndProtectsItsPath(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{
		testMissing, "empty supplied", testDirectory, "input alias", "output alias", "project alias", "hardlink output",
	} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			runner, request, events := runnerFixture(t, true)
			path := filepath.Join(filepath.Dir(request.InputFile), licenseFilename)
			invalidateLicense(t, runner, &request, path, mode)

			data, err := runner.Protect(context.Background(), request)
			requireProtectionFailure(t, data, err)
			requireErrorContains(t, err, "license")

			if mode == testMissing {
				requireTestf(
					t,
					errors.Is(err, os.ErrNotExist),
					"missing license error lost: %v",
					err,
				)
			}

			requireTestf(
				t,
				len(runnerEvents(t, events)) == 0,
				"license validation launched a process",
			)

			if mode != testMissing && mode != testDirectory {
				assertLicenseRestored(t, path, []byte("fixture license"), mustLicenseInfo(t, path))
			}
		})
	}
}

func invalidateLicense(t *testing.T, runner *Runner, request *Protection, path, mode string) {
	t.Helper()

	switch mode {
	case testMissing:
		requireNoError(t, os.Remove(path))
	case testDirectory:
		requireNoError(t, os.Remove(path))
		requireNoError(t, os.Mkdir(path, 0o700))
	case "empty supplied":
		runner.License = []byte{}
	case "input alias":
		request.InputFile = path
	case "output alias":
		request.OutputFile = path
	case "project alias":
		request.ProjectFile = path
	case "hardlink output":
		err := os.Link(path, request.OutputFile)
		if err != nil {
			t.Skipf("hard links unavailable: %v", err)
		}
	}
}

func mustLicenseInfo(t *testing.T, path string) os.FileInfo {
	t.Helper()
	// File.Stat captures the identity immediately on Windows; os.Stat lazily
	// resolves it by filename, which changes when the file is backed up.
	// #nosec G304 -- The caller supplies a private license fixture.
	file, err := os.Open(path)
	requireNoError(t, err)

	info, statErr := file.Stat()
	closeErr := file.Close()
	requireNoError(t, errors.Join(statErr, closeErr))

	return info
}

func TestRunnerLicenseCleanupFailurePreservesBackupAndExecutionError(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"success", "failure"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			runner, request, _ := runnerFixture(t, false)
			path := filepath.Join(filepath.Dir(request.InputFile), licenseFilename)
			info := mustLicenseInfo(t, path)
			runner.License = []byte("temporary fixture license")

			var executionErr error
			if mode == "failure" {
				executionErr = errors.ErrUnsupported
			}

			runner.Execute = func(ctx context.Context, step string, cmd *exec.Cmd, capture io.Writer, allowed ...int) error {
				err := (&Runner{}).execute(ctx, step, cmd, capture, allowed...)
				if err != nil {
					return err
				}

				requireNoError(t, os.Remove(path))
				requireNoError(t, os.Mkdir(path, 0o700))
				requireNoError(
					t,
					os.WriteFile(filepath.Join(path, "blocker"), []byte("blocked"), 0o600),
				)

				return executionErr
			}
			data, err := runner.Protect(context.Background(), request)
			requireProtectionFailure(t, data, err)
			requireErrorContains(t, err, "clean up Themida license")

			if executionErr != nil {
				requireTestf(t, errors.Is(err, executionErr), "primary error lost: %v", err)
			}

			backups, globErr := filepath.Glob(
				filepath.Join(filepath.Dir(path), ".themida-license-backup-*"),
			)
			requireNoError(t, globErr)
			requireTestf(t, len(backups) == 1, "original license backup lost: %v", backups)
			requireErrorContains(t, err, backups[0])
			requireTestf(
				t,
				os.SameFile(info, mustLicenseInfo(t, backups[0])),
				"original license identity lost",
			)

			// Recover the deliberately obstructed fixture and verify the backup bytes.
			requireNoError(t, os.Remove(filepath.Join(path, "blocker")))
			requireNoError(t, os.Remove(path))
			requireNoError(t, os.Rename(backups[0], path))
			assertLicenseRestored(t, path, []byte("fixture license"), info)
		})
	}
}

func TestRunnerLicenseLivesBesideExecutable(t *testing.T) {
	t.Parallel()
	runner, request, events := runnerFixture(t, false)
	installation := t.TempDir()
	executable := filepath.Join(installation, "Themida32.exe")
	// #nosec G304 -- Read the mock executable supplied by the test fixture.
	data, err := os.ReadFile(filepath.Join(runner.Path, testThemida64))
	requireNoError(t, err)
	// #nosec G306 G703 -- Copy the mock executable into a private fixture; owner execute permission is required.
	requireNoError(t, os.WriteFile(executable, data, 0o700))

	runner.Path = executable
	_, err = runner.Protect(context.Background(), request)
	requireTestf(t, errors.Is(err, os.ErrNotExist), "missing license error lost: %v", err)
	requireErrorContains(t, err, filepath.Join(installation, licenseFilename))
	requireTestf(t, len(runnerEvents(t, events)) == 0, "missing license launched a process")

	runner.License = []byte("temporary fixture license")
	runner.Execute = func(ctx context.Context, step string, cmd *exec.Cmd, capture io.Writer, allowed ...int) error {
		requireTestf(t, cmd.Dir == installation, "wrong installation")
		requireTestf(t, cmd.Path == executable, "wrong executable")

		// #nosec G304 -- Read the license installed in the private mock installation.
		data, err := os.ReadFile(filepath.Join(installation, licenseFilename))
		requireNoError(t, err)
		requireTestf(
			t,
			bytes.Equal(data, runner.License),
			"supplied license not placed beside executable",
		)

		return (&Runner{}).execute(ctx, step, cmd, capture, allowed...)
	}
	_, err = runner.Protect(context.Background(), request)
	requireNoError(t, err)
	assertLicenseRestored(t, filepath.Join(installation, licenseFilename), nil, nil)

	// #nosec G304 -- Check the unrelated license fixture in the private project directory.
	data, err = os.ReadFile(filepath.Join(filepath.Dir(request.InputFile), licenseFilename))
	requireNoError(t, err)
	requireTestf(
		t,
		string(data) == "fixture license",
		"modified unrelated project-directory license",
	)
}

func checkLicenseLifecycle(t *testing.T, useWine bool, mode string) {
	t.Helper()
	runner, request, _ := runnerFixture(t, useWine)
	path := filepath.Join(filepath.Dir(request.InputFile), licenseFilename)
	// #nosec G304 -- Read the license fixture in its private test directory.
	original, err := os.ReadFile(path)
	requireNoError(t, err)

	info := mustLicenseInfo(t, path)
	want := original

	if mode != "existing" {
		runner.License = []byte("temporary fixture license\r\n")
		want = runner.License
	}

	if mode == "create" {
		requireNoError(t, os.Remove(path))

		original = nil
	}
	// Each subprocess, including Wine shutdown, sees the installed license.
	runner.Execute = licenseTestExecutor(t, path, mode, want, info)
	if useWine {
		environment := fixtureEnvironment(t, runner)
		environment.exec = runner.Execute
		runner.Environment = environment
	}

	// Reuse also verifies cleanup completed before the next call.
	for range 2 {
		data, err := runner.Protect(context.Background(), request)
		requireProtectedBinary(t, data, err)
		assertLicenseRestored(t, path, original, info)
	}
}

func licenseTestExecutor(
	t *testing.T,
	path, mode string,
	want []byte,
	info os.FileInfo,
) CommandExecutor {
	t.Helper()

	return func(ctx context.Context, step string, cmd *exec.Cmd, capture io.Writer, allowed ...int) error {
		// #nosec G304 -- Check the installed license fixture during the command.
		data, err := os.ReadFile(path)
		requireNoError(t, err)
		requireTestf(t, bytes.Equal(data, want), "license unavailable during %s", step)

		backups, err := filepath.Glob(
			filepath.Join(filepath.Dir(path), ".themida-license-backup-*"),
		)
		requireNoError(t, err)

		if mode == "replace" {
			requireTestf(t, len(backups) == 1, "original license was not backed up")

			backupInfo, err := os.Stat(backups[0])
			requireNoError(t, err)
			requireTestf(
				t,
				os.SameFile(info, backupInfo),
				"original license was not renamed to its backup",
			)
		} else {
			requireTestf(t, len(backups) == 0, "unexpected backup")
		}

		return (&Runner{}).execute(ctx, step, cmd, capture, allowed...)
	}
}

func checkLicenseFailure(t *testing.T, originalExists bool, mode string) {
	t.Helper()
	runner, request, _ := runnerFixture(t, mode != testStartup)
	path := filepath.Join(filepath.Dir(request.InputFile), licenseFilename)
	// #nosec G304 -- Read the original license fixture.
	original, err := os.ReadFile(path)
	requireNoError(t, err)

	info := mustLicenseInfo(t, path)
	if !originalExists {
		requireNoError(t, os.Remove(path))

		original = nil
	}

	runner.License = []byte("temporary fixture license")
	setRunnerEnv(runner, "GO_THEMIDA_TEST_MODE", mode)

	switch mode {
	case "server-fail":
		setRunnerEnv(runner, "GO_THEMIDA_TEST_SERVER_FAIL", "-w")
	case testStartup:
		runner.Path = filepath.Join(filepath.Dir(path), "missing.exe")
	case "cleanup-timeout":
		gate := filepath.Join(filepath.Dir(path), "license-cleanup-gate")
		setRunnerEnv(runner, "GO_THEMIDA_TEST_CLEANUP_GATE", gate)

		runner.CleanupTimeout = 500 * time.Millisecond
	case "cancel":
		checkCancelledLicense(t, runner, request, path)
		assertLicenseRestored(t, path, original, info)

		return
	}

	data, err := runner.Protect(context.Background(), request)
	requireProtectionFailure(t, data, err)
	assertLicenseRestored(t, path, original, info)
}

func checkCancelledLicense(t *testing.T, runner *Runner, request Protection, path string) {
	t.Helper()

	ready := filepath.Join(filepath.Dir(path), "license-protect-ready")
	setRunnerEnv(runner, "GO_THEMIDA_TEST_PROTECT_READY", ready)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)

	go func() {
		_, err := runner.Protect(ctx, request)
		done <- err
	}()

	waitRunnerReady(t, ready)

	// #nosec G304 -- Check that the license fixture remains installed until cancellation.
	data, err := os.ReadFile(path)
	requireNoError(t, err)
	requireTestf(
		t,
		bytes.Equal(data, runner.License),
		"license not present during running protection",
	)
	cancel()

	select {
	case err := <-done:
		requireTestf(t, errors.Is(err, context.Canceled), "cancellation error lost: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled protection did not finish")
	}
}

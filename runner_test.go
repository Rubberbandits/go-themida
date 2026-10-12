//nolint:testpackage // White-box tests exercise internal parser and execution behavior.
package themida

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRunnerNativeAndWine(t *testing.T) {
	t.Parallel()

	for _, useWine := range []bool{false, true} {
		t.Run(fmt.Sprint("wine=", useWine), func(t *testing.T) {
			t.Parallel()
			runner, request, eventsPath := runnerFixture(t, useWine)

			var stdout, stderr bytes.Buffer

			runner.Stdout, runner.Stderr = &stdout, &stderr
			runner.Env = append(runner.Env,
				"CUSTOM_VALUE=preserved",
				"WINEPREFIX=wrong",
				"WINEDLLOVERRIDES=other=n,b",
			)
			data, err := runner.Protect(context.Background(), request)
			requireProtectedBinary(t, data, err)
			requireTestf(
				t,
				strings.Contains(stdout.String(), "protection progress"),
				"stdout was not streamed",
			)
			requireTestf(
				t,
				strings.Contains(stderr.String(), "protection diagnostic"),
				"stderr was not streamed",
			)
			assertRunnerCommands(t, runner, request, runnerEvents(t, eventsPath), useWine)
		})
	}
}

func assertRunnerCommands(
	t *testing.T,
	runner *Runner,
	request Protection,
	events []runnerEvent,
	useWine bool,
) {
	t.Helper()
	assertRunnerEnvironment(t, runner, events, useWine)

	protect := events[0]
	expected := []string{
		protectFlag,
		request.ProjectFile,
		inputFlag,
		request.InputFile,
		outputFlag,
		request.OutputFile,
	}

	if useWine {
		protect = events[3]
		expected = []string{
			filepath.Join(runner.Path, testThemida64),
			protectFlag, testWinePath(request.ProjectFile),
			inputFlag, testWinePath(request.InputFile),
			outputFlag, testWinePath(request.OutputFile),
		}
	}

	requireTestf(
		t,
		reflect.DeepEqual(protect.Args, expected),
		"incorrect arguments: %v",
		protect.Args,
	)
	requireTestf(t, protect.Dir == runner.Path, "incorrect working directory: %s", protect.Dir)
	requireTestf(t, eventEnv(protect, "CUSTOM_VALUE") == "preserved", "lost custom environment")
}

func assertRunnerEnvironment(t *testing.T, runner *Runner, events []runnerEvent, useWine bool) {
	t.Helper()

	if !useWine {
		requireTestf(t, len(events) == 1, "native run invoked Wine helpers")

		return
	}

	assertRunnerStopped(t, events)
	requireTestf(t, len(events) == 6, "unexpected number of Wine commands")

	prefix := fixtureEnvironment(t, runner).prefix
	for _, event := range events {
		requireTestf(
			t,
			eventEnv(event, "WINEPREFIX") == prefix,
			"incorrect Wine prefix",
		)
		requireTestf(
			t,
			eventEnv(event, "WINEDLLOVERRIDES") == "other=n,b;winedbg.exe=",
			"incorrect DLL overrides",
		)
		requireTestf(
			t,
			eventEnv(event, "WINEDEBUG") == "-all,err+all",
			"incorrect Wine diagnostics environment",
		)
	}
}

func TestRunnerExecutableAndProjectSources(t *testing.T) {
	t.Parallel()

	for _, source := range []string{"file overrides", testMemory, testOptions} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			runner, request, eventsPath := runnerFixture(t, true)
			// #nosec G304 -- Read the base project in the fixture's private directory.
			original, err := os.ReadFile(request.ProjectFile)
			requireNoError(t, err)

			runner.Path = filepath.Join(runner.Path, testThemida64)
			capture := filepath.Join(filepath.Dir(request.ProjectFile), "captured.tmd")
			setRunnerEnv(runner, "GO_THEMIDA_TEST_PROJECT_CAPTURE", capture)

			request.Options = Settings{testFlag: false, "COUNT": 0, "EMPTY": ""}

			var parsed Project

			switch source {
			case testMemory:
				requireNoError(t, parsed.Unmarshal(original))

				request.Project = &parsed
				request.ProjectFile = ""
				request.ProjectDirectory = filepath.Dir(capture)
			case testOptions:
				request.ProjectFile = ""
				request.ProjectDirectory = filepath.Dir(capture)
			}

			request.DeleteOutput = true
			data, err := runner.Protect(context.Background(), request)
			requireProtectedBinary(t, data, err)
			assertEffectiveProject(t, capture, source, &parsed)
			assertProjectFilesPreserved(t, capture, original, request, runnerEvents(t, eventsPath))
		})
	}
}

func assertEffectiveProject(t *testing.T, capture, source string, parsed *Project) {
	t.Helper()
	// #nosec G304 -- Read the project captured by the mock command in its private directory.
	contents, err := os.ReadFile(capture)
	requireNoError(t, err)

	var effective Project
	requireNoError(t, effective.Unmarshal(contents))

	for key, want := range map[string]string{testFlag: "false", "COUNT": "0", "EMPTY": ""} {
		got, exists := effective.Get(key)
		requireTestf(t, exists, "setting %s is missing", key)
		requireTestf(t, got == want, "%s=%q, want %q", key, got, want)
	}

	if source != testOptions {
		got, _ := effective.Get("KEEP")
		requireTestf(t, got == "original", "lost base settings")
	}

	if source == testMemory {
		_, exists := parsed.Get(testFlag)
		requireTestf(t, !exists, "mutated caller project")
	}
}

func assertProjectFilesPreserved(
	t *testing.T,
	capture string,
	original []byte,
	request Protection,
	events []runnerEvent,
) {
	t.Helper()

	originalPath := filepath.Join(filepath.Dir(capture), "base project.tmd")
	// #nosec G304 -- Check the source project fixture after protection.
	got, err := os.ReadFile(originalPath)
	requireNoError(t, err)
	requireTestf(t, bytes.Equal(got, original), "changed source project")

	_, err = os.Stat(request.OutputFile)
	requireTestf(t, os.IsNotExist(err), "protected output was not deleted")

	requireTestf(t, len(events) == 6, "unexpected number of Wine commands")

	effectivePath := testLocalPath(events[3].Args[2])
	requireTestf(
		t,
		filepath.Dir(effectivePath) == filepath.Dir(capture),
		"changed project-relative paths",
	)

	_, err = os.Stat(effectivePath)
	requireTestf(t, os.IsNotExist(err), "temporary project was not deleted")
}

func TestRunnerRejectsFailuresAndStaleOutput(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{
		testCommandFailure, testNoOutput, testEmptyOutput, testPathFailure, "path-empty", "path-multiline",
	} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			runner, request, eventsPath := runnerFixture(t, true)
			request.Options = Settings{testFlag: false}
			request.DeleteOutput = true
			requireNoError(t, os.WriteFile(request.OutputFile, []byte("stale binary"), 0o600))

			setRunnerEnv(runner, "GO_THEMIDA_TEST_MODE", mode)

			data, err := runner.Protect(context.Background(), request)
			requireTestf(
				t,
				err != nil && data == nil,
				"accepted invalid output: %q %v",
				data,
				err,
			)

			if mode == testCommandFailure {
				var exitErr interface{ ExitCode() int }
				requireTestf(t, errors.As(err, &exitErr) && exitErr.ExitCode() == 7 &&
					strings.Contains(
						err.Error(),
						"tool failure details",
					), "lost exit code/diagnostics: %v", err)
			}

			assertRunnerStopped(t, runnerEvents(t, eventsPath))
			assertNoRunnerTemps(t, filepath.Dir(request.ProjectFile))
		})
	}
}

func TestRunnerCleanupFailuresAndAlreadyStoppedServer(t *testing.T) {
	t.Parallel()

	for _, fail := range []string{"", "-k", "-w"} {
		t.Run(fail, func(t *testing.T) {
			t.Parallel()

			runner, request, _ := runnerFixture(t, true)
			if fail == "" {
				setRunnerEnv(runner, "GO_THEMIDA_TEST_SERVER_GONE", "1")
			} else {
				setRunnerEnv(runner, "GO_THEMIDA_TEST_SERVER_FAIL", fail)
			}

			data, err := runner.Protect(context.Background(), request)
			if fail == "" {
				requireProtectedBinary(t, data, err)
			} else {
				requireProtectionFailure(t, data, err)
				requireErrorContains(t, err, "server cleanup failure")
			}

			if fail != "" {
				setRunnerEnv(runner, "GO_THEMIDA_TEST_MODE", testCommandFailure)

				data, err = runner.Protect(context.Background(), request)
				requireProtectionFailure(t, data, err)
				requireErrorContains(t, err, "tool failure details", "server cleanup failure")
			}
		})
	}
}

func TestRunnerCancellationStillCleansWine(t *testing.T) {
	t.Parallel()
	runner, request, events := runnerFixture(t, true)
	request.Options = Settings{testFlag: false}
	request.DeleteOutput = true
	ready := filepath.Join(filepath.Dir(request.InputFile), "protect.ready")
	setRunnerEnv(runner, "GO_THEMIDA_TEST_PROTECT_READY", ready)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)

	go func() { _, err := runner.Protect(ctx, request); done <- err }()

	waitRunnerReady(t, ready)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("cancelled runner did not return")
	}

	assertRunnerStopped(t, runnerEvents(t, events))
	assertNoRunnerTemps(t, filepath.Dir(request.InputFile))
}

func TestRunnerHoldsSlotThroughBoundedCleanup(t *testing.T) {
	t.Parallel()
	runner, request, events := runnerFixture(t, true)
	gate := filepath.Join(filepath.Dir(request.InputFile), "cleanup-gate")
	setRunnerEnv(runner, "GO_THEMIDA_TEST_CLEANUP_GATE", gate)
	t.Cleanup(func() { _ = os.WriteFile(gate, []byte("release"), 0o600) })

	runner.CleanupTimeout = 700 * time.Millisecond
	done := make(chan error, 1)

	go func() { _, err := runner.Protect(context.Background(), request); done <- err }()

	waitRunnerReady(t, gate+".ready")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	{
		_, err := runner.Protect(ctx, request)
		requireTestf(
			t,
			errors.Is(err, context.DeadlineExceeded),
			"waiting call was not cancelled: %v",
			err,
		)
	}

	requireTestf(t, len(runnerEvents(t, events)) == 6, "waiting call launched a process")

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("cleanup did not respect timeout: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup timeout was ignored")
	}
}

func TestRunnerValidationProtectsInputAndProject(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{
		"same input", "same project", "no source", "two sources",
		"empty input", "invalid options", "no prefix", "negative timeout",
	} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			runner, request, events := runnerFixture(t, true)
			// #nosec G304 -- Read the source project fixture before validating an invalid request.
			project, err := os.ReadFile(request.ProjectFile)
			requireNoError(t, err)
			invalidateProtectionRequest(t, runner, &request, mode)

			data, err := runner.Protect(context.Background(), request)
			requireProtectionFailure(t, data, err)
			requireTestf(t, len(runnerEvents(t, events)) == 0, "invalid request launched a process")

			// #nosec G304 -- Check that validation left the private source project fixture unchanged.
			got, err := os.ReadFile(
				filepath.Join(filepath.Dir(request.InputFile), "base project.tmd"),
			)
			requireNoError(t, err)
			requireTestf(t, bytes.Equal(got, project), "validation modified project")
			assertNoRunnerTemps(t, filepath.Dir(request.InputFile))
		})
	}
}

func invalidateProtectionRequest(t *testing.T, runner *Runner, request *Protection, mode string) {
	t.Helper()

	switch mode {
	case "same input":
		request.OutputFile = request.InputFile
	case "same project":
		request.OutputFile = request.ProjectFile
	case "no source":
		request.ProjectFile = ""
	case "two sources":
		request.Project = &Project{}
	case "empty input":
		requireNoError(t, os.WriteFile(request.InputFile, nil, 0o600))
	case "invalid options":
		request.Options = Settings{testFlag: nil}
	case "no prefix":
		environment := fixtureEnvironment(t, runner)
		environment.prefix = ""
		runner.Environment = environment
	case "negative timeout":
		runner.CleanupTimeout = -time.Second
	}
}

func TestRunnerSharedStreamWriter(t *testing.T) {
	t.Parallel()
	runner, request, _ := runnerFixture(t, false)

	var output bytes.Buffer

	runner.Stdout, runner.Stderr = &output, &output
	{
		_, err := runner.Protect(context.Background(), request)
		requireTestf(t, err == nil, "%v", err)
	}

	requireTestf(t, strings.Contains(output.String(), "protection progress") &&
		strings.Contains(output.String(), "protection diagnostic"), "lost shared output stream")
}

func TestRunnerStartupFailure(t *testing.T) {
	t.Parallel()
	runner, request, _ := runnerFixture(t, false)
	runner.Path = filepath.Join(filepath.Dir(request.InputFile), "missing.exe")
	{
		data, err := runner.Protect(
			context.Background(),
			request,
		)
		requireTestf(t, data == nil &&
			errors.Is(err, os.ErrNotExist), "startup error lost: %q %v", data, err)
	}
}

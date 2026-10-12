//nolint:testpackage // White-box tests exercise internal parser and execution behavior.
package themida

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fixtureEnv is a stand-in [Environment] for tests: a minimal Wine-like layer
// that drives the re-exec fixture tool as winepath and wineserver and wraps the
// protector with it, so the Runner's environment seam is exercised end to end
// without depending on a real implementation. It mirrors the contract that
// packages such as go-wine satisfy.
type fixtureEnv struct {
	tool   string
	prefix string
	exec   CommandExecutor
}

func (e fixtureEnv) Setup(
	_ context.Context,
	base []string,
) ([]string, func(context.Context) error, error) {
	if e.prefix == "" {
		return nil, nil, fmt.Errorf("%w: a dedicated prefix is required", ErrInvalidProtection)
	}

	env := fixtureWineEnv(base, e.prefix)
	teardown := func(ctx context.Context) error {
		kill := newCommand(ctx, e.tool, "-k")
		kill.Env = env
		{
			err := e.runner()(ctx, "Wine server shutdown", kill, nil, 1)
			if err != nil {
				return err
			}
		}

		wait := newCommand(ctx, e.tool, "-w")
		wait.Env = env

		return e.runner()(ctx, "Wine server exit", wait, nil)
	}

	return env, teardown, nil
}

func (e fixtureEnv) TranslatePath(ctx context.Context, path string, env []string) (string, error) {
	cmd := newCommand(ctx, e.tool, "-w", path)
	cmd.Env = env

	var out strings.Builder
	{
		err := e.runner()(ctx, "winepath", cmd, &out)
		if err != nil {
			return "", err
		}
	}

	result := strings.TrimRight(out.String(), "\r\n")
	if result == "" || strings.ContainsAny(result, "\r\n\x00") {
		return "", fmt.Errorf("%w: winepath returned an invalid path", ErrInvalidProtection)
	}

	return result, nil
}

func (e fixtureEnv) Command(protector string) (string, []string, error) {
	return e.tool, []string{protector}, nil
}

func (e fixtureEnv) runner() CommandExecutor {
	if e.exec != nil {
		return e.exec
	}

	return (&Runner{}).execute
}

// fixtureWineEnv reproduces just enough of a Wine environment for the runner
// assertions: it selects the prefix, appends the debugger override, and defaults
// the debug channel, preserving any other variables.
func fixtureWineEnv(base []string, prefix string) []string {
	values := make(map[string]string)

	for _, entry := range base {
		if key, value, ok := strings.Cut(entry, "="); ok {
			values[key] = value
		}
	}

	overrides := strings.TrimRight(values["WINEDLLOVERRIDES"], ";")
	if overrides != "" {
		overrides += ";"
	}

	values["WINEDLLOVERRIDES"] = overrides + "winedbg.exe="
	if values["WINEDEBUG"] == "" {
		values["WINEDEBUG"] = "-all,err+all"
	}

	values["WINEPREFIX"] = prefix

	env := make([]string, 0, len(values))
	for key, value := range values {
		env = append(env, key+"="+value)
	}

	return env
}

func TestMain(m *testing.M) {
	if os.Getenv("GO_THEMIDA_TEST_TOOL") == "1" {
		os.Exit(runRunnerTestTool())
	}

	os.Exit(m.Run())
}

type runnerEvent struct {
	Args []string `json:"args"`
	Dir  string   `json:"dir"`
	Env  []string `json:"env"`
}

func runRunnerTestTool() int {
	args := os.Args[1:]
	{
		err := recordTestToolEvent(args)
		if err != nil {
			return 10
		}
	}

	if len(args) == 1 {
		return runTestWineServer(args[0])
	}

	mode := os.Getenv("GO_THEMIDA_TEST_MODE")
	if len(args) == 2 && args[0] == "-w" {
		return runTestWinePath(args[1], mode)
	}

	if len(args) == 7 {
		args = args[1:]
	}

	return runTestProtection(args, mode)
}

func recordTestToolEvent(args []string) error {
	events := os.Getenv("GO_THEMIDA_TEST_EVENTS")
	if events == "" {
		return nil
	}
	// #nosec G304 G703 -- Only the test fixture chooses this private event file.
	file, err := os.OpenFile(events, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open fixture events: %w", err)
	}

	dir, err := os.Getwd()
	if err != nil {
		return errors.Join(fmt.Errorf("get fixture directory: %w", err), file.Close())
	}

	return errors.Join(
		json.NewEncoder(file).Encode(runnerEvent{Args: args, Dir: dir, Env: os.Environ()}),
		file.Close(),
	)
}

func runTestWineServer(arg string) int {
	if arg != "-k" && arg != "-w" {
		return 12
	}

	if os.Getenv("GO_THEMIDA_TEST_SERVER_FAIL") == arg {
		_, _ = fmt.Fprintln(os.Stderr, "server cleanup failure")

		return 5
	}

	if arg == "-k" && os.Getenv("GO_THEMIDA_TEST_SERVER_GONE") == "1" {
		return 1
	}

	gate := os.Getenv("GO_THEMIDA_TEST_CLEANUP_GATE")
	if arg == "-w" && gate != "" {
		{
			err := waitTestCleanupGate(gate)
			if err != nil {
				return 10
			}
		}
	}

	return 0
}

func waitTestCleanupGate(gate string) error {
	// #nosec G703 -- The fixture creates the gate inside its private temporary directory.
	err := os.WriteFile(gate+".ready", []byte("ready"), 0o600)
	if err != nil {
		return fmt.Errorf("write fixture gate: %w", err)
	}

	for {
		{
			// #nosec G703 -- The fixture owns the path and releases this gate or kills the process.
			_, err := os.Stat(gate)
			if err == nil {
				return nil
			}
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func runTestWinePath(path, mode string) int {
	_, _ = fmt.Fprintln(os.Stderr, "path diagnostic on stderr")

	switch mode {
	case testPathFailure:
		_, _ = fmt.Fprintln(os.Stderr, "path conversion failure")

		return 4
	case "path-empty":
		return 0
	case "path-multiline":
		_, err := fmt.Fprintln(os.Stdout, "Z:\\one\nZ:\\two")
		if err != nil {
			return 10
		}

		return 0
	default:
		_, err := fmt.Fprintln(os.Stdout, testWinePath(path))
		if err != nil {
			return 10
		}

		return 0
	}
}

func runTestProtection(args []string, mode string) int {
	if len(args) != 6 || args[0] != protectFlag || args[2] != inputFlag || args[4] != outputFlag {
		return 12
	}
	{
		err := captureTestProject(args[1])
		if err != nil {
			return 11
		}
	}
	{
		_, err := fmt.Fprintln(os.Stdout, "protection progress")
		if err != nil {
			return 10
		}
	}
	{
		_, err := fmt.Fprintln(os.Stderr, "protection diagnostic")
		if err != nil {
			return 10
		}
	}

	if mode == testCommandFailure {
		_, _ = fmt.Fprintln(os.Stderr, "tool failure details")

		return 7
	}
	{
		err := pauseTestProtection()
		if err != nil {
			return 10
		}
	}

	return writeTestProtection(args[3], args[5], mode)
}

func captureTestProject(path string) error {
	capture := os.Getenv("GO_THEMIDA_TEST_PROJECT_CAPTURE")
	if capture == "" {
		return nil
	}
	// #nosec G304 G703 -- Both files are selected by the test fixture in its temporary directory.
	data, err := os.ReadFile(testLocalPath(path))
	if err != nil {
		return fmt.Errorf("read fixture project: %w", err)
	}
	{
		// #nosec G703 -- The fixture controls the capture destination.
		err := os.WriteFile(capture, data, 0o600)
		if err != nil {
			return fmt.Errorf("capture fixture project: %w", err)
		}
	}

	return nil
}

func pauseTestProtection() error {
	ready := os.Getenv("GO_THEMIDA_TEST_PROTECT_READY")
	if ready == "" {
		return nil
	}
	{
		// #nosec G703 -- The fixture chooses this private readiness file.
		err := os.WriteFile(ready, []byte("ready"), 0o600)
		if err != nil {
			return fmt.Errorf("write fixture readiness: %w", err)
		}
	}

	time.Sleep(time.Minute)

	return nil
}

func writeTestProtection(input, output, mode string) int {
	if mode == testNoOutput {
		return 0
	}
	// #nosec G304 G703 -- The fixture explicitly supplies the mock command's input file.
	data, err := os.ReadFile(testLocalPath(input))
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)

		return 9
	}

	if mode == testEmptyOutput {
		data = nil
	} else {
		data = append([]byte("protected:"), data...)
	}
	{
		// #nosec G703 -- The fixture explicitly supplies the mock command's output file.
		err := os.WriteFile(testLocalPath(output), data, 0o600)
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)

			return 9
		}
	}

	return 0
}

func testWinePath(path string) string {
	return "Z:\\" + strings.ReplaceAll(filepath.ToSlash(path), "/", "\\")
}

func testLocalPath(path string) string {
	if local, found := strings.CutPrefix(path, "Z:\\"); found {
		return hostPath(local)
	}

	return path
}

func runnerFixture(t *testing.T, useWine bool) (*Runner, Protection, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "project with spaces & punctuation!")
	requireNoError(t, os.Mkdir(dir, 0o700))

	executable, err := os.Executable()
	requireTestf(t, err == nil, "%v", err)

	installed := filepath.Join(dir, testThemida64)
	// #nosec G304 -- Read the running test binary used as the mock executable.
	data, err := os.ReadFile(executable)
	requireTestf(t, err == nil, "%v", err)
	// #nosec G306 G703 -- Copy the test executable into a private directory with owner execute permission.
	requireNoError(t, os.WriteFile(installed, data, 0o700))
	requireNoError(t, os.WriteFile(
		filepath.Join(dir, licenseFilename),
		[]byte("fixture license"),
		0o600,
	))

	input := filepath.Join(dir, "input file.exe")
	requireNoError(t, os.WriteFile(input, []byte("binary"), 0o600))

	project := filepath.Join(dir, "base project.tmd")
	requireNoError(t, os.WriteFile(
		project,
		[]byte(
			"[SECURE_ENGINE]\r\nSOFTWARE_NAME=Base\r\nKEEP=original\r\nOUTPUT_FILE_NAME=old.exe\r\n",
		),
		0o600,
	))

	events := filepath.Join(dir, "events.jsonl")

	runner := &Runner{Path: dir, Env: os.Environ()}
	setRunnerEnv(runner, "GO_THEMIDA_TEST_TOOL", "1")
	setRunnerEnv(runner, "GO_THEMIDA_TEST_EVENTS", events)
	setRunnerEnv(runner, "GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))

	if useWine {
		runner.Environment = fixtureEnv{
			tool:   executable,
			prefix: filepath.Join(dir, "dedicated prefix"),
			exec:   nil,
		}
	}

	return runner, Protection{
		ProjectFile: project,
		InputFile:   input,
		OutputFile:  filepath.Join(dir, "protected file.exe"),
	}, events
}

// fixtureEnvironment returns the fixture's environment, failing the test if the
// runner was built without one.
func fixtureEnvironment(t *testing.T, runner *Runner) fixtureEnv {
	t.Helper()

	environment, ok := runner.Environment.(fixtureEnv)
	requireTestf(t, ok, "runner has no fixture environment")

	return environment
}

func runnerEvents(t *testing.T, path string) []runnerEvent {
	t.Helper()

	// #nosec G304 -- The fixture supplies this private event log.
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}

	requireTestf(t, err == nil, "%v", err)

	defer func() {
		err := file.Close()
		if err != nil {
			t.Error(err)
		}
	}()

	decoder := json.NewDecoder(file)

	var events []runnerEvent

	for {
		var event runnerEvent

		err := decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			break
		}

		requireTestf(t, err == nil, "%v", err)

		events = append(events, event)
	}

	return events
}

func eventEnv(event runnerEvent, key string) string {
	for _, entry := range event.Env {
		if name, value, ok := strings.Cut(entry, "="); ok && name == key {
			return value
		}
	}

	return ""
}

func assertRunnerStopped(t *testing.T, events []runnerEvent) {
	t.Helper()
	requireTestf(
		t,
		len(events) >= 2 && reflect.DeepEqual(events[len(events)-2].Args, []string{"-k"}) &&
			reflect.DeepEqual(events[len(events)-1].Args, []string{"-w"}),
		"missing server shutdown/wait",
	)
}

func assertNoRunnerTemps(t *testing.T, directory string) {
	t.Helper()

	files, err := os.ReadDir(directory)
	requireTestf(t, err == nil, "%v", err)

	for _, file := range files {
		requireTestf(
			t, !strings.HasPrefix(file.Name(), ".themida-"), "temporary project left behind: %s",
			file.Name(),
		)
	}
}

func waitRunnerReady(t *testing.T, path string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for {
		{
			_, err := os.Stat(path)
			if err == nil {
				return
			}
		}

		requireTestf(t, !time.Now().After(deadline), "subprocess not ready: %s", path)

		time.Sleep(10 * time.Millisecond)
	}
}

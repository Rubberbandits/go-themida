//nolint:testpackage // White-box tests exercise parser state, temporary files, and subprocess helpers.
package themida

import (
	"slices"
	"strings"
	"testing"
)

const (
	testThemida64       = "Themida64.exe"
	testProtectedBinary = "protected:binary"
	testCommandFailure  = "command-fail"
	testPathFailure     = "path-fail"
	testNoOutput        = "no-output"
	testEmptyOutput     = "empty-output"
	testStartup         = "startup"
	testMissing         = "missing"
	testDirectory       = "directory"
	testMemory          = "memory"
	testOptions         = "options"
	testFlag            = "FLAG"
)

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

func requireTestf(t *testing.T, condition bool, format string, args ...any) {
	t.Helper()

	if !condition {
		t.Fatalf(format, args...)
	}
}

func setRunnerEnv(runner *Runner, key, value string) {
	prefix := key + "="
	runner.Env = slices.DeleteFunc(
		runner.Env,
		func(entry string) bool { return strings.HasPrefix(entry, prefix) },
	)
	runner.Env = append(runner.Env, prefix+value)
}

func requireProtectedBinary(t *testing.T, binary []byte, err error) {
	t.Helper()
	requireNoError(t, err)
	requireTestf(
		t,
		string(binary) == testProtectedBinary,
		"unexpected protected binary: %q",
		binary,
	)
}

func requireProtectionFailure(t *testing.T, binary []byte, err error) {
	t.Helper()
	requireTestf(t, binary == nil, "failed protection returned a binary: %q", binary)
	requireTestf(t, err != nil, "expected failed protection")
}

func requireErrorContains(t *testing.T, err error, fragments ...string) {
	t.Helper()
	requireTestf(t, err != nil, "expected an error")

	for _, fragment := range fragments {
		requireTestf(
			t,
			strings.Contains(err.Error(), fragment),
			"error %q omitted %q",
			err,
			fragment,
		)
	}
}

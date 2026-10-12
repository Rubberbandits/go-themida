//nolint:testpackage // White-box tests exercise internal parser and execution behavior.
package themida

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRunnerArchitecture(t *testing.T) {
	t.Parallel()

	for _, useWine := range []bool{false, true} {
		for _, testCase := range []architectureCase{
			{"default", 0, testThemida64, false},
			{"32-bit", Arch32, "Themida.exe", false},
			{"64-bit", Arch64, testThemida64, false},
			{"explicit path", Arch32, testThemida64, true},
		} {
			mode := "native/"
			if useWine {
				mode = "wine/"
			}

			t.Run(
				mode+testCase.name,
				func(t *testing.T) { t.Parallel(); checkArchitectureCase(t, useWine, testCase) },
			)
		}
	}
}

func TestRunnerRejectsUnsupportedArchitecture(t *testing.T) {
	t.Parallel()
	runner, request, events := runnerFixture(t, true)
	runner.Arch = 16
	runner.License = []byte("temporary fixture license")
	{
		data, err := runner.Protect(context.Background(), request)
		requireTestf(t, data == nil && err != nil, "unsupported architecture succeeded: %v", err)
	}

	requireTestf(t, len(runnerEvents(t, events)) == 0, "invalid architecture launched a process")

	path := filepath.Join(runner.Path, licenseFilename)
	{
		// #nosec G304 -- Check the license fixture after invalid architecture selection.
		data, err := os.ReadFile(path)
		requireTestf(
			t,
			err == nil && string(data) == "fixture license",
			"invalid architecture modified the license",
		)
	}

	assertNoRunnerTemps(t, runner.Path)
}

func checkArchitectureCase(t *testing.T, useWine bool, testCase architectureCase) {
	t.Helper()
	runner, request, _ := runnerFixture(t, useWine)
	installation := runner.Path

	executable := filepath.Join(installation, testCase.executable)
	if testCase.executable != testThemida64 {
		requireNoError(t, os.Rename(
			filepath.Join(installation, testThemida64),
			executable,
		))
	}

	runner.Arch = testCase.arch
	if testCase.explicit {
		runner.Path = executable
	}

	called := false
	runner.Execute = func(ctx context.Context, step string, cmd *exec.Cmd, capture io.Writer, allowed ...int) error {
		if step == "Themida protection" {
			called = true

			selected := cmd.Path
			if useWine {
				selected = cmd.Args[1]
			}

			requireTestf(
				t,
				selected == executable && cmd.Dir == installation,
				"wrong executable or directory: %s, %s",
				selected,
				cmd.Dir,
			)
		}

		return (&Runner{}).execute(ctx, step, cmd, capture, allowed...)
	}
	{
		data, err := runner.Protect(
			context.Background(),
			request,
		)
		requireTestf(t, err == nil &&
			string(data) == testProtectedBinary, "protection failed: %v", err)
	}

	requireTestf(t, called, "protection was not executed")
}

type architectureCase struct {
	name       string
	arch       Arch
	executable string
	explicit   bool
}

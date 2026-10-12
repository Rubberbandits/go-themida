package themida

import "context"

// Environment customizes how the Themida protector is launched on the host, so
// the same project and [Runner] work natively on Windows or through a
// compatibility layer elsewhere. A nil [Runner.Environment] runs the protector
// directly.
//
// Implementations are supplied by the caller; the go-wine package's Environment
// is one, running the protector through Wine. The interface is deliberately
// defined in terms of standard-library types so an implementation needs no
// dependency on this package.
type Environment interface {
	// Setup prepares the environment for one protection run. base is the
	// inherited process environment. It returns the environment for the
	// subprocess and a teardown run after the protector exits, including on
	// failure and cancellation. The teardown is given an independent,
	// timeout-bounded context so it can finish after the run's context is
	// cancelled.
	Setup(
		ctx context.Context,
		base []string,
	) (env []string, teardown func(context.Context) error, err error)
	// TranslatePath converts a host path to the form the protector receives on
	// its command line (for example a Windows path under Wine), using env from
	// Setup. A native layer returns path unchanged.
	TranslatePath(ctx context.Context, path string, env []string) (string, error)
	// Command returns the executable to launch and the arguments to place before
	// the protector's own arguments. A native layer returns the protector path
	// and no prefix; a loader-based one returns the loader and the protector
	// path as the first prefix argument.
	Command(protector string) (executable string, prefix []string, err error)
}

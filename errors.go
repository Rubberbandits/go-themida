package themida

import "errors"

// These sentinel errors classify the package's validation failures. Every error
// this package returns for invalid input wraps one of them, so callers can match
// a category with errors.Is rather than inspecting error text.
var (
	// ErrInvalidProject indicates a malformed .tmd project or an invalid setting
	// name or value (for example a bad header, a non-serializable value, a
	// corrupt indexed collection, or a duplicate macro).
	ErrInvalidProject = errors.New("invalid Themida project")
	// ErrInvalidProtection indicates an invalid protection request or result,
	// such as a missing executable path, aliased input/output paths, an
	// unsupported architecture, or missing/empty protected output.
	ErrInvalidProtection = errors.New("invalid Themida protection")
	// ErrInvalidLicense indicates an invalid supplied license or license file,
	// such as a non-nil empty license or a required license that is absent or
	// not a regular file.
	ErrInvalidLicense = errors.New("invalid Themida license")
)

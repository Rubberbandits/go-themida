package themida

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// hostPath converts a possibly Windows-style path (with backslash separators)
// to the host's native separators, so project paths authored on Windows resolve
// correctly when protection is driven from another OS.
func hostPath(path string) string { return filepath.FromSlash(strings.ReplaceAll(path, "\\", "/")) }

// absolutePath resolves path against the current working directory after
// normalizing its separators, so protection paths are anchored before the runner
// changes into the Themida installation directory.
func absolutePath(path string) (string, error) {
	resolved, err := filepath.Abs(hostPath(path))
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}

	return resolved, nil
}

// sameFile reports whether two paths refer to the same file. It compares the
// strings directly (case-insensitively on Windows) and, failing that, consults
// the filesystem via os.SameFile so hard links and symlinks are caught. It is
// used to reject aliasing between the input, output, project, and license files.
func sameFile(first, second string) bool {
	if first == second || (runtime.GOOS == "windows" && strings.EqualFold(first, second)) {
		return true
	}

	firstInfo, firstErr := os.Stat(first)
	secondInfo, secondErr := os.Stat(second)

	return firstErr == nil && secondErr == nil && os.SameFile(firstInfo, secondInfo)
}

// removeFile deletes path, treating an already-absent file as success so cleanup
// is idempotent.
func removeFile(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) || err == nil {
		return nil
	}

	return fmt.Errorf("remove %q: %w", path, err)
}

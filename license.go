package themida

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// licenseBackup records where a temporary license was installed and, if an
// original license was displaced, where it was moved, so cleanup can remove the
// temporary file and restore the original.
type licenseBackup struct {
	path   string
	backup string
}

// licenseFilename is the fixed name of the Themida license file, which must sit
// beside the protector executable.
const licenseFilename = "TMLicenseA1.dat"

// prepareLicense makes the license available beside the protector for one
// protection run and returns a cleanup function to undo it.
//
// With a nil license it installs nothing and only requires that a regular
// license file already exist, returning a no-op cleanup. An empty (non-nil)
// license is rejected. Otherwise it stages the supplied bytes to a temporary
// file, moves any existing license aside to a unique backup, and renames the
// staged file into place; the returned cleanup removes the temporary license and
// restores the backup. The caller must serialize runs that share one
// installation, since the license file name is fixed.
func prepareLicense(installation string, license []byte) (func() error, error) {
	path := filepath.Join(installation, licenseFilename)
	if license == nil {
		return noCleanup, requireExistingLicense(path)
	}

	if len(license) == 0 {
		return nil, fmt.Errorf("%w: supplied license must not be empty", ErrInvalidLicense)
	}

	exists, err := replaceableLicense(path)
	if err != nil {
		return nil, err
	}
	// Stage the complete replacement before moving the original out of the way.
	staged, err := stageLicense(installation, license)
	if err != nil {
		return nil, err
	}

	state := licenseBackup{path: path, backup: ""}
	if exists {
		state.backup, err = backupLicense(path)
		if err != nil {
			return nil, errors.Join(err, removeFile(staged))
		}
	}
	{
		err := os.Rename(staged, path)
		if err != nil {
			return nil, errors.Join(
				fmt.Errorf("install Themida license: %w", err),
				state.restore(),
				removeFile(staged),
			)
		}
	}

	return state.cleanup, nil
}

// requireExistingLicense verifies that a usable license already exists at path,
// following symlinks but leaving the file untouched. It is used when the runner
// is configured with no license of its own.
func requireExistingLicense(path string) error {
	// Follow links when using an existing license, but leave the file untouched.
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("themida license required at %s: %w", path, err)
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: license must be a regular file: %s", ErrInvalidLicense, path)
	}

	return nil
}

// replaceableLicense reports whether an existing license at path may be safely
// displaced. A missing file is replaceable (false, nil); a present file must be
// a regular file or a symlink, not some other special file.
func replaceableLicense(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("check Themida license: %w", err)
	}

	if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		return false, fmt.Errorf("%w: license must be a regular file: %s", ErrInvalidLicense, path)
	}

	return true, nil
}

// stageLicense writes the license bytes to a fresh temporary file in the
// installation directory and returns its path. Staging in the same directory
// keeps the later install a same-filesystem rename, which is atomic. On any
// write failure the temporary file is removed.
func stageLicense(installation string, license []byte) (string, error) {
	file, err := os.CreateTemp(installation, ".themida-license-*")
	if err != nil {
		return "", fmt.Errorf("stage Themida license: %w", err)
	}

	staged := file.Name()

	_, writeErr := file.Write(license)
	{
		err := errors.Join(writeErr, file.Close())
		if err != nil {
			return "", errors.Join(fmt.Errorf("write Themida license: %w", err), removeFile(staged))
		}
	}

	return staged, nil
}

// backupLicense moves the existing license to a unique backup path in the same
// directory and returns that path. It reserves a unique name, then removes the
// placeholder so the rename succeeds on Windows, where Rename will not overwrite
// an existing file.
func backupLicense(path string) (string, error) {
	// Reserve a unique name, then remove the placeholder for Windows Rename.
	reservation, err := os.CreateTemp(filepath.Dir(path), ".themida-license-backup-*")
	if err != nil {
		return "", fmt.Errorf("reserve Themida license backup: %w", err)
	}

	backup := reservation.Name()
	{
		err := errors.Join(reservation.Close(), removeFile(backup))
		if err != nil {
			return "", fmt.Errorf("prepare Themida license backup: %w", err)
		}
	}
	{
		err := os.Rename(path, backup)
		if err != nil {
			return "", fmt.Errorf("back up Themida license: %w", err)
		}
	}

	return backup, nil
}

// restore moves a backed-up original license back into place. It is a no-op when
// no original was displaced. If the restore fails, the error reports where the
// original is retained so it can be recovered manually.
func (l licenseBackup) restore() error {
	if l.backup == "" {
		return nil
	}

	err := os.Rename(l.backup, l.path)
	if err != nil {
		return fmt.Errorf("restore Themida license (original retained at %s): %w", l.backup, err)
	}

	return nil
}

// cleanup removes the temporary license and then restores any original. If
// removing the temporary license fails, the error reports where a displaced
// original is retained so it can be recovered manually.
func (l licenseBackup) cleanup() error {
	err := removeFile(l.path)
	if err != nil {
		if l.backup != "" {
			return fmt.Errorf(
				"remove temporary Themida license (original retained at %s): %w",
				l.backup,
				err,
			)
		}

		return fmt.Errorf("remove temporary Themida license: %w", err)
	}

	return l.restore()
}

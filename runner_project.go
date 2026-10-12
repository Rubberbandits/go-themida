package themida

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// prepareProtectionProject resolves the effective project file for a protection
// request and returns its path plus a cleanup function.
//
// When the request names a project file and applies no overrides, that file is
// used in place and cleanup is a no-op. Otherwise the project (loaded from the
// file, cloned from an in-memory project, or started empty for an options-only
// request) is overridden and written to a temporary .tmd file whose cleanup
// removes it. The temporary file is placed beside the source project when there
// is one, else in the request's ProjectDirectory (defaulting to the current
// directory), so project-relative resource paths still resolve.
func prepareProtectionProject(request Protection) (string, func() error, error) {
	{
		err := validateProjectSource(request)
		if err != nil {
			return "", nil, err
		}
	}

	project, source, err := loadProtectionProject(request)
	if err != nil {
		return "", nil, err
	}
	{
		err := project.Override(request.Options)
		if err != nil {
			return "", nil, fmt.Errorf("override Themida project: %w", err)
		}
	}

	if source != "" && request.Options == nil {
		return source, noCleanup, nil
	}

	directory := hostPath(request.ProjectDirectory)
	if source != "" {
		directory = filepath.Dir(source)
	}

	if directory == "" {
		directory = "."
	}

	return writeProtectionProject(project, directory)
}

// validateProjectSource enforces the request's project-source rules: a project
// file and an in-memory project are mutually exclusive, and at least one of a
// project file, an in-memory project, or options must be supplied.
func validateProjectSource(request Protection) error {
	if request.ProjectFile != "" && request.Project != nil {
		return fmt.Errorf(
			"%w: specify either a project file or an in-memory project",
			ErrInvalidProtection,
		)
	}

	if request.ProjectFile == "" && request.Project == nil && request.Options == nil {
		return fmt.Errorf(
			"%w: project file, in-memory project, or options are required",
			ErrInvalidProtection,
		)
	}

	return nil
}

// loadProtectionProject returns the project to protect and the absolute path of
// its source file, if any. A file-based request is read and parsed from disk; an
// in-memory project is cloned through serialization so overrides never mutate
// the caller's value; an options-only request yields an empty project with no
// source path.
func loadProtectionProject(request Protection) (*Project, string, error) {
	project := &Project{}

	if request.ProjectFile != "" {
		source, err := absolutePath(request.ProjectFile)
		if err != nil {
			return nil, "", err
		}
		// #nosec G304 -- Project files are explicitly selected by the caller.
		data, err := os.ReadFile(source)
		if err != nil {
			return nil, "", fmt.Errorf("read Themida project: %w", err)
		}
		{
			err := project.Unmarshal(data)
			if err != nil {
				return nil, "", err
			}
		}

		return project, source, nil
	}

	if request.Project != nil {
		// Clone through serialization so overrides never mutate the caller's project.
		data, err := request.Project.Marshal()
		if err != nil {
			return nil, "", err
		}
		{
			err := project.Unmarshal(data)
			if err != nil {
				return nil, "", err
			}
		}
	}

	return project, "", nil
}

// writeProtectionProject serializes the project to a temporary .tmd file in
// directory and returns its path and a cleanup that removes it. A failure to
// write or close the file removes the partial file before returning.
func writeProtectionProject(project *Project, directory string) (string, func() error, error) {
	directory, err := absolutePath(directory)
	if err != nil {
		return "", nil, err
	}

	data, err := project.Marshal()
	if err != nil {
		return "", nil, err
	}

	file, err := os.CreateTemp(directory, ".themida-*.tmd")
	if err != nil {
		return "", nil, fmt.Errorf("create Themida project: %w", err)
	}

	closeProject := func() error { return removeFile(file.Name()) }
	_, writeErr := file.Write(data)

	closeErr := file.Close()
	{
		err := errors.Join(writeErr, closeErr)
		if err != nil {
			return "", nil, errors.Join(
				fmt.Errorf("write Themida project: %w", err),
				closeProject(),
			)
		}
	}

	return file.Name(), closeProject, nil
}

// noCleanup is the cleanup function used when there is nothing to undo.
func noCleanup() error { return nil }

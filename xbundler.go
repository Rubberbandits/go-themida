package themida

import (
	"fmt"
	"strconv"
)

// XBundlerFile is one file embedded by XBundler. XBundler compresses and
// encrypts DLLs and data files inside the protected application so they ship as
// a single executable and cannot be reused by other software.
type XBundlerFile struct {
	// Name is the original file location read at protection time. It accepts a
	// full path or a Themida path constant such as %INPUT_FILE_FOLDER% or
	// %PROJECT_FOLDER%.
	Name string
	// Type is the native extraction-mode value controlling what happens at
	// runtime: kept in memory and never written to disk, or extracted to disk
	// always / only if missing / only if an older or different copy exists.
	Type string
	// VirtualPath is where the file is seen at runtime, relative to a root
	// constant such as %APP_FOLDER%, %WIN_FOLDER%, or %LOCAL_APP_DATA%. It may
	// include subfolders.
	VirtualPath string
}

// XBundlerOptions configures Themida's XBundler, serialized under the
// OPTION_XBUNDLER prefix: the embedded file list together with the runtime
// behavior applied to all of them.
type XBundlerOptions struct {
	// Files lists the embedded files. A nil slice leaves the project's existing
	// file list untouched; a non-nil slice (including an empty one) replaces it.
	Files []XBundlerFile
	// Enabled reports whether XBundler is active for the project.
	Enabled bool `tmd:"IS_ENABLED"`
	// DeleteOnExit removes files that were extracted to disk when the
	// application closes. It has no effect on files kept only in memory.
	DeleteOnExit bool `tmd:"IS_DELETE_ON_EXIT"`
	// HookFindNextFile hooks the FindFirstFile/FindNextFile APIs so embedded
	// files are enumerable by file-listing APIs and shell dialogs, while staying
	// hidden from the user and other applications.
	HookFindNextFile bool `tmd:"HOOK_FIND_NEXT_FILE"`
	// MaximizeSpeed reduces the encryption and virtualization applied to
	// XBundler's own code, improving performance when embedded files are
	// accessed frequently at the cost of protection strength.
	MaximizeSpeed bool `tmd:"MAXIMIZE_SPEED"`
	// ActiveXSupport registers embedded DLLs/OCXs before startup, like running
	// regsvr32. It requires administrator rights on first run.
	ActiveXSupport bool `tmd:"IS_ACTIVEX_SUPPORT"`
	// SupportGetPrivateProfile hooks the Win32 GetPrivateProfile INI-reading
	// APIs. Enable it when bundling .INI files kept only in memory.
	SupportGetPrivateProfile bool `tmd:"IS_SUPPORT_GETPRIVATEPROFILE"`
	// ExceptionSupport lets an embedded DLL raise handled exceptions during
	// startup without conflicting with the protection code's exception handling.
	ExceptionSupport bool `tmd:"EXCEPTION_SUPPORT"`
}

// Unmarshal replaces the bundler settings from the native keys under prefix,
// including the indexed file collection. It returns an error wrapping
// [ErrInvalidProject] if the file count is invalid or an indexed NAME, TYPE, or
// VIRTUAL_PATH entry is missing.
func (o *XBundlerOptions) Unmarshal(prefix string, variables map[string]string) error {
	*o = XBundlerOptions{}
	{
		err := decodeFields(prefix, o, variables)
		if err != nil {
			return err
		}
	}

	count, exists, err := entryCount(prefix+"_NUMBER_FILES", variables)
	if err != nil || !exists {
		return err
	}

	o.Files = make([]XBundlerFile, count)
	for index := range o.Files {
		file := &o.Files[index]
		for _, item := range []struct {
			tag    string
			target *string
		}{
			{"NAME", &file.Name}, {"TYPE", &file.Type}, {"VIRTUAL_PATH", &file.VirtualPath},
		} {
			key := fmt.Sprintf("%s_FILE_%s_AT_%d", prefix, item.tag, index)

			value, exists := variables[key]
			if !exists {
				return fmt.Errorf("%w: missing Themida setting %s", ErrInvalidProject, key)
			}

			*item.target = value
		}
	}

	return nil
}

// Marshal writes the bundler settings and files to the native keys under prefix,
// rewriting NUMBER_FILES and the indexed NAME/TYPE/VIRTUAL_PATH entries to match
// Files.
func (o *XBundlerOptions) Marshal(prefix string, variables map[string]string) {
	encodeFields(prefix, o, variables)

	if o.Files == nil {
		return
	}

	variables[prefix+"_NUMBER_FILES"] = strconv.Itoa(len(o.Files))
	for index, file := range o.Files {
		variables[fmt.Sprintf("%s_FILE_NAME_AT_%d", prefix, index)] = file.Name
		variables[fmt.Sprintf("%s_FILE_TYPE_AT_%d", prefix, index)] = file.Type
		variables[fmt.Sprintf("%s_FILE_VIRTUAL_PATH_AT_%d", prefix, index)] = file.VirtualPath
	}
}

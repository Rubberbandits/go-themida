package themida

import (
	"bytes"
	"fmt"
	"maps"
	"sort"
	"strings"
)

// Options groups the typed protection settings this package models, serialized
// under the OPTION prefix. Settings outside these groups remain reachable
// through the project's untyped accessors.
type Options struct {
	// Macro holds the protection-macro settings (OPTION_MACROS).
	Macro MacroOptions `tmd:"MACROS"`
	// XBundler holds the file-bundling settings (OPTION_XBUNDLER).
	XBundler XBundlerOptions `tmd:"XBUNDLER"`
}

// Project models a Themida .tmd project. The modeled settings are exposed as
// typed fields; every other setting, every comment, and the original key order
// are retained so a parsed project round-trips with minimal diffs.
//
// The zero value is a usable, empty project: it can be populated with typed
// fields or [Project.Override] and then serialized with [Project.Marshal]
// without a base file. A Project is not safe for concurrent modification.
type Project struct {
	// SoftwareName is the display name of the protected application
	// (SOFTWARE_NAME).
	SoftwareName string `tmd:"SOFTWARE_NAME"`
	// InputFile is the project's recorded input binary path (INPUT_FILE_NAME).
	// [Runner] takes the input path from the protection request instead.
	InputFile string `tmd:"INPUT_FILE_NAME"`
	// OutputFile is the project's recorded output binary path
	// (OUTPUT_FILE_NAME). [Runner] takes the output path from the protection
	// request instead.
	OutputFile string `tmd:"OUTPUT_FILE_NAME"`
	// Options holds the typed protection settings (OPTION).
	Options Options `tmd:"OPTION"`
	// Messages holds the customized dialogs (MSG_ID).
	Messages Messages `tmd:"MSG_ID"`
	// variables holds every native key read from the source, including unmodeled
	// settings, so they survive a round trip.
	variables map[string]string
	// baseline is the serialized typed view captured after the last successful
	// decode, used to detect which typed settings changed.
	baseline map[string]string
	// lines is the source body (after the header) with comments and order
	// preserved for re-emission.
	lines []string
	// bom records whether the source began with a UTF-8 byte order mark, so
	// Marshal can reproduce it.
	bom bool
}

// Unmarshal replaces the project from the bytes of a .tmd file. On any failure
// the receiver is left unchanged. The data must begin (after an optional UTF-8
// BOM) with the [SECURE_ENGINE] header; both CRLF and LF line endings are
// accepted, and lines beginning with ';' or '#' are treated as comments.
func (p *Project) Unmarshal(data []byte) error {
	bom := bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if lines[0] != "[SECURE_ENGINE]" {
		return fmt.Errorf("%w: expected [SECURE_ENGINE] header", ErrInvalidProject)
	}

	variables := make(map[string]string)

	for _, line := range lines[1:] {
		if key, value, ok := projectEntry(line); ok {
			variables[key] = value
		}
	}

	project := Project{variables: variables, lines: lines[1:], bom: bom}

	err := decodeFields("", &project, variables)
	if err != nil {
		return err
	}

	project.baseline = project.typedValues()
	*p = project

	return nil
}

// projectEntry splits one source line into a trimmed key and its raw value,
// reporting false for comment lines and lines without a '=' separator.
func projectEntry(line string) (string, string, bool) {
	if strings.HasPrefix(strings.TrimSpace(line), ";") ||
		strings.HasPrefix(strings.TrimSpace(line), "#") {
		return "", "", false
	}

	key, value, ok := strings.Cut(line, "=")

	return strings.TrimSpace(key), value, ok
}

// Override applies the supplied settings and re-decodes the typed fields
// atomically: if any value is invalid the project is left unchanged. Explicit
// false, zero, and empty-string values are applied as overrides; omitted keys
// keep their current values.
func (p *Project) Override(options Settings) error {
	overrides, err := options.values()
	if err != nil {
		return err
	}

	values := p.values()
	maps.Copy(values, overrides)

	project := Project{variables: values, lines: p.lines, bom: p.bom}
	{
		err := decodeFields("", &project, values)
		if err != nil {
			return err
		}
	}

	project.baseline = project.typedValues()
	*p = project

	return nil
}

// Get returns a setting's current serialized value and whether it is present,
// resolving typed fields and raw settings alike.
func (p *Project) Get(
	key string,
) (string, bool) {
	value, ok := p.values()[key]

	return value, ok
}

// Set atomically updates one native setting. It is shorthand for a single-entry
// [Project.Override].
func (p *Project) Set(key string, value any) error { return p.Override(Settings{key: value}) }

// Marshal serializes the project back to .tmd bytes, preserving unmodeled
// settings, comments, and the original ordering, and reproducing the source
// BOM. All occurrences of a duplicated key are updated so that a later duplicate
// cannot silently undo an override. It returns an error wrapping
// [ErrInvalidProject] if any current value cannot be serialized safely.
func (p *Project) Marshal() ([]byte, error) {
	values := p.values()

	settings := make(Settings, len(values))
	for key, value := range values {
		settings[key] = value
	}

	err := settings.Validate()
	if err != nil {
		return nil, err
	}

	var out strings.Builder
	if p.bom {
		out.WriteString("\xef\xbb\xbf")
	}

	out.WriteString("[SECURE_ENGINE]\r\n")

	p.writeEntries(&out, values)

	return []byte(out.String()), nil
}

// typedValues serializes just the typed fields to their native keys, the view
// used as the baseline for change detection.
func (p *Project) typedValues() map[string]string {
	values := make(map[string]string)
	encodeFields("", p, values)

	return values
}

// values returns the project's current native settings: the raw variables
// overlaid with the typed fields wherever those changed from the baseline, and
// with baseline keys that the typed fields no longer produce removed. This keeps
// unmodeled settings intact while letting typed edits win.
func (p *Project) values() map[string]string {
	values := make(map[string]string, len(p.variables))
	maps.Copy(values, p.variables)

	baseline := p.baseline
	if baseline == nil {
		baseline = (&Project{}).typedValues()
	}

	current := p.typedValues()
	for key, value := range current {
		if previous, exists := baseline[key]; !exists || previous != value {
			values[key] = value
		}
	}

	for key := range baseline {
		if _, exists := current[key]; !exists {
			delete(values, key)
		}
	}

	return values
}

// writeEntries emits the body: existing lines first, in their original order and
// with comments intact, updating each present key in place; then any new keys in
// sorted order for deterministic output.
func (p *Project) writeEntries(out *strings.Builder, values map[string]string) {
	seen := make(map[string]bool)

	for index, line := range p.lines {
		key, _, entry := projectEntry(line)
		if !entry {
			if line != "" || index < len(p.lines)-1 {
				out.WriteString(line + "\r\n")
			}

			continue
		}

		if value, exists := values[key]; exists {
			out.WriteString(key + "=" + value + "\r\n")
			seen[key] = true
		}
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		if !seen[key] {
			keys = append(keys, key)
		}
	}

	sort.Strings(keys)

	for _, key := range keys {
		out.WriteString(key + "=" + values[key] + "\r\n")
	}
}

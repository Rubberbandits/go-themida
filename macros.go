package themida

import (
	"fmt"
	"sort"
	"strconv"
)

// Macro maps one application function to a Themida protection type.
//
// In Themida, SecureEngine macros mark regions of code to protect. A mapped
// function is the project-level equivalent: the protector applies the chosen
// protection to the named function without the source needing inline VM_START/
// VM_END markers. Themida records these as the MAP_FUNCTION entries of the macro
// options.
type Macro struct {
	// Name is the function's symbol name, as it appears in the input binary's
	// MAP file or exports.
	Name string
	// Type is the native protection-type value that selects how the function is
	// protected, typically the virtual machine applied to it (for example a
	// heavier VM such as TIGER for sensitive code, or a lighter VM for
	// performance-critical code). It is serialized as a 0x%08X hexadecimal
	// value, matching the Themida project format.
	Type uint64
}

// MacroOptions configures Themida's protection macros: the per-function
// protection mapping together with the project-wide string-encryption and
// integrity-check settings that apply to macro-protected code. It is serialized
// under the OPTION_MACROS prefix.
type MacroOptions struct {
	// Functions maps each protected function's name to its Macro. A nil map
	// leaves the project's existing mapping untouched; a non-nil map (including
	// an empty one) replaces it in full.
	Functions map[string]Macro
	// order preserves the function order read from the project so Marshal can
	// re-emit the indexed MAP_FUNCTION entries stably.
	order []string
	// DisableAtRvaNumber is the count of code RVAs at which macro protection is
	// explicitly disabled. In the Themida GUI this corresponds to individually
	// switched-off macro blocks, used to isolate a block that changes program
	// behavior or raises an exception.
	DisableAtRvaNumber uint64 `tmd:"DISABLE_AT_RVA_NUMBER"`
	// EncryptANSIStrings enables "Encrypt ASCII on VM macros": ANSI string
	// literals referenced inside VM macros are moved into encrypted protection
	// code and decrypted only when the code uses them.
	EncryptANSIStrings bool `tmd:"ENCRYPT_ANSI_STRINGS"`
	// EncryptUnicodeStrings enables "Encrypt Unicode on VM macros", the Unicode
	// counterpart of EncryptANSIStrings.
	EncryptUnicodeStrings bool `tmd:"ENCRYPT_UNICODE_STRINGS"`
	// StringReencrypt enables "Re-Encrypt after decryption": a decrypted string
	// is destroyed (re-encrypted) when the VM_END or STR_ENCRYPT_END marker
	// executes. This can break code that reads the string after that marker.
	StringReencrypt bool `tmd:"STRING_REENCRYPT"`
	// IntegrityChecks enables "Perform Protection checks on VM macros": extra
	// runtime integrity checks run before a VM macro executes, to detect partial
	// tampering with protected code.
	IntegrityChecks bool `tmd:"INTEGRITY_CHECKS"`
}

// Unmarshal replaces the macro settings from the native keys under prefix,
// including the scalar options and the indexed MAP_FUNCTION collection. It
// returns an error wrapping [ErrInvalidProject] if the function count is invalid,
// an indexed entry is missing, a type value is unparsable, or a function name is
// duplicated.
func (o *MacroOptions) Unmarshal(prefix string, variables map[string]string) error {
	*o = MacroOptions{}
	{
		err := decodeFields(prefix, o, variables)
		if err != nil {
			return err
		}
	}

	count, exists, err := entryCount(prefix+"_MAP_FUNCTIONS_NUMBER", variables)
	if err != nil || !exists {
		return err
	}

	o.Functions = make(map[string]Macro, count)
	for index := range count {
		nameKey := fmt.Sprintf("%s_MAP_FUNCTION_NAME_AT_%d", prefix, index)
		typeKey := fmt.Sprintf("%s_MAP_FUNCTION_TYPE_AT_%d", prefix, index)

		name, exists := variables[nameKey]
		if !exists {
			return fmt.Errorf("%w: missing Themida setting %s", ErrInvalidProject, nameKey)
		}

		kind, err := parseUint(variables[typeKey])
		if err != nil {
			return fmt.Errorf("themida setting %s: %w", typeKey, err)
		}

		if _, duplicate := o.Functions[name]; duplicate {
			return fmt.Errorf("%w: duplicate Themida macro %q", ErrInvalidProject, name)
		}

		o.Functions[name] = Macro{Name: name, Type: kind}
		o.order = append(o.order, name)
	}

	return nil
}

// Marshal writes the macro settings to the native keys under prefix. Functions
// read from the project keep their original order; functions added afterward are
// appended in sorted order so output stays deterministic. The MAP_FUNCTIONS_NUMBER
// count and the indexed NAME/TYPE entries are rewritten to match Functions, with
// each type emitted as a 0x%08X value.
func (o *MacroOptions) Marshal(prefix string, variables map[string]string) {
	encodeFields(prefix, o, variables)

	if o.Functions == nil {
		return
	}

	variables[prefix+"_MAP_FUNCTIONS_NUMBER"] = strconv.Itoa(len(o.Functions))
	names := make([]string, 0, len(o.Functions))
	seen := make(map[string]bool)

	for _, name := range o.order {
		if _, exists := o.Functions[name]; exists {
			names = append(names, name)
			seen[name] = true
		}
	}

	added := make([]string, 0)

	for name := range o.Functions {
		if !seen[name] {
			added = append(added, name)
		}
	}

	sort.Strings(added)

	names = append(names, added...)
	for index, name := range names {
		macro := o.Functions[name]
		if macro.Name == "" {
			macro.Name = name
		}

		variables[fmt.Sprintf("%s_MAP_FUNCTION_NAME_AT_%d", prefix, index)] = macro.Name
		variables[fmt.Sprintf("%s_MAP_FUNCTION_TYPE_AT_%d", prefix, index)] = fmt.Sprintf(
			"0x%08X",
			macro.Type,
		)
	}
}

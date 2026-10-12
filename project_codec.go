package themida

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// projectFields is implemented by typed settings groups that need custom
// serialization beyond plain scalar and nested-struct fields, such as indexed
// collections. When a struct field's pointer implements it, the codec delegates
// to these methods instead of walking the field reflectively. prefix is the
// field's fully qualified native key prefix, and variables is the flat native
// key/value map being decoded from or encoded into.
type projectFields interface {
	Unmarshal(prefix string, variables map[string]string) error
	Marshal(prefix string, variables map[string]string)
}

// fieldKey joins a key prefix and a field tag into a native Themida key,
// underscore-separated, treating an empty prefix as the top level.
func fieldKey(prefix, tag string) string {
	if prefix == "" {
		return tag
	}

	return prefix + "_" + tag
}

// decodeFields populates target's `tmd`-tagged fields from variables, keying
// each field by its tag under prefix. Fields whose pointer implements
// [projectFields] are delegated to; nested structs recurse; scalars are parsed
// from their native string. Untagged fields are ignored, and a key absent from
// variables leaves its field unchanged. target must be a non-nil pointer to a
// struct.
func decodeFields(prefix string, target any, variables map[string]string) error {
	value := reflect.ValueOf(target).Elem()
	for index := range value.NumField() {
		tag := value.Type().Field(index).Tag.Get("tmd")
		if tag == "" {
			continue
		}

		key, field := fieldKey(prefix, tag), value.Field(index)
		if codec, ok := field.Addr().Interface().(projectFields); ok {
			{
				err := codec.Unmarshal(key, variables)
				if err != nil {
					return fmt.Errorf("decode fields %s: %w", key, err)
				}
			}

			continue
		}

		if field.Kind() == reflect.Struct {
			{
				err := decodeFields(key, field.Addr().Interface(), variables)
				if err != nil {
					return err
				}
			}

			continue
		}

		text, exists := variables[key]
		if exists {
			{
				err := decodeScalarField(field, text)
				if err != nil {
					return fmt.Errorf("themida setting %s: %w", key, err)
				}
			}
		}
	}

	return nil
}

// decodeScalarField assigns a native string to a scalar field. Supported kinds
// are string, bool, and uint64 (which accepts decimal or 0x-prefixed hex via
// [parseUint]); any other kind is rejected with [ErrInvalidProject].
func decodeScalarField(field reflect.Value, text string) error {
	switch field.Kind() {
	case reflect.String:
		field.SetString(text)
	case reflect.Bool:
		parsed, err := strconv.ParseBool(text)
		if err != nil {
			return fmt.Errorf("parse boolean: %w", err)
		}

		field.SetBool(parsed)
	case reflect.Uint64:
		parsed, err := parseUint(text)
		if err != nil {
			return err
		}

		field.SetUint(parsed)
	default:
		return fmt.Errorf("%w: unsupported field kind %s", ErrInvalidProject, field.Kind())
	}

	return nil
}

// encodeFields writes target's `tmd`-tagged fields into variables, mirroring
// [decodeFields]: [projectFields] fields are delegated to, nested structs
// recurse, and string/bool/uint64 scalars are formatted to their native string.
// Fields of any other kind are internal and never serialized. target must be a
// non-nil pointer to a struct.
func encodeFields(prefix string, target any, variables map[string]string) {
	value := reflect.ValueOf(target).Elem()
	for index := range value.NumField() {
		tag := value.Type().Field(index).Tag.Get("tmd")
		if tag == "" {
			continue
		}

		key, field := fieldKey(prefix, tag), value.Field(index)
		if codec, ok := field.Addr().Interface().(projectFields); ok {
			codec.Marshal(key, variables)

			continue
		}

		switch field.Kind() {
		case reflect.Struct:
			encodeFields(key, field.Addr().Interface(), variables)
		case reflect.String:
			variables[key] = field.String()
		case reflect.Bool:
			variables[key] = strconv.FormatBool(field.Bool())
		case reflect.Uint64:
			variables[key] = strconv.FormatUint(field.Uint(), 10)
		default:
			continue // Internal fields never participate in serialization.
		}
	}
}

// parseUint parses an unsigned 64-bit value, auto-detecting base 16 for a 0x or
// 0X prefix and using base 10 otherwise. Themida writes many numeric settings,
// such as macro type values, in hexadecimal.
func parseUint(text string) (uint64, error) {
	base := 10
	if strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X") {
		base = 0
	}

	parsed, err := strconv.ParseUint(text, base, 64)
	if err != nil {
		return 0, fmt.Errorf("parse unsigned integer %q: %w", text, err)
	}

	return parsed, nil
}

// entryCount reads the count that precedes an indexed collection (for example a
// MAP_FUNCTIONS_NUMBER or NUMBER_FILES key). It reports whether the key exists
// and rejects a count that is non-numeric, negative, or larger than the total
// number of settings (which could not have that many entries). A missing key is
// not an error.
func entryCount(key string, variables map[string]string) (int, bool, error) {
	text, exists := variables[key]
	if !exists {
		return 0, false, nil
	}

	count, err := strconv.Atoi(text)
	if err != nil || count < 0 || count > len(variables) {
		return 0, true, fmt.Errorf("%w: invalid count %s=%q", ErrInvalidProject, key, text)
	}

	return count, true, nil
}

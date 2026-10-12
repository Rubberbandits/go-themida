package themida

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Settings is an untyped set of native .tmd settings keyed by their exact
// Themida key names, with string, boolean, or number values. It is the escape
// hatch for settings this package does not model as typed fields, and the input
// to [Project.Override] and [Project.Set].
//
// Keys that are omitted retain their base values, including settings unknown to
// this package. Explicit false, 0, and empty strings are meaningful overrides,
// not omissions. Hexadecimal values are supplied as strings such as
// "0x2C27C00A". Message text uses a literal \n rather than an actual line break.
// Null, arrays, and objects are rejected.
type Settings map[string]any

// UnmarshalJSON decodes JSON into the settings, preserving JSON numbers as
// json.Number so large or hexadecimal-intended integers are not silently
// converted to float64 and reformatted.
func (s *Settings) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	type settings Settings

	err := decoder.Decode((*settings)(s))
	if err != nil {
		return fmt.Errorf("decode Themida settings: %w", err)
	}

	return nil
}

// validateSettingKey rejects key names that would corrupt the single-line
// KEY=VALUE project format or collide with the section header.
func validateSettingKey(key string) error {
	if key == "" || strings.TrimSpace(key) != key || strings.ContainsAny(key, "=\r\n\x00[]") {
		return fmt.Errorf("%w: invalid setting name %q", ErrInvalidProject, key)
	}

	return nil
}

// settingValue serializes a setting value to its native string form, accepting
// strings, booleans, JSON numbers, and the Go numeric kinds. Other types are
// rejected.
func settingValue(value any) (string, error) {
	switch scalar := value.(type) {
	case string:
		return scalar, nil
	case bool:
		return strconv.FormatBool(scalar), nil
	case json.Number:
		text := string(scalar)

		err := validateNumber(text)
		if err != nil {
			return "", err
		}

		return text, nil
	default:
		return numericSettingValue(reflect.ValueOf(value))
	}
}

// numericSettingValue serializes the Go integer and float kinds; any other kind
// is an unsupported value type.
func numericSettingValue(value reflect.Value) (string, error) {
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(value.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(value.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		text := strconv.FormatFloat(value.Float(), 'f', -1, value.Type().Bits())

		err := validateNumber(text)
		if err != nil {
			return "", err
		}

		return text, nil
	default:
		return "", fmt.Errorf("%w: must be a string, boolean, or number", ErrInvalidProject)
	}
}

// validateNumber rejects non-finite or non-numeric text such as NaN, Inf, or a
// leading plus, which json.Number can otherwise carry.
func validateNumber(text string) error {
	if len(text) == 0 || (text[0] != '-' && (text[0] < '0' || text[0] > '9')) ||
		!json.Valid([]byte(text)) {
		return fmt.Errorf("%w: invalid number", ErrInvalidProject)
	}

	return nil
}

// Validate reports whether every key and value can be serialized safely to the
// project format, returning the first offending entry's error (wrapping
// [ErrInvalidProject]) or nil.
func (s Settings) Validate() error {
	_, err := s.values()

	return err
}

// values serializes the settings to native string values, validating each key
// and value and rejecting any value that contains a line break or NUL.
func (s Settings) values() (map[string]string, error) {
	values := make(map[string]string, len(s))
	for key, value := range s {
		{
			err := validateSettingKey(key)
			if err != nil {
				return nil, err
			}
		}

		text, err := settingValue(value)
		if err != nil {
			return nil, fmt.Errorf("themida setting %s: %w", key, err)
		}

		if strings.ContainsAny(text, "\r\n\x00") {
			return nil, fmt.Errorf(
				"%w: setting %s contains a line break or NUL; use literal \\n for message text",
				ErrInvalidProject,
				key,
			)
		}

		values[key] = text
	}

	return values, nil
}

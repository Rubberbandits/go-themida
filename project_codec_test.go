//nolint:testpackage // White-box tests exercise the internal reflection codec.
package themida

import (
	"errors"
	"reflect"
	"testing"
)

const (
	sampleName   = "widget"
	sampleNested = "deep"
	boolTrue     = "true"
	countKey     = "COUNT"
	enabledKey   = "P_ENABLED"
)

// codecNested is an embedded settings group addressed by a nested prefix.
type codecNested struct {
	Value string `tmd:"VALUE"`
}

// codecSample defines the tagged shape used by both the decode and encode tests,
// so native-key tags live in exactly one place.
type codecSample struct {
	Name    string         `tmd:"NAME"`
	Enabled bool           `tmd:"ENABLED"`
	Count   uint64         `tmd:"COUNT"`
	Nested  codecNested    `tmd:"NESTED"`
	Codec   recordingCodec `tmd:"CODEC"`
	Signed  int            `tmd:"SIGNED"` // Unsupported kind: never serialized.
	Ignored string         // No tmd tag: always skipped.
}

// recordingCodec is a projectFields implementation that records the prefixes it
// receives, exercising decodeFields/encodeFields interface dispatch.
type recordingCodec struct {
	decodedPrefix string
	encodedPrefix string
}

func (c *recordingCodec) Unmarshal(prefix string, variables map[string]string) error {
	c.decodedPrefix = prefix
	if _, ok := variables[prefix+"_FAIL"]; ok {
		return ErrInvalidProject
	}

	return nil
}

func (c *recordingCodec) Marshal(prefix string, variables map[string]string) {
	c.encodedPrefix = prefix
	variables[prefix+"_MARSHALLED"] = "1"
}

func TestFieldKey(t *testing.T) {
	t.Parallel()

	if got := fieldKey("", "TAG"); got != "TAG" {
		t.Fatalf("empty prefix: got %q, want %q", got, "TAG")
	}

	if got := fieldKey("OPTION", "TAG"); got != "OPTION_TAG" {
		t.Fatalf("prefixed: got %q, want %q", got, "OPTION_TAG")
	}
}

func TestParseUint(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		text string
		want uint64
	}{
		{"0", 0},
		{"42", 42},
		{"0x2C27C00A", 0x2C27C00A},
		{"0XFF", 0xFF},
	} {
		got, err := parseUint(testCase.text)
		requireNoError(t, err)
		requireTestf(
			t,
			got == testCase.want,
			"parseUint(%q) = %d, want %d",
			testCase.text,
			got,
			testCase.want,
		)
	}

	for _, invalid := range []string{"", "abc", "0xZZ", "-1", "18446744073709551616"} {
		_, err := parseUint(invalid)
		requireErrorContains(t, err, "parse unsigned integer")
	}
}

func TestEntryCount(t *testing.T) {
	t.Parallel()

	variables := map[string]string{countKey: "2", "A": "x", "B": "y"}

	count, exists, err := entryCount(countKey, variables)
	requireNoError(t, err)
	requireTestf(t, exists && count == 2, "valid count: got (%d, %t)", count, exists)

	count, exists, err = entryCount("MISSING", variables)
	requireNoError(t, err)
	requireTestf(t, !exists && count == 0, "missing key: got (%d, %t)", count, exists)

	for _, bad := range []string{"-1", "abc", "9999"} {
		_, exists, err := entryCount(countKey, map[string]string{countKey: bad})
		requireTestf(t, exists, "invalid count %q should report the key exists", bad)
		requireErrorContains(t, err, "invalid count")

		if !errors.Is(err, ErrInvalidProject) {
			t.Fatalf("invalid count %q: error is not ErrInvalidProject: %v", bad, err)
		}
	}
}

func TestDecodeScalarField(t *testing.T) {
	t.Parallel()

	var target struct {
		Text   string
		Flag   bool
		Number uint64
		Signed int
	}

	value := reflect.ValueOf(&target).Elem()

	requireNoError(t, decodeScalarField(value.FieldByName("Text"), "hello"))
	requireTestf(t, target.Text == "hello", "string: got %q", target.Text)

	requireNoError(t, decodeScalarField(value.FieldByName("Flag"), boolTrue))
	requireTestf(t, target.Flag, "bool: got %t", target.Flag)

	requireNoError(t, decodeScalarField(value.FieldByName("Number"), "0x10"))
	requireTestf(t, target.Number == 16, "uint64 hex: got %d", target.Number)

	requireErrorContains(
		t,
		decodeScalarField(value.FieldByName("Flag"), "notabool"),
		"parse boolean",
	)
	requireErrorContains(
		t,
		decodeScalarField(value.FieldByName("Number"), "bad"),
		"parse unsigned integer",
	)

	err := decodeScalarField(value.FieldByName("Signed"), "1")
	requireErrorContains(t, err, "unsupported field kind")

	if !errors.Is(err, ErrInvalidProject) {
		t.Fatalf("unsupported kind: error is not ErrInvalidProject: %v", err)
	}
}

func TestDecodeFields(t *testing.T) {
	t.Parallel()

	var target codecSample

	variables := map[string]string{
		"P_NAME":         sampleName,
		enabledKey:       boolTrue,
		"P_COUNT":        "0x20",
		"P_NESTED_VALUE": sampleNested,
		"P_IGNORED":      "leave me",
	}

	requireNoError(t, decodeFields("P", &target, variables))
	requireTestf(t, target.Name == sampleName, "nested scalar: got %q", target.Name)
	requireTestf(t, target.Enabled, "bool: got %t", target.Enabled)
	requireTestf(t, target.Count == 0x20, "uint64: got %d", target.Count)
	requireTestf(
		t,
		target.Nested.Value == sampleNested,
		"nested struct: got %q",
		target.Nested.Value,
	)
	requireTestf(
		t,
		target.Codec.decodedPrefix == "P_CODEC",
		"codec prefix: got %q",
		target.Codec.decodedPrefix,
	)
	requireTestf(t, target.Ignored == "", "untagged field must be ignored: got %q", target.Ignored)
}

func TestDecodeFieldsReportsKeyOnScalarError(t *testing.T) {
	t.Parallel()

	var target struct {
		Enabled bool `tmd:"ENABLED"`
	}

	err := decodeFields("P", &target, map[string]string{enabledKey: "notabool"})
	requireErrorContains(t, err, "themida setting P_ENABLED", "parse boolean")
}

func TestDecodeFieldsPropagatesCodecError(t *testing.T) {
	t.Parallel()

	var target struct {
		Codec recordingCodec `tmd:"CODEC"`
	}

	err := decodeFields("P", &target, map[string]string{"P_CODEC_FAIL": "1"})
	requireErrorContains(t, err, "decode fields P_CODEC")

	if !errors.Is(err, ErrInvalidProject) {
		t.Fatalf("codec error is not ErrInvalidProject: %v", err)
	}
}

func TestEncodeFields(t *testing.T) {
	t.Parallel()

	target := codecSample{
		Name:    sampleName,
		Enabled: true,
		Count:   0x20,
		Nested:  codecNested{Value: sampleNested},
		Codec:   recordingCodec{},
		Signed:  7,
		Ignored: "leave me",
	}

	variables := map[string]string{}
	encodeFields("P", &target, variables)

	want := map[string]string{
		"P_NAME":             sampleName,
		enabledKey:           boolTrue,
		"P_COUNT":            "32",
		"P_NESTED_VALUE":     sampleNested,
		"P_CODEC_MARSHALLED": "1",
	}

	if !reflect.DeepEqual(variables, want) {
		t.Fatalf("encodeFields produced %v, want %v", variables, want)
	}

	requireTestf(
		t,
		target.Codec.encodedPrefix == "P_CODEC",
		"codec prefix: got %q",
		target.Codec.encodedPrefix,
	)
}

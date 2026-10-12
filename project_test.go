//nolint:testpackage // White-box tests exercise internal parser and execution behavior.
package themida

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func readSampleData(t *testing.T) []byte {
	t.Helper()

	data, err := os.ReadFile("test/project.tmd")
	requireNoError(t, err)

	return data
}

func readSample(t *testing.T) Project {
	t.Helper()

	var project Project
	requireNoError(t, project.Unmarshal(readSampleData(t)))

	return project
}

func TestProjectRoundTrip(t *testing.T) {
	t.Parallel()
	project := readSample(t)
	requireTestf(t, project.SoftwareName == "Sample", "software name was not parsed")
	requireTestf(
		t,
		project.Options.Macro.Functions["zeta"].Type == 10,
		"hexadecimal macro type was not parsed",
	)
	requireTestf(
		t,
		project.Options.Macro.Functions["alpha"].Type == 16,
		"decimal macro type was not parsed",
	)
	requireTestf(t, project.Options.Macro.DisableAtRvaNumber == 3, "macro options were not parsed")
	requireTestf(t, project.Messages.Debugger.Enabled, "message was not parsed")
	requireTestf(t, len(project.Messages.Debugger.Texts) == 2, "message languages were not parsed")
	requireTestf(
		t,
		project.Messages.Debugger.Texts[0].Text == `Hello\nworld`,
		"message text was not preserved",
	)
	requireTestf(t, len(project.Options.XBundler.Files) == 1, "bundled files were not parsed")

	data, err := project.Marshal()
	requireNoError(t, err)
	requireTestf(
		t,
		bytes.Equal(data, readSampleData(t)),
		"round trip changed base settings:\n%s",
		data,
	)

	data2, err := project.Marshal()
	requireNoError(t, err)
	requireTestf(t, bytes.Equal(data, data2), "serialization is not deterministic")
}

func TestTypedEditsAndCollectionRemoval(t *testing.T) {
	t.Parallel()
	project := readSample(t)
	project.SoftwareName = ""
	project.Options.Macro.DisableAtRvaNumber = 0
	macro := project.Options.Macro.Functions["alpha"]
	macro.Type = 0
	project.Options.Macro.Functions["alpha"] = macro
	project.Messages.Debugger.Enabled = false
	project.Messages.Debugger.Texts = project.Messages.Debugger.Texts[:1]
	project.Messages.Debugger.Texts[0].Text = "new=text"
	project.Options.XBundler.Files = []XBundlerFile{}
	data, err := project.Marshal()
	requireNoError(t, err)

	var got Project
	requireNoError(t, got.Unmarshal(data))
	requireTestf(t, got.SoftwareName == "", "empty software name was lost")
	requireTestf(t, got.Options.Macro.DisableAtRvaNumber == 0, "zero macro option was lost")
	requireTestf(t, got.Options.Macro.Functions["alpha"].Type == 0, "zero macro type was lost")
	requireTestf(t, got.Options.Macro.Functions["zeta"].Type == 10, "original macro type was lost")
	requireTestf(t, !got.Messages.Debugger.Enabled, "message was not disabled")
	requireTestf(t, len(got.Messages.Debugger.Texts) == 1, "message language was not removed")
	requireTestf(
		t,
		got.Messages.Debugger.Texts[0].Text == "new=text",
		"message text was not updated",
	)
	requireTestf(t, len(got.Options.XBundler.Files) == 0, "bundled files were not removed")

	for _, key := range []string{"MSG_ID_DEBUGGER_LANGUAGE_1_ID", "OPTION_XBUNDLER_FILE_NAME_AT_0"} {
		_, exists := got.Get(key)
		requireTestf(t, !exists, "stale collection key %s was retained", key)
	}

	delete(project.Options.Macro.Functions, "zeta")
	data, err = project.Marshal()
	requireNoError(t, err)
	requireNoError(t, got.Unmarshal(data))
	requireTestf(
		t,
		len(got.Options.Macro.Functions) == 1,
		"macro deletion did not update counts and indexed names",
	)
	requireTestf(
		t,
		got.Options.Macro.Functions["alpha"].Type == 0,
		"macro deletion changed the retained macro",
	)
}

func TestOverridesPreserveUnspecifiedSettings(t *testing.T) {
	t.Parallel()
	project := readSample(t)

	var settings Settings
	requireNoError(t, json.Unmarshal(
		[]byte(`{"SOFTWARE_NAME":"","OPTION_PROTECTION_IS_ANTIDEBUG":false,`+
			`"OPTION_MACROS_DISABLE_AT_RVA_NUMBER":0,"OPTION_LARGE_NUMBER":18446744073709551615,`+
			`"NEW_OPTION":"new"}`),
		&settings,
	))
	requireNoError(t, project.Override(settings))

	for key, want := range map[string]string{
		"SOFTWARE_NAME":                       "",
		"OPTION_PROTECTION_IS_ANTIDEBUG":      "false",
		"OPTION_MACROS_DISABLE_AT_RVA_NUMBER": "0",
		"OPTION_LARGE_NUMBER":                 "18446744073709551615",
		"NEW_OPTION":                          "new",
		"OPTION_FUTURE_DATA":                  " value=with=equals ",
	} {
		got, exists := project.Get(key)
		requireTestf(t, exists && got == want, "%s=%q, want %q", key, got, want)
	}

	requireTestf(
		t,
		project.Options.Macro.DisableAtRvaNumber == 0,
		"override did not refresh typed data",
	)
	requireTestf(
		t,
		project.OutputFile == "protected.exe",
		"override changed omitted output setting",
	)
	requireTestf(
		t,
		len(project.Messages.Debugger.Texts) == 2,
		"override changed omitted message settings",
	)

	data, err := project.Marshal()
	requireNoError(t, err)

	var reloaded Project
	requireNoError(t, reloaded.Unmarshal(data))
	requireTestf(
		t,
		reflect.DeepEqual(reloaded.values(), project.values()),
		"overrides did not survive serialization",
	)
}

func TestDuplicateSettingsCannotUndoOverride(t *testing.T) {
	t.Parallel()

	var project Project
	//nolint:dupword // Repeated keys verify that every duplicate is overridden.
	requireNoError(t, project.Unmarshal([]byte("[SECURE_ENGINE]\nFLAG=true\nFLAG=true\n")))
	requireNoError(t, project.Set(testFlag, false))

	data, err := project.Marshal()
	requireNoError(t, err)
	requireTestf(
		t,
		!strings.Contains(string(data), "FLAG=true"),
		"duplicate undid override: %s",
		data,
	)
	requireTestf(
		t,
		strings.Count(string(data), "FLAG=false") == 2,
		"duplicate keys were lost: %s",
		data,
	)
}

func TestInvalidProjectsAndOverridesAreAtomic(t *testing.T) {
	t.Parallel()
	project := readSample(t)
	original, err := project.Marshal()
	requireNoError(t, err)

	for _, data := range []string{
		"",
		"[SECURE_ENGINE]invalid\n",
		"[SECURE_ENGINE]\nOPTION_MACROS_MAP_FUNCTIONS_NUMBER=-1\n",
		"[SECURE_ENGINE]\nMSG_ID_DEBUGGER_IS_ENABLED=invalid\n",
		"[SECURE_ENGINE]\nOPTION_MACROS_MAP_FUNCTIONS_NUMBER=1\n",
		"[SECURE_ENGINE]\nOPTION_MACROS_MAP_FUNCTIONS_NUMBER=1\n" +
			"OPTION_MACROS_MAP_FUNCTION_NAME_AT_0=a\nOPTION_MACROS_MAP_FUNCTION_TYPE_AT_0=invalid\n",
		"[SECURE_ENGINE]\nOPTION_XBUNDLER_NUMBER_FILES=1\n",
		"[SECURE_ENGINE]\nMSG_ID_DEBUGGER_NUMBER_LANGUAGES=1\n",
	} {
		requireTestf(
			t,
			project.Unmarshal([]byte(data)) != nil,
			"accepted invalid project: %q",
			data,
		)

		got, err := project.Marshal()
		requireNoError(t, err)
		requireTestf(t, bytes.Equal(got, original), "failed parsing changed receiver")
	}

	for _, options := range []Settings{
		{"BAD=KEY": true},
		{testFlag: nil},
		{testFlag: []string{"x"}},
		{testFlag: "x\nBAD=true"},
		{"OPTION_MACROS_DISABLE_AT_RVA_NUMBER": "invalid"},
		{testFlag: json.Number("invalid")},
	} {
		requireTestf(t, project.Override(options) != nil, "accepted invalid settings: %+v", options)

		got, err := project.Marshal()
		requireNoError(t, err)
		requireTestf(t, bytes.Equal(got, original), "failed override changed receiver")
	}

	requireNoError(t, project.Unmarshal([]byte("[SECURE_ENGINE]\nSOFTWARE_NAME=New\n")))
	requireTestf(t, project.OutputFile == "", "reusing receiver retained old output setting")
	requireTestf(t, project.Options.Macro.Functions == nil, "reusing receiver retained old macros")
}

func TestNewProjectFromTypedFields(t *testing.T) {
	t.Parallel()

	project := Project{
		SoftwareName: "Generated",
		OutputFile:   "app.exe",
		Options: Options{
			Macro: MacroOptions{Functions: map[string]Macro{"entry": {Type: 10}}},
		},
	}
	data, err := project.Marshal()
	requireNoError(t, err)

	var got Project
	requireNoError(t, got.Unmarshal(data))
	requireTestf(t, got.SoftwareName == "Generated", "cannot generate project: %s", data)
	requireTestf(
		t,
		got.Options.Macro.Functions["entry"].Type == 10,
		"cannot generate macros: %s",
		data,
	)
}

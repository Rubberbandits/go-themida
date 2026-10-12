package themida

import (
	"fmt"
	"strconv"
)

// MessageIcon selects the icon Themida shows with a customized dialog. Themida
// displays each message as a standard Windows message box; the icon is set per
// message.
type MessageIcon string

const (
	// MsgIconInfo selects the information icon.
	MsgIconInfo MessageIcon = "INFORMATION"
	// MsgIconWarning selects the warning icon.
	MsgIconWarning MessageIcon = "WARNING"
	// MsgIconError selects the error icon.
	MsgIconError MessageIcon = "ERROR"
)

// Language is the native Themida language identifier of a message translation,
// for example "ENGLISH" or "GERMAN". The special "[DEFAULT]" identifier marks
// the text shown when the end user's system language matches no defined
// translation.
type Language string

// MessageText is one language-specific translation of a customized dialog.
type MessageText struct {
	// Language identifies the translation (see [Language]).
	Language Language `tmd:"ID"`
	// Text is the message body for that language. Line breaks are written as a
	// literal \n sequence, never an actual newline, because a project entry
	// occupies a single line.
	Text string `tmd:"TEXT"`
}

// Message is one of Themida's customized dialogs: its enabled state, icon, and
// per-language text. Each dialog is shown to the end user when its corresponding
// protection event occurs (see [Messages] for the catalog of events).
type Message struct {
	// Icon selects the dialog's icon.
	Icon MessageIcon `tmd:"ICON"`
	// Enabled reports whether Themida shows this dialog. When disabled, the
	// protected application reacts to the event without displaying a message.
	Enabled bool `tmd:"IS_ENABLED"`
	// Texts holds the message's translations. A nil slice leaves the project's
	// existing translations untouched; a non-nil slice replaces them in full.
	Texts []MessageText
}

// Unmarshal replaces the message from the native keys under prefix, including
// its indexed LANGUAGE translations. It returns an error wrapping
// [ErrInvalidProject] if the language count is invalid or a translation's ID or
// TEXT entry is missing.
func (m *Message) Unmarshal(prefix string, variables map[string]string) error {
	*m = Message{}
	{
		err := decodeFields(prefix, m, variables)
		if err != nil {
			return err
		}
	}

	count, exists, err := entryCount(prefix+"_NUMBER_LANGUAGES", variables)
	if err != nil || !exists {
		return err
	}

	m.Texts = make([]MessageText, count)
	for index := range m.Texts {
		key := fmt.Sprintf("%s_LANGUAGE_%d", prefix, index)
		for _, suffix := range []string{"_ID", "_TEXT"} {
			if _, exists := variables[key+suffix]; !exists {
				return fmt.Errorf("%w: missing Themida setting %s", ErrInvalidProject, key+suffix)
			}
		}

		err := decodeFields(key, &m.Texts[index], variables)
		if err != nil {
			return err
		}
	}

	return nil
}

// Marshal writes the message and its translations to the native keys under
// prefix, rewriting NUMBER_LANGUAGES and the indexed LANGUAGE entries to match
// Texts.
func (m *Message) Marshal(prefix string, variables map[string]string) {
	encodeFields(prefix, m, variables)

	if m.Texts != nil {
		variables[prefix+"_NUMBER_LANGUAGES"] = strconv.Itoa(len(m.Texts))
		for index := range m.Texts {
			encodeFields(fmt.Sprintf("%s_LANGUAGE_%d", prefix, index), &m.Texts[index], variables)
		}
	}
}

// Messages is the catalog of Themida customized dialogs, serialized under the
// MSG_ID prefix. Each field is the dialog shown for one protection event.
type Messages struct {
	// GlobalCaption is the window caption shown with every message box.
	GlobalCaption Message `tmd:"GLOBAL_CAPTION"`
	// Debugger is shown when a debugger is detected attached to the application.
	Debugger Message `tmd:"DEBUGGER"`
	// Monitor is shown when a file or registry monitoring tool is detected.
	Monitor Message `tmd:"MONITOR"`
	// CrackTool is shown when a known cracking or patching tool is detected.
	CrackTool Message `tmd:"CRACK_TOOL"`
	// FileCorrupted is shown when the protected file on disk has been patched or
	// corrupted (the anti-file-patching check).
	FileCorrupted Message `tmd:"FILE_CORRUPTED"`
	// Exception is shown when an unhandled exception occurs in the application.
	Exception Message `tmd:"EXCEPTION"`
	// VirtualPc is shown when the application runs under a virtual machine such
	// as VMware or Virtual PC and that is not permitted.
	VirtualPc Message `tmd:"VIRTUAL_PC"`
	// MissingDll is shown when a DLL the application requires cannot be found.
	MissingDll Message `tmd:"MISSING_DLL"`
}

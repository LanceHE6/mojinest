package desktop

import "strings"

// DialogMode controls when the client pops a native dialog box.
type DialogMode string

const (
	// DialogAuto shows a dialog only when no console is attached (double-click,
	// hotkey bindings) and writes to stderr otherwise (terminal, scripts).
	DialogAuto DialogMode = "auto"
	// DialogAlways forces dialogs, e.g. for a shortcut the user watches.
	DialogAlways DialogMode = "always"
	// DialogNever suppresses dialogs entirely — right for scripts, because a
	// modal box blocks the process until someone clicks OK.
	DialogNever DialogMode = "never"
)

// ParseDialogMode validates a -dialogs value.
func ParseDialogMode(s string) (DialogMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return DialogAuto, true
	case "always", "1", "true":
		return DialogAlways, true
	case "never", "0", "false", "none":
		return DialogNever, true
	}
	return DialogAuto, false
}

// ShouldDialog decides whether a user-facing message becomes a dialog.
func ShouldDialog(mode DialogMode) bool {
	switch mode {
	case DialogAlways:
		return true
	case DialogNever:
		return false
	default:
		return consoleAttached()
	}
}

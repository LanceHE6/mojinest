//go:build !windows

package desktop

import (
	"fmt"
	"os"
)

// Notify prints to stderr on platforms where a dialog box would be noise.
func Notify(title, text string, isError bool) {
	prefix := "提示"
	if isError {
		prefix = "错误"
	}
	fmt.Fprintf(os.Stderr, "%s: %s\n%s\n", prefix, title, text)
}

// CanPrompt reports whether AskYesNoCancel can actually show a dialog.
func CanPrompt() bool { return canPromptPlatform() }

// AskYesNoCancel has no GUI on these platforms: explain and answer "cancel".
func AskYesNoCancel(title, text string) AskResult {
	fmt.Fprintf(os.Stderr, "%s\n%s\n（非 Windows 平台请直接用子命令，见 mojinest-desktop help）\n", title, text)
	return AskCancel
}

func canPromptPlatform() bool { return false }

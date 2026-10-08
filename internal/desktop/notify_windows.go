//go:build windows

package desktop

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32dll      = windows.NewLazySystemDLL("user32.dll")
	procMessageBox = user32dll.NewProc("MessageBoxW")
)

const (
	mbOK            = 0x00000000
	mbYesNoCancel   = 0x00000003
	mbIconError     = 0x00000010
	mbIconInfo      = 0x00000040
	mbSetForeground = 0x00010000
	mbTopMost       = 0x00040000
	idYes           = 6
	idNo            = 7
)

// Notify shows a native dialog when one is appropriate. DialogMode decides:
// scripts get stderr (a modal box would block them forever), while a
// double-clicked exe or a hotkey action gets a real dialog.
func Notify(mode DialogMode, title, text string, isError bool) {
	if !ShouldDialog(mode) {
		NotifyStderr(title, text, isError)
		return
	}
	flags := uintptr(mbOK | mbSetForeground | mbTopMost)
	if isError {
		flags |= mbIconError
	} else {
		flags |= mbIconInfo
	}
	messageBox(title, text, flags)
}

// CanPrompt reports whether AskYesNoCancel can actually show a dialog.
func CanPrompt() bool { return canPromptPlatform() }

// AskYesNoCancel shows a three-way question. Cancel is the default answer so a
// stray Escape never starts anything long-running.
func AskYesNoCancel(title, text string) AskResult {
	switch messageBox(title, text, mbYesNoCancel|mbIconInfo|mbSetForeground|mbTopMost) {
	case idYes:
		return AskYes
	case idNo:
		return AskNo
	default:
		return AskCancel
	}
}

func canPromptPlatform() bool { return true }

func messageBox(title, text string, flags uintptr) int {
	t, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return 0
	}
	m, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return 0
	}
	r, _, _ := procMessageBox.Call(0,
		uintptr(unsafe.Pointer(m)),
		uintptr(unsafe.Pointer(t)),
		flags)
	return int(r)
}

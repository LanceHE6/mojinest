//go:build windows

package desktop

import "golang.org/x/sys/windows"

// consoleAttached reports whether this process owns a console window. A
// double-clicked exe has none; a terminal or script invocation does.
func consoleAttached() bool {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")
	hwnd, _, _ := proc.Call()
	return hwnd != 0
}

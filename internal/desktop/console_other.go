//go:build !windows

package desktop

// consoleAttached: on Unix-like systems stderr is always there, so messages go
// to it and no dialog is needed.
func consoleAttached() bool { return false }

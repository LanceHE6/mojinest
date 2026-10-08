//go:build windows

package desktop

import "golang.org/x/sys/windows"

// detachedSysProcAttr detaches the child from this process so the browser keeps
// running after the CLI exits.
func detachedSysProcAttr() *windows.SysProcAttr {
	return &windows.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
}

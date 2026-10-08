//go:build !windows

package desktop

import "syscall"

// detachedSysProcAttr puts the child in its own session so it survives us.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

package desktop

import "syscall"

// DetachedSysProcAttr returns the platform's "survive my parent" process
// attributes, so a launched child (relay, browser) outlives this process.
func DetachedSysProcAttr() *syscall.SysProcAttr { return detachedSysProcAttr() }

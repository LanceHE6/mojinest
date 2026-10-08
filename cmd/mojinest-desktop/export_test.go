package main

import "mojinest/internal/desktop"

// loadDesktopConfig is a tiny seam so tests can read back what setup wrote.
func loadDesktopConfig(path string) (*desktop.Config, error) {
	return desktop.Load(path)
}

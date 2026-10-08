// Package webui embeds the built web frontend.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the frontend filesystem rooted at dist/.
func FS() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}

// Built reports whether a real frontend build is present.
func Built() bool {
	f, err := FS()
	if err != nil {
		return false
	}
	_, err = fs.Stat(f, "index.html")
	return err == nil
}

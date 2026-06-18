// Package webui embeds the static operator UI served by the core HTTP API.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed dist/*
var dist embed.FS

// FS returns the embedded web UI assets rooted at the dist directory.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// dist is embedded at build time; a failure here is a programming error.
		panic(err)
	}
	return sub
}

// Package ui carries the built dashboard inside the binary.
package ui

import (
	"embed"
	"io/fs"
)

// The dist directory is produced by "pnpm build" in web/.
//
//go:embed all:dist
var dist embed.FS

// Files returns the dashboard's static files.
func Files() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

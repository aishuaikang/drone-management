// Package webassets embeds the built frontend.
package webassets

import "embed"

// Assets contains the built frontend files.
//
//go:embed all:dist
var Assets embed.FS

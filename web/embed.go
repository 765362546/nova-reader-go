// Package webassets embeds the compiled Vue frontend (dist) into the server
// binary so it can be distributed as a single file.
package webassets

import "embed"

// Dist holds the built frontend. Before `npm run build` the directory only
// contains .gitkeep, in which case the HTTP layer falls back to serving files
// from disk.
//
//go:embed all:dist
var Dist embed.FS

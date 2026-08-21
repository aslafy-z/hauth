// Package version carries build metadata stamped by the release pipeline.
package version

// Set via -ldflags at build time; defaults describe a source build.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

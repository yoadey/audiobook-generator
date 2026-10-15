// Package buildinfo carries version information injected at link time:
//
//	-ldflags "-X github.com/yoadey/audiobook-generator/internal/buildinfo.Version=v1.2.3
//	          -X github.com/yoadey/audiobook-generator/internal/buildinfo.Commit=abc123"
package buildinfo

// Set via -ldflags.
var (
	Version = "dev"
	Commit  = "unknown"
)

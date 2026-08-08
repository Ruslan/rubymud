// Package buildinfo exposes metadata injected into release binaries by the Go linker.
package buildinfo

// Version is replaced with `go build -ldflags "-X ...Version=<version>"`.
// Direct `go run` and test builds intentionally identify themselves as development builds.
var Version = "dev"

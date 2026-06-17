// Package version exposes build metadata injected at link time via -ldflags
// (see the Makefile LDFLAGS block). When built without ldflags (e.g. `go run`
// or a plain `go build`), the defaults below apply so callers always have a
// usable value.
package version

var (
	// Version is the semantic/tag version of the build (e.g. "v1.4").
	Version = "dev"
	// Commit is the short git commit hash the binary was built from.
	// Used as the default round identifier for crawler speed metrics.
	Commit = "unknown"
	// Built is the UTC build timestamp (RFC3339).
	Built = "unknown"
)

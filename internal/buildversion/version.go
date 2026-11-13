// Package buildversion identifies the release of the running component.
package buildversion

import (
	_ "embed"
	"runtime/debug"
	"strings"
)

// Stamped by tools/release/build.py. Module builds (go install @vX.Y.Z)
// use the module version; source builds use the checked-in release version.
var Version string

//go:embed VERSION
var sourceVersion string

func Current() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return strings.TrimSpace(sourceVersion)
}

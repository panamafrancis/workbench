package version

import "runtime/debug"

// Version is stamped via -ldflags by the Makefile. A plain `go install
// …@vX.Y.Z` passes no ldflags, so it falls back to the module version Go
// records in the binary — otherwise every such install reports "dev" and the
// update check skips it.
var Version = "dev"

func init() {
	if Version != "dev" {
		return
	}
	if v := moduleVersion(debug.ReadBuildInfo()); v != "" {
		Version = v
	}
}

// moduleVersion is the main module's version from build info, or "" when
// there is none worth reporting ("(devel)" is what a build with no version
// information says).
func moduleVersion(info *debug.BuildInfo, ok bool) string {
	if !ok || info == nil {
		return ""
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	return ""
}

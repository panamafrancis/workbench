package version

import (
	"runtime/debug"
	"testing"
)

func TestModuleVersion(t *testing.T) {
	for _, tc := range []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{"go install at a tag", &debug.BuildInfo{Main: debug.Module{Version: "v0.0.14"}}, true, "v0.0.14"},
		{"no version info", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true, ""},
		{"empty version", &debug.BuildInfo{}, true, ""},
		{"no build info", nil, false, ""},
	} {
		if got := moduleVersion(tc.info, tc.ok); got != tc.want {
			t.Errorf("%s: moduleVersion = %q, want %q", tc.name, got, tc.want)
		}
	}
}

package setup

import (
	"strings"
	"testing"
)

// release is the release every case is compared against.
const release = "v0.0.14"

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		current, latest string
		wantHint        bool
	}{
		{release, release, false},
		{"v0.0.13", release, true},
		{"v0.0.15", release, false},
		{"dev", release, false},
		// Suffixes a local, `make`, or `go install` build reports.
		{"v0.0.14+dirty", release, false},
		{"v0.0.12+dirty", release, true},
		{"v0.0.14-3-gabc1234-dirty", release, false},
		{"v0.0.15-0.20260923082845-5fad243abcde", release, false},
	} {
		got := compareVersions(tc.current, tc.latest)
		if (got != "") != tc.wantHint {
			t.Errorf("compareVersions(%q, %q) = %q, want hint %v", tc.current, tc.latest, got, tc.wantHint)
		}
	}
}

func TestUpdateHintInstallsBothBinaries(t *testing.T) {
	hint := compareVersions("v0.0.13", release)
	for _, pkg := range []string{"github.com/panamafrancis/workbench@latest", "github.com/panamafrancis/workbench/cmd/supatree@latest"} {
		if !strings.Contains(hint, pkg) {
			t.Errorf("hint %q does not install %s", hint, pkg)
		}
	}
}

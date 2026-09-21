package github

import "testing"

// wantRepo is the repository half of every well-formed remote below; goconst
// objects to it appearing inline six times.
const wantRepo = "workbench"

func TestParseRepoSlug(t *testing.T) {
	tests := []struct {
		remote      string
		owner, name string
		wantErr     bool
	}{
		{remote: "git@github.com:panamafrancis/workbench.git", owner: "panamafrancis", name: wantRepo},
		{remote: "git@github.com:panamafrancis/workbench", owner: "panamafrancis", name: wantRepo},
		{remote: "https://github.com/panamafrancis/workbench.git", owner: "panamafrancis", name: wantRepo},
		{remote: "https://github.com/panamafrancis/workbench", owner: "panamafrancis", name: wantRepo},
		{remote: "ssh://git@github.com/panamafrancis/workbench.git", owner: "panamafrancis", name: wantRepo},
		// An enterprise host with a port, and a nested group path: the last two
		// segments are the slug either way.
		{remote: "https://git.example.com:8443/team/sub/workbench.git", owner: "sub", name: wantRepo},
		{remote: "/local/path/only", owner: "path", name: "only"},
		{remote: "git@github.com:noslash", wantErr: true},
		{remote: "", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.remote, func(t *testing.T) {
			owner, name, err := parseRepoSlug(tc.remote)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseRepoSlug(%q) = %q/%q, want error", tc.remote, owner, name)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRepoSlug(%q): %v", tc.remote, err)
			}
			if owner != tc.owner || name != tc.name {
				t.Errorf("parseRepoSlug(%q) = %q/%q, want %q/%q", tc.remote, owner, name, tc.owner, tc.name)
			}
		})
	}
}

// Unresolved is what the whole GraphQL detour buys, so it must exclude both
// resolved threads and ones whose lines no longer exist.
func TestUnresolved(t *testing.T) {
	fb := PRFeedback{Threads: []ReviewThread{
		{Path: "a.go", Resolved: false, Outdated: false},
		{Path: "b.go", Resolved: true},
		{Path: "c.go", Resolved: false, Outdated: true},
	}}
	got := fb.Unresolved()
	if len(got) != 1 || got[0].Path != "a.go" {
		t.Fatalf("Unresolved = %+v, want only a.go", got)
	}
}

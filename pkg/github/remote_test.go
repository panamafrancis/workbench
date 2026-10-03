package github

import "testing"

func TestParseAndResolveRemote(t *testing.T) {
	orig := sshResolve
	t.Cleanup(func() { sshResolve = orig })
	sshResolve = func(host string) string {
		if host == "github-work" {
			return "github.com"
		}
		return host
	}

	cases := []struct {
		raw   string
		key   string
		alias string
	}{
		{"git@github.com:Fraud-Zero/Keystone.git", "github.com/fraud-zero/keystone", ""},
		{"https://github.com/fraud-zero/keystone", "github.com/fraud-zero/keystone", ""},
		{"ssh://git@github.com/fraud-zero/keystone.git", "github.com/fraud-zero/keystone", ""},
		{"git@github-work:fraud-zero/keystone.git", "github.com/fraud-zero/keystone", "github-work"},
		{"git@gitlab.example.com:Team/Repo.git", "gitlab.example.com/Team/Repo", ""},
		{"https://gitlab.example.com/team/repo.git", "gitlab.example.com/team/repo", ""},
	}
	for _, tc := range cases {
		r, ok := ResolveRemote(tc.raw)
		if !ok || r.Key() != tc.key {
			t.Errorf("ResolveRemote(%q) = %q, %v; want %q", tc.raw, r.Key(), ok, tc.key)
		}
		if got := SSHAlias(tc.raw); got != tc.alias {
			t.Errorf("SSHAlias(%q) = %q, want %q", tc.raw, got, tc.alias)
		}
	}
	for _, bad := range []string{"", "/local/path", "https://github.com/onlyowner"} {
		if _, ok := ResolveRemote(bad); ok {
			t.Errorf("ResolveRemote(%q) accepted", bad)
		}
	}
	if got := (RemoteRef{Host: "github.com", Owner: "o", Name: "r"}).SSHURL(); got != "git@github.com:o/r.git" {
		t.Errorf("SSHURL = %s", got)
	}
	// The thin wrapper still answers GitHub only, alias resolved.
	if ref, ok := RepoRefFromRemote("git@github-work:fraud-zero/keystone.git"); !ok || ref.String() != "fraud-zero/keystone" {
		t.Errorf("RepoRefFromRemote via alias = %v, %v", ref, ok)
	}
	if _, ok := RepoRefFromRemote("git@gitlab.example.com:team/repo.git"); ok {
		t.Error("RepoRefFromRemote accepted a non-GitHub host")
	}
}

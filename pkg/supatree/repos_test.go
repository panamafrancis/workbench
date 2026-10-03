package supatree

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panamafrancis/workbench/pkg/github"
	"github.com/panamafrancis/workbench/pkg/testutil"
)

// The cache key is the repository, not the spelling: an ssh alias and the real
// host land on one clone.
func TestCacheKey(t *testing.T) {
	orig := resolveRemote
	t.Cleanup(func() { resolveRemote = orig })
	resolveRemote = func(raw string) (github.RemoteRef, bool) {
		r, ok := github.ParseRemote(raw)
		if r.Host == "github-work" {
			r.Host = "github.com"
		}
		return r, ok
	}
	a, err := CacheKey("git@github-work:fraud-zero/keystone.git")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := CacheKey("https://github.com/fraud-zero/keystone")
	if a != b || a != "github.com/fraud-zero/keystone" {
		t.Errorf("keys %q and %q, want both github.com/fraud-zero/keystone", a, b)
	}
	if k, err := CacheKey("/srv/git/thing"); err != nil || k != "local/srv/git/thing" {
		t.Errorf("local path key = %q, %v", k, err)
	}
	if _, err := CacheKey("not a url"); err == nil {
		t.Error("accepted a non-URL")
	}
}

// ResolveMember clones once, into the cache path, and copy_files settings are
// looked up by the same key.
func TestResolveMemberClonesOnce(t *testing.T) {
	testutil.IsolateHome(t)
	origin := originRepo(t)
	c := &Config{}
	key, _ := CacheKey(origin)
	c.Repos = map[string]RepoSettings{key: {CopyFiles: []string{".env"}}}

	bc, err := c.ResolveMember(aliasAPI, origin)
	if err != nil {
		t.Fatalf("ResolveMember: %v", err)
	}
	if !strings.HasPrefix(bc.Clone, ReposDir()) || !isClone(bc.Clone) {
		t.Errorf("clone at %s, want a clone under %s", bc.Clone, ReposDir())
	}
	if len(bc.CopyFiles) != 1 {
		t.Errorf("copy_files not found by key: %+v", bc)
	}
	marker := filepath.Join(bc.Clone, "untracked-marker")
	if err := os.WriteFile(marker, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ResolveMember("other-alias", origin); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("a second resolve re-cloned instead of reusing the clone")
	}
	repos, _ := ListCachedRepos()
	if len(repos) != 1 || repos[0].Key != key {
		t.Errorf("ListCachedRepos = %+v", repos)
	}
}

func TestOldSpecFormatSaysMigrate(t *testing.T) {
	_, err := parseSpec([]byte("members: [a, b]\n"))
	if !errors.Is(err, ErrOldSpec) {
		t.Errorf("err = %v, want ErrOldSpec", err)
	}
	if _, err := parseSpec([]byte("members:\n  a: \"\"\n")); err == nil {
		t.Error("a member with no URL was accepted")
	}
	s, err := parseSpec([]byte("members:\n  b: git@github.com:o/b.git\n  a: git@github.com:o/a.git\ndeps:\n  b: [a]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.Aliases(), ","); got != "a,b" {
		t.Errorf("Aliases = %s", got)
	}
}

func TestParseMemberArg(t *testing.T) {
	cases := []struct{ in, alias, url string }{
		{"fraud-zero/Keystone-API", "keystone-api", "git@github.com:fraud-zero/Keystone-API.git"},
		{"git@github.com:fraud-zero/admin.git", "admin", "git@github.com:fraud-zero/admin.git"},
		{"api=fraud-zero/keystone-api", aliasAPI, "git@github.com:fraud-zero/keystone-api.git"},
		{"api=https://gitlab.com/team/api", aliasAPI, "https://gitlab.com/team/api"},
	}
	for _, tc := range cases {
		alias, url, err := ParseMemberArg(tc.in)
		if err != nil || alias != tc.alias || url != tc.url {
			t.Errorf("ParseMemberArg(%q) = %q, %q, %v; want %q, %q", tc.in, alias, url, err, tc.alias, tc.url)
		}
	}
	if _, _, err := ParseMemberArg("nope"); err == nil {
		t.Error("accepted a bare word")
	}
}

// A review's pull request is matched to a member by the URL in the spec.
func TestAliasForRepo(t *testing.T) {
	members := map[string]string{
		aliasAPI: "git@github.com:Fraud-Zero/keystone-api.git",
		"web":    "https://github.com/fraud-zero/admin-frontend",
	}
	if a, err := aliasForRepo("fraud-zero/keystone-api", members); err != nil || a != aliasAPI {
		t.Errorf("aliasForRepo = %q, %v", a, err)
	}
	if _, err := aliasForRepo("fraud-zero/other", members); err == nil {
		t.Error("matched a repo that is not a member")
	}
}

// The setup hook runs once, cannot read a terminal, and a failure is a warning
// on a tree that still exists.
func TestRunSetup(t *testing.T) {
	testutil.IsolateHome(t)
	root := filepath.Join(t.TempDir(), treeLima)
	if err := (&Meta{Name: treeLima, Root: root}).Save(root); err != nil {
		t.Fatal(err)
	}
	inst := &Instance{Name: treeLima, Root: root}
	if w := RunSetup(inst); w != "" {
		t.Errorf("no script should be silent, got %q", w)
	}
	script := filepath.Join(root, SetupScript)
	if err := os.MkdirAll(filepath.Dir(script), 0755); err != nil {
		t.Fatal(err)
	}
	// read returns at once on a closed stdin; were it a terminal, this would hang.
	body := "read -r answer || true\necho ran >> \"$SUPATREE_ROOT/ran\"\nexit 3\n"
	if err := os.WriteFile(script, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	w := RunSetup(inst)
	if !strings.Contains(w, "setup.log") {
		t.Errorf("failure warning = %q, want it to point at the log", w)
	}
	if RunSetup(inst) != "" {
		t.Error("ran twice")
	}
	data, _ := os.ReadFile(filepath.Join(root, "ran"))
	if strings.Count(string(data), "ran") != 1 {
		t.Errorf("script ran %d times, want once", strings.Count(string(data), "ran"))
	}
	meta, _ := LoadMeta(root)
	if meta.SetupAt.IsZero() || meta.SetupErr == "" {
		t.Errorf("meta does not record the run: %+v", meta)
	}
}

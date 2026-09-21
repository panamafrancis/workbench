package supatree

import (
	"strings"
	"testing"
)

// The PM's grant is the security boundary, so it gets asserted rather than
// assumed: it may write supatree's own state and must never reach a member
// repo's working tree.
func TestPMGrants(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &Config{Stacks: []Stack{{Alias: "s", Path: "/elsewhere/stacks/s"}}}
	insts := []*Instance{
		{Name: treeA, Root: "/trees/canberra"},
		{Name: treeB, Root: "/trees/darwin"},
	}
	g := PMGrants(cfg, insts)

	allow := strings.Join(g.Allow, " ")
	for _, want := range []string{PMDir(), "/trees/canberra/.supatree", "/trees/darwin/.supatree", "/elsewhere/stacks/s"} {
		if !strings.Contains(allow, want) {
			t.Errorf("Allow = %v, want it to include %q", g.Allow, want)
		}
	}
	// The tree roots are readable but not writable, and repos/ is neither
	// granted nor implied by anything above it.
	for _, w := range g.Allow {
		if strings.HasSuffix(w, "/repos") || w == "/trees/canberra" || w == "/trees/darwin" {
			t.Errorf("Allow includes %q — the PM must never write a member repo's worktree", w)
		}
	}
	if len(g.Read) == 0 {
		t.Error("Read is empty — the PM cannot see the trees at all")
	}
}

// A stack at the default location is already covered by the stacks dir grant
// and must not be granted twice.
func TestPMGrantsNoDuplicateStack(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &Config{Stacks: []Stack{{Alias: "s", Path: DefaultStackPath("s")}}}
	g := PMGrants(cfg, nil)
	seen := map[string]int{}
	for _, p := range g.Allow {
		seen[p]++
	}
	for p, n := range seen {
		if n > 1 {
			t.Errorf("%q granted %d times", p, n)
		}
	}
}

func TestPMEnvGatesPMToolsOnly(t *testing.T) {
	env := PMEnv()
	if env["SUPATREE_PM"] != "1" {
		t.Error("SUPATREE_PM not set — the cross-tree tools would be gated off")
	}
	// SUPATREE must stay unset: the PM is in no supatree, so the tree-scoped
	// tools would resolve nothing and fail confusingly rather than being hidden.
	if _, ok := env["SUPATREE"]; ok {
		t.Error("SUPATREE is set for the PM, which is in no supatree")
	}
}

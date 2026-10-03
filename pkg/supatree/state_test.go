package supatree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panamafrancis/workbench/pkg/testutil"
)

// Tree state lives outside the tree, behind a .supatree link, so a sandbox can
// be granted all of it without being granted anything inside a tree.
func TestStateLivesOutsideTheTree(t *testing.T) {
	testutil.IsolateHome(t)
	root := filepath.Join(t.TempDir(), "lima")
	if err := (&Meta{Name: "lima", Root: root}).Save(root); err != nil {
		t.Fatal(err)
	}
	dir := StateDir(root)
	if want := filepath.Join(TreesStateDir(), "lima"); dir != want {
		t.Errorf("StateDir = %s, want %s", dir, want)
	}
	if strings.HasPrefix(dir, root) {
		t.Error("state is inside the tree")
	}
	target, err := os.Readlink(StateLink(root))
	if err != nil || target != dir {
		t.Errorf(".supatree link = %q, %v; want it to point at %s", target, err, dir)
	}
	// Every path agents and docs use still resolves through the link.
	if _, err := os.Stat(filepath.Join(root, ".supatree", "meta.yml")); err != nil {
		t.Errorf(".supatree/meta.yml does not resolve: %v", err)
	}
	// Idempotent.
	if err := LinkState(root); err != nil {
		t.Errorf("relinking: %v", err)
	}
}

// Discovery comes from the state dirs, and a tree is found wherever its meta
// says it is checked out — not only under the current trees base.
func TestListFindsTreesByState(t *testing.T) {
	testutil.IsolateHome(t)
	elsewhere := filepath.Join(t.TempDir(), "lima")
	if err := (&Meta{Name: "lima", Root: elsewhere}).Save(elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := SaveSpec(elsewhere, &Spec{}); err != nil {
		t.Fatal(err)
	}
	c := &Config{TreesBase: t.TempDir()}
	insts, err := List(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(insts) != 1 || insts[0].Root != elsewhere {
		t.Fatalf("List = %+v, want lima at %s", insts, elsewhere)
	}
	if _, err := Get(c, "lima"); err != nil {
		t.Errorf("Get: %v", err)
	}
}

// The PM gets every tree's state with one grant; a coding agent gets its own
// tree and state only.
func TestTreeGrantsScope(t *testing.T) {
	testutil.IsolateHome(t)
	base := t.TempDir()
	a := filepath.Join(base, treeA)
	b := filepath.Join(base, treeB)
	for _, root := range []string{a, b} {
		if err := (&Meta{Name: filepath.Base(root), Root: root}).Save(root); err != nil {
			t.Fatal(err)
		}
	}
	g := TreeGrants(&Config{}, &Instance{Name: treeA, Root: a})
	allow := strings.Join(g.Allow, "\n")
	for _, want := range []string{a, StateDir(a)} {
		if !strings.Contains(allow, want) {
			t.Errorf("Allow = %v, want %s", g.Allow, want)
		}
	}
	for _, deny := range []string{b, StateDir(b), PMDir(), TreesStateDir(), StateRoot()} {
		for _, p := range g.Allow {
			if p == deny {
				t.Errorf("a coding agent is granted %s", deny)
			}
		}
	}
}

func TestOldLayout(t *testing.T) {
	home := testutil.IsolateHome(t)
	if OldLayout() {
		t.Error("fresh home reads as the old layout")
	}
	if err := os.MkdirAll(filepath.Join(home, ".supatree"), 0755); err != nil {
		t.Fatal(err)
	}
	if !OldLayout() {
		t.Error("~/.supatree with no XDG config is the old layout")
	}
	if err := DefaultConfig().Save(); err != nil {
		t.Fatal(err)
	}
	if OldLayout() {
		t.Error("once the XDG config exists the old dir is a leftover, not the layout")
	}
}

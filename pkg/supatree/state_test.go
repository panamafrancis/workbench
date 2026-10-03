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
	root := filepath.Join(t.TempDir(), treeLima)
	if err := (&Meta{Name: treeLima, Root: root}).Save(root); err != nil {
		t.Fatal(err)
	}
	dir := StateDir(root)
	if want := filepath.Join(TreesStateDir(), treeLima); dir != want {
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

// Discovery comes from the state dirs, and a tree is where the trees base says
// — never where its own meta.yml says, which its agents can write.
func TestListFindsTreesByState(t *testing.T) {
	testutil.IsolateHome(t)
	base := t.TempDir()
	root := filepath.Join(base, treeLima)
	elsewhere := t.TempDir()
	if err := (&Meta{Name: treeLima, Root: elsewhere}).Save(root); err != nil {
		t.Fatal(err)
	}
	if err := SaveSpec(root, &Spec{}); err != nil {
		t.Fatal(err)
	}
	c := &Config{TreesBase: base}
	insts, err := List(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(insts) != 1 || insts[0].Root != root {
		t.Fatalf("List = %+v, want lima at %s (meta.Root %s must be ignored)", insts, root, elsewhere)
	}
	if inst, err := Get(c, treeLima); err != nil || inst.Root != root {
		t.Errorf("Get = %+v, %v", inst, err)
	}
}

// A rewritten .supatree link changes nothing: state is derived from the name.
func TestStateDirIgnoresTheLink(t *testing.T) {
	testutil.IsolateHome(t)
	root := filepath.Join(t.TempDir(), treeLima)
	if err := (&Meta{Name: treeLima}).Save(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(StateLink(root)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(TreesStateDir(), "other"), StateLink(root)); err != nil {
		t.Fatal(err)
	}
	if got := StateDir(root); got != filepath.Join(TreesStateDir(), treeLima) {
		t.Errorf("StateDir followed a rewritten link to %s", got)
	}
}

// Aliases are held to the name charset: one that climbs out of repos/ would
// put an unsandboxed RemoveAll anywhere.
func TestSpecRejectsPathAliases(t *testing.T) {
	for _, bad := range []string{"members:\n  ../../x: /tmp/r\n", "members:\n  a: /tmp/r\ndeps:\n  a: [../b]\n"} {
		if _, err := parseSpec([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
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

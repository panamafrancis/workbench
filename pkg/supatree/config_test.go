package supatree

import (
	"testing"
	"time"
)

// Member aliases used by the fixtures throughout this package's tests.
const (
	aliasAdmin     = "admin"
	aliasKeystone  = "keystone"
	aliasTerraform = "terraform"
)

func TestConfigRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := DefaultConfig()
	c.DefaultModel = "claude"
	if err := AddStack("mystack", "/some/path"); err != nil {
		t.Fatalf("AddStack() error = %v", err)
	}
	// AddStack persisted a fresh config; reload and check.
	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if s := got.FindStack("mystack"); s == nil {
		t.Fatal("stack not persisted")
	}
	if got.FindStack("mystack").Path != "/some/path" {
		t.Errorf("stack path = %q, want /some/path", got.FindStack("mystack").Path)
	}
}

func TestAddStackIdempotentSamePath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := AddStack("s", "/p"); err != nil {
		t.Fatal(err)
	}
	if err := AddStack("s", "/p"); err != nil {
		t.Errorf("re-adding same stack should be a no-op, got %v", err)
	}
	c, _ := Load()
	if len(c.Stacks) != 1 {
		t.Errorf("stacks = %d, want 1", len(c.Stacks))
	}
}

func TestAddStackConflictingPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_ = AddStack("s", "/p")
	if err := AddStack("s", "/other"); err == nil {
		t.Error("re-adding with a different path should error")
	}
}

func TestMetaRoundTrip(t *testing.T) {
	root := t.TempDir()
	when := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	m := &Meta{Name: "berlin", Slug: "berlin", Stack: "s", Model: "claude", CreatedAt: when}
	if err := m.Save(root); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := LoadMeta(root)
	if err != nil {
		t.Fatalf("LoadMeta() error = %v", err)
	}
	if got.Name != "berlin" || got.Slug != "berlin" || got.Model != "claude" {
		t.Errorf("meta round-trip mismatch: %+v", got)
	}
	if got.MemberBranch(aliasTerraform) != "st/berlin/terraform" {
		t.Errorf("MemberBranch = %q", got.MemberBranch(aliasTerraform))
	}
}

func TestSpecOrderedMembers(t *testing.T) {
	root := t.TempDir()
	spec := &Spec{
		Members: []string{aliasAdmin, aliasKeystone, aliasTerraform},
		Deps:    map[string][]string{aliasKeystone: {aliasTerraform}, aliasAdmin: {aliasKeystone}},
	}
	if err := SaveSpec(root, spec); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSpec(root)
	if err != nil {
		t.Fatal(err)
	}
	ordered, err := got.OrderedMembers()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{aliasTerraform, aliasKeystone, aliasAdmin}
	for i := range want {
		if ordered[i] != want[i] {
			t.Fatalf("OrderedMembers() = %v, want %v", ordered, want)
		}
	}
}

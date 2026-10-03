package supatree

import (
	"strings"
	"testing"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/testutil"
)

func TestConfigRoundTrip(t *testing.T) {
	testutil.IsolateHome(t)
	c := DefaultConfig()
	c.DefaultModel = defaultModelKey
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
	testutil.IsolateHome(t)
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
	testutil.IsolateHome(t)
	_ = AddStack("s", "/p")
	if err := AddStack("s", "/other"); err == nil {
		t.Error("re-adding with a different path should error")
	}
}

func TestMetaRoundTrip(t *testing.T) {
	testutil.IsolateHome(t)
	root := t.TempDir()
	when := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	m := &Meta{Name: treeBerlin, Slug: treeBerlin, Stack: "s", Model: defaultModelKey, CreatedAt: when}
	if err := m.Save(root); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := LoadMeta(root)
	if err != nil {
		t.Fatalf("LoadMeta() error = %v", err)
	}
	if got.Name != treeBerlin || got.Slug != treeBerlin || got.Model != defaultModelKey {
		t.Errorf("meta round-trip mismatch: %+v", got)
	}
	if got.MemberBranch(aliasTerraform) != "st/berlin/terraform" {
		t.Errorf("MemberBranch = %q", got.MemberBranch(aliasTerraform))
	}
}

func TestSpecOrderedMembers(t *testing.T) {
	testutil.IsolateHome(t)
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

// Supatree's own models win; with none, workbench's (the migration bridge);
// with neither, the built-in defaults. A model nobody defines names the file
// to add it to.
func TestModelResolutionOrder(t *testing.T) {
	own := &Config{Models: map[string]config.Model{"mine": {Binary: "mine"}}}
	if m, err := own.Model("mine"); err != nil || m.Binary != "mine" {
		t.Errorf("own model = %+v, %v", m, err)
	}
	if _, err := own.Model(defaultModelKey); err == nil {
		t.Error("a supatree config with its own models must not fall through to the defaults")
	}

	bridged := &Config{legacy: &config.Config{DefaultModel: "wbm", Models: map[string]config.Model{"wbm": {Binary: "wb"}}}}
	if got := bridged.ResolveModel(""); got != "wbm" {
		t.Errorf("ResolveModel with only workbench's config = %q, want its default", got)
	}
	if m, err := bridged.Model("wbm"); err != nil || m.Binary != "wb" {
		t.Errorf("bridged model = %+v, %v", m, err)
	}

	bare := &Config{}
	if got := bare.ResolveModel(""); got != defaultModelKey {
		t.Errorf("ResolveModel with nothing configured = %q, want claude", got)
	}
	if m, err := bare.Model(defaultModelKey); err != nil || m.Binary != defaultModelKey {
		t.Errorf("default model = %+v, %v", m, err)
	}
	if _, err := bare.Model("nosuch"); err == nil || !strings.Contains(err.Error(), "models:") {
		t.Errorf("unknown model error = %v, want one naming the models section", err)
	}
}

// Supatree must run with no workbench config at all.
func TestLoadWithoutWorkbench(t *testing.T) {
	testutil.IsolateHome(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.legacy != nil {
		t.Error("loaded a workbench config that does not exist")
	}
	if _, err := c.baseClone("anything"); err == nil {
		t.Error("resolved a member with no source for it")
	}
}

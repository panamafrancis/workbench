package tui

import (
	"strings"
	"testing"

	"github.com/panamafrancis/workbench/pkg/config"
	"github.com/panamafrancis/workbench/pkg/supatree"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

func TestViewEmptyDoesNotPanic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New(supatree.DefaultConfig(), config.DefaultConfig(), zellij.Workspace{})
	out := m.View()
	if !strings.Contains(out, "supatree") {
		t.Errorf("view missing header:\n%s", out)
	}
	if !strings.Contains(out, "no supatrees") {
		t.Errorf("empty view should prompt to create one:\n%s", out)
	}
}

func TestRebuildRowsStructure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New(supatree.DefaultConfig(), config.DefaultConfig(), zellij.Workspace{})
	// Inject a synthetic instance and rebuild.
	m.insts = []*supatree.Instance{{
		Name: "berlin",
		Slug: "berlin",
		Members: []supatree.Member{
			{Alias: "terraform", Branch: "st/berlin/terraform"},
			{Alias: "keystone", Branch: "st/berlin/keystone"},
		},
	}}
	m.rebuildRows()
	// Expect: tree, "agents" subheader, main agent, "repositories" subheader, 2 members.
	kinds := make([]rowKind, 0, len(m.rows))
	for _, r := range m.rows {
		kinds = append(kinds, r.kind)
	}
	want := []rowKind{rowTree, rowSubheader, rowAgent, rowSubheader, rowMember, rowMember}
	if len(kinds) != len(want) {
		t.Fatalf("rows = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("row %d kind = %v, want %v", i, kinds[i], want[i])
		}
	}
	// Cursor must never rest on a subheader.
	m.cursor = 1
	m.clampCursor()
	if m.rows[m.cursor].kind == rowSubheader {
		t.Error("cursor rested on subheader after clamp")
	}
}

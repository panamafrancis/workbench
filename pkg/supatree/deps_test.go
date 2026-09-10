package supatree

import (
	"reflect"
	"testing"
)

func TestTopoSortOrder(t *testing.T) {
	nodes := []string{aliasAdmin, aliasKeystone, aliasTerraform}
	deps := map[string][]string{
		aliasKeystone: {aliasTerraform},
		aliasAdmin:    {aliasKeystone},
	}
	got, err := TopoSort(nodes, deps)
	if err != nil {
		t.Fatalf("TopoSort() error = %v", err)
	}
	want := []string{aliasTerraform, aliasKeystone, aliasAdmin}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TopoSort() = %v, want %v", got, want)
	}
}

func TestTopoSortDeterministicTieBreak(t *testing.T) {
	// No edges: ties resolve alphabetically, regardless of input order.
	got, err := TopoSort([]string{"charlie", "alpha", "bravo"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "bravo", "charlie"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TopoSort() = %v, want %v", got, want)
	}
}

func TestTopoSortCycle(t *testing.T) {
	deps := map[string][]string{
		"a": {"b"},
		"b": {"a"},
	}
	if _, err := TopoSort([]string{"a", "b"}, deps); err == nil {
		t.Fatal("TopoSort() with cycle = nil error, want error")
	}
}

func TestTopoSortIgnoresOutOfSetDeps(t *testing.T) {
	// A dependency on a node not in the set must not block ordering.
	got, err := TopoSort([]string{"a"}, map[string][]string{"a": {"ghost"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("TopoSort() = %v, want [a]", got)
	}
}

func TestTopoSortSelfDepIsNotCycle(t *testing.T) {
	got, err := TopoSort([]string{"a"}, map[string][]string{"a": {"a"}})
	if err != nil {
		t.Fatalf("self-dep treated as cycle: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("TopoSort() = %v, want [a]", got)
	}
}

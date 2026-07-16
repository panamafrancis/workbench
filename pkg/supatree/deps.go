package supatree

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// TopoSort returns nodes ordered so that every dependency precedes the nodes
// that depend on it (Kahn's algorithm). Ties are broken alphabetically for
// deterministic output. deps maps a node to the nodes it depends on; edges that
// reference nodes outside the input set are ignored. Returns an error naming the
// cycle if the dependency graph is not acyclic.
func TopoSort(nodes []string, deps map[string][]string) ([]string, error) {
	set := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		set[n] = true
	}

	// indegree[n] = number of in-set dependencies n still waits on.
	indegree := make(map[string]int, len(nodes))
	// dependents[d] = nodes that depend on d.
	dependents := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		indegree[n] = 0
	}
	for _, n := range nodes {
		seen := make(map[string]bool)
		for _, d := range deps[n] {
			if !set[d] || d == n || seen[d] {
				continue
			}
			seen[d] = true
			indegree[n]++
			dependents[d] = append(dependents[d], n)
		}
	}

	var ready []string
	for _, n := range nodes {
		if indegree[n] == 0 {
			ready = append(ready, n)
		}
	}
	sort.Strings(ready)

	out := make([]string, 0, len(nodes))
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		out = append(out, n)
		next := dependents[n]
		sort.Strings(next)
		for _, m := range next {
			indegree[m]--
			if indegree[m] == 0 {
				ready = append(ready, m)
				sort.Strings(ready)
			}
		}
	}

	if len(out) != len(nodes) {
		var cyclic []string
		for _, n := range nodes {
			if !slices.Contains(out, n) {
				cyclic = append(cyclic, n)
			}
		}
		sort.Strings(cyclic)
		return nil, fmt.Errorf("dependency cycle among: %s", strings.Join(cyclic, ", "))
	}
	return out, nil
}

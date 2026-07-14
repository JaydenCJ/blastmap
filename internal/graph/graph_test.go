// Tests for the dependency graph traversals. Chains are the user-facing
// "why" evidence, so their exact shape (endpoints, direction, shortest
// length, deterministic tie-breaks) is asserted, not just membership.
package graph

import (
	"reflect"
	"testing"
)

// build wires edges given as "from->to" pairs.
func build(edges ...[2]string) *Graph {
	g := New()
	for _, e := range edges {
		g.AddEdge(e[0], e[1])
	}
	return g
}

func TestDependentsLinearChainAndMultipleSeeds(t *testing.T) {
	// web -> ui -> utils; a change in utils affects both.
	g := build([2]string{"web", "ui"}, [2]string{"ui", "utils"})
	got := g.Dependents([]string{"utils"})
	want := map[string][]string{
		"ui":  {"ui", "utils"},
		"web": {"web", "ui", "utils"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// Multiple seeds propagate in one BFS, each chain ending at its
	// own seed.
	g2 := build([2]string{"x", "s1"}, [2]string{"y", "s2"})
	got2 := g2.Dependents([]string{"s1", "s2"})
	if len(got2) != 2 {
		t.Fatalf("want 2 dependents, got %v", got2)
	}
	if got2["x"][len(got2["x"])-1] != "s1" || got2["y"][len(got2["y"])-1] != "s2" {
		t.Fatalf("chains must end at their own seed: %v", got2)
	}
}

func TestDependentsExcludesSeedsAndIsolatedNodes(t *testing.T) {
	g := build([2]string{"a", "b"})
	if _, ok := g.Dependents([]string{"b"})["b"]; ok {
		t.Fatal("seed must not appear in its own dependents")
	}
	lone := New()
	lone.AddNode("lonely")
	if got := lone.Dependents([]string{"lonely"}); len(got) != 0 {
		t.Fatalf("isolated node should affect nothing, got %v", got)
	}
}

func TestDependentsDiamondUsesShortestChain(t *testing.T) {
	// top depends on both left and right, both depend on base. The chain
	// for top must have length 3 (one hop through either side), never 4.
	g := build(
		[2]string{"top", "left"}, [2]string{"top", "right"},
		[2]string{"left", "base"}, [2]string{"right", "base"},
	)
	got := g.Dependents([]string{"base"})
	if len(got["top"]) != 3 {
		t.Fatalf("diamond chain should be 3 long, got %v", got["top"])
	}
	// Sorted neighbor expansion makes "left" the deterministic winner.
	if got["top"][1] != "left" {
		t.Fatalf("tie should break alphabetically, got %v", got["top"])
	}
}

func TestDependentsCycleTerminates(t *testing.T) {
	// a <-> b cycle plus c -> a; must not loop forever and must not
	// fabricate infinite chains.
	g := build([2]string{"a", "b"}, [2]string{"b", "a"}, [2]string{"c", "a"})
	got := g.Dependents([]string{"a"})
	if !reflect.DeepEqual(got["b"], []string{"b", "a"}) {
		t.Fatalf("cycle member chain wrong: %v", got["b"])
	}
	if !reflect.DeepEqual(got["c"], []string{"c", "a"}) {
		t.Fatalf("chain through cycle wrong: %v", got["c"])
	}
}

func TestDependenciesChainReadsSeedToNode(t *testing.T) {
	g := build([2]string{"web", "ui"}, [2]string{"ui", "utils"})
	got := g.Dependencies([]string{"web"})
	want := map[string][]string{
		"ui":    {"web", "ui"},
		"utils": {"web", "ui", "utils"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// Seeds never list themselves, even inside a cycle.
	g2 := build([2]string{"a", "b"}, [2]string{"b", "a"})
	if got := g2.Dependencies([]string{"a", "b"}); len(got) != 0 {
		t.Fatalf("all nodes are seeds, want empty, got %v", got)
	}
}

func TestDeterministicAccessorsAndDuplicateEdges(t *testing.T) {
	g := build([2]string{"z", "m"}, [2]string{"z", "a"}, [2]string{"b", "z"})
	g.AddEdge("z", "m") // duplicate must collapse
	if got := g.Nodes(); !reflect.DeepEqual(got, []string{"a", "b", "m", "z"}) {
		t.Fatalf("Nodes not sorted: %v", got)
	}
	if got := g.DepsOf("z"); !reflect.DeepEqual(got, []string{"a", "m"}) {
		t.Fatalf("DepsOf not sorted/deduplicated: %v", got)
	}
}

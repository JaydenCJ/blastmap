// Package graph implements the internal dependency graph and the two
// traversals blastmap needs: transitive dependents (who is affected by a
// change) and transitive dependencies (what an affected package needs).
// Nodes are opaque string keys; the graph is cycle-safe and every result
// is deterministically ordered.
package graph

import "sort"

// Graph is a directed dependency graph. An edge A->B means "A depends on B",
// so a change in B affects A.
type Graph struct {
	deps       map[string][]string // node -> nodes it depends on
	dependents map[string][]string // node -> nodes that depend on it (reverse)
	nodes      []string
	seen       map[string]bool
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{
		deps:       map[string][]string{},
		dependents: map[string][]string{},
		seen:       map[string]bool{},
	}
}

// AddNode registers a node. Adding the same node twice is a no-op.
func (g *Graph) AddNode(key string) {
	if g.seen[key] {
		return
	}
	g.seen[key] = true
	g.nodes = append(g.nodes, key)
}

// AddEdge records that `from` depends on `to`. Unknown endpoints are
// registered implicitly; duplicate edges are collapsed.
func (g *Graph) AddEdge(from, to string) {
	g.AddNode(from)
	g.AddNode(to)
	for _, d := range g.deps[from] {
		if d == to {
			return
		}
	}
	g.deps[from] = append(g.deps[from], to)
	g.dependents[to] = append(g.dependents[to], from)
}

// Nodes returns all node keys in sorted order.
func (g *Graph) Nodes() []string {
	out := append([]string(nil), g.nodes...)
	sort.Strings(out)
	return out
}

// DepsOf returns the direct dependencies of a node, sorted.
func (g *Graph) DepsOf(key string) []string {
	out := append([]string(nil), g.deps[key]...)
	sort.Strings(out)
	return out
}

// Dependents runs a multi-source BFS from the seed nodes over reverse
// edges and returns, for every transitively affected node NOT in the seed
// set, the shortest chain from that node down to a seed:
//
//	chain = [node, intermediate..., seed]
//
// Chains are the "why is this package affected" evidence in reports. BFS
// guarantees minimal length; neighbor ordering is sorted so ties break
// deterministically. Cycles terminate naturally via the visited set.
func (g *Graph) Dependents(seeds []string) map[string][]string {
	visited := map[string]bool{}
	parent := map[string]string{} // node -> the dependency that pulled it in
	queue := append([]string(nil), seeds...)
	sort.Strings(queue)
	for _, s := range queue {
		visited[s] = true
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		next := append([]string(nil), g.dependents[cur]...)
		sort.Strings(next)
		for _, n := range next {
			if visited[n] {
				continue
			}
			visited[n] = true
			parent[n] = cur
			queue = append(queue, n)
		}
	}
	out := map[string][]string{}
	seedSet := map[string]bool{}
	for _, s := range seeds {
		seedSet[s] = true
	}
	for node := range visited {
		if seedSet[node] {
			continue
		}
		chain := []string{node}
		for cur := node; ; {
			p, ok := parent[cur]
			if !ok {
				break
			}
			chain = append(chain, p)
			cur = p
		}
		out[node] = chain
	}
	return out
}

// Dependencies returns every transitive dependency of the seed nodes that
// is not itself a seed, mapped to the shortest chain from a seed down to
// it: chain = [seed, intermediate..., node]. Used by --with-deps to list
// what must be built alongside the affected set.
func (g *Graph) Dependencies(seeds []string) map[string][]string {
	visited := map[string]bool{}
	parent := map[string]string{}
	queue := append([]string(nil), seeds...)
	sort.Strings(queue)
	for _, s := range queue {
		visited[s] = true
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		next := append([]string(nil), g.deps[cur]...)
		sort.Strings(next)
		for _, n := range next {
			if visited[n] {
				continue
			}
			visited[n] = true
			parent[n] = cur
			queue = append(queue, n)
		}
	}
	out := map[string][]string{}
	seedSet := map[string]bool{}
	for _, s := range seeds {
		seedSet[s] = true
	}
	for node := range visited {
		if seedSet[node] {
			continue
		}
		chain := []string{node}
		for cur := node; ; {
			p, ok := parent[cur]
			if !ok {
				break
			}
			chain = append(chain, p)
			cur = p
		}
		// Reverse so the chain reads seed -> ... -> node.
		for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
			chain[i], chain[j] = chain[j], chain[i]
		}
		out[node] = chain
	}
	return out
}

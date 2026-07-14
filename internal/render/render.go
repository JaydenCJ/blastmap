// Package render turns impact results and workspaces into the four output
// surfaces: human text, machine `lines` (one target per line, xargs-ready),
// stable JSON (schema_version 1), and Graphviz dot for the graph view.
// Every renderer is deterministic: identical input, byte-identical output.
package render

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/JaydenCJ/blastmap/internal/impact"
	"github.com/JaydenCJ/blastmap/internal/workspace"
)

// Meta carries report context that is not part of the computation itself.
type Meta struct {
	Source string // "main..HEAD", "uncommitted", "stdin", …
}

// ecosystems joins the workspace's ecosystems for display.
func ecosystems(ws *workspace.Workspace) string {
	parts := make([]string, len(ws.Ecosystems))
	for i, e := range ws.Ecosystems {
		parts[i] = string(e)
	}
	return strings.Join(parts, "+")
}

// plural is the tiny grammar helper every report needs.
func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// Lines prints one affected target per line, sorted, for shell pipelines.
// With paths=true it prints workspace-relative directories instead of
// package names.
func Lines(w io.Writer, res impact.Result, paths bool) {
	seen := map[string]bool{}
	var out []string
	for _, e := range res.Entries {
		v := e.Pkg.Name
		if paths {
			v = e.Pkg.Dir
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	for _, v := range out {
		fmt.Fprintln(w, v)
	}
}

// ListLines prints every workspace package, for `list --format lines`.
func ListLines(w io.Writer, ws *workspace.Workspace, paths bool) {
	var out []string
	for _, p := range ws.Packages {
		if paths {
			out = append(out, p.Dir)
		} else {
			out = append(out, p.Name)
		}
	}
	sort.Strings(out)
	for _, v := range out {
		fmt.Fprintln(w, v)
	}
}

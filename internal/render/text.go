// text.go renders the human-facing reports: the affected view with its
// grouped sections and evidence chains, the package list, and the text
// form of the dependency graph.
package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/JaydenCJ/blastmap/internal/graph"
	"github.com/JaydenCJ/blastmap/internal/impact"
	"github.com/JaydenCJ/blastmap/internal/workspace"
)

// Text renders the affected report for humans.
func Text(w io.Writer, ws *workspace.Workspace, res impact.Result, meta Meta) {
	fmt.Fprintf(w, "blastmap affected — %s, %s changed\n", meta.Source, plural(res.Changed, "file"))
	fmt.Fprintf(w, "workspace: %s (%s)\n", ecosystems(ws), plural(res.Total, "package"))

	nameW, dirW := 0, 0
	for _, e := range res.Entries {
		nameW = max(nameW, len(e.Pkg.Name))
		dirW = max(dirW, len(e.Pkg.Dir))
	}

	section := func(status impact.Status, title string) {
		printed := false
		for _, e := range res.Entries {
			if e.Status != status {
				continue
			}
			if !printed {
				fmt.Fprintf(w, "\n%s\n", title)
				printed = true
			}
			detail := ""
			switch status {
			case impact.StatusChanged:
				detail = plural(len(e.Files), "file")
			case impact.StatusDependent, impact.StatusDependency:
				detail = "via " + strings.Join(e.Via, " -> ")
			case impact.StatusGlobal:
				detail = "via " + strings.Join(e.Via, ", ")
			}
			fmt.Fprintf(w, "  %-*s  %-*s  %s\n", nameW, e.Pkg.Name, dirW, e.Pkg.Dir, detail)
		}
	}
	section(impact.StatusChanged, "changed")
	section(impact.StatusDependent, "dependent")
	section(impact.StatusGlobal, "global blast")
	section(impact.StatusDependency, "dependency (--with-deps)")

	if len(res.Unclaimed) > 0 {
		fmt.Fprintf(w, "\nunclaimed (%s owned by no package)\n", plural(len(res.Unclaimed), "file"))
		for _, f := range res.Unclaimed {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}
	if len(res.Outside) > 0 {
		fmt.Fprintf(w, "\noutside workspace (%s)\n", plural(len(res.Outside), "file"))
		for _, f := range res.Outside {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}

	fmt.Fprintf(w, "\n%d of %d packages affected\n", len(res.Entries), res.Total)
}

// ListText renders the discovered packages as an aligned table.
func ListText(w io.Writer, ws *workspace.Workspace) {
	fmt.Fprintf(w, "blastmap list — %s, %s\n\n", ecosystems(ws), plural(len(ws.Packages), "package"))
	nameW, ecoW, dirW := len("name"), len("ecosystem"), len("directory")
	for _, p := range ws.Packages {
		nameW = max(nameW, len(p.Name))
		ecoW = max(ecoW, len(string(p.Ecosystem)))
		dirW = max(dirW, len(p.Dir))
	}
	fmt.Fprintf(w, "%-*s  %-*s  %-*s  %s\n", nameW, "name", ecoW, "ecosystem", dirW, "directory", "internal deps")
	for _, p := range ws.Packages {
		n := len(p.Deps) + len(p.DevDeps)
		fmt.Fprintf(w, "%-*s  %-*s  %-*s  %d\n", nameW, p.Name, ecoW, string(p.Ecosystem), dirW, p.Dir, n)
	}
	if len(ws.GlobalFiles) > 0 {
		fmt.Fprintf(w, "\nglobal files: %s\n", strings.Join(ws.GlobalFiles, ", "))
	}
}

// GraphText renders one `name -> dep, dep` line per package.
func GraphText(w io.Writer, ws *workspace.Workspace, g *graph.Graph) {
	for _, key := range g.Nodes() {
		p := ws.PackageByKey(key)
		if p == nil {
			continue
		}
		deps := g.DepsOf(key)
		names := make([]string, len(deps))
		for i, d := range deps {
			if dp := ws.PackageByKey(d); dp != nil {
				names[i] = dp.Name
			} else {
				names[i] = d
			}
		}
		if len(names) == 0 {
			fmt.Fprintf(w, "%s (no internal deps)\n", p.Name)
			continue
		}
		fmt.Fprintf(w, "%s -> %s\n", p.Name, strings.Join(names, ", "))
	}
}

// dotEscape makes a string safe inside a double-quoted Graphviz ID.
// Only backslash and double quote need escaping; anything else (including
// scoped npm names like @demo/web) is legal verbatim.
func dotEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

// Dot renders the graph in Graphviz syntax, one subgraph per ecosystem.
func Dot(w io.Writer, ws *workspace.Workspace, g *graph.Graph) {
	fmt.Fprintln(w, "digraph blastmap {")
	fmt.Fprintln(w, "  rankdir=LR;")
	fmt.Fprintln(w, "  node [shape=box, fontname=\"monospace\"];")
	for _, key := range g.Nodes() {
		p := ws.PackageByKey(key)
		if p == nil {
			continue
		}
		// `\n` must reach Graphviz as a single backslash + n: that is the dot
		// escape for a line break inside a label. Go's %q would double the
		// backslash and the label would render literally.
		fmt.Fprintf(w, "  \"%s\" [label=\"%s\\n%s\"];\n",
			dotEscape(key), dotEscape(p.Name), dotEscape(p.Dir))
	}
	for _, key := range g.Nodes() {
		for _, d := range g.DepsOf(key) {
			fmt.Fprintf(w, "  %q -> %q;\n", key, d)
		}
	}
	fmt.Fprintln(w, "}")
}

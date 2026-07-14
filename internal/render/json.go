// json.go renders the machine-readable envelope. The schema is versioned
// (schema_version) and documented in the README; field order is fixed by
// the struct definitions so output is byte-stable.
package render

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/JaydenCJ/blastmap/internal/graph"
	"github.com/JaydenCJ/blastmap/internal/impact"
	"github.com/JaydenCJ/blastmap/internal/version"
	"github.com/JaydenCJ/blastmap/internal/workspace"
)

// jsonEnvelope is the top-level affected document.
type jsonEnvelope struct {
	Tool          string        `json:"tool"`
	Version       string        `json:"version"`
	SchemaVersion int           `json:"schema_version"`
	Source        string        `json:"source"`
	Workspace     jsonWorkspace `json:"workspace"`
	ChangedFiles  int           `json:"changed_files"`
	Affected      []jsonEntry   `json:"affected"`
	GlobalHits    []string      `json:"global_hits"`
	Unclaimed     []string      `json:"unclaimed_files"`
	Outside       []string      `json:"outside_workspace"`
	Counts        jsonCounts    `json:"counts"`
}

type jsonWorkspace struct {
	Ecosystems []string `json:"ecosystems"`
	Packages   int      `json:"packages"`
}

type jsonEntry struct {
	Name      string   `json:"name"`
	Dir       string   `json:"dir"`
	Ecosystem string   `json:"ecosystem"`
	Status    string   `json:"status"`
	Files     []string `json:"files,omitempty"`
	Via       []string `json:"via,omitempty"`
}

type jsonCounts struct {
	Affected   int `json:"affected"`
	Changed    int `json:"changed"`
	Dependent  int `json:"dependent"`
	Global     int `json:"global"`
	Dependency int `json:"dependency"`
}

// JSON renders the affected result.
func JSON(w io.Writer, ws *workspace.Workspace, res impact.Result, meta Meta) error {
	env := jsonEnvelope{
		Tool:          "blastmap",
		Version:       version.Version,
		SchemaVersion: 1,
		Source:        meta.Source,
		Workspace: jsonWorkspace{
			Ecosystems: ecosystemList(ws),
			Packages:   res.Total,
		},
		ChangedFiles: res.Changed,
		Affected:     []jsonEntry{},
		GlobalHits:   emptyNotNil(res.GlobalHits),
		Unclaimed:    emptyNotNil(res.Unclaimed),
		Outside:      emptyNotNil(res.Outside),
	}
	for _, e := range res.Entries {
		env.Affected = append(env.Affected, jsonEntry{
			Name:      e.Pkg.Name,
			Dir:       e.Pkg.Dir,
			Ecosystem: string(e.Pkg.Ecosystem),
			Status:    string(e.Status),
			Files:     e.Files,
			Via:       e.Via,
		})
		switch e.Status {
		case impact.StatusChanged:
			env.Counts.Changed++
		case impact.StatusDependent:
			env.Counts.Dependent++
		case impact.StatusGlobal:
			env.Counts.Global++
		case impact.StatusDependency:
			env.Counts.Dependency++
		}
	}
	env.Counts.Affected = len(res.Entries)
	return writeJSON(w, env)
}

// jsonPackage is one row of the list document.
type jsonPackage struct {
	Name      string   `json:"name"`
	Dir       string   `json:"dir"`
	Ecosystem string   `json:"ecosystem"`
	Deps      []string `json:"deps"`
	DevDeps   []string `json:"dev_deps,omitempty"`
}

// ListJSON renders the discovered workspace.
func ListJSON(w io.Writer, ws *workspace.Workspace) error {
	doc := struct {
		Tool          string        `json:"tool"`
		Version       string        `json:"version"`
		SchemaVersion int           `json:"schema_version"`
		Ecosystems    []string      `json:"ecosystems"`
		GlobalFiles   []string      `json:"global_files"`
		Packages      []jsonPackage `json:"packages"`
	}{
		Tool:          "blastmap",
		Version:       version.Version,
		SchemaVersion: 1,
		Ecosystems:    ecosystemList(ws),
		GlobalFiles:   emptyNotNil(ws.GlobalFiles),
		Packages:      []jsonPackage{},
	}
	for _, p := range ws.Packages {
		doc.Packages = append(doc.Packages, jsonPackage{
			Name:      p.Name,
			Dir:       p.Dir,
			Ecosystem: string(p.Ecosystem),
			Deps:      emptyNotNil(p.Deps),
			DevDeps:   p.DevDeps,
		})
	}
	return writeJSON(w, doc)
}

// GraphJSON renders nodes and edges for external tooling.
func GraphJSON(w io.Writer, ws *workspace.Workspace, g *graph.Graph) error {
	type edge struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	doc := struct {
		Tool          string   `json:"tool"`
		Version       string   `json:"version"`
		SchemaVersion int      `json:"schema_version"`
		Nodes         []string `json:"nodes"`
		Edges         []edge   `json:"edges"`
	}{
		Tool:          "blastmap",
		Version:       version.Version,
		SchemaVersion: 1,
		Nodes:         []string{},
		Edges:         []edge{},
	}
	for _, key := range g.Nodes() {
		p := ws.PackageByKey(key)
		if p == nil {
			continue
		}
		doc.Nodes = append(doc.Nodes, p.Name)
		for _, d := range g.DepsOf(key) {
			if dp := ws.PackageByKey(d); dp != nil {
				doc.Edges = append(doc.Edges, edge{From: p.Name, To: dp.Name})
			}
		}
	}
	return writeJSON(w, doc)
}

// ecosystemList converts the typed slice for JSON.
func ecosystemList(ws *workspace.Workspace) []string {
	out := make([]string, len(ws.Ecosystems))
	for i, e := range ws.Ecosystems {
		out[i] = string(e)
	}
	return out
}

// emptyNotNil keeps empty arrays as [] instead of null in JSON.
func emptyNotNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// writeJSON marshals with two-space indent and a trailing newline.
func writeJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

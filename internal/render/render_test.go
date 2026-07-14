// Tests for the renderers: byte-stable output, correct grouping, and the
// JSON schema contract that CI scripts depend on.
package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JaydenCJ/blastmap/internal/impact"
	"github.com/JaydenCJ/blastmap/internal/workspace"
)

func testWS() *workspace.Workspace {
	return &workspace.Workspace{
		Root:       "/ws",
		Ecosystems: []workspace.Ecosystem{workspace.NPM},
		Packages: []*workspace.Package{
			{Name: "ui", Dir: "packages/ui", Ecosystem: workspace.NPM, Deps: []string{"utils"}},
			{Name: "utils", Dir: "packages/utils", Ecosystem: workspace.NPM},
			{Name: "web", Dir: "apps/web", Ecosystem: workspace.NPM, Deps: []string{"ui"}},
		},
		GlobalFiles: []string{"package.json"},
	}
}

func testResult(ws *workspace.Workspace) impact.Result {
	return impact.Compute(ws, []string{
		"packages/utils/src/a.js", "docs/notes.md",
	}, impact.Options{})
}

func TestTextReportSectionsAndDeterminism(t *testing.T) {
	ws := testWS()
	var buf bytes.Buffer
	Text(&buf, ws, testResult(ws), Meta{Source: "main..HEAD"})
	out := buf.String()
	for _, want := range []string{
		"blastmap affected — main..HEAD, 2 files changed",
		"workspace: npm (3 packages)",
		"changed\n",
		"dependent\n",
		"via web -> ui -> utils",
		"unclaimed (1 file owned by no package)",
		"docs/notes.md",
		"3 of 3 packages affected",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("text report missing %q:\n%s", want, out)
		}
	}
	var again bytes.Buffer
	Text(&again, ws, testResult(ws), Meta{Source: "main..HEAD"})
	if again.String() != out {
		t.Fatal("identical input must render byte-identically")
	}
}

func TestLinesSortedUniqueAndPaths(t *testing.T) {
	ws := testWS()
	var buf bytes.Buffer
	Lines(&buf, testResult(ws), false)
	if got := buf.String(); got != "ui\nutils\nweb\n" {
		t.Fatalf("lines = %q", got)
	}
	buf.Reset()
	Lines(&buf, testResult(ws), true)
	if got := buf.String(); got != "apps/web\npackages/ui\npackages/utils\n" {
		t.Fatalf("paths = %q", got)
	}
}

func TestJSONSchemaContract(t *testing.T) {
	ws := testWS()
	var buf bytes.Buffer
	if err := JSON(&buf, ws, testResult(ws), Meta{Source: "stdin"}); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Tool          string `json:"tool"`
		SchemaVersion int    `json:"schema_version"`
		Source        string `json:"source"`
		Affected      []struct {
			Name   string   `json:"name"`
			Status string   `json:"status"`
			Via    []string `json:"via"`
		} `json:"affected"`
		Unclaimed []string `json:"unclaimed_files"`
		Counts    struct {
			Affected  int `json:"affected"`
			Changed   int `json:"changed"`
			Dependent int `json:"dependent"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if doc.Tool != "blastmap" || doc.SchemaVersion != 1 || doc.Source != "stdin" {
		t.Fatalf("envelope wrong: %+v", doc)
	}
	if doc.Counts.Affected != 3 || doc.Counts.Changed != 1 || doc.Counts.Dependent != 2 {
		t.Fatalf("counts wrong: %+v", doc.Counts)
	}
	if doc.Affected[0].Name != "utils" || doc.Affected[0].Status != "changed" {
		t.Fatalf("first entry should be the changed package: %+v", doc.Affected[0])
	}
}

func TestJSONEmptyArraysNotNull(t *testing.T) {
	ws := testWS()
	var buf bytes.Buffer
	res := impact.Compute(ws, nil, impact.Options{})
	if err := JSON(&buf, ws, res, Meta{Source: "x"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, key := range []string{`"affected": []`, `"unclaimed_files": []`, `"global_hits": []`} {
		if !strings.Contains(out, key) {
			t.Fatalf("empty collections must serialize as [], missing %s:\n%s", key, out)
		}
	}
}

func TestGraphTextAndDot(t *testing.T) {
	ws := testWS()
	g := impact.BuildGraph(ws, false)
	var txt bytes.Buffer
	GraphText(&txt, ws, g)
	for _, want := range []string{"web -> ui", "ui -> utils", "utils (no internal deps)"} {
		if !strings.Contains(txt.String(), want) {
			t.Fatalf("graph text missing %q:\n%s", want, txt.String())
		}
	}
	var dot bytes.Buffer
	Dot(&dot, ws, g)
	for _, want := range []string{"digraph blastmap {", `"npm:web" -> "npm:ui";`, "rankdir=LR"} {
		if !strings.Contains(dot.String(), want) {
			t.Fatalf("dot missing %q:\n%s", want, dot.String())
		}
	}
	// The label line break must reach Graphviz as a SINGLE backslash + n —
	// that is dot's escape for a newline. A doubled backslash renders as
	// literal "\n" text inside the node box.
	if !strings.Contains(dot.String(), `[label="web\napps/web"];`) {
		t.Fatalf("dot label should use \\n as the dot line-break escape:\n%s", dot.String())
	}
	if strings.Contains(dot.String(), `\\n`) {
		t.Fatalf("dot labels must not double-escape the line break:\n%s", dot.String())
	}
}

func TestListTextAndJSON(t *testing.T) {
	ws := testWS()
	var buf bytes.Buffer
	ListText(&buf, ws)
	out := buf.String()
	if !strings.Contains(out, "global files: package.json") {
		t.Fatalf("list should surface global files:\n%s", out)
	}
	if !strings.Contains(out, "blastmap list — npm, 3 packages") {
		t.Fatalf("list header wrong:\n%s", out)
	}
	buf.Reset()
	if err := ListJSON(&buf, ws); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Packages []struct {
			Name string   `json:"name"`
			Deps []string `json:"deps"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Packages) != 3 || doc.Packages[0].Name != "ui" {
		t.Fatalf("list json wrong: %+v", doc.Packages)
	}
}

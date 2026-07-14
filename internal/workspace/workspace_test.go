// Tests for discovery orchestration and file-to-package ownership. The
// per-ecosystem loaders have their own files; this one covers what glues
// them together.
package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeTree materializes files (slash paths -> content) under root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// mixedRepo lays out a repo containing both an npm and a Go workspace.
func mixedRepo(t *testing.T) string {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"package.json":               `{"name":"root","workspaces":["js/*"]}`,
		"js/app/package.json":        `{"name":"app","dependencies":{"lib":"1.0.0"}}`,
		"js/lib/package.json":        `{"name":"lib"}`,
		"go.work":                    "go 1.22\n\nuse (\n\t./svc/api\n\t./svc/core\n)\n",
		"svc/api/go.mod":             "module example.test/api\n\ngo 1.22\n\nrequire example.test/core v0.0.0\n",
		"svc/core/go.mod":            "module example.test/core\n\ngo 1.22\n",
		"js/app/src/index.js":        "x",
		"svc/core/core.go":           "package core",
		"js/lib/nested/package.json": `{"name":"nested"}`, // not a member: js/* only matches depth 1
	})
	return root
}

func TestDiscoverAutoFindsAllEcosystems(t *testing.T) {
	ws, err := Discover(mixedRepo(t), Auto)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ws.Ecosystems, []Ecosystem{GoMod, NPM}) {
		t.Fatalf("ecosystems = %v", ws.Ecosystems)
	}
	var names []string
	for _, p := range ws.Packages {
		names = append(names, p.Name)
	}
	want := []string{"example.test/api", "example.test/core", "app", "lib"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("packages = %v, want %v", names, want)
	}
}

func TestDiscoverRestrictedToOneEcosystem(t *testing.T) {
	ws, err := Discover(mixedRepo(t), NPM)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ws.Ecosystems, []Ecosystem{NPM}) {
		t.Fatalf("ecosystems = %v", ws.Ecosystems)
	}
	if len(ws.Packages) != 2 {
		t.Fatalf("want 2 npm packages, got %d", len(ws.Packages))
	}
}

func TestDiscoverErrorsAreActionable(t *testing.T) {
	// Nothing found: the error lists every manifest it looked for.
	_, err := Discover(t.TempDir(), Auto)
	if err == nil {
		t.Fatal("empty dir should not discover a workspace")
	}
	for _, hint := range []string{"pnpm-workspace.yaml", "go.work", "Cargo.toml"} {
		if !strings.Contains(err.Error(), hint) {
			t.Fatalf("error should mention %s: %v", hint, err)
		}
	}
	// Forcing an ecosystem names its specific manifest.
	root := t.TempDir()
	writeTree(t, root, map[string]string{"go.work": "go 1.22\n"})
	if _, err := Discover(root, Cargo); err == nil || !strings.Contains(err.Error(), "[workspace]") {
		t.Fatalf("forced cargo discovery should name its manifest: %v", err)
	}
	// A missing root directory fails up front.
	if _, err := Discover(filepath.Join(t.TempDir(), "missing"), Auto); err == nil {
		t.Fatal("missing root must fail")
	}
	// And an unknown --ecosystem value is rejected before any I/O.
	for _, ok := range []string{"npm", "go", "cargo", "auto"} {
		if _, err := ParseEcosystem(ok); err != nil {
			t.Fatalf("%s should parse: %v", ok, err)
		}
	}
	if _, err := ParseEcosystem("bazel"); err == nil {
		t.Fatal("unknown ecosystem must be rejected")
	}
}

func TestOwnerPicksDeepestPackage(t *testing.T) {
	// Nested workspaces exist (a package embedding an example package);
	// the deepest directory must win so files map to their true owner.
	ws := &Workspace{Packages: []*Package{
		{Name: "outer", Dir: "packages/outer", Ecosystem: NPM},
		{Name: "inner", Dir: "packages/outer/examples/inner", Ecosystem: NPM},
	}}
	if got := ws.Owner("packages/outer/examples/inner/src/a.js"); got.Name != "inner" {
		t.Fatalf("deepest package should own the file, got %s", got.Name)
	}
	if got := ws.Owner("packages/outer/src/a.js"); got.Name != "outer" {
		t.Fatalf("outer file owned by %s", got.Name)
	}
}

func TestOwnerBoundaries(t *testing.T) {
	ws := &Workspace{Packages: []*Package{{Name: "ui", Dir: "packages/ui"}}}
	if ws.Owner("packages/ui-kit/src/a.js") != nil {
		t.Fatal("packages/ui-kit is not inside packages/ui")
	}
	if ws.Owner("packages/ui") == nil {
		t.Fatal("the package dir itself belongs to the package")
	}
	if ws.Owner("README.md") != nil {
		t.Fatal("root file has no owner")
	}
}

func TestCrossEcosystemNameReuseIsLegal(t *testing.T) {
	// The same name in two ecosystems yields two distinct keys; only a
	// same-ecosystem duplicate (caught by the loaders) is an error.
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"package.json":           `{"workspaces":["js/*"]}`,
		"js/core/package.json":   `{"name":"core"}`,
		"Cargo.toml":             "[workspace]\nmembers = [\"crates/*\"]\n",
		"crates/core/Cargo.toml": "[package]\nname = \"core\"\n",
	})
	ws, err := Discover(root, Auto)
	if err != nil {
		t.Fatalf("cross-ecosystem name reuse should be legal: %v", err)
	}
	if len(ws.Packages) != 2 {
		t.Fatalf("want 2 packages, got %d", len(ws.Packages))
	}
}

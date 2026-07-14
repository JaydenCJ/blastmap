// Tests for the Cargo workspace loader: member globs, excludes, the four
// ways a crate can reference a sibling (path, name, workspace = true,
// renamed package), and dev/build dependency classification.
package workspace

import (
	"reflect"
	"strings"
	"testing"
)

// cargoPackages runs loadCargo and returns packages keyed by crate name.
func cargoPackages(t *testing.T, files map[string]string) map[string]*Package {
	t.Helper()
	root := t.TempDir()
	writeTree(t, root, files)
	pkgs, _, found, err := loadCargo(root)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("workspace not found")
	}
	out := map[string]*Package{}
	for _, p := range pkgs {
		out[p.Name] = p
	}
	return out
}

func TestCargoMembersGlobAndExclude(t *testing.T) {
	pkgs := cargoPackages(t, map[string]string{
		"Cargo.toml":               "[workspace]\nmembers = [\"crates/*\"]\nexclude = [\"crates/legacy\"]\n",
		"crates/core/Cargo.toml":   "[package]\nname = \"core\"\n",
		"crates/cli/Cargo.toml":    "[package]\nname = \"cli\"\n",
		"crates/legacy/Cargo.toml": "[package]\nname = \"legacy\"\n",
	})
	if len(pkgs) != 2 {
		t.Fatalf("want core+cli, got %v", pkgs)
	}
	if _, ok := pkgs["legacy"]; ok {
		t.Fatal("excluded member must not load")
	}
}

func TestCargoRootPackageIsAMember(t *testing.T) {
	// A root [package] next to [workspace] is itself a workspace member
	// with Dir ".".
	pkgs := cargoPackages(t, map[string]string{
		"Cargo.toml":     "[package]\nname = \"root-crate\"\n\n[workspace]\nmembers = [\"sub\"]\n",
		"sub/Cargo.toml": "[package]\nname = \"sub\"\n",
	})
	if pkgs["root-crate"] == nil || pkgs["root-crate"].Dir != "." {
		t.Fatalf("root crate should be a member at '.': %v", pkgs)
	}
}

func TestCargoPathDependencyEdge(t *testing.T) {
	pkgs := cargoPackages(t, map[string]string{
		"Cargo.toml":             "[workspace]\nmembers = [\"crates/*\"]\n",
		"crates/cli/Cargo.toml":  "[package]\nname = \"cli\"\n\n[dependencies]\ncore = { path = \"../core\" }\nserde = \"1\"\n",
		"crates/core/Cargo.toml": "[package]\nname = \"core\"\n",
	})
	if got := pkgs["cli"].Deps; !reflect.DeepEqual(got, []string{"core"}) {
		t.Fatalf("cli deps = %v (serde is external)", got)
	}
}

func TestCargoWorkspaceTrueAndRenamedDeps(t *testing.T) {
	pkgs := cargoPackages(t, map[string]string{
		"Cargo.toml":   "[workspace]\nmembers = [\"a\", \"b\"]\n\n[workspace.dependencies]\nb = { path = \"b\" }\n",
		"a/Cargo.toml": "[package]\nname = \"a\"\n\n[dependencies]\nb = { workspace = true }\n",
		"b/Cargo.toml": "[package]\nname = \"b\"\n",
	})
	if got := pkgs["a"].Deps; !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("workspace = true dep missed: %v", got)
	}
	// `alias = { package = "real", … }` — the edge must land on the
	// real crate, not the alias.
	pkgs = cargoPackages(t, map[string]string{
		"Cargo.toml":      "[workspace]\nmembers = [\"app\", \"real\"]\n",
		"app/Cargo.toml":  "[package]\nname = \"app\"\n\n[dependencies]\nalias = { package = \"real\", version = \"0.1\" }\n",
		"real/Cargo.toml": "[package]\nname = \"real\"\n",
	})
	if got := pkgs["app"].Deps; !reflect.DeepEqual(got, []string{"real"}) {
		t.Fatalf("renamed dep should resolve to real crate: %v", got)
	}
}

func TestCargoDependencyClassification(t *testing.T) {
	// dev-dependencies are dev edges, build-dependencies are runtime
	// edges (they break the build just the same).
	pkgs := cargoPackages(t, map[string]string{
		"Cargo.toml": "[workspace]\nmembers = [\"app\", \"testkit\", \"codegen\"]\n",
		"app/Cargo.toml": "[package]\nname = \"app\"\n\n" +
			"[dev-dependencies]\ntestkit = { path = \"../testkit\" }\n\n" +
			"[build-dependencies]\ncodegen = { path = \"../codegen\" }\n",
		"testkit/Cargo.toml": "[package]\nname = \"testkit\"\n",
		"codegen/Cargo.toml": "[package]\nname = \"codegen\"\n",
	})
	app := pkgs["app"]
	if !reflect.DeepEqual(app.Deps, []string{"codegen"}) {
		t.Fatalf("build-dependencies are runtime edges: %v", app.Deps)
	}
	if !reflect.DeepEqual(app.DevDeps, []string{"testkit"}) {
		t.Fatalf("dev-dependencies must be dev edges: %v", app.DevDeps)
	}
	// A dep in both tables counts as runtime only.
	pkgs = cargoPackages(t, map[string]string{
		"Cargo.toml":      "[workspace]\nmembers = [\"app\", \"core\"]\n",
		"app/Cargo.toml":  "[package]\nname = \"app\"\n\n[dependencies]\ncore = { path = \"../core\" }\n\n[dev-dependencies]\ncore = { path = \"../core\" }\n",
		"core/Cargo.toml": "[package]\nname = \"core\"\n",
	})
	app = pkgs["app"]
	if !reflect.DeepEqual(app.Deps, []string{"core"}) || len(app.DevDeps) != 0 {
		t.Fatalf("dep in both tables must count as runtime only: deps=%v dev=%v", app.Deps, app.DevDeps)
	}
}

func TestCargoDottedAndTargetSpecificTables(t *testing.T) {
	pkgs := cargoPackages(t, map[string]string{
		"Cargo.toml":      "[workspace]\nmembers = [\"app\", \"core\"]\n",
		"app/Cargo.toml":  "[package]\nname = \"app\"\n\n[dependencies.core]\npath = \"../core\"\nfeatures = [\"std\"]\n",
		"core/Cargo.toml": "[package]\nname = \"core\"\n",
	})
	if got := pkgs["app"].Deps; !reflect.DeepEqual(got, []string{"core"}) {
		t.Fatalf("[dependencies.core] table missed: %v", got)
	}
	pkgs = cargoPackages(t, map[string]string{
		"Cargo.toml":           "[workspace]\nmembers = [\"app\", \"unix-shim\"]\n",
		"app/Cargo.toml":       "[package]\nname = \"app\"\n\n[target.'cfg(unix)'.dependencies]\nunix-shim = { path = \"../unix-shim\" }\n",
		"unix-shim/Cargo.toml": "[package]\nname = \"unix-shim\"\n",
	})
	if got := pkgs["app"].Deps; !reflect.DeepEqual(got, []string{"unix-shim"}) {
		t.Fatalf("target-specific dep missed: %v", got)
	}
}

func TestCargoLoaderErrorsAndNotFound(t *testing.T) {
	// A member without [package] name is unusable.
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"Cargo.toml":   "[workspace]\nmembers = [\"a\"]\n",
		"a/Cargo.toml": "[dependencies]\nserde = \"1\"\n",
	})
	if _, _, _, err := loadCargo(root); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("member without [package] name must fail: %v", err)
	}
	// A plain crate (no [workspace] table) is simply not a workspace.
	root = t.TempDir()
	writeTree(t, root, map[string]string{"Cargo.toml": "[package]\nname = \"solo\"\n"})
	_, _, found, err := loadCargo(root)
	if err != nil || found {
		t.Fatalf("plain crate is not a workspace (found=%v err=%v)", found, err)
	}
}

func TestCargoGlobalFiles(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"Cargo.toml":   "[workspace]\nmembers = [\"a\"]\n",
		"Cargo.lock":   "",
		"a/Cargo.toml": "[package]\nname = \"a\"\n",
	})
	_, global, _, err := loadCargo(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(global, []string{"Cargo.toml", "Cargo.lock"}) {
		t.Fatalf("global files = %v", global)
	}
}

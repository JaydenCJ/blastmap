// Tests for the Go workspace loader: go.work `use` forms, go.mod
// require/replace parsing, and the errors a broken workspace produces.
package workspace

import (
	"reflect"
	"strings"
	"testing"
)

// goPackages runs loadGoWork and returns packages keyed by module path.
func goPackages(t *testing.T, files map[string]string) map[string]*Package {
	t.Helper()
	root := t.TempDir()
	writeTree(t, root, files)
	pkgs, _, found, err := loadGoWork(root)
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

func TestGoWorkUseForms(t *testing.T) {
	// Single-line, block, comments, and quoted paths with spaces.
	pkgs := goPackages(t, map[string]string{
		"go.work":           "go 1.22\n\nuse ./single\nuse \"./with space\"\n\nuse (\n\t./block/a\n\t./block/b // trailing comment\n)\n",
		"single/go.mod":     "module example.test/single\n",
		"with space/go.mod": "module example.test/spaced\n",
		"block/a/go.mod":    "module example.test/a\n",
		"block/b/go.mod":    "module example.test/b\n",
	})
	if len(pkgs) != 4 {
		t.Fatalf("want 4 modules, got %v", pkgs)
	}
	if pkgs["example.test/a"].Dir != "block/a" {
		t.Fatalf("dir = %q", pkgs["example.test/a"].Dir)
	}
	if pkgs["example.test/spaced"].Dir != "with space" {
		t.Fatalf("quoted use path mishandled: %+v", pkgs["example.test/spaced"])
	}
}

func TestGoModRequireEdges(t *testing.T) {
	pkgs := goPackages(t, map[string]string{
		"go.work":     "go 1.22\nuse (\n\t./api\n\t./core\n\t./util\n)\n",
		"api/go.mod":  "module example.test/api\n\ngo 1.22\n\nrequire (\n\texample.test/core v0.0.0\n\texample.test/util v0.0.0 // indirect\n\tgolang.org/x/sys v0.30.0\n)\n",
		"core/go.mod": "module example.test/core\n\nrequire example.test/util v0.0.0\n",
		"util/go.mod": "module example.test/util\n",
	})
	if got := pkgs["example.test/api"].Deps; !reflect.DeepEqual(got, []string{"example.test/core", "example.test/util"}) {
		t.Fatalf("api deps = %v (external golang.org/x/sys must be dropped)", got)
	}
	if got := pkgs["example.test/core"].Deps; !reflect.DeepEqual(got, []string{"example.test/util"}) {
		t.Fatalf("single-line require missed: %v", got)
	}
	if len(pkgs["example.test/api"].DevDeps) != 0 {
		t.Fatal("Go has no dev-dependency concept; DevDeps must stay empty")
	}
}

func TestGoModReplaceDirectives(t *testing.T) {
	// Pre-go.work-style wiring: require an external path but replace it
	// with a sibling directory. The replace makes it an internal edge.
	pkgs := goPackages(t, map[string]string{
		"go.work":     "go 1.22\nuse (\n\t./app\n\t./fork\n)\n",
		"app/go.mod":  "module example.test/app\n\nrequire upstream.example/lib v1.0.0\n\nreplace upstream.example/lib => ../fork\n",
		"fork/go.mod": "module example.test/fork\n",
	})
	if got := pkgs["example.test/app"].Deps; !reflect.DeepEqual(got, []string{"example.test/fork"}) {
		t.Fatalf("replace edge missing: %v", got)
	}
	// A registry-to-registry replace is not a local edge.
	pkgs = goPackages(t, map[string]string{
		"go.work":    "go 1.22\nuse ./app\n",
		"app/go.mod": "module example.test/app\n\nreplace old.example/x => new.example/x v1.2.3\n",
	})
	if got := pkgs["example.test/app"].Deps; len(got) != 0 {
		t.Fatalf("registry replace must not create edges: %v", got)
	}
}

func TestGoLoaderErrors(t *testing.T) {
	// A use directory without go.mod.
	root := t.TempDir()
	writeTree(t, root, map[string]string{"go.work": "go 1.22\nuse ./ghost\n"})
	if _, _, _, err := loadGoWork(root); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("missing member go.mod should fail naming the dir: %v", err)
	}
	// go.mod without a module directive.
	root = t.TempDir()
	writeTree(t, root, map[string]string{
		"go.work":  "go 1.22\nuse ./a\n",
		"a/go.mod": "go 1.22\n",
	})
	if _, _, _, err := loadGoWork(root); err == nil || !strings.Contains(err.Error(), "module") {
		t.Fatalf("missing module directive should fail: %v", err)
	}
	// The same module path in two member dirs.
	root = t.TempDir()
	writeTree(t, root, map[string]string{
		"go.work":  "go 1.22\nuse (\n\t./a\n\t./b\n)\n",
		"a/go.mod": "module example.test/dup\n",
		"b/go.mod": "module example.test/dup\n",
	})
	if _, _, _, err := loadGoWork(root); err == nil || !strings.Contains(err.Error(), "dup") {
		t.Fatalf("duplicate module paths must fail: %v", err)
	}
}

func TestGoGlobalFilesAndNotFound(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.work":     "go 1.22\nuse ./a\n",
		"go.work.sum": "",
		"a/go.mod":    "module example.test/a\n",
	})
	_, global, _, err := loadGoWork(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(global, []string{"go.work", "go.work.sum"}) {
		t.Fatalf("global files = %v", global)
	}
	// A single-module repo without go.work is not a workspace.
	root = t.TempDir()
	writeTree(t, root, map[string]string{"go.mod": "module example.test/single\n"})
	_, _, found, err := loadGoWork(root)
	if err != nil || found {
		t.Fatalf("single-module repo is not a workspace (found=%v err=%v)", found, err)
	}
}

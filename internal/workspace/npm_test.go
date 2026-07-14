// Tests for the npm/yarn/pnpm workspace loader: member-list shapes,
// glob expansion, dependency classification, and the failure modes a
// real monorepo can hit.
package workspace

import (
	"reflect"
	"strings"
	"testing"
)

// npmPackages runs loadNPM and returns packages keyed by name.
func npmPackages(t *testing.T, files map[string]string) map[string]*Package {
	t.Helper()
	root := t.TempDir()
	writeTree(t, root, files)
	pkgs, _, found, err := loadNPM(root)
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

func TestNPMWorkspacesFieldBothShapes(t *testing.T) {
	// Plain array form.
	pkgs := npmPackages(t, map[string]string{
		"package.json":            `{"name":"root","workspaces":["packages/*"]}`,
		"packages/a/package.json": `{"name":"a"}`,
		"packages/b/package.json": `{"name":"b"}`,
	})
	if len(pkgs) != 2 {
		t.Fatalf("want 2 packages, got %v", pkgs)
	}
	if pkgs["a"].Dir != "packages/a" || pkgs["a"].Ecosystem != NPM {
		t.Fatalf("bad package: %+v", pkgs["a"])
	}
	// yarn classic's {"packages": […]} object form.
	pkgs = npmPackages(t, map[string]string{
		"package.json":            `{"workspaces":{"packages":["packages/*"],"nohoist":["**/x"]}}`,
		"packages/a/package.json": `{"name":"a"}`,
	})
	if _, ok := pkgs["a"]; !ok {
		t.Fatalf("object form not parsed: %v", pkgs)
	}
}

func TestPnpmWorkspaceYamlPreferredAndNegation(t *testing.T) {
	// When pnpm-workspace.yaml exists it wins over package.json.
	pkgs := npmPackages(t, map[string]string{
		"pnpm-workspace.yaml":     "packages:\n  - \"libs/*\"\n  - '!libs/skip'\n",
		"package.json":            `{"workspaces":["packages/*"]}`,
		"libs/a/package.json":     `{"name":"a"}`,
		"libs/skip/package.json":  `{"name":"skip"}`,
		"packages/x/package.json": `{"name":"x"}`,
	})
	if _, ok := pkgs["x"]; ok {
		t.Fatal("package.json workspaces must be ignored when pnpm-workspace.yaml exists")
	}
	if _, ok := pkgs["skip"]; ok {
		t.Fatal("negated pattern must exclude libs/skip")
	}
	if _, ok := pkgs["a"]; !ok {
		t.Fatalf("libs/a missing: %v", pkgs)
	}
	// Negation works identically in the package.json workspaces field.
	pkgs = npmPackages(t, map[string]string{
		"package.json":                   `{"workspaces":["packages/*","!packages/fixtures"]}`,
		"packages/a/package.json":        `{"name":"a"}`,
		"packages/fixtures/package.json": `{"name":"fixtures"}`,
	})
	if _, ok := pkgs["fixtures"]; ok {
		t.Fatal("negated member should be excluded")
	}
}

func TestPnpmYamlParsing(t *testing.T) {
	// Flow style through the full loader.
	pkgs := npmPackages(t, map[string]string{
		"pnpm-workspace.yaml":    `packages: ["libs/*", "tools/cli"]` + "\n",
		"libs/a/package.json":    `{"name":"a"}`,
		"tools/cli/package.json": `{"name":"cli"}`,
	})
	if len(pkgs) != 2 {
		t.Fatalf("flow-style list not parsed: %v", pkgs)
	}
	// Block style with comments, both quote styles, and a following key.
	got := parsePnpmWorkspace(`# workspace definition
packages:
  # libraries
  - "libs/*"
  - 'apps/web'
  - tools/cli # trailing comment
catalog:
  react: ^19.0.0
`)
	want := []string{"libs/*", "apps/web", "tools/cli"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsePnpmWorkspace = %v, want %v", got, want)
	}
}

func TestNPMDependencyClassification(t *testing.T) {
	pkgs := npmPackages(t, map[string]string{
		"package.json": `{"workspaces":["p/*"]}`,
		"p/app/package.json": `{"name":"app",
			"dependencies":{"lib":"workspace:*","left-pad":"^1.0.0"},
			"peerDependencies":{"peer":"*"},
			"optionalDependencies":{"opt":"*"},
			"devDependencies":{"tools":"workspace:*"}}`,
		"p/lib/package.json":   `{"name":"lib"}`,
		"p/peer/package.json":  `{"name":"peer"}`,
		"p/opt/package.json":   `{"name":"opt"}`,
		"p/tools/package.json": `{"name":"tools"}`,
	})
	app := pkgs["app"]
	if !reflect.DeepEqual(app.Deps, []string{"lib", "opt", "peer"}) {
		t.Fatalf("runtime deps = %v (external left-pad must not appear)", app.Deps)
	}
	if !reflect.DeepEqual(app.DevDeps, []string{"tools"}) {
		t.Fatalf("dev deps = %v", app.DevDeps)
	}
}

func TestNPMUnnamedPackageFallsBackToDir(t *testing.T) {
	pkgs := npmPackages(t, map[string]string{
		"package.json":        `{"workspaces":["p/*"]}`,
		"p/anon/package.json": `{"private":true}`,
	})
	if _, ok := pkgs["p/anon"]; !ok {
		t.Fatalf("unnamed package should use its dir as name: %v", pkgs)
	}
}

func TestNPMLoaderErrors(t *testing.T) {
	// Duplicate package names across two dirs.
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"package.json":     `{"workspaces":["p/*"]}`,
		"p/a/package.json": `{"name":"dup"}`,
		"p/b/package.json": `{"name":"dup"}`,
	})
	if _, _, _, err := loadNPM(root); err == nil || !strings.Contains(err.Error(), "dup") {
		t.Fatalf("duplicate names must fail with both dirs named: %v", err)
	}
	// A malformed member manifest names the offending directory.
	root = t.TempDir()
	writeTree(t, root, map[string]string{
		"package.json":     `{"workspaces":["p/*"]}`,
		"p/a/package.json": `{not json`,
	})
	if _, _, _, err := loadNPM(root); err == nil || !strings.Contains(err.Error(), "p/a") {
		t.Fatalf("bad member manifest should fail naming the dir: %v", err)
	}
}

func TestNPMNodeModulesNeverScanned(t *testing.T) {
	pkgs := npmPackages(t, map[string]string{
		"package.json":                             `{"workspaces":["packages/**"]}`,
		"packages/a/package.json":                  `{"name":"a"}`,
		"packages/a/node_modules/dep/package.json": `{"name":"dep"}`,
	})
	if _, ok := pkgs["dep"]; ok {
		t.Fatal("node_modules must never be treated as a member")
	}
}

func TestNPMNotFoundAndGlobalFiles(t *testing.T) {
	// A plain package.json without a workspaces field is not a workspace.
	root := t.TempDir()
	writeTree(t, root, map[string]string{"package.json": `{"name":"plain-lib"}`})
	_, _, found, err := loadNPM(root)
	if err != nil || found {
		t.Fatalf("plain package.json is not a workspace (found=%v err=%v)", found, err)
	}
	// Global files list only what actually exists on disk.
	root = t.TempDir()
	writeTree(t, root, map[string]string{
		"package.json":     `{"workspaces":["p/*"]}`,
		"p/a/package.json": `{"name":"a"}`,
		"pnpm-lock.yaml":   "lockfileVersion: 9\n",
	})
	_, global, _, err := loadNPM(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(global, []string{"package.json", "pnpm-lock.yaml"}) {
		t.Fatalf("global files = %v", global)
	}
}

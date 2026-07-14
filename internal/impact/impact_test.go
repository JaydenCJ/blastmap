// Tests for the impact engine — the pure core of blastmap. Fixtures are
// in-memory workspaces, so every propagation rule (ownership, chains,
// globals, unclaimed handling, dev edges) is exercised without touching
// disk or git.
package impact

import (
	"reflect"
	"testing"

	"github.com/JaydenCJ/blastmap/internal/workspace"
)

// fixture returns the canonical test workspace:
//
//	web -> ui -> utils        (runtime chain)
//	api -> utils              (runtime)
//	web -> tsconfig (dev)     (dev-only edge)
//	docs                      (isolated package)
func fixture() *workspace.Workspace {
	return &workspace.Workspace{
		Root:       "/ws",
		Ecosystems: []workspace.Ecosystem{workspace.NPM},
		Packages: []*workspace.Package{
			{Name: "api", Dir: "apps/api", Ecosystem: workspace.NPM, Deps: []string{"utils"}},
			{Name: "docs", Dir: "packages/docs", Ecosystem: workspace.NPM},
			{Name: "tsconfig", Dir: "packages/tsconfig", Ecosystem: workspace.NPM},
			{Name: "ui", Dir: "packages/ui", Ecosystem: workspace.NPM, Deps: []string{"utils"}},
			{Name: "utils", Dir: "packages/utils", Ecosystem: workspace.NPM},
			{Name: "web", Dir: "apps/web", Ecosystem: workspace.NPM,
				Deps: []string{"ui"}, DevDeps: []string{"tsconfig"}},
		},
		GlobalFiles: []string{"package.json", "pnpm-lock.yaml"},
	}
}

// namesByStatus extracts entry names for one status, in report order.
func namesByStatus(res Result, st Status) []string {
	var out []string
	for _, e := range res.Entries {
		if e.Status == st {
			out = append(out, e.Pkg.Name)
		}
	}
	return out
}

func TestChangedPackageOnly(t *testing.T) {
	res := Compute(fixture(), []string{"packages/docs/README.md"}, Options{})
	if got := namesByStatus(res, StatusChanged); !reflect.DeepEqual(got, []string{"docs"}) {
		t.Fatalf("changed = %v", got)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("docs has no dependents; entries = %d", len(res.Entries))
	}
}

func TestDependentsPropagateWithChains(t *testing.T) {
	res := Compute(fixture(), []string{"packages/utils/src/a.js"}, Options{})
	if got := namesByStatus(res, StatusDependent); !reflect.DeepEqual(got, []string{"api", "ui", "web"}) {
		t.Fatalf("dependents = %v", got)
	}
	for _, e := range res.Entries {
		if e.Pkg.Name == "web" {
			if !reflect.DeepEqual(e.Via, []string{"web", "ui", "utils"}) {
				t.Fatalf("web chain = %v", e.Via)
			}
		}
	}
}

func TestDirectOnlySkipsPropagation(t *testing.T) {
	res := Compute(fixture(), []string{"packages/utils/src/a.js"}, Options{DirectOnly: true})
	if len(res.Entries) != 1 || res.Entries[0].Pkg.Name != "utils" {
		t.Fatalf("direct-only should stop at utils: %+v", res.Entries)
	}
}

func TestGlobalFileAffectsEverything(t *testing.T) {
	res := Compute(fixture(), []string{"pnpm-lock.yaml"}, Options{})
	if got := namesByStatus(res, StatusGlobal); len(got) != 6 {
		t.Fatalf("lockfile change must affect all 6 packages, got %v", got)
	}
	if !reflect.DeepEqual(res.GlobalHits, []string{"pnpm-lock.yaml"}) {
		t.Fatalf("global hits = %v", res.GlobalHits)
	}
	// The triggering file is the evidence.
	if got := res.Entries[0].Via; !reflect.DeepEqual(got, []string{"pnpm-lock.yaml"}) {
		t.Fatalf("via = %v", got)
	}
	// Directly-hit packages keep the more specific "changed" status.
	res = Compute(fixture(), []string{"pnpm-lock.yaml", "packages/ui/index.js"}, Options{})
	if got := namesByStatus(res, StatusChanged); !reflect.DeepEqual(got, []string{"ui"}) {
		t.Fatalf("ui should stay 'changed', got %v", got)
	}
	if got := namesByStatus(res, StatusGlobal); len(got) != 5 {
		t.Fatalf("remaining 5 packages should be 'global', got %v", got)
	}
}

func TestGlobalRuleConfiguration(t *testing.T) {
	// NoDefaultGlobals downgrades the lockfile to an ordinary unclaimed
	// file.
	res := Compute(fixture(), []string{"pnpm-lock.yaml"}, Options{NoDefaultGlobals: true})
	if len(res.Entries) != 0 {
		t.Fatalf("without defaults the lockfile affects nothing: %+v", res.Entries)
	}
	if !reflect.DeepEqual(res.Unclaimed, []string{"pnpm-lock.yaml"}) {
		t.Fatalf("unclaimed = %v", res.Unclaimed)
	}
	// ExtraGlobals promote arbitrary globs to blast-all rules.
	res = Compute(fixture(), []string{"ci/build.yml"}, Options{ExtraGlobals: []string{"ci/**"}})
	if got := namesByStatus(res, StatusGlobal); len(got) != 6 {
		t.Fatalf("--global ci/** should blast everything, got %v", got)
	}
}

func TestUnclaimedModes(t *testing.T) {
	// Default: reported, affects nothing.
	res := Compute(fixture(), []string{"README.md"}, Options{})
	if len(res.Entries) != 0 {
		t.Fatalf("unclaimed file must not affect packages: %+v", res.Entries)
	}
	if !reflect.DeepEqual(res.Unclaimed, []string{"README.md"}) {
		t.Fatalf("unclaimed = %v", res.Unclaimed)
	}
	// AffectAll: same file blasts everything…
	res = Compute(fixture(), []string{"README.md"}, Options{AffectAll: true})
	if got := namesByStatus(res, StatusGlobal); len(got) != 6 {
		t.Fatalf("affect-all should blast everything, got %v", got)
	}
	// …but stays reported as unclaimed so --unclaimed error can find it.
	if !reflect.DeepEqual(res.Unclaimed, []string{"README.md"}) {
		t.Fatalf("unclaimed = %v", res.Unclaimed)
	}
}

func TestNoDevDropsDevEdges(t *testing.T) {
	files := []string{"packages/tsconfig/base.json"}
	withDev := Compute(fixture(), files, Options{})
	if got := namesByStatus(withDev, StatusDependent); !reflect.DeepEqual(got, []string{"web"}) {
		t.Fatalf("dev edge should pull web in: %v", got)
	}
	noDev := Compute(fixture(), files, Options{NoDev: true})
	if got := namesByStatus(noDev, StatusDependent); len(got) != 0 {
		t.Fatalf("--no-dev must drop the tsconfig->web edge: %v", got)
	}
}

func TestWithDepsAddsBuildRequirements(t *testing.T) {
	// ui changes -> web is a dependent; web's other deps (tsconfig) and
	// ui's deps (utils) are needed to build, status "dependency".
	res := Compute(fixture(), []string{"packages/ui/index.js"}, Options{WithDeps: true})
	if got := namesByStatus(res, StatusDependency); !reflect.DeepEqual(got, []string{"tsconfig", "utils"}) {
		t.Fatalf("dependencies = %v", got)
	}
	for _, e := range res.Entries {
		if e.Pkg.Name == "utils" && !reflect.DeepEqual(e.Via, []string{"ui", "utils"}) {
			t.Fatalf("dependency chain = %v", e.Via)
		}
	}
}

func TestPrefixTranslatesRepoPaths(t *testing.T) {
	// Workspace lives in monorepo/js; repo-relative paths must be
	// translated, and files outside the prefix reported as such.
	res := Compute(fixture(), []string{"js/packages/utils/a.js", "ops/deploy.sh"}, Options{Prefix: "js"})
	if got := namesByStatus(res, StatusChanged); !reflect.DeepEqual(got, []string{"utils"}) {
		t.Fatalf("changed = %v", got)
	}
	if !reflect.DeepEqual(res.Outside, []string{"ops/deploy.sh"}) {
		t.Fatalf("outside = %v", res.Outside)
	}
}

func TestReportShapeIsDeterministic(t *testing.T) {
	// Entries group by status rank then sort by name.
	res := Compute(fixture(), []string{"packages/utils/a.js", "apps/web/b.js", "README.md"}, Options{})
	var got []string
	for _, e := range res.Entries {
		got = append(got, string(e.Status)+":"+e.Pkg.Name)
	}
	want := []string{"changed:utils", "changed:web", "dependent:api", "dependent:ui"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ordering = %v, want %v", got, want)
	}
	if res.Changed != 3 || res.Total != 6 {
		t.Fatalf("changed=%d total=%d", res.Changed, res.Total)
	}
	// Per-package file lists are sorted regardless of input order.
	res = Compute(fixture(), []string{
		"packages/utils/b.js", "packages/utils/a.js",
	}, Options{DirectOnly: true})
	if !reflect.DeepEqual(res.Entries[0].Files, []string{"packages/utils/a.js", "packages/utils/b.js"}) {
		t.Fatalf("files = %v (must be sorted)", res.Entries[0].Files)
	}
}

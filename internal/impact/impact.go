// Package impact is the core engine: given a discovered workspace and a
// set of changed files, it maps files to owning packages, propagates the
// blast radius through the internal dependency graph, and explains every
// verdict with a chain of evidence.
package impact

import (
	"sort"
	"strings"

	"github.com/JaydenCJ/blastmap/internal/globs"
	"github.com/JaydenCJ/blastmap/internal/graph"
	"github.com/JaydenCJ/blastmap/internal/workspace"
)

// Status says why a package is in the result set.
type Status string

const (
	// StatusChanged: files inside the package's directory changed.
	StatusChanged Status = "changed"
	// StatusDependent: the package (transitively) depends on a changed one.
	StatusDependent Status = "dependent"
	// StatusGlobal: a global file (lockfile, workspace manifest, or
	// --global match) changed, so every package is affected.
	StatusGlobal Status = "global"
	// StatusDependency: pulled in by --with-deps — something the affected
	// set needs in order to build, not itself affected.
	StatusDependency Status = "dependency"
)

// rank orders statuses for report grouping.
func (s Status) rank() int {
	switch s {
	case StatusChanged:
		return 0
	case StatusDependent:
		return 1
	case StatusGlobal:
		return 2
	default:
		return 3
	}
}

// Options tune the propagation.
type Options struct {
	NoDev            bool     // ignore dev-dependency edges
	DirectOnly       bool     // do not traverse reverse dependencies
	WithDeps         bool     // add dependencies of the affected set
	NoDefaultGlobals bool     // drop the built-in manifest/lockfile globals
	ExtraGlobals     []string // additional global path globs
	AffectAll        bool     // unclaimed files affect everything (--unclaimed affect-all)

	// Prefix is the workspace root's path relative to the git top-level
	// ("" when they coincide). Changed files arrive top-level-relative
	// and are translated into workspace-relative paths through it.
	Prefix string
}

// Entry is one package in the result.
type Entry struct {
	Pkg    *workspace.Package
	Status Status
	Files  []string // StatusChanged: the workspace-relative files that hit it
	Via    []string // StatusDependent/Dependency: name chain; StatusGlobal: triggering files
}

// Result is the full computation outcome.
type Result struct {
	Entries    []Entry  // grouped by status rank, then sorted by name
	Unclaimed  []string // workspace files owned by no package and not global
	Outside    []string // repo files outside the workspace root entirely
	GlobalHits []string // changed files that matched a global rule
	Changed    int      // total changed files considered
	Total      int      // total packages in the workspace
}

// Compute maps changed files (git top-level-relative slash paths) onto the
// workspace and propagates. It is pure: no filesystem or git access.
func Compute(ws *workspace.Workspace, files []string, opts Options) Result {
	res := Result{Changed: len(files), Total: len(ws.Packages)}

	globalSet := map[string]bool{}
	if !opts.NoDefaultGlobals {
		for _, g := range ws.GlobalFiles {
			globalSet[g] = true
		}
	}

	changedFiles := map[string][]string{} // pkg key -> files
	for _, f := range files {
		rel, ok := stripPrefix(f, opts.Prefix)
		if !ok {
			res.Outside = append(res.Outside, f)
			continue
		}
		if p := ws.Owner(rel); p != nil {
			changedFiles[p.Key()] = append(changedFiles[p.Key()], rel)
			continue
		}
		if globalSet[rel] || globs.MatchAny(opts.ExtraGlobals, rel) {
			res.GlobalHits = append(res.GlobalHits, rel)
			continue
		}
		res.Unclaimed = append(res.Unclaimed, rel)
	}
	sort.Strings(res.GlobalHits)
	sort.Strings(res.Unclaimed)
	sort.Strings(res.Outside)

	g := buildGraph(ws, opts.NoDev)
	keyName := func(key string) string {
		if p := ws.PackageByKey(key); p != nil {
			return p.Name
		}
		return key
	}

	inSet := map[string]bool{}
	add := func(key string, e Entry) {
		if inSet[key] {
			return
		}
		inSet[key] = true
		res.Entries = append(res.Entries, e)
	}

	var seeds []string
	for key, fs := range changedFiles {
		sort.Strings(fs)
		seeds = append(seeds, key)
		add(key, Entry{Pkg: ws.PackageByKey(key), Status: StatusChanged, Files: fs})
	}
	sort.Strings(seeds)

	trigger := res.GlobalHits
	if opts.AffectAll && len(res.Unclaimed) > 0 {
		trigger = append(append([]string(nil), trigger...), res.Unclaimed...)
		sort.Strings(trigger)
	}
	switch {
	case len(trigger) > 0:
		// Global blast: everything not already "changed" is in.
		for _, p := range ws.Packages {
			add(p.Key(), Entry{Pkg: p, Status: StatusGlobal, Via: trigger})
		}
	case !opts.DirectOnly:
		chains := g.Dependents(seeds)
		keys := make([]string, 0, len(chains))
		for k := range chains {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			add(k, Entry{Pkg: ws.PackageByKey(k), Status: StatusDependent, Via: nameChain(chains[k], keyName)})
		}
	}

	if opts.WithDeps {
		affected := make([]string, 0, len(inSet))
		for k := range inSet {
			affected = append(affected, k)
		}
		sort.Strings(affected)
		chains := g.Dependencies(affected)
		keys := make([]string, 0, len(chains))
		for k := range chains {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			add(k, Entry{Pkg: ws.PackageByKey(k), Status: StatusDependency, Via: nameChain(chains[k], keyName)})
		}
	}

	sort.SliceStable(res.Entries, func(i, j int) bool {
		a, b := res.Entries[i], res.Entries[j]
		if a.Status.rank() != b.Status.rank() {
			return a.Status.rank() < b.Status.rank()
		}
		return a.Pkg.Name < b.Pkg.Name
	})
	return res
}

// BuildGraph exposes the (dev-aware) internal dependency graph for the
// `graph` subcommand.
func BuildGraph(ws *workspace.Workspace, noDev bool) *graph.Graph {
	return buildGraph(ws, noDev)
}

func buildGraph(ws *workspace.Workspace, noDev bool) *graph.Graph {
	g := graph.New()
	byName := map[ecoName]string{}
	for _, p := range ws.Packages {
		g.AddNode(p.Key())
		byName[ecoName{p.Ecosystem, p.Name}] = p.Key()
	}
	for _, p := range ws.Packages {
		deps := p.Deps
		if !noDev {
			deps = append(append([]string(nil), deps...), p.DevDeps...)
		}
		for _, d := range deps {
			if key, ok := byName[ecoName{p.Ecosystem, d}]; ok {
				g.AddEdge(p.Key(), key)
			}
		}
	}
	return g
}

// ecoName is an (ecosystem, name) pair used as a lookup key; edges
// never cross ecosystems.
type ecoName struct {
	Eco  workspace.Ecosystem
	Name string
}

// nameChain converts graph keys to display names.
func nameChain(keys []string, keyName func(string) string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = keyName(k)
	}
	return out
}

// stripPrefix translates a top-level-relative path into a
// workspace-relative one; ok is false when the file lies outside.
func stripPrefix(file, prefix string) (string, bool) {
	if prefix == "" || prefix == "." {
		return file, true
	}
	rest, ok := strings.CutPrefix(file, prefix+"/")
	if !ok {
		return "", false
	}
	return rest, true
}

// Package workspace discovers monorepo members and their internal
// dependency edges by reading the manifests the repository already has:
// npm/pnpm/yarn workspaces (package.json, pnpm-workspace.yaml), Go
// multi-module workspaces (go.work + go.mod), and Cargo workspaces
// (Cargo.toml). Discovery is read-only and never executes a package
// manager or build tool.
package workspace

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JaydenCJ/blastmap/internal/globs"
)

// Ecosystem identifies the manifest family a package came from.
type Ecosystem string

const (
	NPM   Ecosystem = "npm"
	GoMod Ecosystem = "go"
	Cargo Ecosystem = "cargo"
	// Auto means "every ecosystem detected at the root".
	Auto Ecosystem = "auto"
)

// ParseEcosystem validates a --ecosystem flag value.
func ParseEcosystem(s string) (Ecosystem, error) {
	switch Ecosystem(s) {
	case NPM, GoMod, Cargo, Auto:
		return Ecosystem(s), nil
	}
	return "", fmt.Errorf("unknown ecosystem %q (want npm, go, cargo, or auto)", s)
}

// Package is one workspace member.
type Package struct {
	Name      string    // package.json name, Go module path, or crate name
	Dir       string    // slash path relative to the workspace root
	Ecosystem Ecosystem //
	Deps      []string  // internal deps: runtime/build edges (names)
	DevDeps   []string  // internal deps that are dev/test-only edges
}

// Key returns a graph key unique across ecosystems (names could collide
// between, say, an npm package and a crate in the same repo).
func (p *Package) Key() string { return string(p.Ecosystem) + ":" + p.Name }

// Workspace is everything blastmap knows about a monorepo.
type Workspace struct {
	Root       string      // absolute path of the workspace root
	Ecosystems []Ecosystem // detected (or requested) ecosystems, sorted
	Packages   []*Package  // sorted by Key()

	// GlobalFiles are workspace-root-relative files whose change affects
	// every package: the workspace-defining manifests and root lockfiles
	// that exist on disk.
	GlobalFiles []string
}

// Discover loads the workspace rooted at dir. With eco == Auto every
// ecosystem whose root manifest is present is loaded; otherwise only the
// requested one. It returns an error when nothing at all is found, listing
// what it looked for.
func Discover(dir string, eco Ecosystem) (*Workspace, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("workspace root %s is not a directory", dir)
	}
	ws := &Workspace{Root: root}
	type loader struct {
		eco  Ecosystem
		load func(root string) ([]*Package, []string, bool, error)
	}
	loaders := []loader{
		{Cargo, loadCargo},
		{GoMod, loadGoWork},
		{NPM, loadNPM},
	}
	for _, l := range loaders {
		if eco != Auto && eco != l.eco {
			continue
		}
		pkgs, global, found, err := l.load(root)
		if err != nil {
			return nil, fmt.Errorf("%s workspace: %w", l.eco, err)
		}
		if !found {
			continue
		}
		ws.Ecosystems = append(ws.Ecosystems, l.eco)
		ws.Packages = append(ws.Packages, pkgs...)
		ws.GlobalFiles = append(ws.GlobalFiles, global...)
	}
	if len(ws.Ecosystems) == 0 {
		want := "package.json workspaces, pnpm-workspace.yaml, go.work, or Cargo.toml [workspace]"
		if eco != Auto {
			want = rootManifestFor(eco)
		}
		return nil, fmt.Errorf("no workspace found at %s (looked for %s)", dir, want)
	}
	sort.Slice(ws.Packages, func(i, j int) bool { return ws.Packages[i].Key() < ws.Packages[j].Key() })
	sort.Slice(ws.Ecosystems, func(i, j int) bool { return ws.Ecosystems[i] < ws.Ecosystems[j] })
	sort.Strings(ws.GlobalFiles)
	for i, p := range ws.Packages {
		if i > 0 && p.Key() == ws.Packages[i-1].Key() {
			return nil, fmt.Errorf("duplicate package %s (dirs %s and %s)",
				p.Name, ws.Packages[i-1].Dir, p.Dir)
		}
	}
	return ws, nil
}

// rootManifestFor names the file(s) a forced --ecosystem discovery needs.
func rootManifestFor(eco Ecosystem) string {
	switch eco {
	case NPM:
		return "package.json with a workspaces field, or pnpm-workspace.yaml"
	case GoMod:
		return "go.work"
	case Cargo:
		return "Cargo.toml with a [workspace] table"
	}
	return "a workspace manifest"
}

// PackageByKey returns the package with the given graph key, or nil.
func (w *Workspace) PackageByKey(key string) *Package {
	for _, p := range w.Packages {
		if p.Key() == key {
			return p
		}
	}
	return nil
}

// Owner maps a workspace-root-relative file to the member that owns it:
// the package with the deepest directory prefix. Returns nil when no
// package claims the file (root files, docs, CI config, …).
func (w *Workspace) Owner(file string) *Package {
	var best *Package
	for _, p := range w.Packages {
		if !underDir(file, p.Dir) {
			continue
		}
		if best == nil || len(p.Dir) > len(best.Dir) {
			best = p
		}
	}
	return best
}

// underDir reports whether file sits inside dir (both slash paths relative
// to the workspace root). dir "." would mean the root itself; members are
// always proper subdirectories, so that case never arises in practice.
func underDir(file, dir string) bool {
	if dir == "." || dir == "" {
		return true
	}
	return file == dir || strings.HasPrefix(file, dir+"/")
}

// expandMemberGlobs resolves workspace member patterns to directories
// under root that contain the given manifest file. Literal patterns are
// checked directly; glob patterns are matched against a bounded walk that
// skips VCS metadata, node_modules, and other vendored trees. Negative
// patterns ("!pattern") subtract from the accumulated set, in order.
func expandMemberGlobs(root string, patterns []string, manifest string) ([]string, error) {
	selected := map[string]bool{}
	var walked []string // lazily built list of candidate dirs
	walkOnce := func() ([]string, error) {
		if walked != nil {
			return walked, nil
		}
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable subtree: skip, don't fail discovery
			}
			if !d.IsDir() {
				return nil
			}
			name := d.Name()
			if p != root && (name == ".git" || name == "node_modules" ||
				name == "target" || name == "vendor" || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			rel, err := filepath.Rel(root, p)
			if err != nil || rel == "." {
				return nil
			}
			if _, err := os.Stat(filepath.Join(p, manifest)); err == nil {
				walked = append(walked, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		sort.Strings(walked)
		if walked == nil {
			walked = []string{}
		}
		return walked, nil
	}
	for _, pat := range patterns {
		neg := strings.HasPrefix(pat, "!")
		pat = strings.TrimPrefix(pat, "!")
		pat = strings.TrimSuffix(strings.TrimPrefix(pat, "./"), "/")
		if pat == "" {
			continue
		}
		if !globs.HasMeta(pat) {
			if neg {
				delete(selected, pat)
				continue
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(pat), manifest)); err == nil {
				selected[pat] = true
			}
			continue
		}
		cands, err := walkOnce()
		if err != nil {
			return nil, err
		}
		for _, c := range cands {
			if globs.Match(pat, c) {
				if neg {
					delete(selected, c)
				} else {
					selected[c] = true
				}
			}
		}
	}
	out := make([]string, 0, len(selected))
	for d := range selected {
		out = append(out, d)
	}
	sort.Strings(out)
	return out, nil
}

// existingFiles filters names to those present in root, as slash paths.
func existingFiles(root string, names ...string) []string {
	var out []string
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(n))); err == nil {
			out = append(out, path.Clean(n))
		}
	}
	return out
}

// cargo.go loads Cargo workspaces: the root Cargo.toml [workspace] table
// lists members (globs allowed) and excludes; each member Cargo.toml names
// the crate and its dependencies. Internal edges come from `path = "…"`
// dependencies that resolve to a member directory, and from plain or
// `workspace = true` dependencies whose name matches a member crate.
package workspace

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JaydenCJ/blastmap/internal/tomlmin"
)

// loadCargo discovers a Cargo workspace at root. found is false when
// Cargo.toml is absent or has no [workspace] table.
func loadCargo(root string) (pkgs []*Package, global []string, found bool, err error) {
	raw, readErr := os.ReadFile(filepath.Join(root, "Cargo.toml"))
	if readErr != nil {
		return nil, nil, false, nil
	}
	doc, err := tomlmin.Parse(string(raw))
	if err != nil {
		return nil, nil, false, fmt.Errorf("Cargo.toml: %w", err)
	}
	if doc.Table("workspace") == nil {
		return nil, nil, false, nil
	}
	patterns := doc.GetStrings("workspace", "members")
	for _, ex := range doc.GetStrings("workspace", "exclude") {
		patterns = append(patterns, "!"+ex)
	}
	dirs, err := expandMemberGlobs(root, patterns, "Cargo.toml")
	if err != nil {
		return nil, nil, false, err
	}
	// A root [package] alongside [workspace] is itself a member.
	rootIsMember := doc.Table("package") != nil

	type member struct {
		pkg     *Package
		doc     tomlmin.Doc
		devOnly map[string]bool // dep name -> only in dev-dependencies
		paths   map[string]string
	}
	var members []member
	byName := map[string]string{} // crate name -> dir
	byDir := map[string]string{}  // dir -> crate name
	addMember := func(dir string, mdoc tomlmin.Doc) error {
		name, ok := mdoc.GetString("package", "name")
		if !ok {
			return fmt.Errorf("%s/Cargo.toml: missing [package] name", dir)
		}
		if prev, dup := byName[name]; dup {
			return fmt.Errorf("crate name %q used by both %s and %s", name, prev, dir)
		}
		byName[name] = dir
		byDir[dir] = name
		members = append(members, member{
			pkg: &Package{Name: name, Dir: dir, Ecosystem: Cargo},
			doc: mdoc,
		})
		return nil
	}
	if rootIsMember {
		if err := addMember(".", doc); err != nil {
			return nil, nil, false, err
		}
	}
	for _, dir := range dirs {
		mraw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dir), "Cargo.toml"))
		if err != nil {
			return nil, nil, false, fmt.Errorf("member %s: %w", dir, err)
		}
		mdoc, err := tomlmin.Parse(string(mraw))
		if err != nil {
			return nil, nil, false, fmt.Errorf("%s/Cargo.toml: %w", dir, err)
		}
		if err := addMember(dir, mdoc); err != nil {
			return nil, nil, false, err
		}
	}

	for i := range members {
		m := &members[i]
		runtime := map[string]bool{}
		dev := map[string]bool{}
		for _, section := range []struct {
			table string
			into  map[string]bool
		}{
			{"dependencies", runtime},
			{"build-dependencies", runtime},
			{"dev-dependencies", dev},
		} {
			collectCargoDeps(m.doc, section.table, m.pkg.Dir, byName, byDir, section.into)
		}
		// A dep that is both runtime and dev counts as runtime.
		for d := range runtime {
			delete(dev, d)
		}
		m.pkg.Deps = sortedKeys(runtime, m.pkg.Name)
		m.pkg.DevDeps = sortedKeys(dev, m.pkg.Name)
		pkgs = append(pkgs, m.pkg)
	}

	global = existingFiles(root, "Cargo.toml", "Cargo.lock")
	return pkgs, global, true, nil
}

// collectCargoDeps walks one dependency table (plus its dotted sub-tables
// and target-specific variants) and records names that resolve to a
// workspace member — via a path that lands on a member dir, or by name.
func collectCargoDeps(doc tomlmin.Doc, table, memberDir string, byName, byDir map[string]string, into map[string]bool) {
	record := func(depName string, spec tomlmin.Value) {
		switch spec.Kind {
		case tomlmin.KindString:
			// `foo = "1.0"`: version-only. Internal only if the name is a
			// member (rare without path, but legal with a registry mirror).
			if _, ok := byName[depName]; ok {
				into[depName] = true
			}
		case tomlmin.KindTable:
			if p, ok := spec.Tab["path"]; ok && p.Kind == tomlmin.KindString {
				if name, ok := byDir[resolvePath(memberDir, p.Str)]; ok {
					into[name] = true
					return
				}
			}
			// `workspace = true` or a renamed `package = "…"` entry:
			// resolve by the real crate name.
			real := depName
			if pkg, ok := spec.Tab["package"]; ok && pkg.Kind == tomlmin.KindString {
				real = pkg.Str
			}
			if _, ok := byName[real]; ok {
				into[real] = true
			}
		}
	}
	// Inline entries under [dependencies].
	for depName, spec := range doc.Table(table) {
		record(depName, spec)
	}
	// Dotted tables: [dependencies.foo] path = "../foo".
	for tablePath, kv := range doc {
		rest, ok := strings.CutPrefix(tablePath, table+".")
		if ok && rest != "" && !strings.Contains(rest, ".") {
			record(rest, tomlmin.Value{Kind: tomlmin.KindTable, Tab: kv})
		}
		// Target-specific: [target.'cfg(…)'.dependencies] and its
		// dotted children [target.'cfg(…)'.dependencies.foo].
		if strings.HasPrefix(tablePath, "target.") {
			if strings.HasSuffix(tablePath, "."+table) {
				for depName, spec := range kv {
					record(depName, spec)
				}
			} else if idx := strings.LastIndex(tablePath, "."+table+"."); idx >= 0 {
				depName := tablePath[idx+len(table)+2:]
				if depName != "" && !strings.Contains(depName, ".") {
					record(depName, tomlmin.Value{Kind: tomlmin.KindTable, Tab: kv})
				}
			}
		}
	}
}

// resolvePath joins a dependency's relative path onto the member dir and
// normalizes it to a workspace-root-relative slash path.
func resolvePath(memberDir, rel string) string {
	if memberDir == "." {
		return path.Clean(filepath.ToSlash(rel))
	}
	return path.Clean(path.Join(memberDir, filepath.ToSlash(rel)))
}

// sortedKeys returns the map's keys minus self, sorted.
func sortedKeys(m map[string]bool, self string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if k != self {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

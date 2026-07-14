// npm.go loads npm / yarn / pnpm workspaces. Member lists come from
// pnpm-workspace.yaml (preferred when present) or the root package.json
// "workspaces" field (array form or {"packages": […]} object form).
// Internal edges are dependency names that resolve to another member,
// whatever the version spec — "workspace:*", "^1.2.3", "file:…" all count.
package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// packageJSON is the subset of package.json blastmap reads.
type packageJSON struct {
	Name                 string            `json:"name"`
	Workspaces           json.RawMessage   `json:"workspaces"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

// loadNPM discovers an npm-family workspace at root. found is false when
// neither pnpm-workspace.yaml nor a workspaces field exists.
func loadNPM(root string) (pkgs []*Package, global []string, found bool, err error) {
	var patterns []string
	pnpmPath := filepath.Join(root, "pnpm-workspace.yaml")
	if raw, readErr := os.ReadFile(pnpmPath); readErr == nil {
		patterns = parsePnpmWorkspace(string(raw))
		found = true
	} else {
		raw, readErr := os.ReadFile(filepath.Join(root, "package.json"))
		if readErr != nil {
			return nil, nil, false, nil
		}
		var rootPkg packageJSON
		if err := json.Unmarshal(raw, &rootPkg); err != nil {
			return nil, nil, false, fmt.Errorf("package.json: %w", err)
		}
		if len(rootPkg.Workspaces) == 0 {
			return nil, nil, false, nil
		}
		patterns, err = parseWorkspacesField(rootPkg.Workspaces)
		if err != nil {
			return nil, nil, false, err
		}
		found = true
	}

	dirs, err := expandMemberGlobs(root, patterns, "package.json")
	if err != nil {
		return nil, nil, false, err
	}

	type member struct {
		pkg  *Package
		json packageJSON
	}
	var members []member
	names := map[string]string{} // name -> dir, to catch duplicates
	for _, dir := range dirs {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dir), "package.json"))
		if err != nil {
			return nil, nil, false, fmt.Errorf("%s/package.json: %w", dir, err)
		}
		var pj packageJSON
		if err := json.Unmarshal(raw, &pj); err != nil {
			return nil, nil, false, fmt.Errorf("%s/package.json: %w", dir, err)
		}
		name := pj.Name
		if name == "" {
			// Unnamed private packages exist; fall back to the dir path
			// so the member still shows up in reports.
			name = dir
		}
		if prev, dup := names[name]; dup {
			return nil, nil, false, fmt.Errorf("package name %q used by both %s and %s", name, prev, dir)
		}
		names[name] = dir
		members = append(members, member{
			pkg:  &Package{Name: name, Dir: dir, Ecosystem: NPM},
			json: pj,
		})
	}

	for _, m := range members {
		m.pkg.Deps = internalNames(names, m.pkg.Name,
			m.json.Dependencies, m.json.PeerDependencies, m.json.OptionalDependencies)
		m.pkg.DevDeps = internalNames(names, m.pkg.Name, m.json.DevDependencies)
		pkgs = append(pkgs, m.pkg)
	}

	global = existingFiles(root,
		"package.json", "pnpm-workspace.yaml",
		"package-lock.json", "pnpm-lock.yaml", "yarn.lock")
	return pkgs, global, true, nil
}

// parseWorkspacesField accepts both shapes of the workspaces field.
func parseWorkspacesField(raw json.RawMessage) ([]string, error) {
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}
	var obj struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Packages != nil {
		return obj.Packages, nil
	}
	return nil, fmt.Errorf(`package.json: "workspaces" must be an array or {"packages": […]}`)
}

// internalNames collects, sorted, the dep names from the given maps that
// resolve to another workspace member (self-references are ignored).
func internalNames(names map[string]string, self string, maps ...map[string]string) []string {
	set := map[string]bool{}
	for _, m := range maps {
		for dep := range m {
			if dep == self {
				continue
			}
			if _, ok := names[dep]; ok {
				set[dep] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// parsePnpmWorkspace extracts the `packages:` list from
// pnpm-workspace.yaml. This is a purpose-built reader for the one shape
// pnpm documents — a top-level key with a block sequence of strings — not
// a general YAML parser (pnpm itself rejects anything fancier here).
func parsePnpmWorkspace(src string) []string {
	var patterns []string
	inPackages := false
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indented := line != strings.TrimLeft(line, " \t")
		if !indented {
			// A new top-level key ends the packages block.
			inPackages = strings.HasPrefix(trimmed, "packages:")
			// Flow style: packages: ["a", "b"]
			if inPackages {
				rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "packages:"))
				if strings.HasPrefix(rest, "[") {
					for _, item := range strings.Split(strings.Trim(rest, "[]"), ",") {
						if v := unquoteYAML(strings.TrimSpace(item)); v != "" {
							patterns = append(patterns, v)
						}
					}
					inPackages = false
				}
			}
			continue
		}
		if inPackages && strings.HasPrefix(trimmed, "-") {
			item := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			if v := unquoteYAML(item); v != "" {
				patterns = append(patterns, v)
			}
		}
	}
	return patterns
}

// unquoteYAML strips quotes and trailing comments from a scalar list item.
func unquoteYAML(s string) string {
	if s == "" {
		return ""
	}
	if s[0] == '"' || s[0] == '\'' {
		q := s[0]
		if end := strings.IndexByte(s[1:], q); end >= 0 {
			return s[1 : end+1]
		}
		return strings.Trim(s, string(q))
	}
	if i := strings.Index(s, " #"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

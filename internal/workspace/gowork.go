// gowork.go loads Go multi-module workspaces: go.work lists the member
// directories (`use` directives), each member's go.mod names the module
// and its requirements. Internal edges are `require` lines whose module
// path belongs to another member, plus `replace` directives that point at
// a member directory by relative path.
package workspace

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// loadGoWork discovers a Go workspace at root. found is false when
// go.work does not exist.
func loadGoWork(root string) (pkgs []*Package, global []string, found bool, err error) {
	raw, readErr := os.ReadFile(filepath.Join(root, "go.work"))
	if readErr != nil {
		return nil, nil, false, nil
	}
	dirs, err := parseGoWorkUses(string(raw))
	if err != nil {
		return nil, nil, false, fmt.Errorf("go.work: %w", err)
	}

	type member struct {
		pkg      *Package
		requires []string
		replaces []string // relative replacement dirs, workspace-root-relative
	}
	var members []member
	byModule := map[string]string{} // module path -> workspace dir
	for _, dir := range dirs {
		modRaw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dir), "go.mod"))
		if err != nil {
			return nil, nil, false, fmt.Errorf("use %s: %w", dir, err)
		}
		mod, err := parseGoMod(string(modRaw))
		if err != nil {
			return nil, nil, false, fmt.Errorf("%s/go.mod: %w", dir, err)
		}
		if mod.module == "" {
			return nil, nil, false, fmt.Errorf("%s/go.mod: missing module directive", dir)
		}
		if prev, dup := byModule[mod.module]; dup {
			return nil, nil, false, fmt.Errorf("module %q used by both %s and %s", mod.module, prev, dir)
		}
		byModule[mod.module] = dir
		var repl []string
		for _, r := range mod.replaceDirs {
			// Resolve `replace x => ../y` relative to the module dir.
			repl = append(repl, path.Clean(path.Join(dir, r)))
		}
		members = append(members, member{
			pkg:      &Package{Name: mod.module, Dir: dir, Ecosystem: GoMod},
			requires: mod.requires,
			replaces: repl,
		})
	}

	dirToModule := map[string]string{}
	for m, d := range byModule {
		dirToModule[d] = m
	}
	for _, m := range members {
		set := map[string]bool{}
		for _, req := range m.requires {
			if req == m.pkg.Name {
				continue
			}
			if _, ok := byModule[req]; ok {
				set[req] = true
			}
		}
		for _, rd := range m.replaces {
			if mod, ok := dirToModule[rd]; ok && mod != m.pkg.Name {
				set[mod] = true
			}
		}
		deps := make([]string, 0, len(set))
		for d := range set {
			deps = append(deps, d)
		}
		sort.Strings(deps)
		m.pkg.Deps = deps // Go has no dev-dependency concept
		pkgs = append(pkgs, m.pkg)
	}

	global = existingFiles(root, "go.work", "go.work.sum")
	return pkgs, global, true, nil
}

// parseGoWorkUses extracts every `use` directory from go.work, handling
// both the single-line and the parenthesized block form. Paths may be
// quoted (required when they contain spaces).
func parseGoWorkUses(src string) ([]string, error) {
	var dirs []string
	inBlock := false
	for i, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(stripGoComment(line))
		if line == "" {
			continue
		}
		switch {
		case inBlock:
			if line == ")" {
				inBlock = false
				continue
			}
			d, err := goPathToken(line)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			dirs = append(dirs, cleanUseDir(d))
		case line == "use (":
			inBlock = true
		case strings.HasPrefix(line, "use ") || strings.HasPrefix(line, "use\t"):
			rest := strings.TrimSpace(line[len("use"):])
			d, err := goPathToken(rest)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			dirs = append(dirs, cleanUseDir(d))
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

// goPathToken reads one path token from the start of s: a quoted string
// (spaces allowed) or a bare whitespace-delimited word.
func goPathToken(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("use directive without a path")
	}
	if s[0] == '"' {
		prefix, err := strconv.QuotedPrefix(s)
		if err != nil {
			return "", fmt.Errorf("bad quoted path %s", s)
		}
		return strconv.Unquote(prefix)
	}
	if fields := strings.Fields(s); len(fields) > 0 {
		return fields[0], nil
	}
	return "", fmt.Errorf("use directive without a path")
}

// cleanUseDir normalizes `./x` to `x` while keeping `..`-free semantics.
func cleanUseDir(d string) string {
	return path.Clean(filepath.ToSlash(d))
}

// goModFile is the subset of go.mod blastmap reads.
type goModFile struct {
	module      string
	requires    []string // required module paths
	replaceDirs []string // relative replacement targets (./x, ../y)
}

// parseGoMod extracts module path, require paths, and local replace
// targets. Version strings and // indirect comments are ignored — the
// edge exists either way.
func parseGoMod(src string) (goModFile, error) {
	var out goModFile
	block := "" // "", "require", "replace", "exclude", ...
	for i, line := range strings.Split(src, "\n") {
		line = stripGoComment(line)
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if block != "" {
			if fields[0] == ")" {
				block = ""
				continue
			}
			if err := out.addDirective(block, fields); err != nil {
				return out, fmt.Errorf("line %d: %w", i+1, err)
			}
			continue
		}
		switch fields[0] {
		case "module":
			if len(fields) < 2 {
				return out, fmt.Errorf("line %d: module directive without a path", i+1)
			}
			m, err := goUnquote(fields[1])
			if err != nil {
				return out, fmt.Errorf("line %d: %w", i+1, err)
			}
			out.module = m
		case "require", "replace", "exclude", "retract", "tool":
			if len(fields) == 2 && fields[1] == "(" {
				block = fields[0]
				continue
			}
			if err := out.addDirective(fields[0], fields[1:]); err != nil {
				return out, fmt.Errorf("line %d: %w", i+1, err)
			}
		}
	}
	return out, nil
}

// addDirective records one require/replace entry (block or single-line).
func (g *goModFile) addDirective(kind string, fields []string) error {
	switch kind {
	case "require":
		if len(fields) < 1 {
			return fmt.Errorf("empty require")
		}
		m, err := goUnquote(fields[0])
		if err != nil {
			return err
		}
		g.requires = append(g.requires, m)
	case "replace":
		// Forms: old [v] => new [v]; only relative-path targets matter.
		arrow := -1
		for i, f := range fields {
			if f == "=>" {
				arrow = i
				break
			}
		}
		if arrow < 0 || arrow+1 >= len(fields) {
			return nil // malformed or empty target: not an edge, not fatal
		}
		target, err := goUnquote(fields[arrow+1])
		if err != nil {
			return err
		}
		if strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../") {
			g.replaceDirs = append(g.replaceDirs, filepath.ToSlash(target))
		}
	}
	return nil
}

// stripGoComment removes a // comment (go.mod strings never contain //).
func stripGoComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		return line[:i]
	}
	return line
}

// goUnquote handles the optionally-quoted paths of go.mod/go.work syntax.
func goUnquote(s string) (string, error) {
	if strings.HasPrefix(s, `"`) {
		u, err := strconv.Unquote(s)
		if err != nil {
			return "", fmt.Errorf("bad quoted path %s", s)
		}
		return u, nil
	}
	return s, nil
}

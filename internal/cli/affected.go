// affected.go implements the flagship subcommand: collect changed files
// (git range, working tree, or stdin), discover the workspace, run the
// impact engine, render, and gate.
package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/JaydenCJ/blastmap/internal/gitio"
	"github.com/JaydenCJ/blastmap/internal/impact"
	"github.com/JaydenCJ/blastmap/internal/render"
)

// runAffected implements `blastmap affected` (also the default command).
func runAffected(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("affected")
	rangeExpr := fs.String("range", "", "git range to diff (default HEAD~1..HEAD)")
	uncommitted := fs.Bool("uncommitted", false, "include working-tree and untracked changes")
	stdinFiles := fs.Bool("stdin-files", false, "read changed paths from stdin instead of git")
	format := fs.String("format", "text", "output format: text, lines, or json")
	paths := fs.Bool("paths", false, "with --format lines, print directories instead of names")
	directOnly := fs.Bool("direct-only", false, "skip reverse-dependency propagation")
	withDeps := fs.Bool("with-deps", false, "also include dependencies of the affected set")
	noDev := fs.Bool("no-dev", false, "ignore dev-dependency edges")
	eco := fs.String("ecosystem", "auto", "npm, go, cargo, or auto")
	var extraGlobals multiFlag
	fs.Var(&extraGlobals, "global", "extra global path glob (repeatable)")
	noDefaultGlobals := fs.Bool("no-default-globals", false, "disable built-in global files")
	unclaimed := fs.String("unclaimed", "ignore", "ignore, affect-all, or error")
	path, ok := parseArgs(fs, args, stderr)
	if !ok {
		return ExitUsage
	}

	switch *format {
	case "text", "lines", "json":
	default:
		fmt.Fprintf(stderr, "blastmap affected: unknown format %q (want text, lines, or json)\n", *format)
		return ExitUsage
	}
	switch *unclaimed {
	case "ignore", "affect-all", "error":
	default:
		fmt.Fprintf(stderr, "blastmap affected: unknown --unclaimed mode %q (want ignore, affect-all, or error)\n", *unclaimed)
		return ExitUsage
	}
	if *stdinFiles && (*rangeExpr != "" || *uncommitted) {
		fmt.Fprintln(stderr, "blastmap affected: --stdin-files cannot be combined with --range or --uncommitted")
		return ExitUsage
	}

	ws, code := discover(path, *eco, stderr)
	if code != ExitOK {
		return code
	}

	files, source, prefix, code := collectChanges(ws.Root, *rangeExpr, *uncommitted, *stdinFiles, stdin, stderr)
	if code != ExitOK {
		return code
	}

	res := impact.Compute(ws, files, impact.Options{
		NoDev:            *noDev,
		DirectOnly:       *directOnly,
		WithDeps:         *withDeps,
		NoDefaultGlobals: *noDefaultGlobals,
		ExtraGlobals:     extraGlobals,
		AffectAll:        *unclaimed == "affect-all",
		Prefix:           prefix,
	})

	meta := render.Meta{Source: source}
	switch *format {
	case "text":
		render.Text(stdout, ws, res, meta)
	case "lines":
		render.Lines(stdout, res, *paths)
	case "json":
		if err := render.JSON(stdout, ws, res, meta); err != nil {
			fmt.Fprintf(stderr, "blastmap: %v\n", err)
			return ExitRuntime
		}
	}

	if *unclaimed == "error" && len(res.Unclaimed) > 0 {
		verb := "files are"
		if len(res.Unclaimed) == 1 {
			verb = "file is"
		}
		fmt.Fprintf(stderr, "blastmap: %d changed %s owned by no package (--unclaimed error):\n", len(res.Unclaimed), verb)
		for _, f := range res.Unclaimed {
			fmt.Fprintf(stderr, "  %s\n", f)
		}
		return ExitGate
	}
	return ExitOK
}

// collectChanges gathers the changed-file list from the selected source.
// It returns top-level-relative paths, a human label for the report
// header, and the workspace root's prefix inside the repository.
func collectChanges(wsRoot, rangeExpr string, uncommitted, stdinFiles bool, stdin io.Reader, stderr io.Writer) (files []string, source, prefix string, code int) {
	if stdinFiles {
		files, err := readStdinFiles(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "blastmap: reading stdin: %v\n", err)
			return nil, "", "", ExitRuntime
		}
		// Stdin paths are workspace-root-relative by contract; no prefix.
		return files, "stdin", "", ExitOK
	}

	repo, err := gitio.Open(wsRoot)
	if err != nil {
		fmt.Fprintf(stderr, "blastmap: %v\n", err)
		return nil, "", "", ExitRuntime
	}
	rel, err := filepath.Rel(repo.TopLevel, wsRoot)
	if err != nil {
		fmt.Fprintf(stderr, "blastmap: %v\n", err)
		return nil, "", "", ExitRuntime
	}
	prefix = filepath.ToSlash(rel)
	if prefix == "." {
		prefix = ""
	}

	seen := map[string]bool{}
	add := func(list []string) {
		for _, f := range list {
			if !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}
	var labels []string
	if rangeExpr == "" && !uncommitted {
		rangeExpr = "HEAD~1..HEAD"
	}
	if rangeExpr != "" {
		ranged, err := repo.ChangedInRange(rangeExpr)
		if err != nil {
			fmt.Fprintf(stderr, "blastmap: %v\n", err)
			return nil, "", "", ExitRuntime
		}
		add(ranged)
		labels = append(labels, rangeExpr)
	}
	if uncommitted {
		dirty, err := repo.Uncommitted()
		if err != nil {
			fmt.Fprintf(stderr, "blastmap: %v\n", err)
			return nil, "", "", ExitRuntime
		}
		add(dirty)
		labels = append(labels, "uncommitted")
	}
	return files, strings.Join(labels, " + "), prefix, ExitOK
}

// readStdinFiles accepts newline- or NUL-separated paths, so both
// `git diff --name-only` and `… -z` pipe straight in.
func readStdinFiles(r io.Reader) ([]string, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, chunk := range strings.Split(string(raw), "\x00") {
		for _, line := range strings.Split(chunk, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				files = append(files, filepath.ToSlash(line))
			}
		}
	}
	return files, nil
}

// Package cli implements the blastmap command-line interface. Run takes
// argv plus explicit streams and returns an exit code, so the entire
// surface is testable in-process without building a binary.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/JaydenCJ/blastmap/internal/impact"
	"github.com/JaydenCJ/blastmap/internal/render"
	"github.com/JaydenCJ/blastmap/internal/version"
	"github.com/JaydenCJ/blastmap/internal/workspace"
)

// Exit codes. Documented in the README; ExitGate is reserved for policy
// verdicts (--unclaimed error) so scripts can tell "worked, said no" from
// "broke".
const (
	ExitOK      = 0
	ExitGate    = 1
	ExitUsage   = 2
	ExitRuntime = 3
)

// Run dispatches argv and returns the process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runAffected(nil, stdin, stdout, stderr)
	}
	switch args[0] {
	case "affected":
		return runAffected(args[1:], stdin, stdout, stderr)
	case "list":
		return runList(args[1:], stdout, stderr)
	case "graph":
		return runGraph(args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "blastmap %s\n", version.Version)
		return ExitOK
	case "help", "--help", "-h":
		usage(stdout)
		return ExitOK
	default:
		if strings.HasPrefix(args[0], "-") {
			// Bare flags: treat as `affected` with flags.
			return runAffected(args, stdin, stdout, stderr)
		}
		if fi, err := os.Stat(args[0]); err == nil && fi.IsDir() {
			// Bare path: treat as `affected <path>`.
			return runAffected(args, stdin, stdout, stderr)
		}
		fmt.Fprintf(stderr, "blastmap: unknown command %q\n\n", args[0])
		usage(stderr)
		return ExitUsage
	}
}

// multiFlag is a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// newFlagSet builds a silent FlagSet; parse errors are reported by us.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

// parseArgs parses flags and extracts the optional trailing [path].
func parseArgs(fs *flag.FlagSet, args []string, stderr io.Writer) (string, bool) {
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "blastmap %s: %v\n", fs.Name(), err)
		return "", false
	}
	rest := fs.Args()
	switch len(rest) {
	case 0:
		return ".", true
	case 1:
		return rest[0], true
	default:
		fmt.Fprintf(stderr, "blastmap %s: expected at most one path argument, got %d\n", fs.Name(), len(rest))
		return "", false
	}
}

// discover loads the workspace or reports a runtime error.
func discover(path, eco string, stderr io.Writer) (*workspace.Workspace, int) {
	e, err := workspace.ParseEcosystem(eco)
	if err != nil {
		fmt.Fprintf(stderr, "blastmap: %v\n", err)
		return nil, ExitUsage
	}
	ws, err := workspace.Discover(path, e)
	if err != nil {
		fmt.Fprintf(stderr, "blastmap: %v\n", err)
		return nil, ExitRuntime
	}
	return ws, ExitOK
}

// runList implements `blastmap list`.
func runList(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("list")
	format := fs.String("format", "text", "output format: text, lines, or json")
	eco := fs.String("ecosystem", "auto", "restrict to npm, go, or cargo")
	paths := fs.Bool("paths", false, "with --format lines, print directories instead of names")
	path, ok := parseArgs(fs, args, stderr)
	if !ok {
		return ExitUsage
	}
	ws, code := discover(path, *eco, stderr)
	if code != ExitOK {
		return code
	}
	switch *format {
	case "text":
		render.ListText(stdout, ws)
	case "lines":
		render.ListLines(stdout, ws, *paths)
	case "json":
		if err := render.ListJSON(stdout, ws); err != nil {
			fmt.Fprintf(stderr, "blastmap: %v\n", err)
			return ExitRuntime
		}
	default:
		fmt.Fprintf(stderr, "blastmap list: unknown format %q (want text, lines, or json)\n", *format)
		return ExitUsage
	}
	return ExitOK
}

// runGraph implements `blastmap graph`.
func runGraph(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("graph")
	format := fs.String("format", "text", "output format: text, json, or dot")
	eco := fs.String("ecosystem", "auto", "restrict to npm, go, or cargo")
	noDev := fs.Bool("no-dev", false, "ignore dev-dependency edges")
	path, ok := parseArgs(fs, args, stderr)
	if !ok {
		return ExitUsage
	}
	ws, code := discover(path, *eco, stderr)
	if code != ExitOK {
		return code
	}
	g := impact.BuildGraph(ws, *noDev)
	switch *format {
	case "text":
		render.GraphText(stdout, ws, g)
	case "dot":
		render.Dot(stdout, ws, g)
	case "json":
		if err := render.GraphJSON(stdout, ws, g); err != nil {
			fmt.Fprintf(stderr, "blastmap: %v\n", err)
			return ExitRuntime
		}
	default:
		fmt.Fprintf(stderr, "blastmap graph: unknown format %q (want text, json, or dot)\n", *format)
		return ExitUsage
	}
	return ExitOK
}

// usage prints the top-level help.
func usage(w io.Writer) {
	fmt.Fprint(w, `blastmap — compute which workspace packages a git range affects

Usage:
  blastmap [affected] [flags] [path]   affected packages for a change set (default)
  blastmap list [flags] [path]         discovered workspace packages
  blastmap graph [flags] [path]        internal dependency graph
  blastmap version                     print the version

Affected flags:
  --range A..B       git range to diff (default HEAD~1..HEAD; use A...B for merge-base)
  --uncommitted      also include working-tree and untracked changes
  --stdin-files      read changed paths from stdin instead of git (one per line or NUL)
  --format FORMAT    text, lines, or json (default text)
  --paths            with --format lines, print package directories instead of names
  --direct-only      only directly changed packages; skip reverse dependencies
  --with-deps        also print dependencies of the affected set (status "dependency")
  --no-dev           ignore dev-dependency edges (npm devDependencies, Cargo dev-deps)
  --ecosystem NAME   npm, go, cargo, or auto (default auto = all detected)
  --global GLOB      extra path pattern whose change affects every package (repeatable)
  --no-default-globals   disable the built-in lockfile/manifest global list
  --unclaimed MODE   ignore | affect-all | error, for files no package owns (default ignore)

List/graph flags: --format, --ecosystem, --paths (list), --no-dev (graph).

Exit codes: 0 ok, 1 gate failure (--unclaimed error), 2 usage error, 3 runtime error.
`)
}

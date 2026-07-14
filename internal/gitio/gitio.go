// Package gitio is blastmap's only process boundary: it shells out to the
// local `git` binary to resolve the repository root and enumerate changed
// files. Everything is NUL-separated (-z) so exotic filenames survive, and
// rename detection is disabled so a moved file counts as a change in both
// its old and new package.
package gitio

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner executes git commands rooted at a repository top-level.
type Runner struct {
	TopLevel string
}

// Open resolves the git top-level directory containing dir.
func Open(dir string) (*Runner, error) {
	out, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%s is not inside a git repository: %w", dir, err)
	}
	top := strings.TrimSpace(string(out))
	return &Runner{TopLevel: filepath.Clean(top)}, nil
}

// ChangedInRange returns the top-level-relative paths touched by the
// given range expression. Both `A..B` (direct diff) and `A...B`
// (merge-base diff, the right choice for PR-style comparisons) are passed
// through to git untouched.
func (r *Runner) ChangedInRange(rangeExpr string) ([]string, error) {
	out, err := run(r.TopLevel, "diff", "--name-only", "--no-renames", "--no-relative", "-z", rangeExpr, "--")
	if err != nil {
		return nil, fmt.Errorf("git diff %s: %w", rangeExpr, err)
	}
	return splitNUL(out), nil
}

// Uncommitted returns files that differ from HEAD (staged and unstaged)
// plus untracked files that are not ignored — i.e. everything a developer
// is about to commit.
func (r *Runner) Uncommitted() ([]string, error) {
	seen := map[string]bool{}
	var files []string
	add := func(list []string) {
		for _, f := range list {
			if !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}
	// A repo with no commits yet has no HEAD to diff against; every
	// tracked file is then "new" and covered by the ls-files calls below.
	if _, err := run(r.TopLevel, "rev-parse", "--verify", "HEAD"); err == nil {
		out, err := run(r.TopLevel, "diff", "--name-only", "--no-renames", "--no-relative", "-z", "HEAD", "--")
		if err != nil {
			return nil, fmt.Errorf("git diff HEAD: %w", err)
		}
		add(splitNUL(out))
	} else {
		out, err := run(r.TopLevel, "ls-files", "--cached", "-z")
		if err != nil {
			return nil, fmt.Errorf("git ls-files: %w", err)
		}
		add(splitNUL(out))
	}
	out, err := run(r.TopLevel, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	add(splitNUL(out))
	return files, nil
}

// run executes git with a fully quiet environment and returns stdout.
func run(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", firstLine(msg))
	}
	return stdout.Bytes(), nil
}

// splitNUL splits -z output into clean slash paths.
func splitNUL(out []byte) []string {
	var files []string
	for _, f := range bytes.Split(out, []byte{0}) {
		if len(f) > 0 {
			files = append(files, filepath.ToSlash(string(f)))
		}
	}
	return files
}

// firstLine keeps error messages single-line for CLI display.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

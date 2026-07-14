// Tests for the git boundary. Each test fabricates a real (temporary,
// offline) repository with pinned identities and dates, so results are
// deterministic on every machine.
package gitio

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// gitEnv isolates git from the host user's configuration.
func gitEnv(seq int) []string {
	date := fmt.Sprintf("2026-01-%02dT10:00:00+00:00", seq)
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=Dev",
		"GIT_AUTHOR_EMAIL=dev@example.test",
		"GIT_COMMITTER_NAME=Dev",
		"GIT_COMMITTER_EMAIL=dev@example.test",
		"GIT_AUTHOR_DATE="+date,
		"GIT_COMMITTER_DATE="+date,
	)
}

func mustGit(t *testing.T, dir string, seq int, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(seq)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo initializes a repo with one initial commit.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, 1, "init", "-q", "-b", "main")
	write(t, dir, "base.txt", "base\n")
	mustGit(t, dir, 1, "add", "-A")
	mustGit(t, dir, 1, "commit", "-q", "--no-gpg-sign", "-m", "initial")
	return dir
}

func TestOpenResolvesTopLevelFromSubdir(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "sub/dir/file.txt", "x")
	r, err := Open(filepath.Join(dir, "sub", "dir"))
	if err != nil {
		t.Fatal(err)
	}
	// Compare resolved paths: macOS tempdirs involve /private symlinks.
	wantDir, _ := filepath.EvalSymlinks(dir)
	gotDir, _ := filepath.EvalSymlinks(r.TopLevel)
	if gotDir != wantDir {
		t.Fatalf("TopLevel = %q, want %q", gotDir, wantDir)
	}
	// And outside any repository Open must fail, not guess.
	if _, err := Open(t.TempDir()); err == nil {
		t.Fatal("Open must fail outside a git repository")
	}
}

func TestChangedInRange(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a/one.txt", "1")
	write(t, dir, "b/two.txt", "2")
	mustGit(t, dir, 2, "add", "-A")
	mustGit(t, dir, 2, "commit", "-q", "--no-gpg-sign", "-m", "add files")
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.ChangedInRange("HEAD~1..HEAD")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"a/one.txt", "b/two.txt"}) {
		t.Fatalf("changed = %v", got)
	}
}

func TestChangedInRangeMergeBase(t *testing.T) {
	// main gains main.txt after the branch point; the branch changes
	// feat.txt. A...B must diff from the merge-base, so main's later
	// commit does not leak into the branch's blast radius.
	dir := newRepo(t)
	mustGit(t, dir, 2, "checkout", "-q", "-b", "feature")
	write(t, dir, "feat.txt", "f")
	mustGit(t, dir, 2, "add", "-A")
	mustGit(t, dir, 2, "commit", "-q", "--no-gpg-sign", "-m", "feature work")
	mustGit(t, dir, 3, "checkout", "-q", "main")
	write(t, dir, "main.txt", "m")
	mustGit(t, dir, 3, "add", "-A")
	mustGit(t, dir, 3, "commit", "-q", "--no-gpg-sign", "-m", "main work")
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.ChangedInRange("main...feature")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"feat.txt"}) {
		t.Fatalf("merge-base diff = %v, want only feat.txt", got)
	}
}

func TestRenameCountsBothPaths(t *testing.T) {
	// A moved file must affect its old package AND its new one, so
	// rename detection is disabled: both paths appear.
	dir := newRepo(t)
	write(t, dir, "old/name.txt", "same content that git would detect as a rename\n")
	mustGit(t, dir, 2, "add", "-A")
	mustGit(t, dir, 2, "commit", "-q", "--no-gpg-sign", "-m", "add")
	mustGit(t, dir, 3, "mv", "old/name.txt", "new-name.txt")
	mustGit(t, dir, 3, "commit", "-q", "--no-gpg-sign", "-m", "move")
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.ChangedInRange("HEAD~1..HEAD")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"new-name.txt", "old/name.txt"}) {
		t.Fatalf("rename should yield both paths, got %v", got)
	}
}

func TestChangedInRangeBadRefFailsCleanly(t *testing.T) {
	r, err := Open(newRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ChangedInRange("no-such-ref..HEAD"); err == nil {
		t.Fatal("bad ref must produce an error")
	}
}

func TestUncommittedSeesWorktreeIndexAndUntracked(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "base.txt", "modified\n") // unstaged modification
	write(t, dir, "staged.txt", "s")
	mustGit(t, dir, 2, "add", "staged.txt") // staged addition
	write(t, dir, "untracked.txt", "u")     // untracked
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Uncommitted()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"base.txt", "staged.txt", "untracked.txt"}) {
		t.Fatalf("uncommitted = %v", got)
	}
}

func TestUncommittedInEmptyRepo(t *testing.T) {
	// No HEAD yet: staged and untracked files must still be visible.
	dir := t.TempDir()
	mustGit(t, dir, 1, "init", "-q", "-b", "main")
	write(t, dir, "fresh.txt", "f")
	mustGit(t, dir, 1, "add", "fresh.txt")
	write(t, dir, "loose.txt", "l")
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Uncommitted()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"fresh.txt", "loose.txt"}) {
		t.Fatalf("uncommitted in empty repo = %v", got)
	}
}

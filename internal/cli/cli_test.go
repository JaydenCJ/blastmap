// End-to-end tests: build real (temporary, offline) git repositories with
// fixed identities and dates, then run the CLI in-process and assert on
// stdout, stderr, and exit codes. Everything is deterministic — commit
// dates are pinned and git config is isolated from the host user.
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitEnv isolates git subprocesses from the host machine's configuration.
func gitEnv(seq int) []string {
	date := fmt.Sprintf("2026-02-%02dT10:00:00+00:00", seq)
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

func commit(t *testing.T, dir string, seq int, msg string) {
	t.Helper()
	mustGit(t, dir, seq, "add", "-A")
	mustGit(t, dir, seq, "commit", "-q", "--no-gpg-sign", "-m", msg)
}

// run executes the CLI in-process.
func run(args ...string) (code int, stdout, stderr string) {
	return runStdin("", args...)
}

func runStdin(stdin string, args ...string) (code int, stdout, stderr string) {
	var out, errBuf bytes.Buffer
	code = Run(args, strings.NewReader(stdin), &out, &errBuf)
	return code, out.String(), errBuf.String()
}

// npmRepo fabricates the canonical demo monorepo:
//
//	@demo/web -> @demo/ui -> @demo/utils
//	@demo/api -> @demo/utils
//	@demo/tsconfig (dev dep of web), @demo/docs (isolated)
//
// Commit 1 is the baseline; commit 2 touches utils + a root file.
func npmRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, 1, "init", "-q", "-b", "main")
	write(t, dir, "package.json", `{"name":"demo","private":true,"workspaces":["packages/*","apps/*"]}`)
	write(t, dir, "packages/utils/package.json", `{"name":"@demo/utils","version":"1.0.0"}`)
	write(t, dir, "packages/utils/src/index.js", "exports.id = (x) => x\n")
	write(t, dir, "packages/ui/package.json", `{"name":"@demo/ui","version":"1.0.0","dependencies":{"@demo/utils":"workspace:*"}}`)
	write(t, dir, "packages/tsconfig/package.json", `{"name":"@demo/tsconfig","version":"1.0.0"}`)
	write(t, dir, "packages/docs/package.json", `{"name":"@demo/docs","version":"1.0.0"}`)
	write(t, dir, "apps/web/package.json", `{"name":"@demo/web","version":"1.0.0","dependencies":{"@demo/ui":"workspace:*"},"devDependencies":{"@demo/tsconfig":"workspace:*"}}`)
	write(t, dir, "apps/api/package.json", `{"name":"@demo/api","version":"1.0.0","dependencies":{"@demo/utils":"workspace:*"}}`)
	commit(t, dir, 1, "baseline")
	write(t, dir, "packages/utils/src/format.js", "exports.fmt = (x) => `${x}`\n")
	write(t, dir, "NOTES.md", "scratch\n")
	commit(t, dir, 2, "change utils and add notes")
	return dir
}

func TestAffectedTextReportAndDefaultCommand(t *testing.T) {
	dir := npmRepo(t)
	code, out, errOut := run("affected", dir)
	if code != ExitOK {
		t.Fatalf("exit %d, stderr: %s", code, errOut)
	}
	for _, want := range []string{
		"blastmap affected — HEAD~1..HEAD, 2 files changed",
		"workspace: npm (6 packages)",
		"@demo/utils",
		"via @demo/web -> @demo/ui -> @demo/utils",
		"NOTES.md",
		"4 of 6 packages affected",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "@demo/docs") {
		t.Fatal("isolated package must not be affected")
	}
	// A bare path must behave exactly like `affected <path>`.
	codeB, outB, _ := run(dir)
	if codeB != code || outB != out {
		t.Fatal("bare path must behave exactly like `affected <path>`")
	}
}

func TestAffectedLinesFormats(t *testing.T) {
	dir := npmRepo(t)
	code, out, _ := run("affected", "--format", "lines", dir)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if out != "@demo/api\n@demo/ui\n@demo/utils\n@demo/web\n" {
		t.Fatalf("lines = %q", out)
	}
	code, out, _ = run("affected", "--format", "lines", "--paths", dir)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if out != "apps/api\napps/web\npackages/ui\npackages/utils\n" {
		t.Fatalf("paths = %q", out)
	}
}

func TestAffectedJSON(t *testing.T) {
	code, out, _ := run("affected", "--format", "json", npmRepo(t))
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	var doc struct {
		Tool          string `json:"tool"`
		SchemaVersion int    `json:"schema_version"`
		ChangedFiles  int    `json:"changed_files"`
		Affected      []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"affected"`
		Unclaimed []string `json:"unclaimed_files"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if doc.Tool != "blastmap" || doc.SchemaVersion != 1 || doc.ChangedFiles != 2 {
		t.Fatalf("envelope: %+v", doc)
	}
	if len(doc.Affected) != 4 || doc.Affected[0].Name != "@demo/utils" || doc.Affected[0].Status != "changed" {
		t.Fatalf("affected: %+v", doc.Affected)
	}
	if len(doc.Unclaimed) != 1 || doc.Unclaimed[0] != "NOTES.md" {
		t.Fatalf("unclaimed: %v", doc.Unclaimed)
	}
}

func TestAffectedDirectOnly(t *testing.T) {
	code, out, _ := run("affected", "--direct-only", "--format", "lines", npmRepo(t))
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if out != "@demo/utils\n" {
		t.Fatalf("direct-only lines = %q", out)
	}
}

func TestAffectedExplicitRange(t *testing.T) {
	dir := npmRepo(t)
	write(t, dir, "apps/api/handler.js", "exports.h = () => 204\n")
	commit(t, dir, 3, "api change")
	// HEAD~1..HEAD sees only the api change; HEAD~2..HEAD sees both.
	_, out, _ := run("affected", "--format", "lines", dir)
	if out != "@demo/api\n" {
		t.Fatalf("last commit should only touch api: %q", out)
	}
	_, out, _ = run("affected", "--range", "HEAD~2..HEAD", "--format", "lines", dir)
	if !strings.Contains(out, "@demo/utils") || !strings.Contains(out, "@demo/api") {
		t.Fatalf("two-commit range should include both: %q", out)
	}
}

func TestAffectedGlobalBlastAndOptOut(t *testing.T) {
	dir := npmRepo(t)
	write(t, dir, "package-lock.json", `{"lockfileVersion": 3}`)
	commit(t, dir, 3, "lockfile update")
	code, out, _ := run("affected", dir)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "global blast") || !strings.Contains(out, "6 of 6 packages affected") {
		t.Fatalf("lockfile change must blast everything:\n%s", out)
	}
	// With the built-in globals disabled the same change affects nothing.
	_, out, _ = run("affected", "--no-default-globals", "--format", "lines", dir)
	if out != "" {
		t.Fatalf("with defaults disabled the lockfile affects nothing: %q", out)
	}
}

func TestAffectedExtraGlobalFlag(t *testing.T) {
	dir := npmRepo(t)
	write(t, dir, "ci/pipeline.yml", "steps: []\n")
	commit(t, dir, 3, "ci change")
	_, out, _ := run("affected", "--global", "ci/**", "--format", "lines", dir)
	if len(strings.Fields(out)) != 6 {
		t.Fatalf("--global ci/** should blast all 6 packages: %q", out)
	}
}

func TestAffectedUnclaimedModes(t *testing.T) {
	dir := npmRepo(t)
	// error: exit 1 and the offending file on stderr.
	code, _, errOut := run("affected", "--unclaimed", "error", dir)
	if code != ExitGate {
		t.Fatalf("unclaimed file with --unclaimed error must exit %d, got %d", ExitGate, code)
	}
	if !strings.Contains(errOut, "NOTES.md") {
		t.Fatalf("stderr must name the unclaimed file: %s", errOut)
	}
	// affect-all: same change set blasts every package.
	code, out, _ := run("affected", "--unclaimed", "affect-all", "--format", "lines", dir)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if len(strings.Fields(out)) != 6 {
		t.Fatalf("affect-all should include all 6 packages: %q", out)
	}
}

func TestAffectedUncommitted(t *testing.T) {
	dir := npmRepo(t)
	write(t, dir, "apps/web/pages/index.js", "export default () => null\n")
	code, out, _ := run("affected", "--uncommitted", "--range", "HEAD..HEAD", "--format", "lines", dir)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if out != "@demo/web\n" {
		t.Fatalf("uncommitted change should affect only web: %q", out)
	}
}

func TestAffectedStdinFiles(t *testing.T) {
	// stdin mode needs no git at all — pipe any file list in.
	dir := npmRepo(t)
	code, out, _ := runStdin("packages/ui/src/button.js\n", "affected", "--stdin-files", "--format", "lines", dir)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if out != "@demo/ui\n@demo/web\n" {
		t.Fatalf("stdin lines = %q", out)
	}
	// Mixing stdin with git sources is ambiguous and refused.
	code, _, errOut := run("affected", "--stdin-files", "--range", "HEAD~1..HEAD", dir)
	if code != ExitUsage || !strings.Contains(errOut, "--stdin-files") {
		t.Fatalf("conflicting sources must be a usage error: %d %s", code, errOut)
	}
}

func TestAffectedNoDev(t *testing.T) {
	dir := npmRepo(t)
	write(t, dir, "packages/tsconfig/base.json", "{}\n")
	commit(t, dir, 3, "tsconfig change")
	_, withDev, _ := run("affected", "--format", "lines", dir)
	if withDev != "@demo/tsconfig\n@demo/web\n" {
		t.Fatalf("dev edge should pull web in: %q", withDev)
	}
	_, noDev, _ := run("affected", "--no-dev", "--format", "lines", dir)
	if noDev != "@demo/tsconfig\n" {
		t.Fatalf("--no-dev must drop web: %q", noDev)
	}
}

func TestAffectedWithDeps(t *testing.T) {
	dir := npmRepo(t)
	write(t, dir, "packages/ui/src/button.js", "b\n")
	commit(t, dir, 3, "ui change")
	_, out, _ := run("affected", "--with-deps", "--format", "lines", dir)
	// ui changed; web depends on it; utils (ui's dep) and tsconfig
	// (web's dev dep) are needed to build.
	if out != "@demo/tsconfig\n@demo/ui\n@demo/utils\n@demo/web\n" {
		t.Fatalf("with-deps lines = %q", out)
	}
}

func TestWorkspaceInRepoSubdirectory(t *testing.T) {
	// The workspace root sits below the git top-level; paths must be
	// translated and repo files outside the workspace reported.
	dir := t.TempDir()
	mustGit(t, dir, 1, "init", "-q", "-b", "main")
	write(t, dir, "frontend/package.json", `{"workspaces":["packages/*"]}`)
	write(t, dir, "frontend/packages/a/package.json", `{"name":"a"}`)
	write(t, dir, "backend/main.py", "print('hi')\n")
	commit(t, dir, 1, "baseline")
	write(t, dir, "frontend/packages/a/index.js", "x\n")
	write(t, dir, "backend/api.py", "pass\n")
	commit(t, dir, 2, "touch both halves")
	code, out, _ := run("affected", filepath.Join(dir, "frontend"))
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "a") || !strings.Contains(out, "outside workspace") {
		t.Fatalf("subdir workspace report wrong:\n%s", out)
	}
}

func TestGoWorkspaceEndToEnd(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, 1, "init", "-q", "-b", "main")
	write(t, dir, "go.work", "go 1.22\n\nuse (\n\t./svc/api\n\t./libs/core\n)\n")
	write(t, dir, "svc/api/go.mod", "module example.test/api\n\ngo 1.22\n\nrequire example.test/core v0.0.0\n")
	write(t, dir, "svc/api/main.go", "package main\n")
	write(t, dir, "libs/core/go.mod", "module example.test/core\n\ngo 1.22\n")
	write(t, dir, "libs/core/core.go", "package core\n")
	commit(t, dir, 1, "baseline")
	write(t, dir, "libs/core/util.go", "package core\n")
	commit(t, dir, 2, "core change")
	code, out, _ := run("affected", "--format", "lines", dir)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if out != "example.test/api\nexample.test/core\n" {
		t.Fatalf("go affected = %q", out)
	}
}

func TestCargoWorkspaceEndToEnd(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, 1, "init", "-q", "-b", "main")
	write(t, dir, "Cargo.toml", "[workspace]\nmembers = [\"crates/*\"]\n")
	write(t, dir, "crates/core/Cargo.toml", "[package]\nname = \"core\"\nversion = \"0.1.0\"\n")
	write(t, dir, "crates/core/src/lib.rs", "pub fn id() {}\n")
	write(t, dir, "crates/cli/Cargo.toml", "[package]\nname = \"cli\"\nversion = \"0.1.0\"\n\n[dependencies]\ncore = { path = \"../core\" }\n")
	write(t, dir, "crates/cli/src/main.rs", "fn main() {}\n")
	commit(t, dir, 1, "baseline")
	write(t, dir, "crates/core/src/extra.rs", "pub fn extra() {}\n")
	commit(t, dir, 2, "core change")
	code, out, _ := run("affected", "--format", "lines", dir)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if out != "cli\ncore\n" {
		t.Fatalf("cargo affected = %q", out)
	}
}

func TestListTextAndJSON(t *testing.T) {
	dir := npmRepo(t)
	code, out, _ := run("list", dir)
	if code != ExitOK || !strings.Contains(out, "blastmap list — npm, 6 packages") {
		t.Fatalf("list text wrong (%d):\n%s", code, out)
	}
	code, out, _ = run("list", "--format", "json", dir)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	var doc struct {
		Packages []struct {
			Name string   `json:"name"`
			Deps []string `json:"deps"`
		} `json:"packages"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Packages) != 6 {
		t.Fatalf("list json packages = %d", len(doc.Packages))
	}
}

func TestGraphDot(t *testing.T) {
	code, out, _ := run("graph", "--format", "dot", npmRepo(t))
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"digraph blastmap {", `"npm:@demo/web" -> "npm:@demo/ui";`} {
		if !strings.Contains(out, want) {
			t.Fatalf("dot missing %q:\n%s", want, out)
		}
	}
}

func TestVersionAndHelp(t *testing.T) {
	for _, argv := range [][]string{{"version"}, {"--version"}, {"-v"}} {
		code, out, _ := run(argv...)
		if code != ExitOK || out != "blastmap 0.1.0\n" {
			t.Fatalf("%v -> %d %q", argv, code, out)
		}
	}
	code, out, _ := run("help")
	if code != ExitOK || !strings.Contains(out, "Usage:") || !strings.Contains(out, "--unclaimed") {
		t.Fatalf("help output wrong (%d):\n%s", code, out)
	}
}

func TestUsageErrors(t *testing.T) {
	dir := npmRepo(t)
	if code, _, errOut := run("explode"); code != ExitUsage || !strings.Contains(errOut, "explode") {
		t.Fatalf("unknown command: %d %s", code, errOut)
	}
	if code, _, errOut := run("affected", "--format", "yaml", dir); code != ExitUsage || !strings.Contains(errOut, "yaml") {
		t.Fatalf("bad format: %d %s", code, errOut)
	}
	if code, _, _ := run("affected", "--unclaimed", "panic", dir); code != ExitUsage {
		t.Fatalf("bad unclaimed mode should exit %d, got %d", ExitUsage, code)
	}
	if code, _, errOut := run("affected", dir, "extra"); code != ExitUsage || !strings.Contains(errOut, "at most one path") {
		t.Fatalf("extra args: %d %s", code, errOut)
	}
	if code, _, _ := run("affected", "--ecosystem", "bazel", dir); code != ExitUsage {
		t.Fatalf("unknown ecosystem is a usage error: %d", code)
	}
}

func TestRuntimeErrors(t *testing.T) {
	// No workspace manifests at all.
	dir := t.TempDir()
	mustGit(t, dir, 1, "init", "-q", "-b", "main")
	write(t, dir, "README.md", "plain repo\n")
	commit(t, dir, 1, "baseline")
	code, _, errOut := run("affected", dir)
	if code != ExitRuntime || !strings.Contains(errOut, "no workspace found") {
		t.Fatalf("no workspace: %d %s", code, errOut)
	}
	// Forcing an ecosystem that is not present names its manifest.
	code, _, errOut = run("affected", "--ecosystem", "cargo", npmRepo(t))
	if code != ExitRuntime || !strings.Contains(errOut, "Cargo.toml") {
		t.Fatalf("forcing an absent ecosystem should fail helpfully: %d %s", code, errOut)
	}
	// A workspace outside any git repository fails for git sources…
	dir = t.TempDir()
	write(t, dir, "package.json", `{"workspaces":["p/*"]}`)
	write(t, dir, "p/a/package.json", `{"name":"a"}`)
	code, _, errOut = run("affected", dir)
	if code != ExitRuntime || !strings.Contains(errOut, "git") {
		t.Fatalf("non-repo should be a runtime error: %d %s", code, errOut)
	}
	// …but --stdin-files works without git entirely.
	code, out, _ := runStdin("p/a/x.js\n", "affected", "--stdin-files", "--format", "lines", dir)
	if code != ExitOK || out != "a\n" {
		t.Fatalf("stdin mode must not need git: %d %q", code, out)
	}
}

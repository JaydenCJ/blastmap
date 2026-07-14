// Tests for the minimal TOML reader. Every case is a shape that actually
// occurs in Cargo manifests found in the wild; the parser's job is to get
// workspace topology right, not to be a general TOML implementation.
package tomlmin

import (
	"reflect"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) Doc {
	t.Helper()
	doc, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return doc
}

func TestTablesAndTopLevelKeys(t *testing.T) {
	doc := mustParse(t, "title = \"root\"\n[package]\nname = \"core\"\nversion = \"0.1.0\"\n")
	if got, _ := doc.GetString("package", "name"); got != "core" {
		t.Fatalf("name = %q", got)
	}
	if got, _ := doc.GetString("", "title"); got != "root" {
		t.Fatalf("top-level key lost: %q", got)
	}
}

func TestWorkspaceMemberArrays(t *testing.T) {
	doc := mustParse(t, `[workspace]
members = ["crates/*", "tools/xtask"]
exclude = ["crates/legacy"]
`)
	if got := doc.GetStrings("workspace", "members"); !reflect.DeepEqual(got, []string{"crates/*", "tools/xtask"}) {
		t.Fatalf("members = %v", got)
	}
	if got := doc.GetStrings("workspace", "exclude"); !reflect.DeepEqual(got, []string{"crates/legacy"}) {
		t.Fatalf("exclude = %v", got)
	}
	// Multi-line arrays with trailing commas and inline comments are the
	// most common real-world members shape.
	doc = mustParse(t, "[workspace]\nmembers = [\n    \"a\",\n    \"b\",  # inline comment\n    \"c\",\n]\n")
	if got := doc.GetStrings("workspace", "members"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("multi-line members = %v", got)
	}
}

func TestInlineTableDependency(t *testing.T) {
	doc := mustParse(t, `[dependencies]
core = { path = "../core", version = "0.1" }
serde = "1.0"
`)
	dep := doc.Table("dependencies")["core"]
	if dep.Kind != KindTable {
		t.Fatalf("core should be an inline table, got kind %d", dep.Kind)
	}
	if dep.Tab["path"].Str != "../core" {
		t.Fatalf("path = %q", dep.Tab["path"].Str)
	}
	if doc.Table("dependencies")["serde"].Str != "1.0" {
		t.Fatal("plain version string lost")
	}
}

func TestDottedAndQuotedTablePaths(t *testing.T) {
	doc := mustParse(t, "[dependencies.core]\npath = \"../core\"\nfeatures = [\"std\"]\n")
	if got, _ := doc.GetString("dependencies.core", "path"); got != "../core" {
		t.Fatalf("dotted table path = %q", got)
	}
	// Quoted segments: [target.'cfg(unix)'.dependencies] normalizes to
	// an unquoted dotted path.
	doc = mustParse(t, "[target.'cfg(unix)'.dependencies]\nnix = { path = \"../nix-shim\" }\n")
	tab := doc.Table("target.cfg(unix).dependencies")
	if tab == nil {
		t.Fatalf("quoted segment table missing; have %v", keysOf(doc))
	}
	if tab["nix"].Tab["path"].Str != "../nix-shim" {
		t.Fatal("nested inline table path lost")
	}
	// Quoted keys inside a table are unquoted the same way.
	doc = mustParse(t, "[dependencies]\n\"weird.name\" = { path = \"../w\" }\n")
	if _, ok := doc.Table("dependencies")["weird.name"]; !ok {
		t.Fatalf("quoted key lost; have %v", keysOf(doc))
	}
}

func TestCommentsStrippedButHashInStringKept(t *testing.T) {
	doc := mustParse(t, `[package]
name = "with#hash" # this is the comment
`)
	if got, _ := doc.GetString("package", "name"); got != "with#hash" {
		t.Fatalf("name = %q", got)
	}
}

func TestStringForms(t *testing.T) {
	// Literal strings ('…') take no escapes; basic strings ("…") do.
	doc := mustParse(t, "[package]\nname = 'raw\\name'\ndescription = \"line\\nbreak \\\"quoted\\\"\"\n")
	if got, _ := doc.GetString("package", "name"); got != `raw\name` {
		t.Fatalf("literal string mangled: %q", got)
	}
	if got, _ := doc.GetString("package", "description"); got != "line\nbreak \"quoted\"" {
		t.Fatalf("escapes mishandled: %q", got)
	}
	// An unterminated string is structurally unusable and must fail.
	if _, err := Parse("[package]\nname = \"oops\n"); err == nil {
		t.Fatal("unterminated string must fail")
	}
}

func TestNonStringValuesDegradeGracefully(t *testing.T) {
	doc := mustParse(t, "[features]\ndefault = true\n[profile]\nopt-level = 3\n[t]\nmixed = [\"a\", 3, \"b\", true]\n")
	if v := doc.Table("features")["default"]; v.Kind != KindBool || !v.Bool {
		t.Fatalf("bool lost: %+v", v)
	}
	if v := doc.Table("profile")["opt-level"]; v.Kind != KindOther {
		t.Fatalf("numbers should degrade to KindOther, got %+v", v)
	}
	if got := doc.GetStrings("t", "mixed"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("mixed array filtering wrong: %v", got)
	}
}

func TestArrayOfTablesToleratedAsTable(t *testing.T) {
	// [[bin]] must not break parsing even though blastmap ignores it.
	doc := mustParse(t, "[[bin]]\nname = \"tool\"\n[package]\nname = \"x\"\n")
	if got, _ := doc.GetString("package", "name"); got != "x" {
		t.Fatal("parsing should continue past [[bin]]")
	}
	if got, _ := doc.GetString("bin", "name"); got != "tool" {
		t.Fatalf("[[bin]] content lost: %q", got)
	}
}

// keysOf lists doc tables for failure messages.
func keysOf(doc Doc) string {
	var b strings.Builder
	for k := range doc {
		b.WriteString(k)
		b.WriteString(";")
	}
	return b.String()
}

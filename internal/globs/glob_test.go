// Tests for the workspace glob dialect. The cases mirror real member
// patterns from npm, pnpm, and Cargo manifests, because that is exactly
// where these globs come from.
package globs

import "testing"

func TestLiteralAndNormalization(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"packages/core", "packages/core", true},
		{"packages/core", "packages/core2", false}, // no prefix-matching
		{"./packages/*", "packages/core", true},    // ./ in pattern ignored
		{"packages/*", "./packages/core", true},    // ./ in path ignored
		{"packages//core/", "packages/core", true}, // empty segments dropped
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestSegmentWildcards(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"packages/*", "packages/core", true},
		{"packages/*", "packages/core/deep", false}, // * never crosses /
		{"packages/*", "packages", false},           // parent itself excluded
		{"crates/tool-*", "crates/tool-fmt", true},  // Cargo-style prefix
		{"crates/tool-*", "crates/lib-fmt", false},
		{"*-app", "web-app", true}, // suffix star
		{"pkg?", "pkg1", true},     // ? = exactly one char
		{"pkg?", "pkg", false},
		{"pkg?", "pkg12", false},
		// The two-pointer matcher must backtrack: the star has to give
		// characters back to find the final "bc".
		{"a*bc", "aXbXbc", true},
		{"a*bc", "aXbXbd", false},
		// Byte-wise matching handles non-ASCII literals fine.
		{"包/*", "包/模块", true},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestDoubleStarSpansSegments(t *testing.T) {
	for _, path := range []string{"apps/a", "apps/x/a", "apps/x/y/a"} {
		if !Match("apps/**/a", path) {
			t.Fatalf("apps/**/a should match %q", path)
		}
	}
	if Match("apps/**/a", "libs/x/a") {
		t.Fatal("literal head must still be honored")
	}
	// pnpm treats "packages/**" as "packages and everything below it";
	// the zero-segment case keeps that semantics.
	if !Match("packages/**", "packages") {
		t.Fatal("trailing ** should match the anchor dir itself")
	}
	if !Match("packages/**", "packages/a/b/c") {
		t.Fatal("trailing ** should match deep descendants")
	}
}

func TestHasMeta(t *testing.T) {
	for pat, want := range map[string]bool{
		"packages/*":    true,
		"apps/**":       true,
		"pkg?":          true,
		"packages/core": false,
		"":              false,
	} {
		if got := HasMeta(pat); got != want {
			t.Fatalf("HasMeta(%q) = %v, want %v", pat, got, want)
		}
	}
}

func TestMatchAny(t *testing.T) {
	pats := []string{"packages/*", "tools/cli"}
	if !MatchAny(pats, "tools/cli") {
		t.Fatal("literal in list should match")
	}
	if !MatchAny(pats, "packages/x") {
		t.Fatal("glob in list should match")
	}
	if MatchAny(pats, "docs/readme") {
		t.Fatal("unrelated path must not match")
	}
	if MatchAny(nil, "anything") {
		t.Fatal("empty pattern list matches nothing")
	}
}

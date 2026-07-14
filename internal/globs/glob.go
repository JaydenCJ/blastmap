// Package globs implements the small glob dialect used by workspace member
// lists (npm/pnpm/Cargo) and by blastmap's own --global flag: `*` matches
// within one path segment, `?` matches a single character, and `**` matches
// any number of whole segments (including none). Patterns are always
// slash-separated and matched against slash-separated relative paths.
package globs

import "strings"

// Match reports whether the slash-separated path matches the pattern.
// Matching is purely lexical: no filesystem access, no symlink resolution.
func Match(pattern, path string) bool {
	return matchSegments(split(pattern), split(path))
}

// HasMeta reports whether the pattern contains any glob metacharacter, i.e.
// whether it can match anything beyond its literal self.
func HasMeta(pattern string) bool {
	return strings.ContainsAny(pattern, "*?")
}

// split normalizes a pattern or path into segments, dropping a leading "./"
// and empty segments produced by duplicate or trailing slashes.
func split(p string) []string {
	p = strings.TrimPrefix(p, "./")
	parts := strings.Split(p, "/")
	out := parts[:0]
	for _, s := range parts {
		if s != "" && s != "." {
			out = append(out, s)
		}
	}
	return out
}

// matchSegments matches pattern segments against path segments. `**` may
// swallow zero or more whole segments; every other segment must line up
// one-to-one.
func matchSegments(pat, path []string) bool {
	if len(pat) == 0 {
		return len(path) == 0
	}
	if pat[0] == "**" {
		// Try consuming 0..len(path) segments. The zero case makes
		// "a/**" match "a" itself only when followed by nothing, which
		// mirrors how workspace tools treat "packages/**".
		for skip := 0; skip <= len(path); skip++ {
			if matchSegments(pat[1:], path[skip:]) {
				return true
			}
		}
		return false
	}
	if len(path) == 0 {
		return false
	}
	if !matchSegment(pat[0], path[0]) {
		return false
	}
	return matchSegments(pat[1:], path[1:])
}

// matchSegment matches one pattern segment (with `*` and `?`) against one
// path segment. Neither wildcard ever crosses a `/`.
func matchSegment(pat, s string) bool {
	// Iterative star matching: remember the last `*` position and retry
	// from there on mismatch (classic two-pointer wildcard match).
	pi, si := 0, 0
	star, mark := -1, 0
	for si < len(s) {
		switch {
		case pi < len(pat) && (pat[pi] == '?' || pat[pi] == s[si]):
			pi++
			si++
		case pi < len(pat) && pat[pi] == '*':
			star = pi
			mark = si
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(pat) && pat[pi] == '*' {
		pi++
	}
	return pi == len(pat)
}

// MatchAny reports whether any pattern in the list matches the path.
func MatchAny(patterns []string, path string) bool {
	for _, p := range patterns {
		if Match(p, path) {
			return true
		}
	}
	return false
}

// Package tomlmin is a deliberately minimal TOML reader covering exactly
// the subset Cargo manifests use for workspace topology: [table] and
// [dotted.table] headers, string / boolean / string-array values
// (including multi-line arrays), inline tables like
// `{ path = "../core", version = "1" }`, and comments. It is not a general
// TOML parser — numbers, dates, and array-of-table syntax are tolerated
// and skipped rather than modeled, because blastmap never needs them.
package tomlmin

import (
	"fmt"
	"strings"
)

// Kind discriminates the small value union.
type Kind int

const (
	KindString Kind = iota
	KindBool
	KindArray
	KindTable
	KindOther // recognized but unmodeled (numbers, dates, array-of-tables)
)

// Value is one parsed TOML value.
type Value struct {
	Kind Kind
	Str  string           // KindString
	Bool bool             // KindBool
	Arr  []Value          // KindArray
	Tab  map[string]Value // KindTable (inline tables)
}

// Doc maps a table path (e.g. "package", "dependencies.serde",
// "workspace") to its key/value pairs. Keys at the top of the file live
// under the empty table path "".
type Doc map[string]map[string]Value

// Table returns the key/value map for a table path, or nil.
func (d Doc) Table(path string) map[string]Value { return d[path] }

// GetString returns the string at table/key, if present and a string.
func (d Doc) GetString(table, key string) (string, bool) {
	if v, ok := d[table][key]; ok && v.Kind == KindString {
		return v.Str, true
	}
	return "", false
}

// GetStrings returns the string elements of the array at table/key.
// Non-string elements are skipped.
func (d Doc) GetStrings(table, key string) []string {
	v, ok := d[table][key]
	if !ok || v.Kind != KindArray {
		return nil
	}
	var out []string
	for _, e := range v.Arr {
		if e.Kind == KindString {
			out = append(out, e.Str)
		}
	}
	return out
}

// Parse reads TOML source into a Doc. It fails only on structurally
// unusable input (unterminated strings or tables); unknown value shapes
// degrade to KindOther so a novel Cargo.toml never blocks analysis.
func Parse(src string) (Doc, error) {
	doc := Doc{"": {}}
	current := ""
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(stripComment(lines[i]))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			name, err := parseHeader(line, i+1)
			if err != nil {
				return nil, err
			}
			current = name
			if doc[current] == nil {
				doc[current] = map[string]Value{}
			}
			continue
		}
		eq := indexTopLevel(line, '=')
		if eq < 0 {
			return nil, fmt.Errorf("tomlmin: line %d: expected key = value", i+1)
		}
		key := unquoteKey(strings.TrimSpace(line[:eq]))
		raw := strings.TrimSpace(line[eq+1:])
		// Multi-line arrays: keep appending lines until brackets balance.
		for !balanced(raw) && i+1 < len(lines) {
			i++
			raw += "\n" + stripComment(lines[i])
		}
		val, err := parseValue(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("tomlmin: key %q: %w", key, err)
		}
		doc[current][key] = val
	}
	return doc, nil
}

// parseHeader handles `[a.b]` and `[[a.b]]`. Array-of-table headers are
// treated as plain tables: later entries overwrite earlier ones, which is
// fine for topology since Cargo never uses [[…]] for dependency tables.
func parseHeader(line string, lineno int) (string, error) {
	inner := line
	inner = strings.TrimPrefix(inner, "[")
	inner = strings.TrimPrefix(inner, "[")
	end := strings.LastIndex(inner, "]")
	if end < 0 {
		return "", fmt.Errorf("tomlmin: line %d: unterminated table header", lineno)
	}
	name := strings.TrimSpace(inner[:end])
	name = strings.TrimSuffix(name, "]")
	name = strings.TrimSpace(name)
	// Normalize quoted segments: [target.'cfg(unix)'.dependencies].
	parts := splitDotted(name)
	return strings.Join(parts, "."), nil
}

// parseValue parses a single (possibly multi-line) raw value.
func parseValue(raw string) (Value, error) {
	switch {
	case raw == "":
		return Value{}, fmt.Errorf("empty value")
	case raw[0] == '"' || raw[0] == '\'':
		s, _, err := readString(raw)
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: KindString, Str: s}, nil
	case raw == "true" || raw == "false":
		return Value{Kind: KindBool, Bool: raw == "true"}, nil
	case raw[0] == '[':
		elems, err := splitList(raw[1 : len(raw)-1])
		if err != nil {
			return Value{}, err
		}
		v := Value{Kind: KindArray}
		for _, e := range elems {
			ev, err := parseValue(e)
			if err != nil {
				return Value{}, err
			}
			v.Arr = append(v.Arr, ev)
		}
		return v, nil
	case raw[0] == '{':
		fields, err := splitList(raw[1 : len(raw)-1])
		if err != nil {
			return Value{}, err
		}
		v := Value{Kind: KindTable, Tab: map[string]Value{}}
		for _, f := range fields {
			eq := indexTopLevel(f, '=')
			if eq < 0 {
				return Value{}, fmt.Errorf("inline table field %q missing =", f)
			}
			fv, err := parseValue(strings.TrimSpace(f[eq+1:]))
			if err != nil {
				return Value{}, err
			}
			v.Tab[unquoteKey(strings.TrimSpace(f[:eq]))] = fv
		}
		return v, nil
	default:
		// Numbers, dates, bare identifiers: tolerated, not modeled.
		return Value{Kind: KindOther}, nil
	}
}

// readString reads a leading basic ("…") or literal ('…') string and
// returns its value plus the number of source bytes consumed.
func readString(raw string) (string, int, error) {
	quote := raw[0]
	var b strings.Builder
	for i := 1; i < len(raw); i++ {
		c := raw[i]
		if c == '\\' && quote == '"' {
			if i+1 >= len(raw) {
				return "", 0, fmt.Errorf("dangling escape")
			}
			i++
			switch raw[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '"', '\\':
				b.WriteByte(raw[i])
			default:
				// Unicode escapes etc.: keep verbatim; topology
				// strings (names, paths) never need them.
				b.WriteByte('\\')
				b.WriteByte(raw[i])
			}
			continue
		}
		if c == quote {
			return b.String(), i + 1, nil
		}
		b.WriteByte(c)
	}
	return "", 0, fmt.Errorf("unterminated string")
}

// splitList splits the inside of [...] or {...} on top-level commas,
// respecting nested brackets, braces, and strings. Empty elements
// (trailing commas, blank lines) are dropped.
func splitList(inner string) ([]string, error) {
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(inner); i++ {
		switch c := inner[i]; c {
		case '"', '\'':
			_, n, err := readString(inner[i:])
			if err != nil {
				return nil, err
			}
			i += n - 1
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, inner[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, inner[start:])
	var trimmed []string
	for _, e := range out {
		if s := strings.TrimSpace(e); s != "" {
			trimmed = append(trimmed, s)
		}
	}
	return trimmed, nil
}

// balanced reports whether brackets and braces balance outside strings —
// used to detect multi-line arrays that must absorb following lines.
func balanced(s string) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\'':
			_, n, err := readString(s[i:])
			if err != nil {
				return false
			}
			i += n - 1
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		}
	}
	return depth == 0
}

// stripComment removes a # comment unless the # sits inside a string.
func stripComment(line string) string {
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"', '\'':
			_, n, err := readString(line[i:])
			if err != nil {
				return line // unterminated here; let the parser decide
			}
			i += n - 1
		case '#':
			return line[:i]
		}
	}
	return line
}

// indexTopLevel finds the first occurrence of sep outside strings,
// brackets, and braces. Returns -1 if absent.
func indexTopLevel(s string, sep byte) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\'':
			_, n, err := readString(s[i:])
			if err != nil {
				return -1
			}
			i += n - 1
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		case sep:
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitDotted splits a table path on dots outside quoted segments.
func splitDotted(name string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(name); i++ {
		switch name[i] {
		case '"', '\'':
			_, n, err := readString(name[i:])
			if err != nil {
				return []string{name}
			}
			i += n - 1
		case '.':
			parts = append(parts, unquoteKey(strings.TrimSpace(name[start:i])))
			start = i + 1
		}
	}
	parts = append(parts, unquoteKey(strings.TrimSpace(name[start:])))
	return parts
}

// unquoteKey strips one layer of quotes from a key if present.
func unquoteKey(k string) string {
	if len(k) >= 2 && (k[0] == '"' || k[0] == '\'') && k[len(k)-1] == k[0] {
		if s, _, err := readString(k); err == nil {
			return s
		}
	}
	return k
}

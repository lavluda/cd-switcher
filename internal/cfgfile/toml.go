package cfgfile

import (
	"regexp"
	"sort"
	"strings"
)

// TOML files are edited line by line rather than decoded and re-encoded, so
// the user's comments, ordering and formatting survive everywhere except the
// exact keys and tables being changed. Reading uses github.com/BurntSushi/toml.

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// TOMLKey quotes a key when it is not a bare key.
func TOMLKey(k string) string {
	if bareKey.MatchString(k) {
		return k
	}
	return TOMLString(k)
}

// TOMLString encodes a basic string. JSON string escapes are valid TOML.
func TOMLString(s string) string {
	b, _ := marshal(s)
	return string(b)
}

// TOMLStrings encodes an array of strings.
func TOMLStrings(ss []string) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = TOMLString(s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// TOMLInline encodes a string map as an inline table, keys sorted.
func TOMLInline(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = TOMLKey(k) + " = " + TOMLString(m[k])
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// header returns the dotted key of a "[table]" line, or nil for any other line
// (including "[[array]]" headers, which are reported as non-matching tables).
func header(line string) (parts []string, isHeader bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "[") {
		return nil, false
	}
	if strings.HasPrefix(t, "[[") {
		return []string{"\x00array"}, true
	}
	end := strings.LastIndex(t, "]")
	if end < 0 {
		return nil, false
	}
	return splitDotted(t[1:end]), true
}

// splitDotted splits a dotted key, honouring quoted parts.
func splitDotted(s string) []string {
	var parts []string
	var cur strings.Builder
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			} else if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '.':
			parts = append(parts, strings.TrimSpace(cur.String()))
			cur.Reset()
		case c == ' ' || c == '\t':
		default:
			cur.WriteByte(c)
		}
	}
	return append(parts, strings.TrimSpace(cur.String()))
}

func hasPrefix(parts, prefix []string) bool {
	if len(parts) < len(prefix) {
		return false
	}
	for i := range prefix {
		if parts[i] != prefix[i] {
			return false
		}
	}
	return true
}

func lines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

func join(ls []string) string {
	if len(ls) == 0 {
		return ""
	}
	return strings.Join(ls, "\n") + "\n"
}

// SetTOMLRootKey sets a key above the first table to an already encoded
// value, or removes it when value is empty. A new key goes after the existing
// root keys.
func SetTOMLRootKey(text, key, value string) string {
	ls := lines(text)
	end := len(ls)
	for i, l := range ls {
		if _, ok := header(l); ok {
			end = i
			break
		}
	}
	for i := 0; i < end; i++ {
		if k, ok := rootKeyOf(ls[i]); ok && k == key {
			if value == "" {
				return join(append(ls[:i:i], ls[i+1:]...))
			}
			ls[i] = TOMLKey(key) + " = " + value
			return join(ls)
		}
	}
	if value == "" {
		return text
	}
	insert := end
	for insert > 0 && strings.TrimSpace(ls[insert-1]) == "" {
		insert-- // keep the blank line before the first table
	}
	out := append([]string{}, ls[:insert]...)
	out = append(out, TOMLKey(key)+" = "+value)
	return join(append(out, ls[insert:]...))
}

// TOMLRootValue returns a root key's encoded value as written, if present.
func TOMLRootValue(text, key string) (string, bool) {
	for _, l := range lines(text) {
		if _, ok := header(l); ok {
			break
		}
		if k, ok := rootKeyOf(l); ok && k == key {
			return strings.TrimSpace(l[strings.Index(l, "=")+1:]), true
		}
	}
	return "", false
}

func rootKeyOf(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") {
		return "", false
	}
	eq := strings.Index(t, "=")
	if eq <= 0 {
		return "", false
	}
	parts := splitDotted(t[:eq])
	if len(parts) != 1 {
		return "", false
	}
	return parts[0], true
}

// SetTOMLTable replaces table name (a dotted key such as
// mcp_servers.context7) together with its sub-tables by body, which must hold
// complete TOML including its own header lines. An empty body removes the
// table. A new table is appended at the end of the file.
func SetTOMLTable(text string, name []string, body string) string {
	ls := lines(text)
	var out []string
	replaced := false
	for i := 0; i < len(ls); {
		parts, ok := header(ls[i])
		if !ok || !hasPrefix(parts, name) {
			out = append(out, ls[i])
			i++
			continue
		}
		// Skip this table's block: lines up to the next header that is not
		// one of its own sub-tables.
		j := i + 1
		for j < len(ls) {
			if p, ok := header(ls[j]); ok && !hasPrefix(p, name) {
				break
			}
			j++
		}
		// Comments and blanks just before the next table belong to it.
		for j > i+1 && j < len(ls) {
			t := strings.TrimSpace(ls[j-1])
			if t != "" && !strings.HasPrefix(t, "#") {
				break
			}
			j--
		}
		if !replaced && body != "" {
			out = append(out, lines(body)...)
		}
		// A removed table must not leave two blank lines behind.
		if body == "" && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" && j < len(ls) && strings.TrimSpace(ls[j]) == "" {
			j++
		}
		replaced = true
		i = j
		if body == "" && i >= len(ls) {
			for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
				out = out[:len(out)-1] // a removed last table leaves no trailing blank lines
			}
		}
	}
	if !replaced && body != "" {
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, lines(body)...)
	}
	return join(out)
}

// TableHeader builds "[a.b]" with each part quoted when needed.
func TableHeader(parts ...string) string {
	q := make([]string, len(parts))
	for i, p := range parts {
		q[i] = TOMLKey(p)
	}
	return "[" + strings.Join(q, ".") + "]"
}

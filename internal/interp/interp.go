// Package interp expands variables the way Docker Compose does in a compose
// file and in an env file, with no shell involved: $NAME, ${NAME},
// ${NAME:-default}, ${NAME-default}, ${NAME:+replacement},
// ${NAME+replacement}, ${NAME:?message}, ${NAME?message} and $$ for a
// literal dollar sign.
//
// An error never holds the value of a variable, only its name.
package interp

import (
	"errors"
	"fmt"
	"strings"
)

// state carries the lookup and the names found unset, in order, without
// repeats.
type state struct {
	lookup func(string) (string, bool)
	unset  []string
	seen   map[string]bool
}

func (st *state) get(name string) (string, bool) {
	if st.lookup == nil {
		return "", false
	}
	return st.lookup(name)
}

func (st *state) markUnset(name string) {
	if st.seen == nil {
		st.seen = map[string]bool{}
	}
	if !st.seen[name] {
		st.seen[name] = true
		st.unset = append(st.unset, name)
	}
}

// Expand expands the variables in s. A variable that is not set expands to the
// empty string and its name is returned in unset (once, in order of first use),
// unless the expression gives a default, a replacement or a required message
// for it. ${NAME:?msg} and ${NAME?msg} are errors that name the variable. The
// text of a default or a replacement is expanded too, and may hold further
// ${...}; it is expanded only when it is used.
func Expand(s string, lookup func(string) (string, bool)) (out string, unset []string, err error) {
	st := &state{lookup: lookup}
	out, err = st.expand(s)
	if err != nil {
		return "", st.unset, err
	}
	return out, st.unset, nil
}

func isNameStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isNameChar(c byte) bool {
	return isNameStart(c) || c >= '0' && c <= '9'
}

func (st *state) expand(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c != '$' {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(s) {
			b.WriteByte('$')
			i++
			continue
		}
		switch n := s[i+1]; {
		case n == '$':
			b.WriteByte('$')
			i += 2
		case n == '{':
			end, err := closing(s, i+2)
			if err != nil {
				return "", err
			}
			v, err := st.braced(s[i+2 : end])
			if err != nil {
				return "", err
			}
			b.WriteString(v)
			i = end + 1
		case isNameStart(n):
			j := i + 2
			for j < len(s) && isNameChar(s[j]) {
				j++
			}
			name := s[i+1 : j]
			v, ok := st.get(name)
			if !ok {
				st.markUnset(name)
			}
			b.WriteString(v)
			i = j
		default:
			b.WriteByte('$')
			i++
		}
	}
	return b.String(), nil
}

// closing returns the index of the } that closes a ${ whose body starts at
// start.
func closing(s string, start int) (int, error) {
	depth := 1
	for i := start; i < len(s); i++ {
		switch {
		case s[i] == '$' && i+1 < len(s) && s[i+1] == '$':
			i++
		case s[i] == '$' && i+1 < len(s) && s[i+1] == '{':
			depth++
			i++
		case s[i] == '}':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, errors.New("a ${ is not closed")
}

func (st *state) braced(body string) (string, error) {
	if body == "" || !isNameStart(body[0]) {
		return "", fmt.Errorf("invalid variable expression ${%s}", body)
	}
	j := 1
	for j < len(body) && isNameChar(body[j]) {
		j++
	}
	name, rest := body[:j], body[j:]
	v, ok := st.get(name)
	switch {
	case rest == "":
		if !ok {
			st.markUnset(name)
		}
		return v, nil
	case strings.HasPrefix(rest, ":-"):
		if !ok || v == "" {
			return st.expand(rest[2:])
		}
		return v, nil
	case strings.HasPrefix(rest, "-"):
		if !ok {
			return st.expand(rest[1:])
		}
		return v, nil
	case strings.HasPrefix(rest, ":+"):
		if ok && v != "" {
			return st.expand(rest[2:])
		}
		return "", nil
	case strings.HasPrefix(rest, "+"):
		if ok {
			return st.expand(rest[1:])
		}
		return "", nil
	case strings.HasPrefix(rest, ":?"):
		if !ok || v == "" {
			return "", requiredError(name, rest[2:])
		}
		return v, nil
	case strings.HasPrefix(rest, "?"):
		if !ok {
			return "", requiredError(name, rest[1:])
		}
		return v, nil
	}
	return "", fmt.Errorf("invalid variable expression for %s", name)
}

func requiredError(name, msg string) error {
	if msg == "" {
		return fmt.Errorf("required variable %s is missing a value", name)
	}
	return fmt.Errorf("required variable %s is missing a value: %s", name, msg)
}

// KV is one entry of an env file. Set is false for a bare NAME that has no
// value in the environment of the caller.
type KV struct {
	Name  string
	Value string
	Set   bool
}

// ParseEnvFile reads the content of an env file in the Compose format: lines
// of KEY=VAL with an optional "export " in front, blank lines, lines that
// start with #, and " #" that starts a comment after an unquoted value. A value
// in single quotes is literal. A value in double quotes understands \n, \r,
// \t, \\, \" and \$ and is interpolated; both quoted forms may span lines. An
// unquoted value is interpolated. A bare NAME takes its value from lookup. A
// byte order mark at the start and CRLF line ends are accepted.
//
// It returns the entries in file order (a name may appear twice) and the names
// of the variables that were not set while interpolating. An error names the
// line, never a value.
func ParseEnvFile(data []byte, lookup func(string) (string, bool)) ([]KV, []string, error) {
	text := strings.TrimPrefix(string(data), "\xef\xbb\xbf")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	st := &state{lookup: lookup}
	var out []KV
	for n := 0; n < len(lines); n++ {
		line := strings.TrimLeft(lines[n], " \t")
		if strings.TrimSpace(line) == "" || line[0] == '#' {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "export"); ok && rest != "" && (rest[0] == ' ' || rest[0] == '\t') {
			line = strings.TrimLeft(rest, " \t")
		}
		name, raw, hasValue := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !validEnvName(name) {
			return nil, st.unset, fmt.Errorf("line %d: not NAME=value", n+1)
		}
		if !hasValue {
			v, ok := st.get(name)
			out = append(out, KV{Name: name, Value: v, Set: ok})
			continue
		}
		start := n
		val, consumed, err := st.envValue(raw, lines[n+1:])
		if err != nil {
			return nil, st.unset, fmt.Errorf("line %d: %s: %w", start+1, name, err)
		}
		n += consumed
		out = append(out, KV{Name: name, Value: val, Set: true})
	}
	return out, st.unset, nil
}

func validEnvName(name string) bool {
	return name != "" && !strings.ContainsAny(name, " \t'\"$#{}")
}

// envValue reads the value after the = of one entry. more holds the lines
// after the current one, for a quoted value that goes on. consumed is how many
// of them were used.
func (st *state) envValue(raw string, more []string) (val string, consumed int, err error) {
	v := strings.TrimLeft(raw, " \t")
	if v == "" {
		return "", 0, nil
	}
	switch v[0] {
	case '\'':
		body, rest, used, ok := quoted(v[1:], more, '\'')
		if !ok {
			return "", 0, errors.New("a single quote is not closed")
		}
		if err := onlyComment(rest); err != nil {
			return "", 0, err
		}
		return body, used, nil
	case '"':
		body, rest, used, ok := quoted(v[1:], more, '"')
		if !ok {
			return "", 0, errors.New("a double quote is not closed")
		}
		if err := onlyComment(rest); err != nil {
			return "", 0, err
		}
		out, err := st.expand(unescape(body))
		return out, used, err
	}
	for i := 1; i < len(v); i++ {
		if v[i] == '#' && (v[i-1] == ' ' || v[i-1] == '\t') {
			v = v[:i]
			break
		}
	}
	out, err := st.expand(strings.TrimRight(v, " \t"))
	return out, 0, err
}

// quoted finds the closing quote of a quoted value that starts in first (the
// text after the opening quote) and may go on in more. Inside double quotes a
// backslash protects the next character. It returns the text between the
// quotes (with the line ends it spans), the text after the closing quote and
// how many lines of more were used.
func quoted(first string, more []string, q byte) (body, rest string, used int, ok bool) {
	cur := first
	var b strings.Builder
	for {
		for i := 0; i < len(cur); i++ {
			if q == '"' && cur[i] == '\\' && i+1 < len(cur) {
				b.WriteByte(cur[i])
				b.WriteByte(cur[i+1])
				i++
				continue
			}
			if cur[i] == q {
				return b.String(), cur[i+1:], used, true
			}
			b.WriteByte(cur[i])
		}
		if used >= len(more) {
			return "", "", 0, false
		}
		b.WriteByte('\n')
		cur = more[used]
		used++
	}
}

func onlyComment(rest string) error {
	rest = strings.TrimLeft(rest, " \t")
	if rest == "" || rest[0] == '#' {
		return nil
	}
	return errors.New("text after the closing quote")
}

// unescape applies the escapes of a double quoted value. \$ becomes $$ so that
// the interpolation that follows keeps a literal dollar sign.
func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '\\':
			b.WriteByte('\\')
		case '"':
			b.WriteByte('"')
		case '$':
			b.WriteString("$$")
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

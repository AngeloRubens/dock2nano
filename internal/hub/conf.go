package hub

import (
	"sort"
	"strings"
)

// Directive is one directive of a configuration written in the syntax of
// nginx: words, then ";" or a block in braces.
type Directive struct {
	// Block names the blocks around the directive, joined with "/"; it is
	// empty at the top level.
	Block string

	// Name is the first word.
	Name string

	// Args are the other words, joined with one space.
	Args string

	// IsBlock says the directive opens a block instead of ending with ";".
	IsBlock bool

	// Start and End are the offsets in the text of the first character of the
	// directive and of the one after its ";" (after the name, for a block).
	Start int
	End   int
}

// scanner walks the text of a configuration.
type scanner struct {
	text string
	pos  int
}

func (s *scanner) skipSpace() {
	for s.pos < len(s.text) {
		switch c := s.text[s.pos]; {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			s.pos++
		case c == '#':
			for s.pos < len(s.text) && s.text[s.pos] != '\n' {
				s.pos++
			}
		default:
			return
		}
	}
}

// word reads one word, quotes included; "" at the end of the text or at a
// character that ends a directive.
func (s *scanner) word() string {
	start := s.pos
	if s.pos < len(s.text) && (s.text[s.pos] == '"' || s.text[s.pos] == '\'') {
		q := s.text[s.pos]
		s.pos++
		for s.pos < len(s.text) && s.text[s.pos] != q {
			if s.text[s.pos] == '\\' {
				s.pos++
			}
			s.pos++
		}
		if s.pos < len(s.text) {
			s.pos++
		}
		if s.pos > len(s.text) {
			s.pos = len(s.text)
		}
		return s.text[start:s.pos]
	}
	for s.pos < len(s.text) {
		c := s.text[s.pos]
		if c == '$' && s.pos+1 < len(s.text) && s.text[s.pos+1] == '{' {
			for s.pos < len(s.text) && s.text[s.pos] != '}' {
				s.pos++
			}
			if s.pos < len(s.text) {
				s.pos++
			}
			continue
		}
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == ';' || c == '{' || c == '}' || c == '#' {
			break
		}
		s.pos++
	}
	return s.text[start:s.pos]
}

// ParseDirectives reads the directives of a configuration, at every depth. A
// text that is not well formed gives the directives read until the fault.
func ParseDirectives(text string) []Directive {
	var out []Directive
	var stack []string
	s := &scanner{text: text}
	for {
		s.skipSpace()
		if s.pos >= len(s.text) {
			return out
		}
		switch s.text[s.pos] {
		case '}':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			s.pos++
			continue
		case ';', '{':
			s.pos++
			continue
		}
		d := Directive{Block: strings.Join(stack, "/"), Start: s.pos}
		var words []string
		closed := false
		for !closed {
			s.skipSpace()
			if s.pos >= len(s.text) {
				return out
			}
			switch s.text[s.pos] {
			case ';':
				s.pos++
				d.End = s.pos
				closed = true
			case '{':
				d.IsBlock = true
				d.End = s.pos
				s.pos++
				closed = true
			case '}':
				return out
			default:
				words = append(words, s.word())
			}
		}
		d.Name = words[0]
		d.Args = strings.Join(words[1:], " ")
		out = append(out, d)
		if d.IsBlock {
			stack = append(stack, d.Name)
		}
	}
}

// Setting is a directive to set at the top level.
type Setting struct {
	Name  string
	Value string
}

// Change is what SetTopLevel did for one setting.
type Change struct {
	Name string

	// Old is the value there was; empty when the directive was not there.
	Old string

	// New is the value now.
	New string

	// Added says the directive was not there and was put at the top.
	Added bool
}

// SetTopLevel makes the top level of a configuration carry the settings: a
// directive that is there with another value is rewritten in place (every
// copy of it), one that is missing is put at the first line. Everything else
// stays as it is. It returns the new text and what changed; a setting that is
// already as asked changes nothing.
func SetTopLevel(text string, settings []Setting) (string, []Change) {
	ds := ParseDirectives(text)
	type edit struct {
		start, end int
		with       string
	}
	var edits []edit
	var changes []Change
	var added []string
	for _, st := range settings {
		found := false
		old := ""
		differs := false
		for _, d := range ds {
			if d.Block != "" || d.IsBlock || d.Name != st.Name {
				continue
			}
			found = true
			old = d.Args
			if d.Args != st.Value {
				differs = true
				edits = append(edits, edit{d.Start, d.End, st.Name + " " + st.Value + ";"})
			}
		}
		switch {
		case !found:
			added = append(added, st.Name+" "+st.Value+";")
			changes = append(changes, Change{Name: st.Name, New: st.Value, Added: true})
		case differs:
			changes = append(changes, Change{Name: st.Name, Old: old, New: st.Value})
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	for _, e := range edits {
		text = text[:e.start] + e.with + text[e.end:]
	}
	if len(added) > 0 {
		text = strings.Join(added, "\n") + "\n" + text
	}
	return text, changes
}

// TopLevelValue is the value of the first top-level directive of that name.
func TopLevelValue(text, name string) (string, bool) {
	for _, d := range ParseDirectives(text) {
		if d.Block == "" && !d.IsBlock && d.Name == name {
			return d.Args, true
		}
	}
	return "", false
}

// Values are the distinct values of the directives of that name at any depth,
// sorted.
func Values(text, name string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range ParseDirectives(text) {
		if !d.IsBlock && d.Name == name && !seen[d.Args] {
			seen[d.Args] = true
			out = append(out, d.Args)
		}
	}
	sort.Strings(out)
	return out
}

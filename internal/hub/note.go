package hub

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// PlaceholderRe is a placeholder of a note of a row, without its braces:
	// names joined by commas (the first one that is set is used), an optional
	// :ident (a quoted SQL identifier), :lit (a SQL string literal) or :sh (a
	// word of a shell), an optional |default.
	PlaceholderRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*(?:,[A-Za-z_][A-Za-z0-9_]*)*)(:ident|:lit|:sh)?(?:\|(.*))?$`)

	// SectionRe is a conditional section of a note: [[if TEST]]text[[end]] or
	// [[unless TEST]]text[[end]].
	SectionRe = regexp.MustCompile(`(?s)\[\[(if|unless) ([^\]]*)\]\](.*?)\[\[end\]\]`)

	// TermRe is one term of the test of a section: NAMES, NAMES!="text" or
	// NAMES!=OTHER, with an optional "not " before it. A test is terms joined
	// by " and ".
	TermRe = regexp.MustCompile(`^(not )?([A-Za-z_][A-Za-z0-9_]*(?:,[A-Za-z_][A-Za-z0-9_]*)*)(?:!=("[^"]*"|[A-Za-z_][A-Za-z0-9_]*))?$`)

	sectionTokenRe = regexp.MustCompile(`\[\[(?:(?:if|unless) [^\]]*|end)\]\]`)
)

// NoteFixed are the placeholders that the tool itself fills for a package of
// the row, and the field of the row each one comes from.
var NoteFixed = map[string]string{
	"HUB_SUPERUSER": "superusers",
	"HUB_FACTS":     "package_facts",
}

// ValidateNote checks the text of a note: sections are closed and not nested,
// every test is made of known terms, every brace pair is a placeholder or is
// not a placeholder at all (text). It returns the first fault.
func ValidateNote(note string) error {
	open := false
	rest := note
	for {
		i := strings.Index(rest, "[[")
		if i < 0 {
			break
		}
		tok := sectionTokenRe.FindString(rest[i:])
		if tok == "" || !strings.HasPrefix(rest[i:], tok) {
			return fmt.Errorf("%q is not [[if TEST]], [[unless TEST]] or [[end]]", clip(rest[i:]))
		}
		switch {
		case tok == "[[end]]":
			if !open {
				return fmt.Errorf("[[end]] without a section before it")
			}
			open = false
		default:
			if open {
				return fmt.Errorf("sections do not nest: %q inside a section", tok)
			}
			open = true
			test := strings.TrimSpace(strings.SplitN(tok[2:len(tok)-2], " ", 2)[1])
			for _, term := range strings.Split(test, " and ") {
				if !TermRe.MatchString(strings.TrimSpace(term)) {
					return fmt.Errorf("the test %q has the term %q, which is not NAMES, NAMES!=\"text\" or NAMES!=OTHER", test, term)
				}
			}
		}
		rest = rest[i+len(tok):]
	}
	if open {
		return fmt.Errorf("a section is not closed with [[end]]")
	}
	// the braces of the text outside the tests
	text := SectionRe.ReplaceAllString(note, "$3")
	for {
		i := strings.IndexByte(text, '{')
		if i < 0 {
			return nil
		}
		j := strings.IndexByte(text[i:], '}')
		if j < 0 {
			return nil
		}
		j += i
		inner := text[i+1 : j]
		if !PlaceholderRe.MatchString(inner) && looksLikePlaceholder(inner) {
			return fmt.Errorf("the placeholder {%s} is not {NAME}, {A,B}, {NAME:ident}, {NAME:lit}, {NAME:sh} or {NAME|default}", inner)
		}
		text = text[j+1:]
	}
}

// looksLikePlaceholder tells whether a brace pair holds what was meant as a
// placeholder: one word-like token, with no space.
func looksLikePlaceholder(s string) bool {
	return s != "" && !strings.ContainsAny(s, " \t\n")
}

// NoteUses tells whether the note names the placeholder name.
func NoteUses(note, name string) bool {
	return strings.Contains(note, name)
}

func clip(s string) string {
	if len(s) > 30 {
		return s[:30] + "..."
	}
	return s
}

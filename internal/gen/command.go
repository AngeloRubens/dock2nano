package gen

import (
	"strings"

	"github.com/AngeloRubens/dock2nano/internal/compose"
	"github.com/AngeloRubens/dock2nano/internal/docker"
)

// EffectiveCommand returns what the container really starts with: the
// entrypoint and the command after the compose file has overridden the image.
//
// The rules are those of Docker Compose. A command in the file replaces the CMD
// of the image. An entrypoint in the file replaces the ENTRYPOINT of the image
// and also clears its CMD, unless the file gives a command too. A key that is
// given empty (an empty list or string) clears the setting without replacing
// it. A nil entrypoint means the container has no entrypoint at all, not that
// the image default applies: the image default is already folded in. img may
// be nil.
//
// A command in shell form is split into words when it is simple: words
// separated by blanks, with single and double quotes around words. When it
// holds anything a shell would act on (| & ; < > ( ) $ ` \ * ? [ ] ! a newline,
// a ~ or # at the start of a word, an assignment before the program, or a quote
// that is not closed) it is not split and is run as
// ["/bin/sh", "-c", string], which is what Docker does for a shell form.
func EffectiveCommand(s *compose.Service, img *docker.ImageConfig) (entrypoint *[]string, cmd []string) {
	var ep []string
	switch {
	case s.Entrypoint.IsSet():
		ep = words(s.Entrypoint)
	case img != nil:
		ep = append(ep, img.Entrypoint...)
	}
	if len(ep) > 0 {
		entrypoint = &ep
	}
	switch {
	case s.Command.IsSet():
		cmd = words(s.Command)
	case s.Entrypoint.IsSet():
	case img != nil:
		cmd = append(cmd, img.Cmd...)
	}
	return entrypoint, cmd
}

// words turns a command of the compose file into a list of arguments.
func words(c compose.Command) []string {
	if c.Shell == "" {
		return append([]string(nil), c.Exec...)
	}
	if w, ok := splitSimple(c.Shell); ok {
		return w
	}
	return []string{"/bin/sh", "-c", c.Shell}
}

// splitSimple splits s like a shell would, but only when s has no shell
// syntax. ok is false when it has.
func splitSimple(s string) (out []string, ok bool) {
	var cur strings.Builder
	inWord := false
	var quote rune
	flush := func() {
		if inWord {
			out = append(out, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for _, r := range s {
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch r {
			case '"':
				quote = 0
			case '$', '`', '\\':
				return nil, false
			default:
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t':
			flush()
		case strings.ContainsRune("|&;<>()$`\\*?[]!\n\r", r):
			return nil, false
		case (r == '~' || r == '#') && !inWord:
			return nil, false
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, false
	}
	flush()
	if len(out) > 0 && isAssignment(out[0]) {
		return nil, false
	}
	return out, true
}

// isAssignment reports whether w looks like NAME=value, which a shell reads as
// a variable set before the program.
func isAssignment(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

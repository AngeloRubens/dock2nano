package gen

import (
	"errors"
	"fmt"
	"strings"

	"github.com/AngeloRubens/dock2nano/internal/compose"
	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/interp"
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
// be nil. The Entrypoint and Cmd of the image are used as they are.
//
// A string in the file is first interpolated (${VAR} and the like, with lookup)
// and then split into words like Compose does: blanks separate words, single
// quotes keep their text as it is, double quotes keep it except for a
// backslash before ", \ or $, and outside quotes a backslash protects the
// next character. No shell is involved: nothing is expanded after the split
// (no $, no globbing, no pipes). An unclosed quote is an error. A list is
// interpolated element by element and not split.
func EffectiveCommand(s *compose.Service, img *docker.ImageConfig, lookup func(string) (string, bool)) (entrypoint *[]string, cmd []string, err error) {
	var ep []string
	switch {
	case s.Entrypoint.IsSet():
		ep, err = words(s.Entrypoint, lookup)
		if err != nil {
			return nil, nil, fmt.Errorf("entrypoint: %w", err)
		}
	case img != nil:
		ep = append(ep, img.Entrypoint...)
	}
	if len(ep) > 0 {
		entrypoint = &ep
	}
	switch {
	case s.Command.IsSet():
		cmd, err = words(s.Command, lookup)
		if err != nil {
			return nil, nil, fmt.Errorf("command: %w", err)
		}
	case s.Entrypoint.IsSet():
	case img != nil:
		cmd = append(cmd, img.Cmd...)
	}
	return entrypoint, cmd, nil
}

// words turns a command of the compose file into a list of arguments.
func words(c compose.Command, lookup func(string) (string, bool)) ([]string, error) {
	if c.Shell == "" {
		var out []string
		for _, e := range c.Exec {
			v, _, err := interp.Expand(e, lookup)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	v, _, err := interp.Expand(c.Shell, lookup)
	if err != nil {
		return nil, err
	}
	return splitWords(v)
}

// splitWords splits s into words the way a shell does it, but does nothing else.
func splitWords(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			out = append(out, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch r {
		case ' ', '\t', '\n', '\r':
			flush()
		case '\\':
			inWord = true
			if i+1 < len(rs) {
				i++
				cur.WriteRune(rs[i])
			} else {
				cur.WriteRune(r)
			}
		case '\'':
			inWord = true
			closed := false
			for i++; i < len(rs); i++ {
				if rs[i] == '\'' {
					closed = true
					break
				}
				cur.WriteRune(rs[i])
			}
			if !closed {
				return nil, errors.New("a single quote is not closed")
			}
		case '"':
			inWord = true
			closed := false
			for i++; i < len(rs); i++ {
				if rs[i] == '"' {
					closed = true
					break
				}
				if rs[i] == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\' || rs[i+1] == '$') {
					i++
				}
				cur.WriteRune(rs[i])
			}
			if !closed {
				return nil, errors.New("a double quote is not closed")
			}
		default:
			inWord = true
			cur.WriteRune(r)
		}
	}
	flush()
	return out, nil
}

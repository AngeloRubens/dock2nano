package dockerfile

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// maxSize bounds the input, so that a huge file ends in an error.
const maxSize = 16 << 20

// Only these parser directives exist; a comment like "# a=b" is a comment.
var directiveRe = regexp.MustCompile(`(?i)^#\s*(syntax|escape|check)\s*=\s*(.+?)\s*$`)

// A heredoc word such as <<EOF, <<-EOF, <<'EOF' or 2<<EOF.
var heredocRe = regexp.MustCompile(`^[0-9]*<<(-?)(["']?)([A-Za-z_][A-Za-z0-9_]*)(["']?)$`)

var portRe = regexp.MustCompile(`^[0-9]+(-[0-9]+)?(/(?i:tcp|udp|sctp))?$`)

// known lists the instructions that the parser understands; each of them needs
// arguments.
var known = map[string]bool{
	"FROM": true, "ARG": true, "ENV": true, "WORKDIR": true, "USER": true,
	"EXPOSE": true, "VOLUME": true, "LABEL": true, "SHELL": true, "STOPSIGNAL": true,
	"HEALTHCHECK": true, "COPY": true, "ADD": true, "RUN": true, "CMD": true,
	"ENTRYPOINT": true,
}

type parser struct {
	lines []string
	pos   int
	esc   byte
	f     *File
	cur   *Stage
}

// Parse parses the content of a Dockerfile. Errors name the line, counted
// from 1, where the instruction starts.
func Parse(r io.Reader) (*File, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSize {
		return nil, fmt.Errorf("the Dockerfile is too large (more than %d bytes)", maxSize)
	}
	lines := strings.Split(strings.TrimPrefix(string(data), "\ufeff"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	p := &parser{lines: lines, esc: '\\', f: &File{}}
	if err := p.directives(); err != nil {
		return nil, err
	}
	for {
		line, ins, ok := p.next()
		if !ok {
			break
		}
		verb, rest := splitVerb(ins)
		if err := p.instruction(line, verb, rest); err != nil {
			return nil, err
		}
	}
	if len(p.f.Stages) == 0 {
		return nil, fmt.Errorf("the Dockerfile has no FROM")
	}
	return p.f, nil
}

func errAt(line int, format string, args ...any) error {
	return fmt.Errorf("line %d: %s", line, fmt.Sprintf(format, args...))
}

// note records something that is not modelled, in the file and in the stage.
func (p *parser) note(line int, what string) {
	msg := fmt.Sprintf("line %d: %s", line, what)
	p.f.Unknown = append(p.f.Unknown, msg)
	if p.cur != nil {
		p.cur.Unknown = append(p.cur.Unknown, msg)
	}
}

// vars records that a value holds a $ (an ARG or ENV reference) that is kept
// as written and not resolved.
func (p *parser) vars(line int, what string, vals ...string) {
	for _, v := range vals {
		if strings.Contains(v, "$") {
			p.note(line, what+" holds a variable (kept as written)")
			return
		}
	}
}

// directives reads the parser directives at the top of the file. They end at
// the first line that is not a directive.
func (p *parser) directives() error {
	for p.pos < len(p.lines) {
		m := directiveRe.FindStringSubmatch(p.lines[p.pos])
		if m == nil {
			return nil
		}
		line := p.pos + 1
		key := strings.ToLower(m[1])
		if key == "escape" {
			if m[2] != "\\" && m[2] != "`" {
				return errAt(line, "escape directive must be \\ or `, got %q", m[2])
			}
			p.esc = m[2][0]
		} else {
			p.f.Unknown = append(p.f.Unknown, fmt.Sprintf("line %d: parser directive %s", line, key))
		}
		p.pos++
	}
	return nil
}

func skippable(l string) bool {
	t := strings.TrimSpace(l)
	return t == "" || strings.HasPrefix(t, "#")
}

// next returns the next instruction with its continuation lines joined, and the
// line where it starts. Blank lines and comment lines are skipped, also inside
// a continued instruction. The joined lines are not separated by anything: the
// escape character and the line break are removed.
func (p *parser) next() (int, string, bool) {
	for p.pos < len(p.lines) && skippable(p.lines[p.pos]) {
		p.pos++
	}
	if p.pos >= len(p.lines) {
		return 0, "", false
	}
	line := p.pos + 1
	var sb strings.Builder
	for {
		cur := p.lines[p.pos]
		p.pos++
		t := strings.TrimRight(cur, " \t")
		if t == "" || t[len(t)-1] != p.esc {
			sb.WriteString(cur)
			break
		}
		sb.WriteString(t[:len(t)-1])
		for p.pos < len(p.lines) && skippable(p.lines[p.pos]) {
			p.pos++
		}
		if p.pos >= len(p.lines) {
			break
		}
	}
	return line, strings.TrimSpace(sb.String()), true
}

// splitVerb splits an instruction into its name in upper case and the rest.
func splitVerb(s string) (string, string) {
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return strings.ToUpper(s), ""
	}
	return strings.ToUpper(s[:i]), strings.TrimSpace(s[i:])
}

func (p *parser) instruction(line int, verb, rest string) error {
	if rest == "" && known[verb] {
		return errAt(line, "%s needs arguments", verb)
	}
	switch {
	case verb == "FROM":
		return p.from(line, rest)
	case p.cur == nil && verb == "ARG":
		args, err := p.args(line, rest)
		p.f.Args = append(p.f.Args, args...)
		return err
	case p.cur == nil:
		return errAt(line, "%s before the first FROM", verb)
	}
	s := p.cur
	switch verb {
	case "ARG":
		args, err := p.args(line, rest)
		s.Args = append(s.Args, args...)
		return err
	case "ENV":
		kv, err := p.nameValues(line, verb, rest)
		for _, e := range kv {
			p.vars(line, "ENV "+e.Name, e.Value)
		}
		s.Env = append(s.Env, kv...)
		return err
	case "LABEL":
		kv, err := p.nameValues(line, verb, rest)
		s.Labels = append(s.Labels, kv...)
		return err
	case "WORKDIR":
		v, err := p.value(line, verb, rest)
		s.WorkDir = v
		return err
	case "USER":
		v, err := p.value(line, verb, rest)
		s.User = v
		return err
	case "STOPSIGNAL":
		v, err := p.value(line, verb, rest)
		s.StopSignal = v
		return err
	case "EXPOSE":
		words, err := scanWords(rest, p.esc, true)
		if err != nil {
			return errAt(line, "EXPOSE: %v", err)
		}
		for _, w := range words {
			if !strings.Contains(w, "$") && !portRe.MatchString(w) {
				return errAt(line, "EXPOSE: %q is not a port", w)
			}
		}
		p.vars(line, "EXPOSE", words...)
		s.Expose = append(s.Expose, words...)
	case "VOLUME":
		list, isJSON, err := parseJSONList(rest)
		if err != nil {
			return errAt(line, "VOLUME: %v", err)
		}
		if !isJSON {
			if list, err = scanWords(rest, p.esc, true); err != nil {
				return errAt(line, "VOLUME: %v", err)
			}
		}
		p.vars(line, "VOLUME", list...)
		s.Volumes = append(s.Volumes, list...)
	case "SHELL":
		list, isJSON, err := parseJSONList(rest)
		if err != nil {
			return errAt(line, "SHELL: %v", err)
		}
		if !isJSON {
			return errAt(line, "SHELL requires a JSON list")
		}
		if len(list) == 0 {
			return errAt(line, "SHELL needs at least one element")
		}
		s.Shell = list
	case "CMD":
		c, err := command(line, verb, rest)
		s.Cmd = c
		return err
	case "ENTRYPOINT":
		c, err := command(line, verb, rest)
		s.Entrypoint = c
		return err
	case "HEALTHCHECK":
		h, err := p.healthcheck(line, rest)
		if err != nil {
			return err
		}
		s.Healthcheck = h
	case "COPY", "ADD":
		return p.copyInstruction(line, verb, rest)
	case "RUN":
		return p.run(line, rest)
	default:
		p.note(line, verb)
	}
	return nil
}

type option struct {
	name  string
	value string
}

// leadingFlags splits the --name[=value] options at the start of s from the
// rest of it.
func leadingFlags(s string) ([]option, string) {
	var out []option
	for strings.HasPrefix(s, "--") {
		tok := s
		if end := strings.IndexAny(s, " \t"); end >= 0 {
			tok = s[:end]
			s = strings.TrimLeft(s[end:], " \t")
		} else {
			s = ""
		}
		name, value, _ := strings.Cut(tok[2:], "=")
		out = append(out, option{name: name, value: value})
	}
	return out, s
}

func (p *parser) from(line int, rest string) error {
	opts, rest := leadingFlags(rest)
	st := &Stage{Line: line}
	var other []string
	for _, o := range opts {
		if !strings.EqualFold(o.name, "platform") {
			other = append(other, "FROM --"+o.name)
			continue
		}
		if o.value == "" {
			return errAt(line, "FROM --platform needs a value")
		}
		st.Platform = o.value
	}
	words, err := scanWords(rest, p.esc, true)
	if err != nil {
		return errAt(line, "FROM: %v", err)
	}
	switch {
	case len(words) == 0:
		return errAt(line, "FROM needs an image")
	case len(words) == 1:
	case len(words) == 2 && strings.EqualFold(words[1], "AS"):
		return errAt(line, "FROM AS needs a name")
	case len(words) == 3 && strings.EqualFold(words[1], "AS"):
		st.Name = words[2]
	default:
		return errAt(line, "FROM expects image [AS name]")
	}
	st.Base = words[0]
	if st.Name != "" {
		for _, prev := range p.f.Stages {
			if strings.EqualFold(prev.Name, st.Name) {
				return errAt(line, "duplicate stage name %q", st.Name)
			}
		}
	}
	p.f.Stages = append(p.f.Stages, st)
	p.cur = st
	p.vars(line, "FROM", st.Base, st.Platform)
	for _, o := range other {
		p.note(line, o)
	}
	return nil
}

func (p *parser) args(line int, rest string) ([]Arg, error) {
	words, err := scanWords(rest, p.esc, true)
	if err != nil {
		return nil, errAt(line, "ARG: %v", err)
	}
	var out []Arg
	for _, w := range words {
		name, def, has := strings.Cut(w, "=")
		if name == "" {
			return nil, errAt(line, "ARG: empty name")
		}
		if has {
			p.vars(line, "ARG "+name, def)
		}
		out = append(out, Arg{Name: name, Default: def, HasDefault: has})
	}
	return out, nil
}

// nameValues reads the arguments of ENV or LABEL: either name=value pairs or,
// in the old form, a name and the rest of the line as its value.
func (p *parser) nameValues(line int, verb, rest string) ([]EnvVar, error) {
	words, err := scanWords(rest, p.esc, true)
	if err != nil {
		return nil, errAt(line, "%s: %v", verb, err)
	}
	if len(words) == 0 {
		return nil, errAt(line, "%s needs arguments", verb)
	}
	if strings.Contains(words[0], "=") {
		var out []EnvVar
		for _, w := range words {
			name, val, ok := strings.Cut(w, "=")
			if !ok {
				return nil, errAt(line, "%s: %q has no value, expected name=value", verb, w)
			}
			if name == "" {
				return nil, errAt(line, "%s: empty name", verb)
			}
			out = append(out, EnvVar{Name: name, Value: val})
		}
		return out, nil
	}
	i := strings.IndexAny(rest, " \t")
	if i < 0 {
		return nil, errAt(line, "%s %s has no value", verb, words[0])
	}
	if words[0] == "" {
		return nil, errAt(line, "%s: empty name", verb)
	}
	val, err := scanWords(strings.TrimSpace(rest[i:]), p.esc, false)
	if err != nil {
		return nil, errAt(line, "%s: %v", verb, err)
	}
	return []EnvVar{{Name: words[0], Value: val[0]}}, nil
}

// value reads the single value of WORKDIR, USER or STOPSIGNAL.
func (p *parser) value(line int, verb, rest string) (string, error) {
	w, err := scanWords(rest, p.esc, false)
	if err != nil {
		return "", errAt(line, "%s: %v", verb, err)
	}
	p.vars(line, verb, w[0])
	return w[0], nil
}

// command reads the arguments of CMD, ENTRYPOINT, RUN or HEALTHCHECK CMD. As
// in Docker, text that starts with [ but is not a JSON list is the shell form
// ("[ -f x ] && y"); an error is given only when it looks like a JSON list
// that is broken, such as ["a", "b".
func command(line int, verb, rest string) (Command, error) {
	list, isJSON, err := parseJSONList(rest)
	if err != nil {
		return Command{}, errAt(line, "%s: %v", verb, err)
	}
	if isJSON {
		return Command{Exec: list, Set: true}, nil
	}
	return Command{Shell: rest, Set: true}, nil
}

// parseJSONList reads a JSON list of strings. isJSON is false when s does not
// start with [ or is not a JSON list; err is set when s starts with [" or ['
// and is not valid.
func parseJSONList(s string) (list []string, isJSON bool, err error) {
	if !strings.HasPrefix(s, "[") {
		return nil, false, nil
	}
	if err := json.Unmarshal([]byte(s), &list); err != nil {
		if len(s) > 1 && (s[1] == '"' || s[1] == '\'') {
			return nil, false, fmt.Errorf("malformed JSON list: %v", err)
		}
		return nil, false, nil
	}
	return list, true, nil
}

func (p *parser) healthcheck(line int, rest string) (*Healthcheck, error) {
	opts, body := leadingFlags(rest)
	h := &Healthcheck{}
	for _, o := range opts {
		switch strings.ToLower(o.name) {
		case "interval", "timeout", "start-period", "start-interval", "retries":
			if o.value == "" {
				return nil, errAt(line, "HEALTHCHECK --%s needs a value", o.name)
			}
			h.Options = append(h.Options, "--"+o.name+"="+o.value)
		default:
			p.note(line, "HEALTHCHECK --"+o.name)
		}
	}
	word, args := splitVerb(body)
	switch word {
	case "NONE":
		if args != "" {
			return nil, errAt(line, "HEALTHCHECK NONE takes no arguments")
		}
		h.None = true
	case "CMD":
		if args == "" {
			return nil, errAt(line, "HEALTHCHECK CMD needs a command")
		}
		c, err := command(line, "HEALTHCHECK CMD", args)
		if err != nil {
			return nil, err
		}
		h.Test = c
	default:
		return nil, errAt(line, "HEALTHCHECK expects NONE or CMD")
	}
	return h, nil
}

func (p *parser) copyInstruction(line int, verb, rest string) error {
	opts, body := leadingFlags(rest)
	c := Copy{Instruction: verb, Line: line}
	for _, o := range opts {
		name := strings.ToLower(o.name)
		if (name == "from" || name == "chown" || name == "chmod") && o.value == "" {
			return errAt(line, "%s --%s needs a value", verb, name)
		}
		switch name {
		case "from":
			c.From = o.value
		case "chown":
			c.Chown = o.value
		case "chmod":
			c.Chmod = o.value
		default:
			p.note(line, verb+" --"+o.name)
		}
	}
	found, err := p.heredoc(line, body)
	if err != nil {
		return err
	}
	if found {
		p.note(line, verb+" heredoc")
		return nil
	}
	list, isJSON, err := parseJSONList(body)
	if err != nil {
		return errAt(line, "%s: %v", verb, err)
	}
	if !isJSON {
		if list, err = scanWords(body, p.esc, true); err != nil {
			return errAt(line, "%s: %v", verb, err)
		}
	}
	if len(list) < 2 {
		return errAt(line, "%s needs a source and a destination", verb)
	}
	c.Sources = list[:len(list)-1]
	c.Dest = list[len(list)-1]
	p.vars(line, verb, list...)
	p.cur.Copies = append(p.cur.Copies, c)
	return nil
}

func (p *parser) run(line int, rest string) error {
	opts, body := leadingFlags(rest)
	for _, o := range opts {
		p.note(line, "RUN --"+o.name)
	}
	if body == "" {
		return errAt(line, "RUN needs a command")
	}
	found, err := p.heredoc(line, body)
	if err != nil {
		return err
	}
	if found {
		p.note(line, "RUN heredoc")
		return nil
	}
	c, err := command(line, "RUN", body)
	if err != nil {
		return err
	}
	p.cur.Runs = append(p.cur.Runs, Run{Command: c, Line: line})
	return nil
}

type heredocMark struct {
	name string
	dash bool
}

// heredoc looks for heredoc words (<<EOF) in the shell form of body. If it
// finds any, it skips their bodies in the input, up to each terminator, and
// returns true. The bodies are not parsed.
func (p *parser) heredoc(line int, body string) (bool, error) {
	if _, isJSON, _ := parseJSONList(body); isJSON {
		return false, nil
	}
	var marks []heredocMark
	for _, w := range strings.Fields(body) {
		m := heredocRe.FindStringSubmatch(w)
		if m != nil && m[2] == m[4] {
			marks = append(marks, heredocMark{name: m[3], dash: m[1] == "-"})
		}
	}
	for _, h := range marks {
		found := false
		for p.pos < len(p.lines) {
			l := p.lines[p.pos]
			p.pos++
			if h.dash {
				l = strings.TrimLeft(l, "\t")
			}
			if l == h.name {
				found = true
				break
			}
		}
		if !found {
			return false, errAt(line, "heredoc <<%s is not terminated", h.name)
		}
	}
	return len(marks) > 0, nil
}

// scanWords removes quotes and escapes from s as the Dockerfile parser does:
// single quotes keep everything, double quotes keep everything except that the
// escape character before ", $ or itself is removed, and outside quotes the
// escape character makes the next character literal. If split is true the
// words are separated by unquoted white space; if not, s is one word and white
// space is kept.
func scanWords(s string, esc byte, split bool) ([]string, error) {
	var out []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, fmt.Errorf("unterminated single quote")
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
			inWord = true
		case c == '"':
			inWord = true
			closed := false
			for i++; i < len(s); i++ {
				d := s[i]
				if d == '"' {
					closed = true
					break
				}
				if d == esc && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '$' || s[i+1] == esc) {
					i++
					d = s[i]
				}
				cur.WriteByte(d)
			}
			if !closed {
				return nil, fmt.Errorf("unterminated double quote")
			}
		case c == esc:
			inWord = true
			if i+1 < len(s) {
				i++
				c = s[i]
			}
			cur.WriteByte(c)
		case split && (c == ' ' || c == '\t'):
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord || !split {
		out = append(out, cur.String())
	}
	return out, nil
}

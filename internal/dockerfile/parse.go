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

// A parser directive line: "# key=value". Only the lines at the very top of
// the file count; see directives.
var directiveRe = regexp.MustCompile(`^#\s*([a-zA-Z][a-zA-Z0-9]*)\s*=\s*(.+?)\s*$`)

// A heredoc word such as <<EOF, <<-EOF, <<'EOF' or 2<<EOF.
var heredocRe = regexp.MustCompile(`^[0-9]*<<(-?)(["']?)([A-Za-z_][A-Za-z0-9_]*)(["']?)$`)

// An EXPOSE word: [ip:][hostPort:]containerPort[/proto], where a port can be
// a range.
var exposeRe = regexp.MustCompile(`^(?:(?:\[[0-9A-Fa-f:.]+\]|[0-9.]+):)?(?:[0-9]+(?:-[0-9]+)?:)?[0-9]+(?:-[0-9]+)?(?:/(?i:tcp|udp|sctp))?$`)

// known lists the instructions that the parser understands; each of them needs
// arguments.
var known = map[string]bool{
	"FROM": true, "ARG": true, "ENV": true, "WORKDIR": true, "USER": true,
	"EXPOSE": true, "VOLUME": true, "LABEL": true, "SHELL": true, "STOPSIGNAL": true,
	"HEALTHCHECK": true, "COPY": true, "ADD": true, "RUN": true, "CMD": true,
	"ENTRYPOINT": true,
}

// flagVerbs are the instructions that take --name[=value] options. The other
// known instructions take none, and an option on them is an error.
var flagVerbs = map[string]bool{
	"FROM": true, "COPY": true, "ADD": true, "RUN": true, "HEALTHCHECK": true,
}

type parser struct {
	lines []string
	pos   int
	esc   byte
	f     *File
	cur   *Stage
	names map[string]bool
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
	p := &parser{lines: lines, esc: '\\', f: &File{}, names: map[string]bool{}}
	if err := p.directives(); err != nil {
		return nil, err
	}
	for {
		line, ins, ok := p.next()
		if !ok {
			break
		}
		if ins == "" {
			// Only an escape character was left, at the end of the file.
			continue
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

// refs records that a value holds a variable reference ($ that is not escaped
// and not inside single quotes), which is kept as written and not resolved.
func (p *parser) refs(line int, what string, ws ...word) {
	for _, w := range ws {
		if w.ref {
			p.note(line, what+" holds a variable (kept as written)")
			return
		}
	}
}

// directives reads the parser directives at the top of the file. They end at
// the first line that is not a directive (a comment, a blank line, an
// instruction), so a directive later in the file is a plain comment. A
// directive key can be given once.
func (p *parser) directives() error {
	seen := map[string]bool{}
	for p.pos < len(p.lines) {
		m := directiveRe.FindStringSubmatch(p.lines[p.pos])
		if m == nil {
			return nil
		}
		line := p.pos + 1
		key := strings.ToLower(m[1])
		if seen[key] {
			return errAt(line, "parser directive %s given more than once", key)
		}
		seen[key] = true
		switch key {
		case "escape":
			if m[2] != "\\" && m[2] != "`" {
				return errAt(line, "escape directive must be \\ or `, got %q", m[2])
			}
			p.esc = m[2][0]
		case "syntax", "check":
			p.f.Unknown = append(p.f.Unknown, fmt.Sprintf("line %d: parser directive %s", line, key))
		default:
			p.f.Unknown = append(p.f.Unknown, fmt.Sprintf("line %d: parser directive %s (not recognized)", line, key))
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
	if known[verb] && !flagVerbs[verb] {
		opts, body, err := leadingFlags(rest, p.esc)
		if err != nil {
			return errAt(line, "%s: %v", verb, err)
		}
		if len(opts) > 0 {
			return errAt(line, "%s does not take options, got --%s", verb, opts[0].name)
		}
		rest = body
		if rest == "" {
			return errAt(line, "%s needs arguments", verb)
		}
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
		return p.expose(line, rest)
	case "VOLUME":
		return p.volume(line, rest)
	case "SHELL":
		list, kind, err := parseJSONList(rest)
		if err != nil {
			return errAt(line, "SHELL: %v", err)
		}
		if kind != jsonList {
			return errAt(line, "SHELL requires a JSON list")
		}
		if len(list) == 0 {
			return errAt(line, "SHELL needs at least one element")
		}
		s.Shell = list
	case "CMD":
		c, err := p.command(line, verb, rest, false)
		s.Cmd = c
		s.CmdLine = line
		return err
	case "ENTRYPOINT":
		c, err := p.command(line, verb, rest, false)
		s.Entrypoint = c
		s.EntrypointLine = line
		return err
	case "HEALTHCHECK":
		h, err := p.healthcheck(line, rest)
		if err != nil {
			return err
		}
		s.Healthcheck = h
		s.HealthcheckLine = line
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
	// ref is true if the option holds a variable reference.
	ref bool
}

// leadingFlags splits the --name[=value] options at the start of s from the
// rest of it, as BuildKit does: an option ends at white space outside quotes,
// quotes and escapes in it are removed, and a lone "--" ends the options and
// is dropped. Option names are taken exactly as written, in lower case.
func leadingFlags(s string, esc byte) ([]option, string, error) {
	var out []option
	for {
		s = strings.TrimLeft(s, " \t")
		if !strings.HasPrefix(s, "--") {
			return out, s, nil
		}
		end := tokenEnd(s, esc)
		tok := s[:end]
		s = s[end:]
		if tok == "--" {
			return out, strings.TrimLeft(s, " \t"), nil
		}
		ws, err := scan(tok, esc, false)
		if err != nil {
			return nil, "", err
		}
		name, value, _ := strings.Cut(strings.TrimPrefix(ws[0].val, "--"), "=")
		out = append(out, option{name: name, value: value, ref: ws[0].ref})
	}
}

// tokenEnd returns the index where the word at the start of s ends: the first
// white space that is outside quotes and not escaped, or len(s).
func tokenEnd(s string, esc byte) int {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return len(s)
			}
			i += j + 1
		case c == '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == esc && i+1 < len(s) {
					i++
				}
			}
			if i >= len(s) {
				return len(s)
			}
		case c == esc:
			i++
		case c == ' ' || c == '\t':
			return i
		}
	}
	return len(s)
}

// rawWords splits s at white space outside quotes, keeping the quotes and the
// escapes in the words.
func rawWords(s string, esc byte) []string {
	var out []string
	for {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			return out
		}
		end := tokenEnd(s, esc)
		out = append(out, s[:end])
		s = s[end:]
	}
}

func (p *parser) from(line int, rest string) error {
	opts, rest, err := leadingFlags(rest, p.esc)
	if err != nil {
		return errAt(line, "FROM: %v", err)
	}
	st := &Stage{Line: line}
	var other []string
	var platform word
	for _, o := range opts {
		if o.name != "platform" {
			other = append(other, "FROM --"+o.name)
			continue
		}
		if o.value == "" {
			return errAt(line, "FROM --platform needs a value")
		}
		st.Platform = o.value
		platform.ref = o.ref
	}
	words, err := scan(rest, p.esc, true)
	if err != nil {
		return errAt(line, "FROM: %v", err)
	}
	switch {
	case len(words) == 0:
		return errAt(line, "FROM needs an image")
	case len(words) == 1:
	case len(words) == 2 && strings.EqualFold(words[1].val, "AS"):
		return errAt(line, "FROM AS needs a name")
	case len(words) == 3 && strings.EqualFold(words[1].val, "AS"):
		st.Name = words[2].val
	default:
		return errAt(line, "FROM expects image [AS name]")
	}
	st.Base = words[0].val
	if st.Name != "" {
		key := strings.ToLower(st.Name)
		if p.names[key] {
			return errAt(line, "duplicate stage name %q", st.Name)
		}
		p.names[key] = true
	}
	p.f.Stages = append(p.f.Stages, st)
	p.cur = st
	p.refs(line, "FROM", words[0], platform)
	for _, o := range other {
		p.note(line, o)
	}
	return nil
}

// pair splits a word at the first = of its raw text, as BuildKit does, and
// removes the quotes and the escapes from each part. has is false if the word
// has no =.
func (p *parser) pair(w word) (name string, val word, has bool, err error) {
	rawName, rawVal, has := strings.Cut(w.raw, "=")
	ns, err := scan(rawName, p.esc, false)
	if err != nil {
		return "", word{}, false, err
	}
	if !has {
		return ns[0].val, word{}, false, nil
	}
	vs, err := scan(rawVal, p.esc, false)
	if err != nil {
		return "", word{}, false, err
	}
	return ns[0].val, vs[0], true, nil
}

func (p *parser) args(line int, rest string) ([]Arg, error) {
	words, err := scan(rest, p.esc, true)
	if err != nil {
		return nil, errAt(line, "ARG: %v", err)
	}
	var out []Arg
	for _, w := range words {
		name, def, has, err := p.pair(w)
		if err != nil {
			return nil, errAt(line, "ARG: %v", err)
		}
		if name == "" {
			return nil, errAt(line, "ARG: empty name")
		}
		if has {
			p.refs(line, "ARG "+name, def)
		}
		out = append(out, Arg{Name: name, Default: def.val, RawDefault: def.raw, HasDefault: has})
	}
	return out, nil
}

// nameValues reads the arguments of ENV or LABEL: either name=value pairs or,
// in the old form, a name and the rest of the line as its value.
func (p *parser) nameValues(line int, verb, rest string) ([]EnvVar, error) {
	words, err := scan(rest, p.esc, true)
	if err != nil {
		return nil, errAt(line, "%s: %v", verb, err)
	}
	if len(words) == 0 {
		return nil, errAt(line, "%s needs arguments", verb)
	}
	if strings.Contains(words[0].raw, "=") {
		var out []EnvVar
		for _, w := range words {
			name, val, ok, err := p.pair(w)
			if err != nil {
				return nil, errAt(line, "%s: %v", verb, err)
			}
			if !ok {
				return nil, errAt(line, "%s: %q has no value, expected name=value", verb, w.val)
			}
			if name == "" {
				return nil, errAt(line, "%s: empty name", verb)
			}
			p.refs(line, verb+" "+name, val)
			out = append(out, EnvVar{Name: name, Value: val.val, Raw: val.raw})
		}
		return out, nil
	}
	first := words[0]
	after := strings.TrimSpace(rest[len(first.raw):])
	if after == "" {
		return nil, errAt(line, "%s %s has no value", verb, first.val)
	}
	if first.val == "" {
		return nil, errAt(line, "%s: empty name", verb)
	}
	vs, err := scan(after, p.esc, false)
	if err != nil {
		return nil, errAt(line, "%s: %v", verb, err)
	}
	p.refs(line, verb+" "+first.val, vs[0])
	return []EnvVar{{Name: first.val, Value: vs[0].val, Raw: vs[0].raw}}, nil
}

// value reads the single value of WORKDIR, USER or STOPSIGNAL.
func (p *parser) value(line int, verb, rest string) (string, error) {
	ws, err := scan(rest, p.esc, false)
	if err != nil {
		return "", errAt(line, "%s: %v", verb, err)
	}
	p.refs(line, verb, ws[0])
	return ws[0].val, nil
}

// expose reads EXPOSE. A word is [ip:][hostPort:]containerPort[/proto], where
// a port can be a range; it is kept as written. A word that holds a variable
// is kept without a check. A word of another form that has at least one digit
// (such as 80/http) is not recorded and goes to Unknown. A word without any
// digit (such as abc) cannot be a port and is an error.
func (p *parser) expose(line int, rest string) error {
	ws, err := scan(rest, p.esc, true)
	if err != nil {
		return errAt(line, "EXPOSE: %v", err)
	}
	p.refs(line, "EXPOSE", ws...)
	for _, w := range ws {
		switch {
		case w.ref || exposeRe.MatchString(w.val):
			p.cur.Expose = append(p.cur.Expose, w.val)
		case !strings.ContainsAny(w.val, "0123456789"):
			return errAt(line, "EXPOSE: %q is not a port", w.val)
		default:
			p.note(line, "EXPOSE "+w.val+" is not a recognized port (not recorded)")
		}
	}
	return nil
}

func (p *parser) volume(line int, rest string) error {
	list, kind, err := parseJSONList(rest)
	if kind == brokenList {
		p.note(line, "VOLUME looks like JSON but is not; taken as shell form")
		kind, err = notList, nil
	}
	if err != nil {
		return errAt(line, "VOLUME: %v", err)
	}
	var ws []word
	if kind == jsonList {
		ws = plain(list)
	} else if ws, err = scan(rest, p.esc, true); err != nil {
		return errAt(line, "VOLUME: %v", err)
	}
	p.refs(line, "VOLUME", ws...)
	p.cur.Volumes = append(p.cur.Volumes, values(ws)...)
	return nil
}

// command reads the arguments of CMD, ENTRYPOINT, RUN or HEALTHCHECK CMD.
//
// A valid JSON list of strings is the exec form. A valid JSON list that holds
// something other than strings is an error, as in BuildKit. Text that starts
// with [ but is not a JSON list is the shell form ("[ -f x ] && y"). Text that
// starts with [" or [' and is not a JSON list looks like a broken exec form;
// as Docker does, it is taken as the shell form and listed in Unknown, unless
// strict is true (RUN), when it is an error.
func (p *parser) command(line int, verb, rest string, strict bool) (Command, error) {
	list, kind, err := parseJSONList(rest)
	if kind == brokenList && !strict {
		p.note(line, verb+" looks like JSON but is not; taken as shell form")
		kind, err = notList, nil
	}
	if err != nil {
		return Command{}, errAt(line, "%s: %v", verb, err)
	}
	if kind == jsonList {
		return Command{Exec: list, Set: true}, nil
	}
	return Command{Shell: rest, Set: true}, nil
}

type listKind int

const (
	// notList: s does not start with [, or is not a JSON list and does not
	// look like one.
	notList listKind = iota
	// jsonList: s is a JSON list. err is set if it holds a non-string.
	jsonList
	// brokenList: s starts with [" or [' and is not valid JSON. err says why.
	brokenList
)

// parseJSONList reads a JSON list of strings.
func parseJSONList(s string) ([]string, listKind, error) {
	if !strings.HasPrefix(s, "[") {
		return nil, notList, nil
	}
	var elems []json.RawMessage
	if err := json.Unmarshal([]byte(s), &elems); err != nil {
		if len(s) > 1 && (s[1] == '"' || s[1] == '\'') {
			return nil, brokenList, fmt.Errorf("malformed JSON list: %v", err)
		}
		return nil, notList, nil
	}
	list := make([]string, 0, len(elems))
	for _, e := range elems {
		var str string
		if len(e) == 0 || e[0] != '"' || json.Unmarshal(e, &str) != nil {
			return nil, jsonList, fmt.Errorf("a JSON list must hold only strings, got %s", e)
		}
		list = append(list, str)
	}
	return list, jsonList, nil
}

func (p *parser) healthcheck(line int, rest string) (*Healthcheck, error) {
	opts, body, err := leadingFlags(rest, p.esc)
	if err != nil {
		return nil, errAt(line, "HEALTHCHECK: %v", err)
	}
	h := &Healthcheck{}
	for _, o := range opts {
		switch o.name {
		case "interval", "timeout", "start-period", "start-interval", "retries":
			if o.value == "" {
				return nil, errAt(line, "HEALTHCHECK --%s needs a value", o.name)
			}
			h.Options = append(h.Options, "--"+o.name+"="+o.value)
		default:
			p.note(line, "HEALTHCHECK --"+o.name)
		}
		if o.ref {
			p.note(line, "HEALTHCHECK --"+o.name+" holds a variable (kept as written)")
		}
	}
	sub, args := splitVerb(body)
	switch sub {
	case "NONE":
		if args != "" {
			return nil, errAt(line, "HEALTHCHECK NONE takes no arguments")
		}
		h.None = true
	case "CMD":
		if args == "" {
			return nil, errAt(line, "HEALTHCHECK CMD needs a command")
		}
		c, err := p.command(line, "HEALTHCHECK CMD", args, false)
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
	opts, body, err := leadingFlags(rest, p.esc)
	if err != nil {
		return errAt(line, "%s: %v", verb, err)
	}
	c := Copy{Instruction: verb, Line: line}
	seen := map[string]bool{}
	for _, o := range opts {
		switch o.name {
		case "from", "chown", "chmod":
			if o.value == "" {
				return errAt(line, "%s --%s needs a value", verb, o.name)
			}
			if seen[o.name] {
				return errAt(line, "%s --%s given more than once", verb, o.name)
			}
			seen[o.name] = true
			switch o.name {
			case "from":
				c.From = o.value
			case "chown":
				c.Chown = o.value
			default:
				c.Chmod = o.value
			}
		default:
			p.note(line, verb+" --"+o.name)
		}
		if o.ref {
			p.note(line, verb+" --"+o.name+" holds a variable (kept as written)")
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
	list, kind, err := parseJSONList(body)
	if err != nil {
		return errAt(line, "%s: %v", verb, err)
	}
	var ws []word
	if kind == jsonList {
		ws = plain(list)
	} else if ws, err = scan(body, p.esc, true); err != nil {
		return errAt(line, "%s: %v", verb, err)
	}
	if len(ws) < 2 {
		return errAt(line, "%s needs a source and a destination", verb)
	}
	vals := values(ws)
	c.Sources = vals[:len(vals)-1]
	c.Dest = vals[len(vals)-1]
	p.refs(line, verb, ws...)
	p.cur.Copies = append(p.cur.Copies, c)
	return nil
}

func (p *parser) run(line int, rest string) error {
	opts, body, err := leadingFlags(rest, p.esc)
	if err != nil {
		return errAt(line, "RUN: %v", err)
	}
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
	c, err := p.command(line, "RUN", body, true)
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

// heredoc looks for heredoc words (<<EOF) in the shell form of body. A word is
// a run of text up to white space outside quotes, so a << inside quotes is not
// a heredoc. If it finds any, it skips their bodies in the input, up to each
// terminator, and returns true. The bodies are not parsed.
func (p *parser) heredoc(line int, body string) (bool, error) {
	if _, kind, _ := parseJSONList(body); kind == jsonList {
		return false, nil
	}
	var marks []heredocMark
	for _, w := range rawWords(body, p.esc) {
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

// word is one word of an instruction.
type word struct {
	// val is the word without its quotes and escapes.
	val string
	// raw is the word as written.
	raw string
	// ref is true if the word holds a $ that is not escaped and not inside
	// single quotes, that is, a variable reference.
	ref bool
}

// plain makes words from the strings of a JSON list. No quotes are removed; a
// $ anywhere counts as a reference.
func plain(list []string) []word {
	out := make([]word, len(list))
	for i, v := range list {
		out[i] = word{val: v, raw: v, ref: strings.Contains(v, "$")}
	}
	return out
}

func values(ws []word) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.val
	}
	return out
}

// scan removes quotes and escapes from s as the Dockerfile parser does: single
// quotes keep everything, double quotes keep everything except that the escape
// character before ", $ or itself is removed, and outside quotes the escape
// character makes the next character literal. If split is true the words are
// separated by unquoted white space; if not, s is one word and white space is
// kept (the result then always has exactly one word).
func scan(s string, esc byte, split bool) ([]word, error) {
	var out []word
	var cur strings.Builder
	start, inWord, ref := 0, false, false
	begin := func(i int) {
		if !inWord {
			start, inWord = i, true
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			begin(i)
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, fmt.Errorf("unterminated single quote")
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
		case c == '"':
			begin(i)
			closed := false
			for i++; i < len(s); i++ {
				d := s[i]
				if d == '"' {
					closed = true
					break
				}
				if d == esc && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '$' || s[i+1] == esc) {
					i++
					cur.WriteByte(s[i])
					continue
				}
				if d == '$' {
					ref = true
				}
				cur.WriteByte(d)
			}
			if !closed {
				return nil, fmt.Errorf("unterminated double quote")
			}
		case c == esc:
			begin(i)
			if i+1 < len(s) {
				i++
				c = s[i]
			}
			cur.WriteByte(c)
		case split && (c == ' ' || c == '\t'):
			if inWord {
				out = append(out, word{val: cur.String(), raw: s[start:i], ref: ref})
				cur.Reset()
				inWord, ref = false, false
			}
		default:
			begin(i)
			if c == '$' {
				ref = true
			}
			cur.WriteByte(c)
		}
	}
	if !split {
		return []word{{val: cur.String(), raw: s, ref: ref}}, nil
	}
	if inWord {
		out = append(out, word{val: cur.String(), raw: s[start:], ref: ref})
	}
	return out, nil
}

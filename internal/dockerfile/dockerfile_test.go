package dockerfile

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// df joins lines into a Dockerfile text.
func df(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func mustParse(t *testing.T, src string) *File {
	t.Helper()
	f, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return f
}

// ev is an EnvVar whose value was written without quotes or escapes.
func ev(name, val string) EnvVar { return EnvVar{Name: name, Value: val, Raw: val} }

// evr is an EnvVar with a raw value that differs from the value.
func evr(name, val, raw string) EnvVar { return EnvVar{Name: name, Value: val, Raw: raw} }

// arg is an Arg whose default was written without quotes or escapes.
func arg(name, def string, has bool) Arg {
	return Arg{Name: name, Default: def, RawDefault: def, HasDefault: has}
}

// held is the Unknown text for a value that holds a variable.
func held(line int, what string) string {
	return fmt.Sprintf("line %d: %s holds a variable (kept as written)", line, what)
}

func check(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", what, got, want)
	}
}

func TestFromForms(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		base     string
		platform string
		stage    string
	}{
		{"plain", df("FROM alpine:3.19"), "alpine:3.19", "", ""},
		{"lower case", df("from alpine"), "alpine", "", ""},
		{"scratch", df("FROM scratch"), "scratch", "", ""},
		{"as", df("FROM golang:1.22 AS builder"), "golang:1.22", "", "builder"},
		{"lower as", df("FROM golang as Build"), "golang", "", "Build"},
		{"platform", df("FROM --platform=linux/arm64 alpine"), "alpine", "linux/arm64", ""},
		{"platform and as", df("FROM --platform=linux/amd64 a AS b"), "a", "linux/amd64", "b"},
		{"digest", df("FROM alpine@sha256:abc"), "alpine@sha256:abc", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := mustParse(t, tt.src).Final()
			check(t, "Base", s.Base, tt.base)
			check(t, "Platform", s.Platform, tt.platform)
			check(t, "Name", s.Name, tt.stage)
			check(t, "Line", s.Line, 1)
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"empty", "", "the Dockerfile has no FROM"},
		{"comment only", df("# only a comment"), "the Dockerfile has no FROM"},
		{"arg only", df("ARG a=1"), "the Dockerfile has no FROM"},
		{"before from", df("RUN x"), "line 1: RUN before the first FROM"},
		{"from alone", df("FROM"), "line 1: FROM needs arguments"},
		{"from without image", df("FROM --platform=linux/amd64"), "line 1: FROM needs an image"},
		{"from extra", df("FROM a b"), "line 1: FROM expects image [AS name]"},
		{"as without name", df("FROM a AS"), "line 1: FROM AS needs a name"},
		{"platform without value", df("FROM --platform= a"), "line 1: FROM --platform needs a value"},
		{"duplicate stage", df("FROM a AS b", "FROM c AS B"), "line 2: duplicate stage name"},
		{"env without value", df("FROM a", "ENV FOO"), "line 2: ENV FOO has no value"},
		{"env alone", df("FROM a", "ENV"), "line 2: ENV needs arguments"},
		{"env empty name", df("FROM a", "ENV =x"), "line 2: ENV: empty name"},
		{"env pair without value", df("FROM a", "ENV a=b c"), `line 2: ENV: "c" has no value`},
		{"env open quote", df("FROM a", `ENV a="x`), "line 2: ENV: unterminated double quote"},
		{"env open single quote", df("FROM a", "ENV a='x"), "line 2: ENV: unterminated single quote"},
		{"arg empty name", df("FROM a", "ARG =x"), "line 2: ARG: empty name"},
		{"run bad json", df("FROM a", `RUN ["a",]`), "line 2: RUN: malformed JSON list"},
		{"run single quotes", df("FROM a", "FROM b", `RUN ['a']`), "line 3: RUN: malformed JSON list"},
		{"copy bad json", df("FROM a", `COPY ["a", "b"`), "line 2: COPY: malformed JSON list"},
		{"add bad json", df("FROM a", `ADD ['a', 'b']`), "line 2: ADD: malformed JSON list"},
		{"shell bad json", df("FROM a", `SHELL ["a"`), "line 2: SHELL: malformed JSON list"},
		{"cmd not strings", df("FROM a", `CMD [1,"a"]`), "line 2: CMD: a JSON list must hold only strings"},
		{"entrypoint not strings", df("FROM a", `ENTRYPOINT [true]`), "line 2: ENTRYPOINT: a JSON list must hold only strings"},
		{"run not strings", df("FROM a", `RUN ["a", 1]`), "line 2: RUN: a JSON list must hold only strings"},
		{"volume not strings", df("FROM a", `VOLUME [null]`), "line 2: VOLUME: a JSON list must hold only strings"},
		{"cmd option", df("FROM a", "CMD --help"), "line 2: CMD does not take options, got --help"},
		{"workdir option", df("FROM a", "WORKDIR --x /a"), "line 2: WORKDIR does not take options, got --x"},
		{"env option", df("FROM a", "ENV --x a=b"), "line 2: ENV does not take options, got --x"},
		{"expose option", df("FROM a", "EXPOSE --x 80"), "line 2: EXPOSE does not take options, got --x"},
		{"option only", df("FROM a", "CMD --"), "line 2: CMD needs arguments"},
		{"copy from twice", df("FROM a", "COPY --from=a --from=b x y"), "line 2: COPY --from given more than once"},
		{"add chown twice", df("FROM a", "ADD --chown=1 --chown=2 x y"), "line 2: ADD --chown given more than once"},
		{"copy open quote", df("FROM a", `COPY --chown="a b x y`), "line 2: COPY: unterminated double quote"},
		{"duplicate directive", df("# escape=`", "# ESCAPE=`", "FROM a"), "line 2: parser directive escape given more than once"},
		{"duplicate unknown directive", df("# foo=1", "# foo=2", "FROM a"), "line 2: parser directive foo given more than once"},
		{"shell not json", df("FROM a", "SHELL sh"), "line 2: SHELL requires a JSON list"},
		{"shell empty", df("FROM a", "SHELL []"), "line 2: SHELL needs at least one element"},
		{"expose not a port", df("FROM a", "EXPOSE 80 abc"), `line 2: EXPOSE: "abc" is not a port`},
		{"expose http", df("FROM a", "EXPOSE http"), `line 2: EXPOSE: "http" is not a port`},
		{"copy one argument", df("FROM a", "COPY a"), "line 2: COPY needs a source and a destination"},
		{"copy from empty", df("FROM a", "COPY --from= a b"), "line 2: COPY --from needs a value"},
		{"run alone", df("FROM a", "RUN"), "line 2: RUN needs arguments"},
		{"run only flags", df("FROM a", "RUN --mount=type=cache"), "line 2: RUN needs a command"},
		{"healthcheck alone", df("FROM a", "HEALTHCHECK"), "line 2: HEALTHCHECK needs arguments"},
		{"healthcheck word", df("FROM a", "HEALTHCHECK foo"), "line 2: HEALTHCHECK expects NONE or CMD"},
		{"healthcheck cmd empty", df("FROM a", "HEALTHCHECK CMD"), "line 2: HEALTHCHECK CMD needs a command"},
		{"healthcheck none args", df("FROM a", "HEALTHCHECK NONE x"), "line 2: HEALTHCHECK NONE takes no arguments"},
		{"healthcheck option", df("FROM a", "HEALTHCHECK --interval= CMD x"), "line 2: HEALTHCHECK --interval needs a value"},
		{"bad escape", df("# escape=x", "FROM a"), "line 1: escape directive"},
		{"heredoc open", df("FROM a", "RUN <<EOF", "echo"), "line 2: heredoc <<EOF is not terminated"},
		{"error after continuation", df("FROM a", "# c", "ENV a=b \\", "c"), `line 3: ENV: "c" has no value`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.src))
			if err == nil {
				t.Fatalf("Parse succeeded, want an error with %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestTooLarge(t *testing.T) {
	_, err := Parse(strings.NewReader(strings.Repeat("#", maxSize+1)))
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("error = %v, want too large", err)
	}
}

func TestEnv(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []EnvVar
	}{
		{"pairs", df("FROM a", "ENV a=b c=d"), []EnvVar{ev("a", "b"), ev("c", "d")}},
		{"old form", df("FROM a", "ENV a b c"), []EnvVar{ev("a", "b c")}},
		{"old form keeps spaces", df("FROM a", `ENV a "b  c"`), []EnvVar{evr("a", "b  c", `"b  c"`)}},
		{"old form with equals later", df("FROM a", "ENV a b=c"), []EnvVar{ev("a", "b=c")}},
		{"old form quoted name", df("FROM a", `ENV "a b" c`), []EnvVar{ev("a b", "c")}},
		{"quotes", df("FROM a", `ENV a="x y" b='z w' c=p\ q`), []EnvVar{evr("a", "x y", `"x y"`), evr("b", "z w", `'z w'`), evr("c", "p q", `p\ q`)}},
		{"escapes in quotes", df("FROM a", `ENV d="q\"r" e='s\t' f="u\v"`), []EnvVar{evr("d", `q"r`, `"q\"r"`), evr("e", `s\t`, `'s\t'`), evr("f", `u\v`, `"u\v"`)}},
		{"empty value", df("FROM a", "ENV a="), []EnvVar{ev("a", "")}},
		{"empty quoted value", df("FROM a", `ENV a=""`), []EnvVar{evr("a", "", `""`)}},
		{"value with equals", df("FROM a", "ENV a=b=c"), []EnvVar{ev("a", "b=c")}},
		{"quoted equals", df("FROM a", `ENV a="b=c"`), []EnvVar{evr("a", "b=c", `"b=c"`)}},
		{"lower case", df("FROM a", "env a=b"), []EnvVar{ev("a", "b")}},
		{"repeated name", df("FROM a", "ENV a=1", "ENV a=2"), []EnvVar{ev("a", "1"), ev("a", "2")}},
		{"quoted name", df("FROM a", `ENV "a b"=c`), []EnvVar{ev("a b", "c")}},
		{"escaped dollar keeps raw", df("FROM a", `ENV a=\$x b=$x c='$x'`), []EnvVar{evr("a", "$x", `\$x`), ev("b", "$x"), evr("c", "$x", `'$x'`)}},
		{"path keeps raw", df("FROM a", "ENV PATH=/app/bin:$PATH"), []EnvVar{ev("PATH", "/app/bin:$PATH")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check(t, "Env", mustParse(t, tt.src).Final().Env, tt.want)
		})
	}
}

func TestEnviron(t *testing.T) {
	s := mustParse(t, df("FROM a", "ENV A=1 B=2", `ENV A="3"`, "ENV C c")).Final()
	check(t, "Environ", s.Environ(), []EnvVar{evr("A", "3", `"3"`), ev("B", "2"), ev("C", "c")})
	check(t, "Env is not changed", s.Env, []EnvVar{ev("A", "1"), ev("B", "2"), evr("A", "3", `"3"`), ev("C", "c")})
	check(t, "empty", (&Stage{}).Environ(), []EnvVar(nil))
}

func TestStageFields(t *testing.T) {
	tests := []struct {
		name string
		src  string
		get  func(*Stage) any
		want any
	}{
		{"workdir", df("FROM a", "WORKDIR /a", `workdir "/b c"`), func(s *Stage) any { return s.WorkDir }, "/b c"},
		{"workdir relative", df("FROM a", "WORKDIR app"), func(s *Stage) any { return s.WorkDir }, "app"},
		{"user", df("FROM a", "USER app:grp"), func(s *Stage) any { return s.User }, "app:grp"},
		{"stopsignal", df("FROM a", "STOPSIGNAL SIGTERM"), func(s *Stage) any { return s.StopSignal }, "SIGTERM"},
		{"expose", df("FROM a", "EXPOSE 80 443/tcp 8000-8010/udp"), func(s *Stage) any { return s.Expose }, []string{"80", "443/tcp", "8000-8010/udp"}},
		{"expose twice", df("FROM a", "EXPOSE 80", "EXPOSE 53/UDP"), func(s *Stage) any { return s.Expose }, []string{"80", "53/UDP"}},
		{"volume json", df("FROM a", `VOLUME ["/data", "/logs"]`), func(s *Stage) any { return s.Volumes }, []string{"/data", "/logs"}},
		{"volume words", df("FROM a", "VOLUME /a /b"), func(s *Stage) any { return s.Volumes }, []string{"/a", "/b"}},
		{"volume one", df("FROM a", "VOLUME /x", "VOLUME /y"), func(s *Stage) any { return s.Volumes }, []string{"/x", "/y"}},
		{"label pairs", df("FROM a", `LABEL a=b "c d"="e f"`), func(s *Stage) any { return s.Labels }, []EnvVar{ev("a", "b"), evr("c d", "e f", `"e f"`)}},
		{"label old form", df("FROM a", "LABEL k some value"), func(s *Stage) any { return s.Labels }, []EnvVar{ev("k", "some value")}},
		{"args", df("FROM a", "ARG a", "ARG b=1 c=", `ARG d="x y"`), func(s *Stage) any { return s.Args }, []Arg{arg("a", "", false), arg("b", "1", true), arg("c", "", true), {Name: "d", Default: "x y", RawDefault: `"x y"`, HasDefault: true}}},
		{"shell", df("FROM a", `SHELL ["/bin/bash", "-c"]`), func(s *Stage) any { return s.Shell }, []string{"/bin/bash", "-c"}},
		{"no shell", df("FROM a"), func(s *Stage) any { return s.Shell }, []string(nil)},
		{"runs", df("FROM a", "RUN a b", `RUN ["x", "y"]`, "run [ -f /x ] && y"), func(s *Stage) any { return s.Runs }, []Run{
			{Command: Command{Shell: "a b", Set: true}, Line: 2},
			{Command: Command{Exec: []string{"x", "y"}, Set: true}, Line: 3},
			{Command: Command{Shell: "[ -f /x ] && y", Set: true}, Line: 4},
		}},
		{"copy", df("FROM a", "copy a b /dest/"), func(s *Stage) any { return s.Copies }, []Copy{{"COPY", "", []string{"a", "b"}, "/dest/", "", "", 2}}},
		{"copy options", df("FROM a", "COPY --from=builder --chown=1:2 --chmod=755 /x /y"), func(s *Stage) any { return s.Copies }, []Copy{{"COPY", "builder", []string{"/x"}, "/y", "1:2", "755", 2}}},
		{"add json", df("FROM a", `ADD ["a b", "/c"]`), func(s *Stage) any { return s.Copies }, []Copy{{"ADD", "", []string{"a b"}, "/c", "", "", 2}}},
		{"add url", df("FROM a", "ADD https://example.com/x.tgz /opt/"), func(s *Stage) any { return s.Copies }, []Copy{{"ADD", "", []string{"https://example.com/x.tgz"}, "/opt/", "", "", 2}}},
		{"healthcheck", df("FROM a", "HEALTHCHECK --interval=30s --retries=3 CMD curl -f http://localhost/"), func(s *Stage) any { return s.Healthcheck }, &Healthcheck{Test: Command{Shell: "curl -f http://localhost/", Set: true}, Options: []string{"--interval=30s", "--retries=3"}}},
		{"healthcheck exec", df("FROM a", `HEALTHCHECK CMD ["a", "b"]`), func(s *Stage) any { return s.Healthcheck }, &Healthcheck{Test: Command{Exec: []string{"a", "b"}, Set: true}}},
		{"healthcheck none", df("FROM a", "healthcheck none"), func(s *Stage) any { return s.Healthcheck }, &Healthcheck{None: true}},
		{"healthcheck none after a test", df("FROM a", "HEALTHCHECK CMD x", "HEALTHCHECK NONE"), func(s *Stage) any { return s.Healthcheck }, &Healthcheck{None: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check(t, tt.name, tt.get(mustParse(t, tt.src).Final()), tt.want)
		})
	}
}

func TestCommandForms(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		entry bool
		want  Command
	}{
		{"cmd json", df("FROM a", `CMD ["a","b"]`), false, Command{Exec: []string{"a", "b"}, Set: true}},
		{"cmd json spaces", df("FROM a", `CMD [ "a" ]`), false, Command{Exec: []string{"a"}, Set: true}},
		{"cmd shell", df("FROM a", "CMD a b"), false, Command{Shell: "a b", Set: true}},
		{"cmd shell test", df("FROM a", "CMD [ -f /x ] || echo no"), false, Command{Shell: "[ -f /x ] || echo no", Set: true}},
		{"cmd empty list", df("FROM a", "CMD []"), false, Command{Exec: []string{}, Set: true}},
		{"cmd not given", df("FROM a"), false, Command{}},
		{"cmd last wins", df("FROM a", "CMD a", "CMD b"), false, Command{Shell: "b", Set: true}},
		{"cmd reset", df("FROM a", `CMD ["a"]`, "CMD []"), false, Command{Exec: []string{}, Set: true}},
		{"entrypoint json", df("FROM a", `ENTRYPOINT ["/x"]`), true, Command{Exec: []string{"/x"}, Set: true}},
		{"entrypoint shell lower", df("FROM a", "entrypoint /x --y"), true, Command{Shell: "/x --y", Set: true}},
		{"entrypoint empty list", df("FROM a", "ENTRYPOINT []"), true, Command{Exec: []string{}, Set: true}},
		{"entrypoint not given", df("FROM a", "CMD x"), true, Command{}},
		{"cmd is not entrypoint", df("FROM a", "CMD x"), false, Command{Shell: "x", Set: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := mustParse(t, tt.src).Final()
			got := s.Cmd
			if tt.entry {
				got = s.Entrypoint
			}
			check(t, "command", got, tt.want)
		})
	}
}

func TestContinuationAndComments(t *testing.T) {
	src := df("# top comment", "", "FROM a", "  # indented comment",
		`RUN apt-get update && \`,
		"# a comment inside the instruction",
		"",
		"    apt-get install -y x",
		"COPY a b",
		`RUN echo a \ `,
		"b")
	s := mustParse(t, src).Final()
	check(t, "Runs", s.Runs, []Run{
		{Command: Command{Shell: "apt-get update && " + "    apt-get install -y x", Set: true}, Line: 5},
		{Command: Command{Shell: "echo a b", Set: true}, Line: 10},
	})
	check(t, "Copies", s.Copies, []Copy{{"COPY", "", []string{"a"}, "b", "", "", 9}})
}

func TestEscapeDirective(t *testing.T) {
	s := mustParse(t, df("# escape=`", "FROM a", "RUN echo a `", "    b", `WORKDIR C:\app`)).Final()
	check(t, "Runs", s.Runs, []Run{{Command: Command{Shell: "echo a " + "    b", Set: true}, Line: 3}})
	check(t, "WorkDir", s.WorkDir, `C:\app`)

	// With the backtick as escape character, a backslash does not continue.
	s = mustParse(t, df("# escape=`", "FROM a", `RUN echo a \`, "ENV x=y")).Final()
	check(t, "Runs", s.Runs, []Run{{Command: Command{Shell: `echo a \`, Set: true}, Line: 3}})
	check(t, "Env", s.Env, []EnvVar{ev("x", "y")})

	// Without the directive the backslash continues.
	s = mustParse(t, df("FROM a", `RUN echo a \`, "ENV x=y")).Final()
	check(t, "Runs", s.Runs, []Run{{Command: Command{Shell: "echo a ENV x=y", Set: true}, Line: 2}})
	check(t, "Env", s.Env, []EnvVar(nil))

	// The directive key is not case sensitive: with the backtick as escape
	// character the line continues; if the directive were ignored the
	// backtick would stay in the command and "b" would be an instruction.
	f := mustParse(t, df("# ESCAPE = `", "FROM a", "RUN a `", "b"))
	check(t, "Runs", f.Final().Runs, []Run{{Command: Command{Shell: "a b", Set: true}, Line: 3}})
	check(t, "Unknown", f.Unknown, []string(nil))
}

func TestDirectives(t *testing.T) {
	// A directive after a comment, or after a blank line, is a comment: the
	// backslash still continues the line.
	for name, src := range map[string]string{
		"after a comment":    df("# hello", "# escape=`", "FROM a", "RUN a \\", "b"),
		"after a blank line": df("", "# escape=`", "FROM a", "RUN a \\", "b"),
	} {
		t.Run(name, func(t *testing.T) {
			s := mustParse(t, src).Final()
			check(t, "Runs", s.Runs, []Run{{Command: Command{Shell: "a b", Set: true}, Line: 4}})
		})
	}

	// Directives go on until a line that is not one; unknown keys are listed.
	f := mustParse(t, df("# foo=bar", "# syntax=x", "# escape=`", "FROM a", "RUN a `", "b"))
	check(t, "Runs", f.Final().Runs, []Run{{Command: Command{Shell: "a b", Set: true}, Line: 5}})
	check(t, "Unknown", f.Unknown, []string{
		"line 1: parser directive foo (not recognized)",
		"line 2: parser directive syntax",
	})
}

func TestBOMAndCRLF(t *testing.T) {
	s := mustParse(t, "\uFEFFFROM a\r\nENV a=b\r\nRUN x \\\r\n y\r\n").Final()
	check(t, "Base", s.Base, "a")
	check(t, "Env", s.Env, []EnvVar{ev("a", "b")})
	check(t, "Runs", s.Runs, []Run{{Command: Command{Shell: "x  y", Set: true}, Line: 3}})
}

func TestHeredocs(t *testing.T) {
	f := mustParse(t, df("FROM a",
		"RUN <<EOF",
		"echo hi",
		"ENV NOT=parsed",
		"EOF",
		"COPY <<-EOT /x",
		"\tbody",
		"\tEOT",
		"ENV after=1"))
	s := f.Final()
	check(t, "Env", s.Env, []EnvVar{ev("after", "1")})
	check(t, "Runs", s.Runs, []Run(nil))
	check(t, "Copies", s.Copies, []Copy(nil))
	check(t, "Unknown", s.Unknown, []string{"line 2: RUN heredoc", "line 6: COPY heredoc"})
	check(t, "File Unknown", f.Unknown, []string{"line 2: RUN heredoc", "line 6: COPY heredoc"})

	f = mustParse(t, df("FROM a",
		"RUN cat <<'EOF' > /x",
		"line",
		"EOF",
		"RUN echo $((1<<2))",
		`RUN ["sh", "-c", "cat <<EOF"]`))
	s = f.Final()
	check(t, "Unknown", s.Unknown, []string{"line 2: RUN heredoc"})
	check(t, "Runs", s.Runs, []Run{
		{Command: Command{Shell: "echo $((1<<2))", Set: true}, Line: 5},
		{Command: Command{Exec: []string{"sh", "-c", "cat <<EOF"}, Set: true}, Line: 6},
	})

	// A << inside quotes is not a heredoc.
	f = mustParse(t, df("FROM a", `RUN echo "see <<EOF here"`, `RUN echo "<<EOF"`, "ENV after=1"))
	s = f.Final()
	check(t, "Env", s.Env, []EnvVar{ev("after", "1")})
	check(t, "Runs", s.Runs, []Run{
		{Command: Command{Shell: `echo "see <<EOF here"`, Set: true}, Line: 2},
		{Command: Command{Shell: `echo "<<EOF"`, Set: true}, Line: 3},
	})
	check(t, "Unknown", f.Unknown, []string(nil))

	// Different quotes around the name are not a heredoc.
	f = mustParse(t, df("FROM a", `RUN cat <<"EOF'`, "ENV after=1"))
	s = f.Final()
	check(t, "Env", s.Env, []EnvVar{ev("after", "1")})
	check(t, "Runs", s.Runs, []Run{{Command: Command{Shell: `cat <<"EOF'`, Set: true}, Line: 2}})

	f = mustParse(t, df("FROM a", "RUN <<A <<B", "one", "A", "two", "B", "ENV x=y"))
	check(t, "two heredocs", f.Final().Env, []EnvVar{ev("x", "y")})
}

func TestUnknown(t *testing.T) {
	f := mustParse(t, df("FROM a",
		"ONBUILD RUN x",
		"MAINTAINER me",
		"foo bar",
		"RUN --network=none --mount=type=secret,id=x y",
		"COPY --link a b",
		"ENV P=$HOME/x",
		"WORKDIR /w/${D}",
		"FROM b"))
	want := []string{
		"line 2: ONBUILD",
		"line 3: MAINTAINER",
		"line 4: FOO",
		"line 5: RUN --network",
		"line 5: RUN --mount",
		"line 6: COPY --link",
		"line 7: ENV P holds a variable (kept as written)",
		"line 8: WORKDIR holds a variable (kept as written)",
	}
	check(t, "File Unknown", f.Unknown, want)
	check(t, "Stage Unknown", f.Stages[0].Unknown, want)
	check(t, "second stage", f.Stages[1].Unknown, []string(nil))
	s := f.Stages[0]
	check(t, "Runs", s.Runs, []Run{{Command: Command{Shell: "y", Set: true}, Line: 5}})
	check(t, "Copies", s.Copies, []Copy{{"COPY", "", []string{"a"}, "b", "", "", 6}})
	check(t, "Env", s.Env, []EnvVar{ev("P", "$HOME/x")})
	check(t, "WorkDir", s.WorkDir, "/w/${D}")
}

func TestMultiStage(t *testing.T) {
	f, err := ParseFile("testdata/multistage.Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "Args", f.Args, []Arg{arg("GO_VERSION", "1.22", true)})
	if len(f.Stages) != 2 {
		t.Fatalf("got %d stages, want 2", len(f.Stages))
	}
	b, fin := f.Stages[0], f.Stages[1]
	if f.Final() != fin {
		t.Error("Final is not the last stage")
	}
	if f.Stage("builder") != b || f.Stage("BUILDER") != b || f.Stage("0") != b || f.Stage("1") != fin {
		t.Error("Stage did not find the stages by name and by index")
	}
	if f.Stage("2") != nil || f.Stage("nope") != nil || f.Stage("") != nil || f.Stage("-1") != nil || f.Stage("01") != nil {
		t.Error("Stage found a stage that does not exist")
	}
	check(t, "builder Base", b.Base, "golang:${GO_VERSION}")
	check(t, "builder Platform", b.Platform, "$BUILDPLATFORM")
	check(t, "builder Line", b.Line, 3)
	check(t, "builder WorkDir", b.WorkDir, "/src")
	check(t, "builder Env", b.Env, []EnvVar{ev("CGO_ENABLED", "0")})
	check(t, "builder Runs", b.Runs, []Run{{Command: Command{Shell: "go build -o /out/app ./...", Set: true}, Line: 6}})
	check(t, "final Base", fin.Base, "alpine:3.19")
	check(t, "final Line", fin.Line, 7)
	check(t, "final inherits no Env", fin.Env, []EnvVar(nil))
	check(t, "final inherits no WorkDir", fin.WorkDir, "")
	check(t, "final Copies", fin.Copies, []Copy{{"COPY", "builder", []string{"/out/app"}, "/usr/local/bin/app", "app:app", "", 8}})
	check(t, "final Expose", fin.Expose, []string{"8080/tcp", "9090"})
	check(t, "final User", fin.User, "app")
	check(t, "final Entrypoint", fin.Entrypoint, Command{Exec: []string{"/usr/local/bin/app"}, Set: true})
	check(t, "final Cmd", fin.Cmd, Command{Exec: []string{"--port", "8080"}, Set: true})
	check(t, "final Unknown", fin.Unknown, []string(nil))
	check(t, "builder Unknown", b.Unknown, []string{
		"line 3: FROM holds a variable (kept as written)",
		"line 6: RUN --mount",
	})
	check(t, "File Unknown", f.Unknown, []string{
		"line 1: parser directive syntax",
		"line 3: FROM holds a variable (kept as written)",
		"line 6: RUN --mount",
	})
}

func TestBaseStage(t *testing.T) {
	f := mustParse(t, df("FROM a AS base", "ENV X=1", "WORKDIR /w", "FROM base", "ENV Y=2"))
	fin := f.Final()
	if f.BaseStage(fin) != f.Stages[0] {
		t.Error("the base of the final stage is not resolved to the first stage")
	}
	if f.BaseStage(f.Stages[0]) != nil {
		t.Error("an image base resolved to a stage")
	}
	check(t, "child Environ", fin.Environ(), []EnvVar{ev("Y", "2")})
	check(t, "child WorkDir", fin.WorkDir, "")
	check(t, "parent Environ", f.Stages[0].Environ(), []EnvVar{ev("X", "1")})

	f = mustParse(t, df("FROM nginx AS nginx"))
	if f.BaseStage(f.Final()) != nil {
		t.Error("FROM nginx AS nginx resolved to itself")
	}

	f = mustParse(t, df("FROM a AS Base", "FROM BASE AS c"))
	if f.BaseStage(f.Stages[1]) != f.Stages[0] {
		t.Error("the stage name was not compared without regard to case")
	}

	f = mustParse(t, df("FROM base", "FROM a AS base"))
	if f.BaseStage(f.Stages[0]) != nil {
		t.Error("a base that names a later stage resolved to it")
	}
	if f.BaseStage(f.Stages[1]) != nil {
		t.Error("an image base resolved to a stage")
	}

	f = mustParse(t, df("FROM a", "FROM 0"))
	if f.BaseStage(f.Stages[1]) != nil {
		t.Error("a stage number was taken as the base")
	}

	f = mustParse(t, df("FROM a AS x", "FROM x AS y", "FROM y"))
	if f.BaseStage(f.Stages[2]) != f.Stages[1] || f.BaseStage(f.Stages[1]) != f.Stages[0] {
		t.Error("a chain of stages was not resolved")
	}
	if f.BaseStage(&Stage{Base: "x"}) != nil {
		t.Error("a stage that is not in the file resolved to a stage")
	}
}

func TestFinalOfEmptyFile(t *testing.T) {
	if (&File{}).Final() != nil {
		t.Error("Final of a file without stages is not nil")
	}
}

func TestParseFileErrors(t *testing.T) {
	_, err := ParseFile("testdata/bad_json.Dockerfile")
	if err == nil || !strings.Contains(err.Error(), "bad_json.Dockerfile: line 2: RUN: malformed JSON list") {
		t.Errorf("error = %v, want the path and line 2", err)
	}
	if _, err := ParseFile("testdata/missing.Dockerfile"); err == nil {
		t.Error("ParseFile of a missing file succeeded")
	}
}

func TestJSONLikeShellForm(t *testing.T) {
	cmdOf := func(s *Stage) Command { return s.Cmd }
	entryOf := func(s *Stage) Command { return s.Entrypoint }
	testOf := func(s *Stage) Command { return s.Healthcheck.Test }
	tests := []struct {
		name    string
		src     string
		get     func(*Stage) Command
		want    Command
		unknown []string
	}{
		{"cmd single quotes", df("FROM a", `CMD ['a']`), cmdOf, Command{Shell: `['a']`, Set: true}, []string{"line 2: CMD looks like JSON but is not; taken as shell form"}},
		{"cmd trailing comment", df("FROM a", `CMD ["a"] # x`), cmdOf, Command{Shell: `["a"] # x`, Set: true}, []string{"line 2: CMD looks like JSON but is not; taken as shell form"}},
		{"cmd unclosed", df("FROM a", `CMD ["a", "b"`), cmdOf, Command{Shell: `["a", "b"`, Set: true}, []string{"line 2: CMD looks like JSON but is not; taken as shell form"}},
		{"cmd trailing comma after a space", df("FROM a", `CMD [ "a", ]`), cmdOf, Command{Shell: `[ "a", ]`, Set: true}, nil},
		{"cmd test command", df("FROM a", `CMD [ "$X" = y ] && z`), cmdOf, Command{Shell: `[ "$X" = y ] && z`, Set: true}, nil},
		{"entrypoint single quotes", df("FROM a", `ENTRYPOINT ['a']`), entryOf, Command{Shell: `['a']`, Set: true}, []string{"line 2: ENTRYPOINT looks like JSON but is not; taken as shell form"}},
		{"healthcheck", df("FROM a", `HEALTHCHECK CMD ["a"`), testOf, Command{Shell: `["a"`, Set: true}, []string{"line 2: HEALTHCHECK CMD looks like JSON but is not; taken as shell form"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := mustParse(t, tt.src)
			check(t, "command", tt.get(f.Final()), tt.want)
			check(t, "Unknown", f.Unknown, tt.unknown)
			check(t, "stage Unknown", f.Final().Unknown, tt.unknown)
		})
	}

	f := mustParse(t, df("FROM a", `VOLUME ['/data']`))
	check(t, "Volumes", f.Final().Volumes, []string{"[/data]"})
	check(t, "Unknown", f.Unknown, []string{"line 2: VOLUME looks like JSON but is not; taken as shell form"})
}

func TestBuilderFlags(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []Copy
	}{
		{"quoted chown", df("FROM a", `COPY --chown="1:2" a b`), []Copy{{"COPY", "", []string{"a"}, "b", "1:2", "", 2}}},
		{"quoted from", df("FROM a", `COPY --from="builder" /x /y`), []Copy{{"COPY", "builder", []string{"/x"}, "/y", "", "", 2}}},
		{"single quoted chmod", df("FROM a", `COPY --chmod='755' /x /y`), []Copy{{"COPY", "", []string{"/x"}, "/y", "", "755", 2}}},
		{"quoted value with a space", df("FROM a", `COPY --chown="a b" x y`), []Copy{{"COPY", "", []string{"x"}, "y", "a b", "", 2}}},
		{"quote in the middle", df("FROM a", `ADD --chown=a" "b x y`), []Copy{{"ADD", "", []string{"x"}, "y", "a b", "", 2}}},
		{"double dash ends the options", df("FROM a", "COPY --chmod=755 -- --odd dest"), []Copy{{"COPY", "", []string{"--odd"}, "dest", "", "755", 2}}},
		{"double dash alone", df("FROM a", "COPY -- a b"), []Copy{{"COPY", "", []string{"a"}, "b", "", "", 2}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check(t, "Copies", mustParse(t, tt.src).Final().Copies, tt.want)
		})
	}

	f := mustParse(t, df("FROM a", `RUN --mount="type=cache,target=/a b" x`))
	check(t, "Runs", f.Final().Runs, []Run{{Command: Command{Shell: "x", Set: true}, Line: 2}})
	check(t, "Unknown", f.Unknown, []string{"line 2: RUN --mount"})

	f = mustParse(t, df("FROM --platform=\"linux/arm64\" a"))
	check(t, "Platform", f.Final().Platform, "linux/arm64")

	f = mustParse(t, df("FROM a", `HEALTHCHECK --interval="30s" CMD x`))
	check(t, "Options", f.Final().Healthcheck.Options, []string{"--interval=30s"})
}

// Option names are matched exactly, as Docker does; an option in another case
// is not the known one and is listed in Unknown.
func TestFlagNames(t *testing.T) {
	f := mustParse(t, df("FROM --PLATFORM=x a", "COPY --FROM=b x y", "HEALTHCHECK --INTERVAL=1s CMD z"))
	s := f.Final()
	check(t, "Platform", s.Platform, "")
	check(t, "Copies", s.Copies, []Copy{{"COPY", "", []string{"x"}, "y", "", "", 2}})
	check(t, "Healthcheck", s.Healthcheck.Options, []string(nil))
	check(t, "Unknown", f.Unknown, []string{
		"line 1: FROM --PLATFORM",
		"line 2: COPY --FROM",
		"line 3: HEALTHCHECK --INTERVAL",
	})
}

func TestFlagNotes(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"from option", df("FROM --foo=x a"), []string{"line 1: FROM --foo"}},
		{"healthcheck option", df("FROM a", "HEALTHCHECK --x=1 CMD y"), []string{"line 2: HEALTHCHECK --x"}},
		{"healthcheck variable", df("FROM a", "HEALTHCHECK --interval=$I CMD y"), []string{held(2, "HEALTHCHECK --interval")}},
		{"copy option", df("FROM a", "COPY --link a b"), []string{"line 2: COPY --link"}},
		{"copy chown", df("FROM a", "COPY --chown=$U a b"), []string{held(2, "COPY --chown")}},
		{"copy chmod", df("FROM a", "COPY --chmod=$M a b"), []string{held(2, "COPY --chmod")}},
		{"copy from", df("FROM a", "COPY --from=$S a b"), []string{held(2, "COPY --from")}},
		{"copy platform", df("FROM a", "COPY --platform=$P a b"), []string{"line 2: COPY --platform", held(2, "COPY --platform")}},
		{"add chown", df("FROM a", "ADD --chown=$U a b"), []string{held(2, "ADD --chown")}},
		{"escaped dollar in an option", df("FROM a", `COPY --chown=\$U a b`), nil},
		{"single quoted dollar in an option", df("FROM a", "COPY --chown='$U' a b"), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check(t, "Unknown", mustParse(t, tt.src).Unknown, tt.want)
		})
	}
}

// Only a $ that is not escaped and not in single quotes is a variable.
func TestVariableNotes(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"env", df("FROM a", "ENV PATH=/app/bin:$PATH"), []string{held(2, "ENV PATH")}},
		{"env braces", df("FROM a", "ENV a=${X}"), []string{held(2, "ENV a")}},
		{"env double quotes", df("FROM a", `ENV a="$x"`), []string{held(2, "ENV a")}},
		{"env old form", df("FROM a", "ENV a b $x"), []string{held(2, "ENV a")}},
		{"env single quotes", df("FROM a", "ENV a='$x'"), nil},
		{"env escaped", df("FROM a", `ENV a=\$x`), nil},
		{"env escaped in double quotes", df("FROM a", `ENV a="\$x"`), nil},
		{"env old form escaped", df("FROM a", `ENV a b \$x`), nil},
		{"env second pair", df("FROM a", `ENV a=\$x b=$x`), []string{held(2, "ENV b")}},
		{"label", df("FROM a", "LABEL v=$X"), []string{held(2, "LABEL v")}},
		{"label old form", df("FROM a", "LABEL k $X"), []string{held(2, "LABEL k")}},
		{"label single quotes", df("FROM a", "LABEL v='$X'"), nil},
		{"arg default", df("FROM a", "ARG a=$x"), []string{held(2, "ARG a")}},
		{"arg before from", df("ARG a=$x", "FROM b"), []string{held(1, "ARG a")}},
		{"arg escaped", df("FROM a", `ARG a=\$x`), nil},
		{"expose", df("FROM a", "EXPOSE $PORT"), []string{held(2, "EXPOSE")}},
		{"expose braces", df("FROM a", "EXPOSE ${P}/tcp"), []string{held(2, "EXPOSE")}},
		{"volume", df("FROM a", "VOLUME $D"), []string{held(2, "VOLUME")}},
		{"volume json", df("FROM a", `VOLUME ["$D"]`), []string{held(2, "VOLUME")}},
		{"copy source", df("FROM a", "COPY a $D"), []string{held(2, "COPY")}},
		{"copy json", df("FROM a", `COPY ["$S", "/d"]`), []string{held(2, "COPY")}},
		{"add source", df("FROM a", "ADD $S /d"), []string{held(2, "ADD")}},
		{"user", df("FROM a", "USER $U"), []string{held(2, "USER")}},
		{"stopsignal", df("FROM a", "STOPSIGNAL $S"), []string{held(2, "STOPSIGNAL")}},
		{"workdir", df("FROM a", "WORKDIR /w/${D}"), []string{held(2, "WORKDIR")}},
		{"workdir single quotes", df("FROM a", "WORKDIR '/w/$D'"), nil},
		{"from", df("FROM $BASE"), []string{held(1, "FROM")}},
		{"from platform", df("FROM --platform=$P a"), []string{held(1, "FROM")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check(t, "Unknown", mustParse(t, tt.src).Unknown, tt.want)
		})
	}

	// The escaped $ is a literal in the value, and the raw text tells it from
	// a reference.
	s := mustParse(t, df("FROM a", `ARG a=\$x b='y z'`)).Final()
	check(t, "Args", s.Args, []Arg{
		{Name: "a", Default: "$x", RawDefault: `\$x`, HasDefault: true},
		{Name: "b", Default: "y z", RawDefault: "'y z'", HasDefault: true},
	})
}

func TestExpose(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		want    []string
		unknown []string
	}{
		{"host port", df("FROM a", "EXPOSE 8080:80"), []string{"8080:80"}, nil},
		{"ip and ports", df("FROM a", "EXPOSE 127.0.0.1:8081:81/udp [::1]:82:82 8000-8010:9000-9010"), []string{"127.0.0.1:8081:81/udp", "[::1]:82:82", "8000-8010:9000-9010"}, nil},
		{"variable", df("FROM a", "EXPOSE $PORT"), []string{"$PORT"}, []string{held(2, "EXPOSE")}},
		{"quoted", df("FROM a", `EXPOSE "80" '81/tcp'`), []string{"80", "81/tcp"}, nil},
		{"unknown protocol", df("FROM a", "EXPOSE 80 80/http"), []string{"80"}, []string{"line 2: EXPOSE 80/http is not a recognized port (not recorded)"}},
		{"odd form", df("FROM a", "EXPOSE 80:"), nil, []string{"line 2: EXPOSE 80: is not a recognized port (not recorded)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := mustParse(t, tt.src)
			check(t, "Expose", f.Final().Expose, tt.want)
			check(t, "Unknown", f.Unknown, tt.unknown)
		})
	}
}

func TestInstructionLines(t *testing.T) {
	s := mustParse(t, df("FROM a", "CMD x", "ENTRYPOINT y", "CMD z", "HEALTHCHECK NONE")).Final()
	check(t, "CmdLine", s.CmdLine, 4)
	check(t, "EntrypointLine", s.EntrypointLine, 3)
	check(t, "HealthcheckLine", s.HealthcheckLine, 5)
	s = mustParse(t, df("FROM a")).Final()
	check(t, "none", []int{s.CmdLine, s.EntrypointLine, s.HealthcheckLine}, []int{0, 0, 0})
}

func TestLoneEscapeAtEnd(t *testing.T) {
	f := mustParse(t, df("FROM a", "ENV x=y", `\`))
	check(t, "Unknown", f.Unknown, []string(nil))
	check(t, "Env", f.Final().Env, []EnvVar{ev("x", "y")})
}

func TestDuplicateStageNames(t *testing.T) {
	f := mustParse(t, df("FROM a AS x", "FROM a AS y", "FROM a", "FROM a AS z"))
	check(t, "stages", len(f.Stages), 4)
	_, err := Parse(strings.NewReader(df("FROM a AS x", "FROM a AS y", "FROM a AS X")))
	if err == nil || !strings.Contains(err.Error(), "line 3: duplicate stage name") {
		t.Errorf("error = %v, want a duplicate stage name at line 3", err)
	}
}

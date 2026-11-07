package dockerfile

import (
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
		{"cmd bad json", df("FROM a", "FROM b", `CMD ["a", "b"`), "line 3: CMD: malformed JSON list"},
		{"entrypoint bad json", df("FROM a", `ENTRYPOINT ['a']`), "line 2: ENTRYPOINT: malformed JSON list"},
		{"run bad json", df("FROM a", `RUN ["a",]`), "line 2: RUN: malformed JSON list"},
		{"volume bad json", df("FROM a", `VOLUME ["a"`), "line 2: VOLUME: malformed JSON list"},
		{"copy bad json", df("FROM a", `COPY ["a", "b"`), "line 2: COPY: malformed JSON list"},
		{"shell not json", df("FROM a", "SHELL sh"), "line 2: SHELL requires a JSON list"},
		{"shell empty", df("FROM a", "SHELL []"), "line 2: SHELL needs at least one element"},
		{"expose not a port", df("FROM a", "EXPOSE 80 abc"), `line 2: EXPOSE: "abc" is not a port`},
		{"expose bad protocol", df("FROM a", "EXPOSE 80/http"), `line 2: EXPOSE: "80/http" is not a port`},
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
		{"pairs", df("FROM a", "ENV a=b c=d"), []EnvVar{{"a", "b"}, {"c", "d"}}},
		{"old form", df("FROM a", "ENV a b c"), []EnvVar{{"a", "b c"}}},
		{"old form keeps spaces", df("FROM a", `ENV a "b  c"`), []EnvVar{{"a", "b  c"}}},
		{"old form with equals later", df("FROM a", "ENV a b=c"), []EnvVar{{"a", "b=c"}}},
		{"quotes", df("FROM a", `ENV a="x y" b='z w' c=p\ q`), []EnvVar{{"a", "x y"}, {"b", "z w"}, {"c", "p q"}}},
		{"escapes in quotes", df("FROM a", `ENV d="q\"r" e='s\t' f="u\v"`), []EnvVar{{"d", `q"r`}, {"e", `s\t`}, {"f", `u\v`}}},
		{"empty value", df("FROM a", "ENV a="), []EnvVar{{"a", ""}}},
		{"empty quoted value", df("FROM a", `ENV a=""`), []EnvVar{{"a", ""}}},
		{"value with equals", df("FROM a", "ENV a=b=c"), []EnvVar{{"a", "b=c"}}},
		{"lower case", df("FROM a", "env a=b"), []EnvVar{{"a", "b"}}},
		{"repeated name", df("FROM a", "ENV a=1", "ENV a=2"), []EnvVar{{"a", "1"}, {"a", "2"}}},
		{"quoted name", df("FROM a", `ENV "a b"=c`), []EnvVar{{"a b", "c"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check(t, "Env", mustParse(t, tt.src).Final().Env, tt.want)
		})
	}
}

func TestEnviron(t *testing.T) {
	s := mustParse(t, df("FROM a", "ENV A=1 B=2", "ENV A=3", "ENV C c")).Final()
	check(t, "Environ", s.Environ(), []EnvVar{{"A", "3"}, {"B", "2"}, {"C", "c"}})
	check(t, "Env is not changed", s.Env, []EnvVar{{"A", "1"}, {"B", "2"}, {"A", "3"}, {"C", "c"}})
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
		{"label pairs", df("FROM a", `LABEL a=b "c d"="e f"`), func(s *Stage) any { return s.Labels }, []EnvVar{{"a", "b"}, {"c d", "e f"}}},
		{"label old form", df("FROM a", "LABEL k some value"), func(s *Stage) any { return s.Labels }, []EnvVar{{"k", "some value"}}},
		{"args", df("FROM a", "ARG a", "ARG b=1 c=", `ARG d="x y"`), func(s *Stage) any { return s.Args }, []Arg{{"a", "", false}, {"b", "1", true}, {"c", "", true}, {"d", "x y", true}}},
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
	check(t, "Env", s.Env, []EnvVar{{"x", "y"}})

	// Without the directive the backslash continues.
	s = mustParse(t, df("FROM a", `RUN echo a \`, "ENV x=y")).Final()
	check(t, "Runs", s.Runs, []Run{{Command: Command{Shell: "echo a ENV x=y", Set: true}, Line: 2}})
	check(t, "Env", s.Env, []EnvVar(nil))

	// A directive in upper case with a backslash is accepted.
	s = mustParse(t, df("# ESCAPE = \\", "FROM a", `RUN a \`, "b")).Final()
	check(t, "Runs", s.Runs, []Run{{Command: Command{Shell: "a b", Set: true}, Line: 3}})
}

func TestBOMAndCRLF(t *testing.T) {
	s := mustParse(t, "\uFEFFFROM a\r\nENV a=b\r\nRUN x \\\r\n y\r\n").Final()
	check(t, "Base", s.Base, "a")
	check(t, "Env", s.Env, []EnvVar{{"a", "b"}})
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
	check(t, "Env", s.Env, []EnvVar{{"after", "1"}})
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

	f = mustParse(t, df("FROM a", "RUN <<A <<B", "one", "A", "two", "B", "ENV x=y"))
	check(t, "two heredocs", f.Final().Env, []EnvVar{{"x", "y"}})
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
	check(t, "Env", s.Env, []EnvVar{{"P", "$HOME/x"}})
	check(t, "WorkDir", s.WorkDir, "/w/${D}")
}

func TestMultiStage(t *testing.T) {
	f, err := ParseFile("testdata/multistage.Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "Args", f.Args, []Arg{{"GO_VERSION", "1.22", true}})
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
	check(t, "builder Env", b.Env, []EnvVar{{"CGO_ENABLED", "0"}})
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

func TestStageAsBase(t *testing.T) {
	f := mustParse(t, df("FROM a AS base", "ENV X=1", "WORKDIR /w", "FROM base", "ENV Y=2"))
	fin := f.Final()
	if f.Stage(fin.Base) != f.Stages[0] {
		t.Error("the base of the final stage is not resolved to the first stage")
	}
	check(t, "child Environ", fin.Environ(), []EnvVar{{"Y", "2"}})
	check(t, "child WorkDir", fin.WorkDir, "")
	check(t, "parent Environ", f.Stages[0].Environ(), []EnvVar{{"X", "1"}})
}

func TestFinalOfEmptyFile(t *testing.T) {
	if (&File{}).Final() != nil {
		t.Error("Final of a file without stages is not nil")
	}
}

func TestParseFileErrors(t *testing.T) {
	_, err := ParseFile("testdata/bad_json.Dockerfile")
	if err == nil || !strings.Contains(err.Error(), "bad_json.Dockerfile: line 2: CMD: malformed JSON list") {
		t.Errorf("error = %v, want the path and line 2", err)
	}
	if _, err := ParseFile("testdata/missing.Dockerfile"); err == nil {
		t.Error("ParseFile of a missing file succeeded")
	}
}

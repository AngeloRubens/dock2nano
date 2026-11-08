package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/compose"
	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/gen"
	"github.com/AngeloRubens/dock2nano/internal/observe"
	"github.com/AngeloRubens/dock2nano/internal/output"
	"github.com/AngeloRubens/dock2nano/internal/plan"
)

type fakeDocker struct {
	calls []string

	builds []docker.BuildOptions

	have map[string]bool

	images map[string]*docker.ImageConfig

	buildErr error

	// events is shared with the harness: the order of what happened.
	events *[]string
	// netCreated and netRemoved are the names given to the network calls.
	netCreated []string
	netRemoved []string
	netErr     error
	// netLabels are the labels given to each NetworkCreate.
	netLabels [][]string
	// netRemoveErr fails NetworkRemove.
	netRemoveErr error
	// removeCtxErr is the error of the context of each NetworkRemove.
	removeCtxErr []error
}

func (f *fakeDocker) NetworkCreate(ctx context.Context, name string, labels ...string) error {
	*f.events = append(*f.events, "net create")
	f.netCreated = append(f.netCreated, name)
	f.netLabels = append(f.netLabels, labels)
	return f.netErr
}

func (f *fakeDocker) NetworkRemove(ctx context.Context, name string) error {
	*f.events = append(*f.events, "net rm")
	f.netRemoved = append(f.netRemoved, name)
	f.removeCtxErr = append(f.removeCtxErr, ctx.Err())
	return f.netRemoveErr
}

// fakeStopper is a started dependency.
type fakeStopper struct {
	h    *harness
	name string
}

func (s *fakeStopper) Stop(ctx context.Context) error {
	s.h.events = append(s.h.events, "stop "+s.name)
	return s.h.stopErr[s.name]
}

// NoteList is what Start noticed about the container, by its image.
func (s *fakeStopper) NoteList() []string { return s.h.startNotes[s.name] }

func (f *fakeDocker) Build(ctx context.Context, o docker.BuildOptions) error {
	f.calls = append(f.calls, "build "+o.Tag)
	f.builds = append(f.builds, o)
	return f.buildErr
}

func (f *fakeDocker) Pull(ctx context.Context, image string) error {
	f.calls = append(f.calls, "pull "+image)
	f.have[image] = true
	return nil
}

func (f *fakeDocker) HasImage(ctx context.Context, image string) (bool, error) {
	f.calls = append(f.calls, "has "+image)
	return f.have[image], nil
}

func (f *fakeDocker) Inspect(ctx context.Context, image string) (*docker.ImageConfig, error) {
	f.calls = append(f.calls, "inspect "+image)
	if img := f.images[image]; img != nil {
		return img, nil
	}
	return &docker.ImageConfig{}, nil
}

var netName = regexp.MustCompile(`^dock2nano-[0-9a-f]{8}$`)

type harness struct {
	t *testing.T

	app *App

	dock *fakeDocker

	dir string

	stdout *bytes.Buffer

	stderr *bytes.Buffer

	observed []observe.Options

	obsErr map[string]error

	genIn []gen.Input

	wrote []*plan.Plan

	wopts []output.Options

	removed []string

	env map[string]string

	// events is the order of the network calls, starts, stops and observations.
	events []string
	// started are the options of each Start; startErr fails the start of an image.
	started  []observe.StartOptions
	startErr map[string]error
	// startNotes are the notes a started container carries, by its image.
	startNotes map[string][]string
	// stopErr fails the Stop of a started container, by its image.
	stopErr map[string]error
	// onObserve runs at the start of each observation, with its options.
	onObserve func(o observe.Options) error
	// tmp is the temporary directory of the test: the copies of the binds are
	// made in it, and it must be empty when the test ends.
	tmp string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, dir: t.TempDir(), tmp: t.TempDir(), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	t.Setenv("TMPDIR", h.tmp)
	t.Cleanup(func() {
		if left, _ := os.ReadDir(h.tmp); len(left) != 0 {
			t.Errorf("the temporary directory is not empty after the run: %v", left)
		}
	})
	h.dock = &fakeDocker{have: map[string]bool{}, images: map[string]*docker.ImageConfig{}}
	h.obsErr = map[string]error{}
	h.startErr = map[string]error{}
	h.startNotes = map[string][]string{}
	h.stopErr = map[string]error{}
	h.dock.events = &h.events
	h.env = map[string]string{}
	h.app = &App{Docker: h.dock, Stdout: h.stdout, Stderr: h.stderr}
	h.app.Observe = func(ctx context.Context, o observe.Options) (*observe.Result, error) {
		h.observed = append(h.observed, o)
		h.events = append(h.events, "observe "+o.Image)
		if h.onObserve != nil {
			if err := h.onObserve(o); err != nil {
				return nil, err
			}
		}
		if err := h.obsErr[o.Image]; err != nil {
			return nil, err
		}
		return &observe.Result{Sysroot: "/sysroots/" + o.Image, Program: "/usr/bin/app", Argv: []string{"/usr/bin/app"}}, nil
	}
	h.app.Start = func(ctx context.Context, o observe.StartOptions) (Stopper, error) {
		h.started = append(h.started, o)
		if err := h.startErr[o.Image]; err != nil {
			return nil, err
		}
		h.events = append(h.events, "start "+o.Image)
		return &fakeStopper{h: h, name: o.Image}, nil
	}
	h.app.Generate = func(in gen.Input) (*plan.Plan, error) {
		h.genIn = append(h.genIn, in)
		return gen.Generate(in)
	}
	h.app.Write = func(p *plan.Plan, o output.Options) ([]string, error) {
		h.wrote = append(h.wrote, p)
		h.wopts = append(h.wopts, o)
		var out []string
		for _, s := range p.Services {
			out = append(out, "/w/"+s.PackageName)
		}
		return append(out, o.ComposePath), nil
	}
	h.app.LookupEnv = func(k string) (string, bool) {
		v, ok := h.env[k]
		return v, ok
	}
	h.app.Getwd = func() (string, error) { return h.dir, nil }
	h.app.Stat = os.Stat
	h.app.ReadFile = os.ReadFile
	h.app.UserHomeDir = func() (string, error) { return "/home/u", nil }
	h.app.RemoveAll = func(p string) error {
		h.removed = append(h.removed, p)
		return nil
	}
	return h
}

func (h *harness) write(name, content string) string {
	h.t.Helper()
	p := filepath.Join(h.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
	return p
}

func (h *harness) run(args ...string) int {
	h.t.Helper()
	return h.app.Run(context.Background(), args)
}

// mustRun runs the command and fails the test unless the status is want.
func (h *harness) mustRun(want int, args ...string) {
	h.t.Helper()
	if got := h.run(args...); got != want {
		h.t.Fatalf("exit status %d, want %d\nstdout:\n%s\nstderr:\n%s", got, want, h.stdout, h.stderr)
	}
}

func (h *harness) names() []string {
	var out []string
	for _, o := range h.observed {
		out = append(out, o.Image)
	}
	return out
}

func TestParseDefaults(t *testing.T) {
	h := newHarness(t)
	o, err := h.app.parseConvert(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := &options{out: ".", seconds: 30}
	if !reflect.DeepEqual(o, want) {
		t.Fatalf("got %+v, want %+v", o, want)
	}
}

func TestParseAllFlags(t *testing.T) {
	h := newHarness(t)
	args := []string{"-f", "a.yml", "--service", "web", "-service", "db", "-o", "out", "--ops-home", "/h", "--seconds", "5", "--pull", "--force", "--strict"}
	o, err := h.app.parseConvert(args)
	if err != nil {
		t.Fatal(err)
	}
	want := &options{file: "a.yml", out: "out", opsHome: "/h", seconds: 5, pull: true, force: true, strict: true, services: []string{"web", "db"}}
	if !reflect.DeepEqual(o, want) {
		t.Fatalf("got %+v, want %+v", o, want)
	}
	o, err = h.app.parseConvert([]string{"--dockerfile", "d/Dockerfile", "--context", "d", "--name", "n"})
	if err != nil {
		t.Fatal(err)
	}
	want = &options{dockerfile: "d/Dockerfile", context: "d", name: "n", out: ".", seconds: 30}
	if !reflect.DeepEqual(o, want) {
		t.Fatalf("got %+v, want %+v", o, want)
	}
}

func TestUsageErrors(t *testing.T) {
	rows := []struct {
		args []string
		want string
	}{
		{[]string{"convert", "-f", "a.yml", "--dockerfile", "Dockerfile"}, "-f and --dockerfile cannot be used together"},
		{[]string{"convert", "--context", "x"}, "--context and --name go with --dockerfile"},
		{[]string{"convert", "--name", "x"}, "--context and --name go with --dockerfile"},
		{[]string{"convert", "--seconds", "0"}, "--seconds must be greater than 0, not 0"},
		{[]string{"convert", "extra"}, `unexpected argument "extra"`},
		{[]string{"convert", "--nope"}, "flag provided but not defined: -nope"},
		{[]string{"convert", "--service", ""}, "the service name is empty"},
		{[]string{"frobnicate"}, `unknown command "frobnicate"`},
	}
	for _, r := range rows {
		h := newHarness(t)
		h.mustRun(1, r.args...)
		if !strings.Contains(h.stderr.String(), r.want) {
			t.Errorf("%v: stderr %q does not contain %q", r.args, h.stderr, r.want)
		}
		if !strings.HasPrefix(h.stderr.String(), ToolName+": ") {
			t.Errorf("%v: stderr %q does not start with the tool name", r.args, h.stderr)
		}
		if len(h.dock.calls) != 0 || len(h.wrote) != 0 {
			t.Errorf("%v: something ran: %v", r.args, h.dock.calls)
		}
	}
}

func TestNoArgsAndHelpAndVersion(t *testing.T) {
	h := newHarness(t)
	h.mustRun(1)
	if !strings.Contains(h.stderr.String(), "Usage:\n  "+ToolName+" convert [flags]\n  "+ToolName+" version\n") {
		t.Errorf("no usage: %q", h.stderr)
	}
	h = newHarness(t)
	h.mustRun(0, "help")
	if !strings.Contains(h.stdout.String(), "--strict") || strings.Contains(h.stdout.String(), "{tool}") {
		t.Errorf("bad help: %q", h.stdout)
	}
	h = newHarness(t)
	h.mustRun(0, "convert", "-h")
	if !strings.Contains(h.stdout.String(), "--service NAME") {
		t.Errorf("convert -h printed no usage: %q", h.stdout)
	}
	if !strings.Contains(h.stdout.String(), "like the OPS_HOME variable of ops: the folder that holds .ops") {
		t.Errorf("the help does not say what --ops-home means: %q", h.stdout)
	}
	h = newHarness(t)
	h.mustRun(0, "version")
	if h.stdout.String() != ToolName+" "+Version+"\n" {
		t.Errorf("version: %q", h.stdout)
	}
}

func TestComposeDiscoveryOrder(t *testing.T) {
	all := []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}
	for i, want := range all {
		h := newHarness(t)
		for _, n := range all[i:] {
			h.write(n, "services:\n  from-"+strings.ReplaceAll(n, ".", "-")+":\n    image: x\n")
		}
		p, dir, err := h.app.load(&options{out: "."}, h.dir)
		if err != nil {
			t.Fatal(err)
		}
		wantSvc := "from-" + strings.ReplaceAll(want, ".", "-")
		if len(p.Services) != 1 || p.Services[0].Name != wantSvc {
			t.Errorf("with %v present: got %v, want the service of %s", all[i:], p.Services, want)
		}
		if dir != h.dir {
			t.Errorf("dir %q, want %q", dir, h.dir)
		}
	}
}

func TestComposeNotFoundAndExplicitFile(t *testing.T) {
	h := newHarness(t)
	_, _, err := h.app.load(&options{}, h.dir)
	if err == nil || !strings.Contains(err.Error(), "docker-compose.yml, docker-compose.yaml, compose.yml, compose.yaml") {
		t.Fatalf("got %v", err)
	}
	h.write("docker-compose.yml", "services:\n  other:\n    image: x\n")
	h.write("deploy/prod.yml", "services:\n  prod:\n    image: x\n")
	p, dir, err := h.app.load(&options{file: "deploy/prod.yml"}, h.dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Services[0].Name != "prod" || dir != filepath.Join(h.dir, "deploy") {
		t.Fatalf("got %s in %s", p.Services[0].Name, dir)
	}
}

const buildCompose = `name: shop
services:
  web:
    build:
      context: ./web
      dockerfile: docker/Web.Dockerfile
      target: prod
      args:
        A: "1"
        B: ${B_VAL}
        C: x-${B_VAL:-d}
        D:
        E:
`

const stagesDockerfile = "FROM alpine AS prod\nEXPOSE 80\nFROM alpine AS last\nEXPOSE 81\n"

func TestBuildCall(t *testing.T) {
	h := newHarness(t)
	h.env["B_VAL"] = "bv"
	h.env["D"] = "dv"
	h.write("compose.yaml", buildCompose)
	h.write("web/docker/Web.Dockerfile", stagesDockerfile)
	h.mustRun(0, "convert")
	if len(h.dock.builds) != 1 {
		t.Fatalf("builds: %v", h.dock.builds)
	}
	tag := ToolName + "/shop/web:latest"
	want := docker.BuildOptions{Context: filepath.Join(h.dir, "web"), Dockerfile: filepath.Join(h.dir, "web", "docker", "Web.Dockerfile"), Target: "prod", Tag: tag, Quiet: true}
	want.Args = map[string]string{"A": "1", "B": "bv", "C": "x-bv", "D": "dv"}
	if !reflect.DeepEqual(h.dock.builds[0], want) {
		t.Fatalf("got %+v\nwant %+v", h.dock.builds[0], want)
	}
	if !reflect.DeepEqual(h.dock.calls, []string{"build " + tag, "inspect " + tag}) {
		t.Fatalf("calls: %v", h.dock.calls)
	}
	if !reflect.DeepEqual(h.names(), []string{tag}) {
		t.Fatalf("observed: %v", h.names())
	}
	if st := h.genIn[0].Services["web"].Stage; st == nil || st.Name != "prod" {
		t.Fatalf("stage: %+v", st)
	}
	if h.genIn[0].Services["web"].Image == nil {
		t.Fatal("the image config did not reach the generator")
	}
	if strings.Contains(h.stderr.String(), "bv") || strings.Contains(h.stderr.String(), "dv") {
		t.Errorf("a build argument value was printed: %q", h.stderr)
	}
}

func TestBuildDefaults(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "name: shop\nservices:\n  web:\n    build: ./web\n")
	h.write("web/Dockerfile", stagesDockerfile)
	h.mustRun(0, "convert")
	want := docker.BuildOptions{Context: filepath.Join(h.dir, "web"), Dockerfile: filepath.Join(h.dir, "web", "Dockerfile"), Tag: ToolName + "/shop/web:latest", Quiet: true}
	if !reflect.DeepEqual(h.dock.builds[0], want) {
		t.Fatalf("got %+v\nwant %+v", h.dock.builds[0], want)
	}
	if st := h.genIn[0].Services["web"].Stage; st == nil || st.Name != "last" {
		t.Fatalf("without a target the final stage is expected: %+v", st)
	}
}

func TestBuildArgUnresolved(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", buildCompose)
	h.mustRun(0, "convert")
	if len(h.dock.builds) != 1 {
		t.Fatalf("builds: %v", h.dock.builds)
	}
	want := map[string]string{"A": "1", "B": "", "C": "x-d"}
	if !reflect.DeepEqual(h.dock.builds[0].Args, want) {
		t.Errorf("args %v, want %v", h.dock.builds[0].Args, want)
	}
	msg := h.stderr.String()
	for _, w := range []string{"web: warning: build argument B", "B_VAL", "not set"} {
		if !strings.Contains(msg, w) {
			t.Errorf("stderr %q does not contain %q", msg, w)
		}
	}
	if strings.Contains(msg, "build argument C") {
		t.Errorf("a variable with a default was reported: %q", msg)
	}
}

func TestImageTag(t *testing.T) {
	rows := []struct{ project, dir, service, want string }{
		{"My_Shop X", "/srv/ignored", "Web.1", "my_shop-x/web.1"},
		{"", "/srv/My App", "web", "my-app/web"},
		{"a-b", "/d", "c", "a-b/c"},
		{"a", "/d", "b-c", "a/b-c"},
		{"_x", "/d", ".y", "x/y"},
		{"a--b", "/d", "c__d", "a-b/c_d"},
		{"a-.b", "/d", "c", "a-b/c"},
		{"---", "/d", "c", "x/c"},
		{"p", "/d", "Ünï", "p/n"},
	}
	for _, r := range rows {
		got := imageTag(&compose.Project{Name: r.project}, r.dir, &compose.Service{Name: r.service})
		if want := ToolName + "/" + r.want + ":latest"; got != want {
			t.Errorf("%q %q: got %q, want %q", r.project, r.service, got, want)
		}
	}
}

func TestPullRules(t *testing.T) {
	file := "services:\n  nginx:\n    image: nginx:1\n"
	h := newHarness(t)
	h.write("compose.yaml", file)
	h.mustRun(1, "convert")
	if !strings.Contains(h.stderr.String(), `service nginx: the image "nginx:1" is not on this machine`) || !strings.Contains(h.stderr.String(), "--pull") {
		t.Errorf("stderr: %q", h.stderr)
	}
	if !reflect.DeepEqual(h.dock.calls, []string{"has nginx:1"}) || len(h.observed) != 0 || len(h.wrote) != 0 {
		t.Errorf("calls %v, observed %v", h.dock.calls, h.names())
	}

	h = newHarness(t)
	h.write("compose.yaml", file)
	h.mustRun(0, "convert", "--pull")
	if !reflect.DeepEqual(h.dock.calls, []string{"has nginx:1", "pull nginx:1", "inspect nginx:1"}) {
		t.Errorf("calls %v", h.dock.calls)
	}
	if h.observed[0].Pull {
		t.Error("observe was asked to pull")
	}

	h = newHarness(t)
	h.write("compose.yaml", file)
	h.dock.have["nginx:1"] = true
	h.mustRun(0, "convert", "--pull")
	if !reflect.DeepEqual(h.dock.calls, []string{"has nginx:1", "inspect nginx:1"}) {
		t.Errorf("an image that is there was pulled: %v", h.dock.calls)
	}
}

const optionsCompose = `services:
  web:
    image: nginx:1
    entrypoint: ["/entry.sh"]
    command: ["serve", "--port", "80"]
    environment:
      A: "1"
      B: ${HOST_B}
      C: c
      D: ${SET_LATER}
    env_file: web.env
    working_dir: /srv/app
    user: "1000:1000"
    volumes:
      - ./conf:/etc/conf:ro
      - ./rw:/rw
      - ./missing:/m
      - data:/data
volumes:
  data:
`

func TestObserveOptions(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", optionsCompose)
	h.write("web.env", "E=from-file\nA=file-a\n# comment\nexport Q=\"quoted\"\nD=file-d\nR=raw # trailing\nS=\"keep # this\"\n")
	if err := os.MkdirAll(filepath.Join(h.dir, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.dir, "rw"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.dock.have["nginx:1"] = true
	h.env["HOST_B"] = "hb"
	h.env["SET_LATER"] = "sl"
	h.mustRun(0, "convert", "--seconds", "7")
	if len(h.observed) != 1 {
		t.Fatalf("observed: %v", h.names())
	}
	got := h.observed[0]
	if got.Log == nil {
		t.Fatal("no Log")
	}
	got.Log("hello %d\n", 1)
	if !strings.Contains(h.stderr.String(), "web: hello 1\n") {
		t.Errorf("the log is not prefixed with the service: %q", h.stderr)
	}
	got.Log = nil
	if !netName.MatchString(got.Network) || !reflect.DeepEqual(got.NetworkAliases, []string{"web"}) {
		t.Errorf("network %q, aliases %v", got.Network, got.NetworkAliases)
	}
	got.Network, got.NetworkAliases = "", nil
	ep := []string{"/entry.sh"}
	want := observe.Options{Image: "nginx:1", Entrypoint: &ep, Cmd: []string{"serve", "--port", "80"}, Seconds: 7, WorkingDir: "/srv/app", User: "1000:1000"}
	want.Env = []string{"A=1", "B=hb", "C=c", "D=sl", "E=from-file", "Q=quoted", "R=raw", "S=keep # this"}
	// the read-only bind is mounted as it is, the other one as a copy
	if len(got.Binds) != 2 || got.Binds[0] != filepath.Join(h.dir, "conf")+":/etc/conf:ro" {
		t.Fatalf("binds %v", got.Binds)
	}
	h.wantCopy(got.Binds[1], filepath.Join(h.dir, "rw"), "/rw")
	want.Binds = got.Binds
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	for _, v := range []string{"from-file", "hb", "sl", "keep # this"} {
		if strings.Contains(h.stderr.String(), v) {
			t.Errorf("an environment value was printed: %q", h.stderr)
		}
	}
}

func TestBindSourceWithColon(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n    volumes:\n      - type: bind\n        source: ./c:d\n        target: /x\n      - type: bind\n        source: ./ok\n        target: /y\n")
	for _, d := range []string{"c:d", "ok"} {
		if err := os.MkdirAll(filepath.Join(h.dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.dock.have["w"] = true
	h.mustRun(0, "convert")
	if len(h.observed[0].Binds) != 1 {
		t.Fatalf("binds %v", h.observed[0].Binds)
	}
	h.wantCopy(h.observed[0].Binds[0], filepath.Join(h.dir, "ok"), "/y")
	if !strings.Contains(h.stderr.String(), "web: warning: the bind source") || !strings.Contains(h.stderr.String(), "c:d") {
		t.Errorf("no warning: %q", h.stderr)
	}
}

func TestObserveCommand(t *testing.T) {
	rows := []struct {
		name  string
		extra string
		image docker.ImageConfig
		ep    []string
		cmd   []string
	}{
		{"image only", "", docker.ImageConfig{Entrypoint: []string{"/e"}, Cmd: []string{"c"}}, []string{"/e"}, []string{"c"}},
		{"image cmd only", "", docker.ImageConfig{Cmd: []string{"c"}}, []string{}, []string{"c"}},
		{"command replaces cmd", "    command: run now\n", docker.ImageConfig{Entrypoint: []string{"/e"}, Cmd: []string{"c"}}, []string{"/e"}, []string{"run", "now"}},
		{"entrypoint clears cmd", "    entrypoint: /other\n", docker.ImageConfig{Entrypoint: []string{"/e"}, Cmd: []string{"c"}}, []string{"/other"}, nil},
		{"empty entrypoint", "    entrypoint: []\n", docker.ImageConfig{Entrypoint: []string{"/e"}, Cmd: []string{"c"}}, []string{}, nil},
		{"empty command", "    command: []\n", docker.ImageConfig{Entrypoint: []string{"/e"}, Cmd: []string{"c"}}, []string{"/e"}, nil},
		{"interpolated command", "    command: run ${CMD_ARG} \"two words\"\n", docker.ImageConfig{Entrypoint: []string{"/e"}}, []string{"/e"}, []string{"run", "one", "two words"}},
	}
	for _, r := range rows {
		h := newHarness(t)
		h.env["CMD_ARG"] = "one"
		h.write("compose.yaml", "services:\n  web:\n    image: img\n"+r.extra)
		h.dock.have["img"] = true
		img := r.image
		h.dock.images["img"] = &img
		h.mustRun(0, "convert")
		got := h.observed[0]
		if got.Entrypoint == nil || !reflect.DeepEqual(*got.Entrypoint, r.ep) {
			t.Errorf("%s: entrypoint %v, want %v", r.name, got.Entrypoint, r.ep)
		}
		if !reflect.DeepEqual(got.Cmd, r.cmd) {
			t.Errorf("%s: cmd %v, want %v", r.name, got.Cmd, r.cmd)
		}
	}
}

func TestCommandErrorStopsTheRun(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n    command: run ${BROKEN\n")
	h.dock.have["w"] = true
	h.mustRun(1, "convert")
	if !strings.Contains(h.stderr.String(), "service web:") || len(h.observed) != 0 || len(h.wrote) != 0 {
		t.Errorf("stderr %q, observed %v", h.stderr, h.names())
	}
}

func TestPackageNamesChecked(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "name: p\nservices:\n  Web:\n    image: a\n  web:\n    image: b\n")
	h.mustRun(1, "convert")
	if len(h.dock.calls) != 0 || len(h.observed) != 0 || len(h.wrote) != 0 {
		t.Errorf("something ran before the package names were checked: %v", h.dock.calls)
	}
}

func TestSkippedServices(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", `services:
  main:
    image: m
  dbg:
    image: d
    profiles: [debug]
  zero:
    image: z
    deploy:
      replicas: 0
  bare:
    command: x
`)
	h.dock.have["m"] = true
	h.mustRun(0, "convert")
	if !reflect.DeepEqual(h.names(), []string{"m"}) {
		t.Fatalf("observed: %v", h.names())
	}
	if !reflect.DeepEqual(h.dock.calls, []string{"has m", "inspect m"}) {
		t.Fatalf("docker was called for a skipped service: %v", h.dock.calls)
	}
	if len(h.genIn[0].Services) != 1 || h.genIn[0].Services["main"].Observed == nil {
		t.Fatalf("inputs: %v", h.genIn[0].Services)
	}
	wantSkipped := map[string]string{"dbg": "has profiles (debug)", "zero": "has 0 replicas", "bare": "has no image and no build"}
	if !reflect.DeepEqual(h.genIn[0].Skipped, wantSkipped) {
		t.Errorf("skipped %v, want %v", h.genIn[0].Skipped, wantSkipped)
	}
	out := h.stdout.String()
	for _, want := range []string{"service dbg:", "service zero:", "service bare:", "profiles", "replicas"} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not mention %q:\n%s", want, out)
		}
	}
}

func TestDependencyOrder(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", `services:
  web:
    image: w
    depends_on: [db]
  db:
    image: d
    depends_on:
      cache:
        condition: service_started
  cache:
    image: c
  solo:
    image: s
`)
	for _, i := range []string{"w", "d", "c", "s"} {
		h.dock.have[i] = true
	}
	h.mustRun(0, "convert")
	if want := []string{"c", "d", "w", "s"}; !reflect.DeepEqual(h.names(), want) {
		t.Fatalf("observed in order %v, want %v", h.names(), want)
	}
}

func TestDependencyErrors(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  a:\n    image: a\n    depends_on: [b]\n  b:\n    image: b\n    depends_on: [a]\n")
	h.mustRun(1, "convert")
	if !strings.Contains(h.stderr.String(), "cycle") || len(h.dock.calls) != 0 {
		t.Errorf("stderr %q, calls %v", h.stderr, h.dock.calls)
	}
}

func TestObserveErrorStopsTheRun(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n    depends_on: [db]\n  db:\n    image: d\n    depends_on: [cache]\n  cache:\n    image: c\n")
	for _, i := range []string{"w", "d", "c"} {
		h.dock.have[i] = true
	}
	h.obsErr["d"] = errors.New("boom")
	h.mustRun(1, "convert")
	if !strings.Contains(h.stderr.String(), "service db: boom") {
		t.Errorf("stderr: %q", h.stderr)
	}
	if !reflect.DeepEqual(h.names(), []string{"c", "d"}) {
		t.Errorf("observed %v: the run went on after the error", h.names())
	}
	if len(h.genIn) != 0 || len(h.wrote) != 0 {
		t.Errorf("a result was produced: gen %d, write %d", len(h.genIn), len(h.wrote))
	}
	if h.stdout.Len() != 0 {
		t.Errorf("stdout: %q", h.stdout)
	}
	if !reflect.DeepEqual(h.removed, []string{"/sysroots/c"}) {
		t.Errorf("sysroots removed: %v", h.removed)
	}
}

func TestSysrootsRemovedAfterWrite(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  a:\n    image: a\n  b:\n    image: b\n")
	h.dock.have["a"] = true
	h.dock.have["b"] = true
	h.mustRun(0, "convert")
	if !reflect.DeepEqual(h.removed, []string{"/sysroots/a", "/sysroots/b"}) {
		t.Errorf("removed: %v", h.removed)
	}
}

func TestBuildErrorNamesService(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    build: .\n")
	h.dock.buildErr = errors.New("no space")
	h.mustRun(1, "convert")
	if !strings.Contains(h.stderr.String(), "service web: no space") || len(h.wrote) != 0 || len(h.observed) != 0 {
		t.Errorf("stderr %q", h.stderr)
	}
}

func TestStrict(t *testing.T) {
	bad := "services:\n  web:\n    image: w\n    privileged: true\n"
	h := newHarness(t)
	h.write("compose.yaml", bad)
	h.dock.have["w"] = true
	h.mustRun(0, "convert")

	h = newHarness(t)
	h.write("compose.yaml", bad)
	h.dock.have["w"] = true
	h.mustRun(2, "convert", "--strict")
	if !strings.Contains(h.stdout.String(), "unsupported privileged") || len(h.wrote) != 1 {
		t.Errorf("the report and the files must still be produced:\n%s", h.stdout)
	}
	if !strings.Contains(h.stderr.String(), "1 unsupported item(s)") {
		t.Errorf("stderr: %q", h.stderr)
	}

	h = newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n")
	h.dock.have["w"] = true
	h.mustRun(0, "convert", "--strict")
}

func TestServiceFilter(t *testing.T) {
	file := "services:\n  web:\n    image: w\n  db:\n    image: d\n"
	h := newHarness(t)
	h.write("compose.yaml", file)
	h.dock.have["w"] = true
	h.dock.have["d"] = true
	h.mustRun(0, "convert", "--service", "db")
	if !reflect.DeepEqual(h.names(), []string{"d"}) {
		t.Fatalf("observed: %v", h.names())
	}
	if len(h.wrote[0].Services) != 1 || h.wrote[0].Services[0].Name != "db" {
		t.Errorf("services written: %+v", h.wrote[0].Services)
	}
	if !strings.Contains(h.stdout.String(), "service web:") {
		t.Errorf("the service left out is not in the report:\n%s", h.stdout)
	}
	if want := map[string]string{"web": "not selected with --service"}; !reflect.DeepEqual(h.genIn[0].Skipped, want) {
		t.Errorf("skipped %v, want %v", h.genIn[0].Skipped, want)
	}

	h = newHarness(t)
	h.write("compose.yaml", file)
	h.mustRun(1, "convert", "--service", "nope")
	if !strings.Contains(h.stderr.String(), "--service nope") || len(h.dock.calls) != 0 {
		t.Errorf("stderr %q", h.stderr)
	}
}

func TestDockerfileMode(t *testing.T) {
	h := newHarness(t)
	h.write("app/Dockerfile", stagesDockerfile)
	h.mustRun(0, "convert", "--dockerfile", "app/Dockerfile", "-o", "out", "--ops-home", "/oh", "--force")
	tag := ToolName + "/app/app:latest"
	want := docker.BuildOptions{Context: filepath.Join(h.dir, "app"), Dockerfile: filepath.Join(h.dir, "app", "Dockerfile"), Tag: tag, Quiet: true}
	if len(h.dock.builds) != 1 || !reflect.DeepEqual(h.dock.builds[0], want) {
		t.Fatalf("builds: %+v\nwant %+v", h.dock.builds, want)
	}
	if !reflect.DeepEqual(h.names(), []string{tag}) {
		t.Fatalf("observed: %v", h.names())
	}
	wantOpts := output.Options{OpsHome: "/oh", ComposePath: filepath.Join(h.dir, "out", "compose.yaml"), Force: true}
	if !reflect.DeepEqual(h.wopts, []output.Options{wantOpts}) {
		t.Fatalf("write options: %+v", h.wopts)
	}
	if h.wrote[0].Services[0].Name != "app" {
		t.Errorf("services: %+v", h.wrote[0].Services)
	}
	if st := h.genIn[0].Services["app"].Stage; st == nil || st.Name != "last" {
		t.Errorf("stage: %+v", st)
	}
	if h.genIn[0].Project.Name != "app" || h.genIn[0].Dir != h.dir {
		t.Errorf("project %q in %q", h.genIn[0].Project.Name, h.genIn[0].Dir)
	}
}

func TestDockerfileModeNameAndContext(t *testing.T) {
	h := newHarness(t)
	h.write("ci/Dockerfile", "FROM alpine\n")
	h.mustRun(0, "convert", "--dockerfile", "ci/Dockerfile", "--context", "src", "--name", "demo")
	want := docker.BuildOptions{Context: filepath.Join(h.dir, "src"), Dockerfile: filepath.Join(h.dir, "ci", "Dockerfile"), Tag: ToolName + "/demo/demo:latest", Quiet: true}
	if !reflect.DeepEqual(h.dock.builds[0], want) {
		t.Fatalf("got %+v\nwant %+v", h.dock.builds[0], want)
	}
	if h.wrote[0].Services[0].Name != "demo" {
		t.Errorf("services: %+v", h.wrote[0].Services)
	}

	h = newHarness(t)
	h.write("My App/Dockerfile", "FROM alpine\n")
	h.mustRun(0, "convert", "--dockerfile", "My App/Dockerfile")
	if h.wrote[0].Services[0].Name != "my-app" {
		t.Errorf("default name: %+v", h.wrote[0].Services)
	}

	h = newHarness(t)
	h.write("my_app/Dockerfile", "FROM alpine\n")
	h.mustRun(0, "convert", "--dockerfile", "my_app/Dockerfile")
	if h.wrote[0].Services[0].Name != "my_app" {
		t.Errorf("a name with an underscore: %+v", h.wrote[0].Services)
	}
}

func TestOutputAndNextSteps(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n")
	h.dock.have["w"] = true
	h.mustRun(0, "convert")
	composePath := filepath.Join(h.dir, "compose.yaml")
	// the project is named after the folder of the compose file
	pkg := strings.ToLower(filepath.Base(h.dir)) + "_web"
	if got := h.wrote[0].Services[0].PackageName; got != pkg {
		t.Fatalf("package name %q, want %q", got, pkg)
	}
	want := gen.Render(h.wrote[0].Report) + "\nWritten:\n  /w/" + pkg + "\n  " + composePath + "\n\nNext steps:\n  OPS_HOME=/home/u ops pkg get eyberg/ops-dns:0.0.1    (once, if you do not have it yet)\n  sudo env OPS_HOME=/home/u PATH=\"$PATH\" ops compose up -f " + composePath + "\n"
	if h.stdout.String() != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", h.stdout, want)
	}
	if !reflect.DeepEqual(h.wopts, []output.Options{{OpsHome: "/home/u", ComposePath: composePath}}) {
		t.Errorf("write options: %+v", h.wopts)
	}

	h = newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n")
	h.dock.have["w"] = true
	h.env["OPS_HOME"] = "/from/env"
	h.mustRun(0, "convert")
	if h.wopts[0].OpsHome != "/from/env" || !strings.Contains(h.stdout.String(), "  sudo env OPS_HOME=/from/env PATH=\"$PATH\" ops compose up -f ") {
		t.Errorf("write options %+v, stdout:\n%s", h.wopts, h.stdout)
	}

	h = newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n")
	h.dock.have["w"] = true
	h.mustRun(0, "convert", "-o", "/abs/dir", "--ops-home", "/my ops")
	if !strings.Contains(h.stdout.String(), "  sudo env OPS_HOME='/my ops' PATH=\"$PATH\" ops compose up -f /abs/dir/compose.yaml\n") || !strings.Contains(h.stdout.String(), "  OPS_HOME='/my ops' ops pkg get eyberg/ops-dns:0.0.1") {
		t.Errorf("stdout:\n%s", h.stdout)
	}
	if h.wopts[0].ComposePath != "/abs/dir/compose.yaml" || h.wopts[0].OpsHome != "/my ops" {
		t.Errorf("write options: %+v", h.wopts)
	}
}

func TestOpsHomeIsAbsolute(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n")
	h.dock.have["w"] = true
	h.mustRun(0, "convert", "--ops-home", "rel/home")
	abs := filepath.Join(h.dir, "rel", "home")
	if h.wopts[0].OpsHome != abs {
		t.Errorf("OpsHome = %q, want %q", h.wopts[0].OpsHome, abs)
	}
	if !strings.Contains(h.stdout.String(), "  OPS_HOME="+abs+" ops pkg get ") || !strings.Contains(h.stdout.String(), "  sudo env OPS_HOME="+abs+" PATH=\"$PATH\" ops compose up -f ") {
		t.Errorf("stdout:\n%s", h.stdout)
	}

	h = newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n")
	h.dock.have["w"] = true
	h.env["OPS_HOME"] = "from/env"
	h.mustRun(0, "convert")
	if want := filepath.Join(h.dir, "from", "env"); h.wopts[0].OpsHome != want {
		t.Errorf("OpsHome = %q, want %q", h.wopts[0].OpsHome, want)
	}
}

func TestRelativeCurrentDirectoryIsRefused(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n")
	h.app.Getwd = func() (string, error) { return "rel/dir", nil }
	h.mustRun(1, "convert")
	if !strings.Contains(h.stderr.String(), "not an absolute path") || len(h.observed) != 0 {
		t.Errorf("stderr %q, observed %d", h.stderr, len(h.observed))
	}
}

func TestBindWithRelativeTargetIsNotPassed(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n    volumes:\n      - type: bind\n        source: ./ok\n        target: rel/target\n")
	if err := os.MkdirAll(filepath.Join(h.dir, "ok"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.dock.have["w"] = true
	h.mustRun(0, "convert")
	if len(h.observed[0].Binds) != 0 || !strings.Contains(h.stderr.String(), "needs absolute paths") {
		t.Errorf("binds %v, stderr %q", h.observed[0].Binds, h.stderr)
	}
}

func TestPackageArch(t *testing.T) {
	ok := []struct{ arch, variant, want string }{
		{"amd64", "", "amd64"}, {"x86_64", "", "amd64"}, {"arm64", "", "arm64"}, {"arm64", "v8", "arm64"}, {"aarch64", "", "arm64"},
	}
	for _, tt := range ok {
		if got, err := packageArch(tt.arch, tt.variant); err != nil || got != tt.want {
			t.Errorf("packageArch(%q, %q) = %q, %v; want %q", tt.arch, tt.variant, got, err, tt.want)
		}
	}
	for _, tt := range []struct{ arch, variant string }{{"arm", "v7"}, {"arm", ""}, {"386", ""}, {"riscv64", ""}, {"s390x", ""}} {
		if _, err := packageArch(tt.arch, tt.variant); err == nil || !strings.Contains(err.Error(), tt.arch) {
			t.Errorf("packageArch(%q, %q): err = %v", tt.arch, tt.variant, err)
		}
	}
}

func TestUnsupportedImageArchitectureIsAnError(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n")
	h.dock.have["w"] = true
	h.dock.images["w"] = &docker.ImageConfig{Architecture: "arm", Variant: "v7"}
	h.mustRun(1, "convert")
	if !strings.Contains(h.stderr.String(), "arm/v7") || len(h.wrote) != 0 {
		t.Errorf("stderr %q", h.stderr)
	}
}

func TestWriteErrorPrintsReportThenError(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n")
	h.dock.have["w"] = true
	var report string
	h.app.Write = func(p *plan.Plan, o output.Options) ([]string, error) {
		report = gen.Render(p.Report)
		return nil, errors.New("exists")
	}
	h.mustRun(1, "convert")
	if report == "" || h.stdout.String() != report || !strings.Contains(h.stderr.String(), "exists") {
		t.Errorf("stdout %q, stderr %q", h.stdout, h.stderr)
	}
	if strings.Contains(h.stdout.String(), "Written") {
		t.Errorf("stdout: %q", h.stdout)
	}
	if len(h.removed) != 1 {
		t.Errorf("removed: %v", h.removed)
	}
}

func TestGenerateErrorPrintsReportThenError(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n")
	h.dock.have["w"] = true
	rep := []plan.Item{{Service: "web", Kind: plan.Unsupported, Key: "service", Message: "gone"}}
	h.app.Generate = func(in gen.Input) (*plan.Plan, error) {
		return &plan.Plan{Report: rep}, errors.New("no service could be converted")
	}
	h.mustRun(1, "convert")
	if h.stdout.String() != gen.Render(rep) || !strings.Contains(h.stderr.String(), "no service could be converted") || len(h.wrote) != 0 {
		t.Errorf("stdout %q, stderr %q", h.stdout, h.stderr)
	}
}

func TestArch(t *testing.T) {
	file := "services:\n  a:\n    image: a\n  b:\n    image: b\n"
	h := newHarness(t)
	h.write("compose.yaml", file)
	h.dock.have["a"], h.dock.have["b"] = true, true
	h.dock.images["a"] = &docker.ImageConfig{Architecture: "arm64"}
	h.dock.images["b"] = &docker.ImageConfig{Architecture: "arm64"}
	h.mustRun(0, "convert")
	if h.genIn[0].Arch != "arm64" {
		t.Errorf("arch %q", h.genIn[0].Arch)
	}

	h = newHarness(t)
	h.write("compose.yaml", file)
	h.dock.have["a"], h.dock.have["b"] = true, true
	h.dock.images["a"] = &docker.ImageConfig{Architecture: "arm64"}
	h.dock.images["b"] = &docker.ImageConfig{Architecture: "amd64"}
	h.mustRun(1, "convert")
	for _, w := range []string{"a (arm64)", "b (amd64)"} {
		if !strings.Contains(h.stderr.String(), w) {
			t.Errorf("stderr %q does not contain %q", h.stderr, w)
		}
	}
	if len(h.wrote) != 0 {
		t.Error("a result was written")
	}
}

func TestShellQuote(t *testing.T) {
	rows := [][2]string{
		{"/a/b-c_d.e", "/a/b-c_d.e"},
		{"/my ops", "'/my ops'"},
		{"it's", `'it'\''s'`},
		{"'", `''\'''`},
		{"", "''"},
		{"a:b=c+d,e@f%g", "a:b=c+d,e@f%g"},
		{"$HOME", "'$HOME'"},
		{"a;b", "'a;b'"},
		{"~/x", "'~/x'"},
	}
	for _, r := range rows {
		if got := shellQuote(r[0]); got != r[1] {
			t.Errorf("%q: got %q, want %q", r[0], got, r[1])
		}
	}
}

func TestRemoteBuildContextIsRefused(t *testing.T) {
	for _, ctx := range []string{"git@github.com:example/repo.git", "https://example.com/repo.git", "git://example.com/repo.git"} {
		h := newHarness(t)
		h.write("compose.yaml", "services:\n  web:\n    build:\n      context: \""+ctx+"\"\n")
		h.mustRun(1, "convert")
		want := "service web: the build context \"" + ctx + "\" is not a local directory"
		if !strings.Contains(h.stderr.String(), want) {
			t.Errorf("%s: stderr %q does not contain %q", ctx, h.stderr, want)
		}
		if len(h.dock.builds) != 0 || len(h.observed) != 0 || len(h.wrote) != 0 {
			t.Errorf("%s: something ran: builds %v, observed %v", ctx, h.dock.builds, h.names())
		}
	}
	// the short form of build is a context too
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    build: \"https://example.com/repo.git\"\n")
	h.mustRun(1, "convert")
	if !strings.Contains(h.stderr.String(), "is not a local directory") || len(h.dock.builds) != 0 {
		t.Errorf("short form: stderr %q, builds %v", h.stderr, h.dock.builds)
	}
}

func TestOnlyBindVolumesWithASourceReachTheObservation(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", `services:
  web:
    image: w
    volumes:
      - db:/var/lib/db
      - type: volume
        source: cache
        target: /cache
      - /anonymous
      - type: tmpfs
        target: /run
      - type: bind
        target: /nosource
      - ./real:/real
`)
	// a directory with the name of each named volume, and the one of the bind
	for _, d := range []string{"db", "cache", "real"} {
		if err := os.MkdirAll(filepath.Join(h.dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.dock.have["w"] = true
	h.mustRun(0, "convert")
	if len(h.observed[0].Binds) != 1 {
		t.Fatalf("binds %v", h.observed[0].Binds)
	}
	h.wantCopy(h.observed[0].Binds[0], filepath.Join(h.dir, "real"), "/real")
}

func TestDependsOnMissingServiceIsAnError(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n    depends_on: [ghost]\n")
	h.dock.have["w"] = true
	h.mustRun(1, "convert")
	want := `service "web" depends_on "ghost", which is not a service of the file`
	if !strings.Contains(h.stderr.String(), want) {
		t.Errorf("stderr %q does not contain %q", h.stderr, want)
	}
	if strings.Contains(h.stderr.String(), "cycle") {
		t.Errorf("a missing service was reported as a cycle: %q", h.stderr)
	}
	if len(h.dock.calls) != 0 || len(h.observed) != 0 || len(h.wrote) != 0 {
		t.Errorf("something ran: %v", h.dock.calls)
	}
}

func TestSysrootOfAnObservationThatFailedIsRemoved(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n")
	h.dock.have["w"] = true
	h.app.Observe = func(ctx context.Context, o observe.Options) (*observe.Result, error) {
		return &observe.Result{Sysroot: "/sysroots/partial"}, errors.New("boom")
	}
	h.mustRun(1, "convert")
	if !strings.Contains(h.stderr.String(), "service web: boom") {
		t.Errorf("stderr: %q", h.stderr)
	}
	if !reflect.DeepEqual(h.removed, []string{"/sysroots/partial"}) {
		t.Errorf("sysroots removed: %v, want the one of the failed observation", h.removed)
	}
	if len(h.genIn) != 0 || len(h.wrote) != 0 {
		t.Errorf("a result was produced: gen %d, write %d", len(h.genIn), len(h.wrote))
	}

	// a failed observation with no sysroot removes nothing
	h = newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n")
	h.dock.have["w"] = true
	h.app.Observe = func(ctx context.Context, o observe.Options) (*observe.Result, error) {
		return &observe.Result{}, errors.New("boom")
	}
	h.mustRun(1, "convert")
	if len(h.removed) != 0 {
		t.Errorf("sysroots removed: %v, want none", h.removed)
	}
}

func TestDockerfileDefaultNameIsAppWhenTheFolderCleansToNothing(t *testing.T) {
	for _, folder := range []string{"---", "___", "...", "-._"} {
		h := newHarness(t)
		h.write(folder+"/Dockerfile", "FROM alpine\n")
		h.mustRun(0, "convert", "--dockerfile", folder+"/Dockerfile")
		if len(h.wrote) != 1 || h.wrote[0].Services[0].Name != "app" {
			t.Errorf("folder %q: services %+v, want the name app", folder, h.wrote)
		}
		if h.genIn[0].Project.Name != "app" {
			t.Errorf("folder %q: project %q, want app", folder, h.genIn[0].Project.Name)
		}
	}
}

func TestEmptyOutputFlagIsAnError(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n")
	h.dock.have["w"] = true
	h.mustRun(1, "convert", "-o", "")
	if !strings.Contains(h.stderr.String(), ToolName+": -o is empty") {
		t.Errorf("stderr: %q", h.stderr)
	}
	if len(h.dock.calls) != 0 || len(h.observed) != 0 || len(h.wrote) != 0 {
		t.Errorf("something ran: %v", h.dock.calls)
	}
}

const depsCompose = `services:
  cache:
    image: c
  db:
    image: d
    environment:
      DB_PASSWORD: s3cret-value
    command: ["mysqld", "--port=3306"]
    working_dir: /var/lib/db
    user: "999"
    depends_on: [cache]
  web:
    image: w
    depends_on: [db]
  api:
    image: a
    depends_on: [db]
`

func (h *harness) haveAll(images ...string) {
	for _, i := range images {
		h.dock.have[i] = true
	}
}

func TestNetworkCreatedOnceAndRemoved(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  one:\n    image: a\n  two:\n    image: b\n")
	h.haveAll("a", "b")
	h.mustRun(0, "convert")
	if len(h.dock.netCreated) != 1 || !netName.MatchString(h.dock.netCreated[0]) {
		t.Fatalf("networks created: %v", h.dock.netCreated)
	}
	name := h.dock.netCreated[0]
	if !reflect.DeepEqual(h.dock.netRemoved, []string{name}) {
		t.Errorf("networks removed: %v", h.dock.netRemoved)
	}
	if want := []string{"net create", "observe a", "observe b", "net rm"}; !reflect.DeepEqual(h.events, want) {
		t.Errorf("events %v, want %v", h.events, want)
	}
	// a service without depends_on gets the network and its own name as an alias
	for i, svc := range []string{"one", "two"} {
		if h.observed[i].Network != name || !reflect.DeepEqual(h.observed[i].NetworkAliases, []string{svc}) {
			t.Errorf("%s: network %q aliases %v", svc, h.observed[i].Network, h.observed[i].NetworkAliases)
		}
	}
}

func TestNoNetworkWhenNothingIsConverted(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  bare:\n    command: x\n")
	// no service can be converted: the report is printed and the status is 1
	h.mustRun(1, "convert")
	if len(h.dock.netCreated) != 0 || len(h.dock.netRemoved) != 0 {
		t.Errorf("network calls: %v %v", h.dock.netCreated, h.dock.netRemoved)
	}
}

func TestNetworkCreateFailureStopsTheRun(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  one:\n    image: a\n")
	h.haveAll("a")
	h.dock.netErr = errors.New("no docker")
	h.mustRun(1, "convert")
	if !strings.Contains(h.stderr.String(), "no docker") || len(h.observed) != 0 {
		t.Errorf("stderr %q, observed %v, removed %v", h.stderr, h.names(), h.dock.netRemoved)
	}
}

func TestDependenciesStartedOnceInOrderAndStoppedAtTheEnd(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", depsCompose)
	h.haveAll("c", "d", "w", "a")
	h.mustRun(0, "convert")
	want := []string{
		"net create", "observe c", "start c", "observe d", "start d", "observe w", "observe a",
		"stop d", "stop c", "net rm",
	}
	if !reflect.DeepEqual(h.events, want) {
		t.Fatalf("events %v\nwant   %v", h.events, want)
	}
	net := h.dock.netCreated[0]
	// cache has no dependency of its own and db is observed with cache running
	if len(h.started) != 2 || h.started[0].Image != "c" || h.started[1].Image != "d" {
		t.Fatalf("started: %+v", h.started)
	}
	for i, svc := range []string{"cache", "db"} {
		so := h.started[i]
		if so.Network != net || !reflect.DeepEqual(so.NetworkAliases, []string{svc}) {
			t.Errorf("%s: network %q aliases %v", svc, so.Network, so.NetworkAliases)
		}
	}
	// the options of the start are those of the observation of the same service
	db := h.observed[1]
	so := h.started[1]
	if db.Image != "d" || !reflect.DeepEqual(db.Entrypoint, so.Entrypoint) || so.Entrypoint == nil || !reflect.DeepEqual(db.Cmd, so.Cmd) ||
		!reflect.DeepEqual(db.Env, so.Env) || db.WorkingDir != so.WorkingDir || db.User != so.User || !reflect.DeepEqual(db.Binds, so.Binds) {
		t.Errorf("observation %+v\nstart       %+v", db, so)
	}
	if !reflect.DeepEqual(so.Cmd, []string{"mysqld", "--port=3306"}) || !reflect.DeepEqual(so.Env, []string{"DB_PASSWORD=s3cret-value"}) || so.WorkingDir != "/var/lib/db" || so.User != "999" {
		t.Errorf("start options %+v", so)
	}
	// web is observed with the network and under its own name
	web := h.observed[2]
	if web.Network != net || !reflect.DeepEqual(web.NetworkAliases, []string{"web"}) {
		t.Errorf("web: network %q aliases %v", web.Network, web.NetworkAliases)
	}
	if strings.Contains(h.stderr.String(), "s3cret-value") {
		t.Errorf("a secret was printed: %q", h.stderr)
	}
	if strings.Contains(h.stdout.String(), "was not started") {
		t.Errorf("the report has a note about a dependency:\n%s", h.stdout)
	}
}

func TestDependencyStartFailureIsANote(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", depsCompose)
	h.haveAll("c", "d", "w", "a")
	h.startErr["d"] = errors.New("exited at once")
	h.mustRun(0, "convert")
	if !reflect.DeepEqual(h.names(), []string{"c", "d", "w", "a"}) {
		t.Fatalf("observed %v", h.names())
	}
	// not tried again for the second dependent
	n := 0
	for _, s := range h.started {
		if s.Image == "d" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("db was started %d times", n)
	}
	for _, svc := range []string{"web", "api"} {
		warns := h.genIn[0].Services[svc].Observed.Warnings
		if len(warns) != 1 || !strings.Contains(warns[0], "the service db was not started") || !strings.Contains(warns[0], "exited at once") || !strings.Contains(warns[0], svc+" was observed without it") {
			t.Errorf("%s: warnings %q", svc, warns)
		}
	}
	if !strings.Contains(h.stdout.String(), "the service db was not started") {
		t.Errorf("the report has no note:\n%s", h.stdout)
	}
	if !strings.Contains(h.stderr.String(), "web: warning: the service db was not started") {
		t.Errorf("no warning on stderr: %q", h.stderr)
	}
	if want := []string{"stop c", "net rm"}; !reflect.DeepEqual(h.events[len(h.events)-2:], want) {
		t.Errorf("events %v", h.events)
	}
}

func TestDependencyNotConvertedIsANote(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  db:\n    image: d\n    profiles: [debug]\n  web:\n    image: w\n    depends_on: [db]\n")
	h.haveAll("w")
	h.mustRun(0, "convert")
	if len(h.started) != 0 || !reflect.DeepEqual(h.names(), []string{"w"}) {
		t.Fatalf("started %v, observed %v", h.started, h.names())
	}
	warns := h.genIn[0].Services["web"].Observed.Warnings
	if len(warns) != 1 || !strings.Contains(warns[0], "the service db was not started") || !strings.Contains(warns[0], "has profiles (debug)") {
		t.Errorf("warnings %q", warns)
	}
}

func TestNetworkAndDependenciesAreCleanedUpOnError(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", depsCompose)
	h.haveAll("c", "d", "w", "a")
	h.obsErr["w"] = errors.New("boom")
	h.mustRun(1, "convert")
	if want := []string{"stop d", "stop c", "net rm"}; !reflect.DeepEqual(h.events[len(h.events)-3:], want) {
		t.Errorf("events %v", h.events)
	}
	if len(h.dock.netRemoved) != 1 {
		t.Errorf("networks removed: %v", h.dock.netRemoved)
	}
}

func TestNetworkAndDependenciesAreCleanedUpOnCancel(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", depsCompose)
	h.haveAll("c", "d", "w", "a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.onObserve = func(o observe.Options) error {
		if o.Image == "w" {
			cancel()
			return ctx.Err()
		}
		return nil
	}
	if code := h.app.Run(ctx, []string{"convert"}); code != 1 {
		t.Fatalf("exit status %d", code)
	}
	if want := []string{"stop d", "stop c", "net rm"}; !reflect.DeepEqual(h.events[len(h.events)-3:], want) {
		t.Errorf("events %v", h.events)
	}
	// the network is removed with a context that is not the cancelled one
	if len(h.dock.removeCtxErr) != 1 || h.dock.removeCtxErr[0] != nil {
		t.Errorf("context of the removal: %v", h.dock.removeCtxErr)
	}
}

func TestCancelWhileStartingADependency(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", depsCompose)
	h.haveAll("c", "d", "w", "a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.app.Start = func(c context.Context, o observe.StartOptions) (Stopper, error) {
		if o.Image == "d" {
			cancel()
			return nil, c.Err()
		}
		h.events = append(h.events, "start "+o.Image)
		return &fakeStopper{h: h, name: o.Image}, nil
	}
	if code := h.app.Run(ctx, []string{"convert"}); code != 1 {
		t.Fatalf("exit status %d", code)
	}
	// the start that was cut short is an error of the run, not a note
	if len(h.observed) != 2 || strings.Contains(h.stderr.String(), "was not started") {
		t.Errorf("observed %v, stderr %q", h.names(), h.stderr)
	}
	if want := []string{"stop c", "net rm"}; !reflect.DeepEqual(h.events[len(h.events)-2:], want) {
		t.Errorf("events %v", h.events)
	}
}

func TestClosureHoldsTheDependenciesOfTheDependencies(t *testing.T) {
	dep := func(names ...string) []compose.Dependency {
		var out []compose.Dependency
		for _, n := range names {
			out = append(out, compose.Dependency{Service: n})
		}
		return out
	}
	svc := func(name string, deps ...string) *compose.Service {
		return &compose.Service{Name: name, DependsOn: dep(deps...)}
	}
	// web needs api, api needs db, db needs cache; side is not needed by web
	rs := &runState{ordered: []*compose.Service{svc("cache"), svc("side"), svc("db", "cache"), svc("api", "db"), svc("web", "api")}}
	web := rs.ordered[4]
	if got, want := rs.closure(web), []string{"cache", "db", "api"}; !reflect.DeepEqual(got, want) {
		t.Errorf("closure of web = %q, want %q", got, want)
	}
	if got, want := rs.closure(rs.ordered[3]), []string{"cache", "db"}; !reflect.DeepEqual(got, want) {
		t.Errorf("closure of api = %q, want %q", got, want)
	}
	if got := rs.closure(rs.ordered[0]); len(got) != 0 {
		t.Errorf("closure of cache = %q, want none", got)
	}
}

func TestNetworkRemoveFailureIsAWarning(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  one:\n    image: a\n")
	h.haveAll("a")
	h.dock.netRemoveErr = errors.New("network has active endpoints")
	h.mustRun(0, "convert")
	want := ToolName + ": warning: the docker network " + h.dock.netCreated[0] + " is not removed: network has active endpoints\n"
	if !strings.Contains(h.stderr.String(), want) {
		t.Errorf("stderr %q does not contain %q", h.stderr, want)
	}
}

func TestStopFailureOfADependencyIsAWarning(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", depsCompose)
	h.haveAll("c", "d", "w", "a")
	h.stopErr["d"] = errors.New("container is busy")
	h.mustRun(0, "convert")
	want := ToolName + ": warning: stopping db: container is busy\n"
	if !strings.Contains(h.stderr.String(), want) {
		t.Errorf("stderr %q does not contain %q", h.stderr, want)
	}
	// the failure of one stop does not keep the others from running
	if want := []string{"stop d", "stop c", "net rm"}; !reflect.DeepEqual(h.events[len(h.events)-3:], want) {
		t.Errorf("events %v", h.events)
	}
}

func TestStartingADependencyIsAnnounced(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", depsCompose)
	h.haveAll("c", "d", "w", "a")
	h.mustRun(0, "convert")
	out := h.stderr.String()
	for _, want := range []string{"cache: starting c for db\n", "db: starting d for web\n"} {
		if strings.Count(out, want) != 1 {
			t.Errorf("stderr does not hold %q exactly once: %q", want, out)
		}
	}
	// db is running by the time api is observed: it is not started again
	if strings.Contains(out, "for api") {
		t.Errorf("a dependency was started for api: %q", out)
	}
}

func TestNotesOfAStartedDependencyReachTheReport(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", depsCompose)
	h.haveAll("c", "d", "w", "a")
	h.startNotes["d"] = []string{"the container was running but listens on no port after 60 seconds"}
	h.mustRun(0, "convert")
	for _, svc := range []string{"web", "api"} {
		warns := h.genIn[0].Services[svc].Observed.Warnings
		want := "the service db was started for the observation of " + svc + ": the container was running but listens on no port after 60 seconds"
		if len(warns) != 1 || warns[0] != want {
			t.Errorf("%s: warnings %q, want %q", svc, warns, want)
		}
	}
}

func TestDeclaredPorts(t *testing.T) {
	s := &compose.Service{
		Ports:  []compose.Port{{Target: "8080", Protocol: "tcp"}, {Target: "53", Protocol: "udp"}, {Target: "9000-9010"}, {Target: "3000"}},
		Expose: []string{"5432", "6000/udp", "7000/tcp", "x"},
	}
	equal := reflect.DeepEqual
	if got, want := declaredPorts(s), []int{8080, 3000, 5432, 7000}; !equal(got, want) {
		t.Errorf("declaredPorts = %v, want %v", got, want)
	}
}

func TestPortsOfTheFileReachTheStartOfADependency(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  db:\n    image: d\n    expose: ['5432']\n    ports: ['3307:3306']\n  web:\n    image: w\n    depends_on: [db]\n")
	h.haveAll("d", "w")
	h.mustRun(0, "convert")
	if len(h.started) != 1 || !reflect.DeepEqual(h.started[0].Ports, []int{3306, 5432}) {
		t.Errorf("started: %+v", h.started)
	}
}

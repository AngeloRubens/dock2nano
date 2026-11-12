package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/compose"
	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/gen"
	"github.com/AngeloRubens/dock2nano/internal/hub"
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

// Up says whether the container still runs; a test stops one with h.crashed.
func (s *fakeStopper) Up(ctx context.Context) bool { return !s.h.crashed[s.name] }

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
	// crashed makes the started container of an image look stopped after an observation.
	crashed map[string]bool
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
	// the bind with a source, and the anonymous volume as a bare path
	if len(h.observed[0].Binds) != 2 || h.observed[0].Binds[1] != "/anonymous" {
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
		// the other line says that cache ran during the observation
		if len(warns) != 2 || !strings.Contains(warns[1], "the services running during this observation: cache") {
			t.Errorf("%s: warnings %q", svc, warns)
			continue
		}
		if !strings.Contains(warns[0], "the service db was not started") || !strings.Contains(warns[0], "exited at once") || !strings.Contains(warns[0], svc+" was observed without it") {
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
		if len(warns) != 2 || warns[0] != want || !strings.Contains(warns[1], "the services running during this observation: cache, db") {
			t.Errorf("%s: warnings %q, want %q and the list of the services that ran", svc, warns, want)
		}
	}
}

func TestDeclaredPorts(t *testing.T) {
	s := &compose.Service{
		Ports:  []compose.Port{{Target: "8080", Protocol: "tcp"}, {Target: "53", Protocol: "udp"}, {Target: "9000-9010"}, {Target: "3000"}, {Target: "0"}, {Target: "70000"}},
		Expose: []string{"5432", "6000/udp", "7000/tcp", "x", "8888/TCP"},
	}
	equal := reflect.DeepEqual
	if got, want := declaredPorts(s), []int{8080, 3000, 5432, 7000, 8888}; !equal(got, want) {
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

func TestADependencyReportsThatItsDependentsWroteIntoIt(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", depsCompose)
	h.haveAll("c", "d", "w", "a")
	h.mustRun(0, "convert")
	var got []string
	for _, w := range h.genIn[0].Services["db"].Observed.Warnings {
		if strings.Contains(w, "was observed while this service was running") {
			got = append(got, w)
		}
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], "web, api was observed while this service was running") {
		t.Errorf("warnings %q", got)
	}
	// a service nobody depends on has no such warning
	for _, w := range h.genIn[0].Services["web"].Observed.Warnings {
		if strings.Contains(w, "was observed while this service was running") {
			t.Errorf("web: %q", w)
		}
	}
}

func TestHiddenVolumesAreTheOnesDockerFillsFromTheImage(t *testing.T) {
	s := &compose.Service{Volumes: []compose.Volume{
		{Type: "bind", Source: "./backend", Target: "/usr/src/app"},
		{Type: "volume", Target: "/usr/src/app/node_modules"},
		{Type: "volume", Source: "modules", Target: "/usr/src/app/vendor/"},
		{Type: "volume", Source: "data", Target: "/var/lib/db"},
		{Type: "volume", Target: "/cache"},
		{Type: "volume", Target: "relative"},
		{Type: "tmpfs", Target: "/run"},
	}}
	got := hiddenVolumes(s)
	want := []string{"/usr/src/app/node_modules", "/usr/src/app/vendor", "/cache"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hiddenVolumes = %v, want %v", got, want)
	}
}

func TestExerciseForPicksTheValuesOfTheService(t *testing.T) {
	p := &compose.Project{Services: []*compose.Service{{Name: "web"}, {Name: "db"}}}
	values := []string{"/all", "web=/only-web", "db=/only-db", "http://container:9000/x", "FOO=1 run", "/p?a=b"}
	got := exerciseFor("web", values, p)
	want := []string{"/all", "/only-web", "http://container:9000/x", "FOO=1 run", "/p?a=b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("web: %v, want %v", got, want)
	}
	got = exerciseFor("db", values, p)
	want = []string{"/all", "/only-db", "http://container:9000/x", "FOO=1 run", "/p?a=b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("db: %v, want %v", got, want)
	}
}

func TestExerciseFlagsReachTheObservation(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n")
	h.dock.have["w"] = true
	h.mustRun(0, "convert", "--exercise", "/health", "--exercise", "web=/x", "--exercise-cmd", "web=curl $DOCK2NANO_HOST")
	if len(h.observed) != 1 {
		t.Fatalf("observed %d", len(h.observed))
	}
	o := h.observed[0]
	if !reflect.DeepEqual(o.Exercise, []string{"/health", "/x"}) || o.ExerciseCmd != "curl $DOCK2NANO_HOST" {
		t.Errorf("exercise %v, cmd %q", o.Exercise, o.ExerciseCmd)
	}
}

func TestKeepTraceNamesAFilePerService(t *testing.T) {
	for _, tc := range []struct {
		name    string
		set     bool
		dir     string
		service string
		want    string
	}{
		{"set", true, "/keep", "web", "/keep/web.strace"},
		{"unset", false, "", "web", ""},
		{"empty", true, "", "web", ""},
		{"path in the service name", true, "/keep", "a/../../x", "/keep/a-x.strace"},
		{"upper case", true, "/keep", "Web", "/keep/web.strace"},
	} {
		h := newHarness(t)
		h.write("compose.yaml", "services:\n  \""+tc.service+"\":\n    image: w\n")
		h.dock.have["w"] = true
		if tc.set {
			h.env["DOCK2NANO_KEEP_TRACE"] = tc.dir
		}
		h.mustRun(0, "convert")
		if len(h.observed) != 1 {
			t.Fatalf("%s: observed %d", tc.name, len(h.observed))
		}
		if got := h.observed[0].KeepTrace; got != tc.want {
			t.Errorf("%s: KeepTrace %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestApplyWithAddsTheDependencyExceptWhereItIsACycle(t *testing.T) {
	p := &compose.Project{Services: []*compose.Service{
		{Name: "backend"},
		{Name: "db", DependsOn: []compose.Dependency{{Service: "volume-init"}}},
		{Name: "volume-init"},
		{Name: "worker", DependsOn: []compose.Dependency{{Service: "db"}}},
	}}
	out, err := applyWith(p, []string{"db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	deps := func(name string) []string {
		var r []string
		for _, d := range p.Service(name).DependsOn {
			r = append(r, d.Service)
		}
		return r
	}
	if got := deps("backend"); !reflect.DeepEqual(got, []string{"db"}) {
		t.Errorf("backend deps = %v", got)
	}
	if got := deps("db"); !reflect.DeepEqual(got, []string{"volume-init"}) {
		t.Errorf("db deps = %v (the service itself)", got)
	}
	if got := deps("volume-init"); len(got) != 0 {
		t.Errorf("volume-init deps = %v (db needs it: that would be a cycle)", got)
	}
	if got := deps("worker"); !reflect.DeepEqual(got, []string{"db"}) {
		t.Errorf("worker deps = %v (already there)", got)
	}
	if !reflect.DeepEqual(out, []string{"backend: db is started with it for the observation (--with db)"}) {
		t.Errorf("lines = %q", out)
	}
	if _, err := dependencyOrder(p); err != nil {
		t.Errorf("order: %v", err)
	}
}

func TestApplyWithRefusesWhatIsNotThere(t *testing.T) {
	p := &compose.Project{Services: []*compose.Service{{Name: "a"}, {Name: "b"}}}
	if _, err := applyWith(p, []string{"nope"}, nil); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("unknown: %v", err)
	}
	if _, err := applyWith(p, []string{"b"}, map[string]bool{"a": true}); err == nil || !strings.Contains(err.Error(), "--service") {
		t.Errorf("not chosen: %v", err)
	}
}

func TestWithStartsTheServiceForTheObservationOfTheOthers(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  backend:\n    image: b\n  db:\n    image: d\n")
	h.dock.have["b"] = true
	h.dock.have["d"] = true
	h.mustRun(0, "convert", "--with", "db")
	if len(h.started) != 1 || h.started[0].Image != "d" {
		t.Fatalf("started %v, want the image d", h.started)
	}
}

// stoppedWith makes the observation of the images in codes stop early with that status, and
// records the observations in order.
func stoppedWith(h *harness, codes map[string]int) {
	h.app.Observe = func(ctx context.Context, o observe.Options) (*observe.Result, error) {
		h.observed = append(h.observed, o)
		h.events = append(h.events, "observe "+o.Image)
		r := &observe.Result{Sysroot: "/sysroots/" + o.Image, Program: "/usr/bin/app", Argv: []string{"/usr/bin/app"}}
		if c, ok := codes[o.Image]; ok {
			r.StoppedEarly, r.ExitCode = true, c
		}
		return r, nil
	}
}

const noDepsCompose = "services:\n  backend:\n    image: b\n  db:\n    image: d\n"

func hasWarning(h *harness, svc, part string) bool {
	for _, w := range h.genIn[len(h.genIn)-1].Services[svc].Observed.Warnings {
		if strings.Contains(w, part) {
			return true
		}
	}
	return false
}

func TestDependsOnServicesRunDuringTheObservationAndTheReportNamesThem(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  backend:\n    image: b\n    depends_on: [db]\n  db:\n    image: d\n")
	h.haveAll("b", "d")
	h.mustRun(0, "convert")
	if !reflect.DeepEqual(h.events, []string{"net create", "observe d", "start d", "observe b", "stop d", "net rm"}) {
		t.Fatalf("events %v", h.events)
	}
	if !hasWarning(h, "backend", "the services running during this observation: db") {
		t.Errorf("warnings %q", h.genIn[0].Services["backend"].Observed.Warnings)
	}
}

func TestNoWithStartsNothing(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  backend:\n    image: b\n    depends_on: [db]\n  db:\n    image: d\n")
	h.haveAll("b", "d")
	h.mustRun(0, "convert", "--no-with")
	if len(h.started) != 0 {
		t.Fatalf("started %v", h.started)
	}
	if !hasWarning(h, "backend", "were not started for the observation of backend (--no-with)") {
		t.Errorf("warnings %q", h.genIn[0].Services["backend"].Observed.Warnings)
	}
}

func TestNoWithAndWithAreExclusive(t *testing.T) {
	h := newHarness(t)
	if _, err := h.app.parseConvert([]string{"--no-with", "--with", "db"}); err == nil {
		t.Fatal("no error")
	}
	o, err := h.app.parseConvert([]string{"--no-with"})
	if err != nil || !o.noWith {
		t.Fatalf("%+v %v", o, err)
	}
}

// failsUnless makes the observation of image stop early with status 1 until the container of
// need was started; the results are numbered by the call so that a test can tell the sysroots.
func failsUnless(h *harness, image, need string) {
	h.app.Observe = func(ctx context.Context, o observe.Options) (*observe.Result, error) {
		h.observed = append(h.observed, o)
		h.events = append(h.events, "observe "+o.Image)
		r := &observe.Result{Sysroot: "/sysroots/" + o.Image + strconv.Itoa(len(h.observed)), Program: "/usr/bin/app", Argv: []string{"/usr/bin/app"}}
		up := false
		for _, e := range h.events {
			up = up || e == "start "+need
		}
		if o.Image == image && !up {
			r.StoppedEarly, r.ExitCode = true, 1
		}
		return r, nil
	}
}

func suggestion(h *harness, svc string) []plan.Item {
	var out []plan.Item
	for _, it := range h.genIn[len(h.genIn)-1].Services[svc].HubItems {
		if it.Key == "depends_on" {
			out = append(out, it)
		}
	}
	return out
}

func TestEarlyErrorWithNoDependencyRunningIsObservedAgainWithTheOthers(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", noDepsCompose)
	h.haveAll("b", "d")
	failsUnless(h, "b", "d")
	h.mustRun(0, "convert")
	want := []string{"net create", "observe b", "observe d", "start d", "observe b", "stop d", "net rm"}
	if !reflect.DeepEqual(h.events, want) {
		t.Fatalf("events %v\nwant   %v", h.events, want)
	}
	if !hasWarning(h, "backend", "the services started beside it for this second observation: db") {
		t.Errorf("warnings %q", h.genIn[0].Services["backend"].Observed.Warnings)
	}
	if si := h.genIn[0].Services["backend"].Observed; si.StoppedEarly {
		t.Errorf("the second observation is the one kept: %+v", si)
	}
	// the second observation did not stop: the report suggests the depends_on, and the project
	// and the output stay as the file has them
	sug := suggestion(h, "backend")
	if len(sug) != 1 || sug[0].Kind != plan.Note || sug[0].Key != "depends_on" ||
		!strings.Contains(sug[0].Message, "stopped with status 1; with db running it did not stop with an error in 30 seconds; if backend needs it, add it to depends_on of backend in docker-compose.yml") {
		t.Errorf("suggestion %+v", sug)
	}
	if got := h.genIn[0].Project.Service("backend").DependsOn; len(got) != 0 {
		t.Errorf("depends_on of backend was changed: %+v", got)
	}
	if !slices.Contains(h.removed, "/sysroots/b1") {
		t.Errorf("removed %v", h.removed)
	}
}

func TestSecondObservationThatStillStopsGivesNoSuggestion(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", noDepsCompose)
	h.haveAll("b", "d")
	stoppedWith(h, map[string]int{"b": 1})
	h.mustRun(0, "convert")
	if n := len(h.names()); n != 3 {
		t.Fatalf("observed %v, want b d b", h.names())
	}
	if sug := suggestion(h, "backend"); len(sug) != 0 {
		t.Errorf("a suggestion although it still stops: %+v", sug)
	}
	if !hasWarning(h, "backend", "with db running it stopped with status 1 as well") {
		t.Errorf("warnings %q", h.genIn[0].Services["backend"].Observed.Warnings)
	}
}

func TestSecondObservationWithACandidateThatDoesNotStartGivesNoSuggestion(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", noDepsCompose)
	h.haveAll("b", "d")
	h.startErr["d"] = errors.New("exited at once")
	failsUnless(h, "b", "d")
	h.mustRun(0, "convert")
	if sug := suggestion(h, "backend"); len(sug) != 0 {
		t.Errorf("a suggestion with a candidate that never ran: %+v", sug)
	}
	if !hasWarning(h, "backend", "were meant to run beside it for the second observation and did not: db (it did not start: exited at once)") {
		t.Errorf("warnings %q", h.genIn[0].Services["backend"].Observed.Warnings)
	}
}

func TestACandidateThatRanInTheFirstObservationIsNotNewEvidence(t *testing.T) {
	ordered := []*compose.Service{{Name: "db"}, {Name: "backend"}}
	rs := &runState{ordered: ordered, skipped: map[string]string{}, late: map[string]string{}, failed: map[string]string{},
		starts: map[string]observe.StartOptions{"db": {}}, firstUp: map[string][]string{"backend": {"db"}}, retried: map[string]bool{}}
	backend := ordered[1]
	if got := rs.candidates(backend, true); len(got) != 0 {
		t.Errorf("db ran already: %v", got)
	}
	res := &observe.Result{StoppedEarly: true, ExitCode: 1}
	if rs.wantsRetry(backend, res) {
		t.Errorf("a second observation with nothing new")
	}
	rs.firstUp["backend"] = nil
	if got := rs.candidates(backend, true); !reflect.DeepEqual(got, []string{"db"}) {
		t.Errorf("candidates %v", got)
	}
	if !rs.wantsRetry(backend, res) {
		t.Errorf("no second observation although db did not run")
	}
}

func TestAServiceWithDeclaredDependenciesIsNotObservedAgainWithUnrelatedOnes(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  backend:\n    image: b\n    depends_on: [db]\n  db:\n    image: d\n  other:\n    image: o\n")
	h.haveAll("b", "d", "o")
	h.startErr["d"] = errors.New("exited at once")
	stoppedWith(h, map[string]int{"b": 1})
	h.mustRun(0, "convert")
	n := 0
	for _, i := range h.names() {
		if i == "b" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("backend was observed %d times: %v", n, h.names())
	}
}

func TestFailureOfTheSecondObservationKeepsTheFirst(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", noDepsCompose)
	h.haveAll("b", "d")
	calls := 0
	h.app.Observe = func(ctx context.Context, o observe.Options) (*observe.Result, error) {
		h.observed = append(h.observed, o)
		if o.Image == "b" {
			calls++
			if calls == 2 {
				return nil, errors.New("boom")
			}
			return &observe.Result{Sysroot: "/sysroots/b", Program: "/usr/bin/app", StoppedEarly: true, ExitCode: 1}, nil
		}
		return &observe.Result{Sysroot: "/sysroots/d", Program: "/usr/bin/app"}, nil
	}
	h.mustRun(0, "convert")
	obs := h.genIn[0].Services["backend"].Observed
	if obs == nil || !obs.StoppedEarly || obs.ExitCode != 1 {
		t.Fatalf("the first observation was not kept: %+v", obs)
	}
	if !hasWarning(h, "backend", "which failed (boom); this is the first observation, which stopped with status 1") {
		t.Errorf("warnings %q", obs.Warnings)
	}
}

func TestEarlyErrorIsNotObservedAgainWithNoWith(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", noDepsCompose)
	h.haveAll("b", "d")
	stoppedWith(h, map[string]int{"b": 1})
	h.mustRun(0, "convert", "--no-with")
	if len(h.started) != 0 || len(h.observed) != 2 {
		t.Errorf("started %v observed %v", h.started, h.names())
	}
}

// Guard, not a test of new logic: with nobody else in the file there is nothing to run beside it.
func TestEarlyErrorOfAServiceThatNeedsNobodyIsObservedOnce(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  backend:\n    image: b\n")
	h.haveAll("b")
	stoppedWith(h, map[string]int{"b": 1})
	h.mustRun(0, "convert")
	if len(h.observed) != 1 {
		t.Errorf("observed %v", h.names())
	}
}

func TestHubIsNotReadForAStopWhileADependencyIsMissing(t *testing.T) {
	h := newHarness(t)
	stub := &stubHub{}
	h.app.Hub = stub
	h.write("compose.yaml", "services:\n  backend:\n    image: b\n    depends_on: [db]\n  db:\n    image: d\n")
	h.haveAll("b", "d")
	h.dock.images["b"] = &docker.ImageConfig{Architecture: "amd64"}
	h.dock.images["d"] = &docker.ImageConfig{Architecture: "amd64"}
	h.startErr["d"] = errors.New("exited at once")
	stoppedWith(h, map[string]int{"b": 1})
	h.mustRun(0, "convert")
	if len(stub.calls) != 0 {
		t.Errorf("the hub was read: %v", stub.calls)
	}
	if !hasWarning(h, "backend", "status 1 while the services it needs (db) were not running") {
		t.Errorf("warnings %q", h.genIn[0].Services["backend"].Observed.Warnings)
	}
}

func TestStopWithTheDependenciesRunningSaysItMayBeTheirProblemAndKeepsTheHub(t *testing.T) {
	h := newHarness(t)
	stub := &stubHub{}
	h.app.Hub = stub
	h.write("compose.yaml", "services:\n  backend:\n    image: b\n    depends_on: [db]\n  db:\n    image: d\n")
	h.haveAll("b", "d")
	h.dock.images["b"] = &docker.ImageConfig{Architecture: "amd64"}
	h.dock.images["d"] = &docker.ImageConfig{Architecture: "amd64"}
	stoppedWith(h, map[string]int{"b": 3})
	h.mustRun(0, "convert")
	if len(stub.calls) == 0 {
		t.Errorf("the hub was not read for a stop with the dependency running")
	}
	if !hasWarning(h, "backend", "status 3 although the services it needs (db) were running") || !hasWarning(h, "backend", "dependency (credentials, a schema, a name) or of the configuration") {
		t.Errorf("warnings %q", h.genIn[0].Services["backend"].Observed.Warnings)
	}
}

// Guard: children always read the hub, whatever the dependencies did.
func TestHubIsStillReadForChildrenWhileADependencyIsMissing(t *testing.T) {
	h := newHarness(t)
	stub := &stubHub{}
	h.app.Hub = stub
	h.write("compose.yaml", "services:\n  backend:\n    image: b\n    depends_on: [db]\n  db:\n    image: d\n")
	h.haveAll("b", "d")
	h.dock.images["b"] = &docker.ImageConfig{Architecture: "amd64"}
	h.dock.images["d"] = &docker.ImageConfig{Architecture: "amd64"}
	h.startErr["d"] = errors.New("exited at once")
	h.app.Observe = func(ctx context.Context, o observe.Options) (*observe.Result, error) {
		h.observed = append(h.observed, o)
		return &observe.Result{Sysroot: "/sysroots/" + o.Image, Program: "/usr/bin/app", Argv: []string{"/usr/bin/app"}, Children: 2}, nil
	}
	h.mustRun(0, "convert")
	if len(stub.calls) == 0 {
		t.Errorf("the hub was not read for a program that forks")
	}
}

func TestHubTriggerHeld(t *testing.T) {
	// the first assertion is the test of the change; the second is a guard
	if got := hubTrigger(&observe.Result{StoppedEarly: true, ExitCode: 1}, true); got != "" {
		t.Errorf("held stop: %q", got)
	}
	if got := hubTrigger(&observe.Result{StoppedEarly: true, ExitCode: 1, Children: 2}, true); !strings.Contains(got, "2 child processes") {
		t.Errorf("held stop with children: %q", got)
	}
}

func newHubApp(h *harness, images ...string) *stubHub {
	stub := &stubHub{}
	h.app.Hub = stub
	for _, i := range images {
		h.haveAll(i)
		h.dock.images[i] = &docker.ImageConfig{Architecture: "amd64"}
	}
	return stub
}

func countCalls(calls []string, what string) int {
	n := 0
	for _, c := range calls {
		if c == what {
			n++
		}
	}
	return n
}

func TestAContainerStartedForADependentIsStoppedAndTheServiceObservedAgain(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  api:\n    image: a\n  web:\n    image: w\n    depends_on: [api]\n  db:\n    image: d\n")
	h.haveAll("a", "w", "d")
	stoppedWith(h, map[string]int{"a": 1})
	h.mustRun(0, "convert")
	n := 0
	for _, i := range h.names() {
		if i == "a" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("api was observed %d times: %v", n, h.names())
	}
	// its own container, started for web, is stopped before the second observation
	last := -1
	stop := -1
	for i, e := range h.events {
		if e == "observe a" {
			last = i
		}
		if e == "stop a" && stop < 0 {
			stop = i
		}
	}
	if stop < 0 || stop > last {
		t.Errorf("the container of api was not stopped before its second observation: %v", h.events)
	}
	if hasWarning(h, "api", "second observation of another service") {
		t.Errorf("a false warning: %q", h.genIn[0].Services["api"].Observed.Warnings)
	}
	if !hasWarning(h, "web", "it was observed with the container of api started from the file; api was observed once more later") {
		t.Errorf("web: %q", h.genIn[0].Services["web"].Observed.Warnings)
	}
}

// failsUnlessAll makes the observation of each of the images stop early with status 1 until the
// container of need was started.
func failsUnlessAll(h *harness, need string, images ...string) {
	h.app.Observe = func(ctx context.Context, o observe.Options) (*observe.Result, error) {
		h.observed = append(h.observed, o)
		h.events = append(h.events, "observe "+o.Image)
		r := &observe.Result{Sysroot: "/sysroots/" + o.Image + strconv.Itoa(len(h.observed)), Program: "/usr/bin/app", Argv: []string{"/usr/bin/app"}}
		up := false
		for _, e := range h.events {
			up = up || e == "start "+need
		}
		if slices.Contains(images, o.Image) && !up {
			r.StoppedEarly, r.ExitCode = true, 1
		}
		return r, nil
	}
}

// the shape of nginx-nodejs-redis: two services that need redis and do not say so
func TestTwoServicesThatNeedAnotherOneThatTheFileDoesNotSayAreBothObservedWithIt(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  redis:\n    image: r\n  web1:\n    image: w1\n  web2:\n    image: w2\n")
	h.haveAll("r", "w1", "w2")
	failsUnlessAll(h, "r", "w1", "w2")
	h.mustRun(0, "convert")
	if got := h.names(); !reflect.DeepEqual(got, []string{"r", "w1", "w2", "w1", "w2"}) {
		t.Fatalf("observed %v", got)
	}
	// one container of redis serves both, and the other web service was not started beside the first
	if len(h.started) != 1 || h.started[0].Image != "r" {
		t.Errorf("started %v", h.started)
	}
	for _, svc := range []string{"web1", "web2"} {
		sug := suggestion(h, svc)
		if len(sug) != 1 || !strings.Contains(sug[0].Message, "with redis running it did not stop") {
			t.Errorf("%s: suggestion %+v", svc, sug)
		}
		if si := h.genIn[0].Services[svc].Observed; si.StoppedEarly {
			t.Errorf("%s: the observation kept is the one that stopped", svc)
		}
	}
}

func TestADependencyThatStoppedIsStartedAgainForTheNextService(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  db:\n    image: d\n  a:\n    image: a\n    depends_on: [db]\n  b:\n    image: b\n    depends_on: [db]\n")
	h.haveAll("d", "a", "b")
	h.crashed = map[string]bool{"d": true}
	h.mustRun(0, "convert")
	n := 0
	for _, e := range h.events {
		if e == "start d" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("db was started %d times: %v", n, h.events)
	}
	if !hasWarning(h, "b", "the service db stopped during an earlier observation; it is started again for the observation of b") {
		t.Errorf("warnings %q", h.genIn[0].Services["b"].Observed.Warnings)
	}
}

func TestAStartedServiceThatStopsDuringAnObservationOfAnotherIsFound(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  db:\n    image: d\n  a:\n    image: a\n    depends_on: [db]\n  c:\n    image: c\n")
	h.haveAll("d", "a", "c")
	h.onObserve = func(o observe.Options) error {
		if o.Image == "c" {
			h.crashed = map[string]bool{"d": true}
		}
		return nil
	}
	h.mustRun(0, "convert")
	if !hasWarning(h, "c", "the service db stopped while c was observed") {
		t.Errorf("warnings %q", h.genIn[0].Services["c"].Observed.Warnings)
	}
}

func TestUpNamesLeavesOutAContainerThatDied(t *testing.T) {
	h := newHarness(t)
	rs := &runState{ordered: []*compose.Service{{Name: "db"}, {Name: "web"}}, running: map[string]Stopper{"db": &fakeStopper{h: h, name: "d"}}, crashed: map[string]bool{"db": true}}
	if got := rs.upNames(); len(got) != 0 {
		t.Errorf("upNames %v: a container that died is up", got)
	}
	rs.crashed["db"] = false
	if got := rs.upNames(); !reflect.DeepEqual(got, []string{"db"}) {
		t.Errorf("upNames %v", got)
	}
}

func TestACandidateIsCountedWithWhatItNeeds(t *testing.T) {
	var ordered []*compose.Service
	var deps []compose.Dependency
	for _, n := range []string{"m1", "m2", "m3", "m4", "m5"} {
		ordered = append(ordered, &compose.Service{Name: n})
		deps = append(deps, compose.Dependency{Service: n})
	}
	ordered = append(ordered, &compose.Service{Name: "c1", DependsOn: deps}, &compose.Service{Name: "backend"})
	rs := &runState{ordered: ordered, running: map[string]Stopper{}, crashed: map[string]bool{}}
	// c1 and the five it needs are six containers: more than retryMax
	if got := rs.pickCandidates([]string{"c1"}); len(got) != 0 {
		t.Errorf("picked %v", got)
	}
	if got := rs.pickCandidates([]string{"m1", "c1"}); !reflect.DeepEqual(got, []string{"m1"}) {
		t.Errorf("picked %v", got)
	}
}

func TestHeldWarningDoesNotPromiseWhatNoHubWouldDo(t *testing.T) {
	h := newHarness(t)
	newHubApp(h, "b", "d")
	h.write("compose.yaml", "services:\n  backend:\n    image: b\n    depends_on: [db]\n  db:\n    image: d\n")
	h.startErr["d"] = errors.New("exited at once")
	stoppedWith(h, map[string]int{"b": 1})
	h.mustRun(0, "convert", "--no-hub")
	if !hasWarning(h, "backend", "status 1 while the services it needs (db) were not running; the stop may be that") {
		t.Fatalf("warnings %q", h.genIn[0].Services["backend"].Observed.Warnings)
	}
	if hasWarning(h, "backend", "the hub was not consulted") {
		t.Errorf("the hub is off and the warning says it was not consulted for this: %q", h.genIn[0].Services["backend"].Observed.Warnings)
	}
}

func TestSecondObservationReusesTheImageOfTheFirst(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  backend:\n    build: ./b\n  db:\n    image: d\n")
	h.write("b/Dockerfile", "FROM scratch\n")
	h.haveAll("d")
	h.app.Observe = func(ctx context.Context, o observe.Options) (*observe.Result, error) {
		h.observed = append(h.observed, o)
		h.events = append(h.events, "observe "+o.Image)
		r := &observe.Result{Sysroot: "/sysroots/" + strconv.Itoa(len(h.observed)), Program: "/usr/bin/app"}
		if strings.HasSuffix(o.Image, "/backend:latest") && !slices.Contains(h.events, "start d") {
			r.StoppedEarly, r.ExitCode = true, 1
		}
		return r, nil
	}
	h.mustRun(0, "convert")
	n := 0
	for _, i := range h.names() {
		if strings.HasSuffix(i, "/backend:latest") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("backend was observed %d times", n)
	}
	if len(h.dock.builds) != 1 {
		t.Errorf("the image was built %d times", len(h.dock.builds))
	}
}
func TestServicesThatAllStopAloneGiveEachOtherNoEvidenceAndReadTheHub(t *testing.T) {
	h := newHarness(t)
	stub := newHubApp(h, "b", "c", "d")
	h.write("compose.yaml", "services:\n  backend:\n    image: b\n  cache:\n    image: c\n  db:\n    image: d\n")
	for _, i := range []string{"b", "c", "d"} {
		h.startErr[i] = errors.New("exited at once")
	}
	stoppedWith(h, map[string]int{"b": 1, "c": 1, "d": 1})
	h.mustRun(0, "convert")
	// each one stopped alone, so none of the others is evidence for another: each is observed once
	if got := h.names(); !reflect.DeepEqual(got, []string{"b", "c", "d"}) {
		t.Fatalf("observed %v", got)
	}
	// and every service reads the hub once
	if n := countCalls(stub.calls, "manifest"); n != 3 {
		t.Errorf("the hub was read %d times: %v", n, stub.calls)
	}
}

func TestSecondObservationStartsFromANewCopyOfTheBind(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  backend:\n    image: b\n    volumes:\n      - ./data:/d\n  db:\n    image: d\n")
	h.haveAll("b", "d")
	h.dataTree()
	var paths []string
	dirty := false
	calls := 0
	h.app.Observe = func(ctx context.Context, o observe.Options) (*observe.Result, error) {
		h.observed = append(h.observed, o)
		r := &observe.Result{Sysroot: "/sysroots/" + o.Image + strconv.Itoa(len(h.observed)), Program: "/usr/bin/app"}
		if o.Image != "b" {
			return r, nil
		}
		calls++
		p := bindPath(o.Binds[0])
		paths = append(paths, p)
		if calls == 1 {
			if err := os.WriteFile(filepath.Join(p, "written-by-the-first"), []byte("x"), 0o644); err != nil {
				return nil, err
			}
			r.StoppedEarly, r.ExitCode = true, 1
		} else if _, err := os.Stat(filepath.Join(p, "written-by-the-first")); err == nil {
			dirty = true
		}
		return r, nil
	}
	h.mustRun(0, "convert")
	if len(paths) != 2 || paths[0] == paths[1] || dirty {
		t.Errorf("paths %v, what the first wrote was seen by the second: %v", paths, dirty)
	}
}

func TestAServiceThatStoppedDuringTheObservationIsNotCountedAsRunning(t *testing.T) {
	h := newHarness(t)
	stub := newHubApp(h, "b", "d")
	h.write("compose.yaml", "services:\n  backend:\n    image: b\n    depends_on: [db]\n  db:\n    image: d\n")
	h.crashed = map[string]bool{"d": true}
	stoppedWith(h, map[string]int{"b": 1})
	h.mustRun(0, "convert")
	if len(stub.calls) != 0 {
		t.Errorf("the hub was read although db had stopped: %v", stub.calls)
	}
	if !hasWarning(h, "backend", "the service db stopped while backend was observed") {
		t.Errorf("warnings %q", h.genIn[0].Services["backend"].Observed.Warnings)
	}
}

func TestSecondObservationIsBoundedAndNamesWhatRanBesideIt(t *testing.T) {
	h := newHarness(t)
	var b strings.Builder
	b.WriteString("services:\n  backend:\n    image: b\n")
	imgs := []string{"b"}
	for i := 0; i < 7; i++ {
		n := "s" + strconv.Itoa(i)
		b.WriteString("  " + n + ":\n    image: " + n + "\n")
		imgs = append(imgs, n)
	}
	h.write("compose.yaml", b.String())
	h.haveAll(imgs...)
	failsUnless(h, "b", "s0")
	h.mustRun(0, "convert")
	if len(h.started) != retryMax {
		t.Errorf("started %d services, want %d: %v", len(h.started), retryMax, h.started)
	}
	sug := suggestion(h, "backend")
	if len(sug) != 1 || !strings.Contains(sug[0].Message, "s0, s1, s2, s3, s4") ||
		!strings.Contains(sug[0].Message, "one or more of them may be needed: if so, add them to depends_on of backend in docker-compose.yml; the tool did not tell which one") {
		t.Errorf("suggestion %+v", sug)
	}
}

func TestDependentsObservedBeforeTheSecondObservationSayIt(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  backend:\n    image: b\n  web:\n    image: w\n    depends_on: [backend]\n  db:\n    image: d\n")
	h.haveAll("b", "w", "d")
	// backend could not be started for web, so web was observed without it
	h.startErr["b"] = errors.New("exited at once")
	stoppedWith(h, map[string]int{"b": 1})
	h.mustRun(0, "convert")
	if !hasWarning(h, "web", "it was observed without backend (backend did not start); backend was observed once more later") {
		t.Errorf("warnings %q", h.genIn[0].Services["web"].Observed.Warnings)
	}
}

func countEvents(h *harness, e string) int {
	n := 0
	for _, x := range h.events {
		if x == e {
			n++
		}
	}
	return n
}

// observeBy makes the observation of each image give what stops says: it returns true for an
// observation that must stop early with status 1. It may change the harness before it answers.
func observeBy(h *harness, stops func(image string) bool) {
	h.app.Observe = func(ctx context.Context, o observe.Options) (*observe.Result, error) {
		h.observed = append(h.observed, o)
		h.events = append(h.events, "observe "+o.Image)
		r := &observe.Result{Sysroot: "/sysroots/" + o.Image + strconv.Itoa(len(h.observed)), Program: "/usr/bin/app", Argv: []string{"/usr/bin/app"}}
		if stops(o.Image) {
			r.StoppedEarly, r.ExitCode = true, 1
		}
		return r, nil
	}
}

func TestAContainerThatDiedBeforeTheObservationIsACandidate(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  r:\n    image: r\n  x:\n    image: x\n    depends_on: [r]\n  s:\n    image: s\n")
	h.haveAll("r", "x", "s")
	observeBy(h, func(image string) bool {
		switch image {
		case "x":
			// r dies during the observation of x, which stops as well
			h.crashed = map[string]bool{"r": true}
			return true
		case "s":
			return countEvents(h, "start r") < 2
		}
		return false
	})
	h.mustRun(0, "convert")
	if got := h.names(); !reflect.DeepEqual(got, []string{"r", "x", "s", "s"}) {
		t.Fatalf("observed %v", got)
	}
	if n := countEvents(h, "start r"); n != 2 {
		t.Errorf("r was started %d times", n)
	}
}

func TestWhatElseRanDuringTheSecondObservationIsNamed(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  redis:\n    image: r\n  s:\n    image: s\n  w2:\n    image: w\n  a:\n    image: a\n    depends_on: [redis]\n  n:\n    image: n\n    depends_on: [w2]\n")
	h.haveAll("r", "s", "w", "a", "n")
	observeBy(h, func(image string) bool {
		switch image {
		case "w", "a", "n":
			return true
		case "s":
			return countEvents(h, "start r") == 0
		}
		return false
	})
	h.mustRun(0, "convert")
	if !hasWarning(h, "s", "also running during the second observation, started for the observation of other services: w2") {
		t.Errorf("warnings %q", h.genIn[0].Services["s"].Observed.Warnings)
	}
}

func TestWhyAServiceWasNotStartedIsTheReasonOfTheFile(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  s:\n    image: s\n  c:\n    image: c\n    depends_on: [y]\n  y:\n    image: y\n")
	h.haveAll("s", "c")
	stoppedWith(h, map[string]int{"s": 1})
	h.mustRun(0, "convert", "--service", "s", "--service", "c")
	if !hasWarning(h, "s", "y (not selected with --service)") {
		t.Errorf("warnings %q", h.genIn[0].Services["s"].Observed.Warnings)
	}
}

func TestHelpSaysWhatTheSecondObservationStarts(t *testing.T) {
	h := newHarness(t)
	h.app.usage(h.stdout)
	if !strings.Contains(strings.Join(strings.Fields(h.stdout.String()), " "), "starting at most 5 containers for it (each candidate counted with what it needs)") {
		t.Errorf("the help does not say it")
	}
}

func TestSecondObservationWithAServiceAlreadyUpSaysNoNewServiceBesideIt(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  r:\n    image: r\n  x:\n    image: x\n    depends_on: [r]\n  s:\n    image: s\n")
	h.haveAll("r", "x", "s")
	observeBy(h, func(image string) bool { return image == "s" && countEvents(h, "start x") == 0 })
	h.mustRun(0, "convert")
	sug := suggestion(h, "s")
	if len(sug) != 1 || !strings.HasPrefix(sug[0].Message, "observed with no new service beside it, it stopped with status 1; with x running") {
		t.Errorf("suggestion %+v", sug)
	}
}

func TestAStopThatDoesNotHappenAgainIsSaidWhenNothingRanBesideIt(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", noDepsCompose)
	h.haveAll("b", "d")
	h.startErr["d"] = errors.New("exited at once")
	calls := 0
	observeBy(h, func(image string) bool {
		if image != "b" {
			return false
		}
		calls++
		return calls == 1
	})
	h.mustRun(0, "convert")
	if !hasWarning(h, "backend", "it stopped with status 1 the first time and did not stop the second time, although none of the other services could run beside it") {
		t.Errorf("warnings %q", h.genIn[0].Services["backend"].Observed.Warnings)
	}
	if sug := suggestion(h, "backend"); len(sug) != 0 {
		t.Errorf("a suggestion although nothing ran beside it: %+v", sug)
	}
}

func TestADependencyThatCannotBeStoppedIsNotStartedAgain(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  db:\n    image: d\n  a:\n    image: a\n    depends_on: [db]\n  b:\n    image: b\n    depends_on: [db]\n")
	h.haveAll("d", "a", "b")
	h.crashed = map[string]bool{"d": true}
	h.stopErr["d"] = errors.New("boom")
	h.mustRun(0, "convert")
	if n := countEvents(h, "start d"); n != 1 {
		t.Errorf("db was started %d times although its old container could not be stopped", n)
	}
	if !hasWarning(h, "b", "the old container of db could not be stopped and may still hold its name on the network, so it is not started again for the observation of b") {
		t.Errorf("warnings %q", h.genIn[0].Services["b"].Observed.Warnings)
	}
}

func TestAContainerForADependentThatCannotBeStoppedIsNotObservedAgain(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  api:\n    image: a\n  web:\n    image: w\n    depends_on: [api]\n  db:\n    image: d\n")
	h.haveAll("a", "w", "d")
	h.stopErr["a"] = errors.New("boom")
	stoppedWith(h, map[string]int{"a": 1})
	h.mustRun(0, "convert")
	n := 0
	for _, i := range h.names() {
		if i == "a" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("api was observed %d times", n)
	}
	if !hasWarning(h, "api", "could not be stopped and may still hold the name api on the network") {
		t.Errorf("warnings %q", h.genIn[0].Services["api"].Observed.Warnings)
	}
}

func TestTheSecondObservationDoesNotSayAgainWhatTheBindsSaid(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  backend:\n    image: b\n    volumes:\n      - type: bind\n        source: ./ok\n        target: rel/target\n  db:\n    image: d\n")
	h.haveAll("b", "d")
	if err := os.MkdirAll(filepath.Join(h.dir, "ok"), 0o755); err != nil {
		t.Fatal(err)
	}
	observeBy(h, func(image string) bool { return image == "b" && countEvents(h, "start d") == 0 })
	h.mustRun(0, "convert")
	if n := strings.Count(h.stderr.String(), "needs absolute paths and is not passed to the observation"); n != 1 {
		t.Errorf("the warning about the bind was printed %d times:\n%s", n, h.stderr)
	}
	if countEvents(h, "observe b") != 2 {
		t.Errorf("the second observation did not happen: %v", h.events)
	}
}

func TestHeldWarningKeepsTheHubClauseOutWhenARequiredBuildWouldBeRead(t *testing.T) {
	h := newHarness(t)
	newHubApp(h, "b", "d")
	h.app.HubRequired = []hub.Required{{Images: []string{"b"}, Programs: []string{"app"}, Packages: []string{"eyberg/b"}, Reason: "a test row"}}
	h.write("compose.yaml", "services:\n  backend:\n    image: b\n    depends_on: [db]\n  db:\n    image: d\n")
	h.startErr["d"] = errors.New("exited at once")
	stoppedWith(h, map[string]int{"b": 1})
	h.mustRun(0, "convert")
	if !hasWarning(h, "backend", "status 1 while the services it needs (db) were not running; the stop may be that") {
		t.Fatalf("warnings %q", h.genIn[0].Services["backend"].Observed.Warnings)
	}
	if hasWarning(h, "backend", "the hub was not consulted") {
		t.Errorf("the hub is read for a required build: %q", h.genIn[0].Services["backend"].Observed.Warnings)
	}
}

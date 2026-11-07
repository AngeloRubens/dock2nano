package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
}

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
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, dir: t.TempDir(), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	h.dock = &fakeDocker{have: map[string]bool{}, images: map[string]*docker.ImageConfig{}}
	h.obsErr = map[string]error{}
	h.env = map[string]string{}
	h.app = &App{Docker: h.dock, Stdout: h.stdout, Stderr: h.stderr}
	h.app.Observe = func(ctx context.Context, o observe.Options) (*observe.Result, error) {
		h.observed = append(h.observed, o)
		if err := h.obsErr[o.Image]; err != nil {
			return nil, err
		}
		return &observe.Result{Sysroot: "/sysroots/" + o.Image, Program: "/usr/bin/app", Argv: []string{"/usr/bin/app"}}, nil
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
	tag := ToolName + "/shop-web:latest"
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
}

func TestBuildDefaults(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "name: shop\nservices:\n  web:\n    build: ./web\n")
	h.write("web/Dockerfile", stagesDockerfile)
	h.mustRun(0, "convert")
	want := docker.BuildOptions{Context: filepath.Join(h.dir, "web"), Dockerfile: filepath.Join(h.dir, "web", "Dockerfile"), Tag: ToolName + "/shop-web:latest", Quiet: true}
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
	h.mustRun(1, "convert")
	msg := h.stderr.String()
	for _, want := range []string{"service web", "build argument B", "B_VAL"} {
		if !strings.Contains(msg, want) {
			t.Errorf("stderr %q does not contain %q", msg, want)
		}
	}
	if len(h.dock.builds) != 0 || len(h.observed) != 0 || len(h.wrote) != 0 {
		t.Errorf("something ran after the error")
	}
}

func TestImageTagSanitized(t *testing.T) {
	got := imageTag(&compose.Project{Name: "My_Shop X"}, "/srv/ignored", &compose.Service{Name: "Web.1"})
	if want := ToolName + "/my_shop-x-web.1:latest"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	got = imageTag(&compose.Project{}, "/srv/My App", &compose.Service{Name: "web"})
	if want := ToolName + "/my-app-web:latest"; got != want {
		t.Fatalf("got %q, want %q", got, want)
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
	ep := []string{"/entry.sh"}
	want := observe.Options{Image: "nginx:1", Entrypoint: &ep, Cmd: []string{"serve", "--port", "80"}, Seconds: 7}
	want.Env = []string{"E=from-file", "A=1", "Q=quoted", "R=raw", "S=keep # this", "C=c"}
	want.Binds = []string{filepath.Join(h.dir, "conf") + ":/etc/conf:ro", filepath.Join(h.dir, "rw") + ":/rw"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
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
	}
	for _, r := range rows {
		h := newHarness(t)
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
	tag := ToolName + "/app-app:latest"
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
	want := docker.BuildOptions{Context: filepath.Join(h.dir, "src"), Dockerfile: filepath.Join(h.dir, "ci", "Dockerfile"), Tag: ToolName + "/demo-demo:latest", Quiet: true}
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
	want := gen.Render(h.wrote[0].Report) + "\nWritten:\n  /w/" + pkg + "\n  " + composePath + "\n\nNext steps:\n  ops pkg get eyberg/ops-dns:0.0.1    (once, if you do not have it yet)\n  ops compose up -f " + composePath + "\n"
	if h.stdout.String() != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", h.stdout, want)
	}
	if !reflect.DeepEqual(h.wopts, []output.Options{{ComposePath: composePath}}) {
		t.Errorf("write options: %+v", h.wopts)
	}

	h = newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n")
	h.dock.have["w"] = true
	h.mustRun(0, "convert", "-o", "/abs/dir", "--ops-home", "/my ops")
	if !strings.Contains(h.stdout.String(), "  OPS_HOME='/my ops' ops compose up -f /abs/dir/compose.yaml\n") || !strings.Contains(h.stdout.String(), "  OPS_HOME='/my ops' ops pkg get eyberg/ops-dns:0.0.1") {
		t.Errorf("stdout:\n%s", h.stdout)
	}
	if h.wopts[0].ComposePath != "/abs/dir/compose.yaml" || h.wopts[0].OpsHome != "/my ops" {
		t.Errorf("write options: %+v", h.wopts)
	}
}

func TestWriteErrorPrintsNothing(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n")
	h.dock.have["w"] = true
	h.app.Write = func(p *plan.Plan, o output.Options) ([]string, error) { return nil, errors.New("exists") }
	h.mustRun(1, "convert")
	if h.stdout.Len() != 0 || !strings.Contains(h.stderr.String(), "exists") {
		t.Errorf("stdout %q, stderr %q", h.stdout, h.stderr)
	}
	if len(h.removed) != 1 {
		t.Errorf("removed: %v", h.removed)
	}
}

func TestExpand(t *testing.T) {
	env := map[string]string{"A": "a", "E": ""}
	lookup := func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
	rows := []struct {
		in, want, err string
	}{
		{"plain", "plain", ""},
		{"$A", "a", ""},
		{"${A}", "a", ""},
		{"x${A}y$A.z", "xaya.z", ""},
		{"$$A", "$A", ""},
		{"cost $", "cost $", ""},
		{"${NO:-d}", "d", ""},
		{"${E:-d}", "d", ""},
		{"${E-d}", "", ""},
		{"${NO-d}", "d", ""},
		{"${A:-d}", "a", ""},
		{"${NO}", "", "the variable NO is not set"},
		{"$NO", "", "the variable NO is not set"},
		{"${NO:?x}", "", "is not supported"},
		{"${A", "", "not closed"},
		{"${}", "", "does not name a variable"},
	}
	for _, r := range rows {
		got, err := expand(r.in, lookup)
		if r.err != "" {
			if err == nil || !strings.Contains(err.Error(), r.err) {
				t.Errorf("%q: error %v, want %q", r.in, err, r.err)
			}
			continue
		}
		if err != nil || got != r.want {
			t.Errorf("%q: got %q, %v, want %q", r.in, got, err, r.want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	rows := [][2]string{{"/a/b-c_d.e", "/a/b-c_d.e"}, {"/my ops", "'/my ops'"}, {"it's", `'it'\''s'`}, {"", "''"}}
	for _, r := range rows {
		if got := shellQuote(r[0]); got != r[1] {
			t.Errorf("%q: got %q, want %q", r[0], got, r[1])
		}
	}
}

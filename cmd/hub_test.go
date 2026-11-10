package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/hub"
	"github.com/AngeloRubens/dock2nano/internal/observe"
	"github.com/AngeloRubens/dock2nano/internal/output"
	"github.com/AngeloRubens/dock2nano/internal/plan"
)

const hubTestSum = "7a1fbdceb9f645836eaecae5e119fd99aa45068b1a3000f1d211e9b98b46fd2d"

const hubTestUserConf = "worker_processes  auto;\nevents {}\nhttp {\n    server {\n        listen 80;\n    }\n}\n"

const hubTestHubConf = "worker_processes 1;\ndaemon off;\nmaster_process off;\nevents {}\nhttp { server { listen 8083; } }\n"

// stubHub is a hub that answers from memory and records what it was asked.
type stubHub struct {
	pkgs        []hub.Package
	manifestErr error
	fetchErr    error
	calls       []string
	wholeFiles  map[string]string
	args        []string
	conf        string
}

func (s *stubHub) Manifest(ctx context.Context) ([]hub.Package, error) {
	s.calls = append(s.calls, "manifest")
	return s.pkgs, s.manifestErr
}

func (s *stubHub) recipe(p hub.Package) *hub.Recipe {
	rec := &hub.Recipe{Package: p, Files: map[string][]byte{}}
	rec.Manifest.Program = p.Name + "_" + p.Version + "/" + p.Name
	rec.Manifest.Args = s.args
	if s.conf != "" {
		rec.Files["/usr/local/nginx/conf/nginx.conf"] = []byte(s.conf)
	}
	return rec
}

func (s *stubHub) FetchRecipe(ctx context.Context, p hub.Package, files []string) (*hub.Recipe, error) {
	s.calls = append(s.calls, "recipe "+p.Ref()+" "+p.Version)
	if s.fetchErr != nil {
		return nil, s.fetchErr
	}
	return s.recipe(p), nil
}

func (s *stubHub) FetchWhole(ctx context.Context, p hub.Package, dest string) (*hub.Recipe, error) {
	s.calls = append(s.calls, "whole "+p.Ref()+" "+p.Version)
	if s.fetchErr != nil {
		return nil, s.fetchErr
	}
	for name, body := range s.wholeFiles {
		if err := os.WriteFile(filepath.Join(dest, name), []byte(body), 0o755); err != nil {
			return nil, err
		}
	}
	return s.recipe(p), nil
}

func hubPkg(ns, name, version string) hub.Package {
	return hub.Package{Namespace: ns, Name: name, Version: version, Arch: "x86_64", SHA256: hubTestSum}
}

// hubHarness is a harness whose observation gives the result that the test
// builds, with a real sysroot, and whose writer keeps the configuration of the
// first service while its sysroot still exists.
type hubHarness struct {
	*harness
	stub *stubHub
	conf string
	plan *plan.Plan

	// made are the sysroots the observation made.
	made []string
}

func newHubHarness(t *testing.T, image string, res func(sysroot string) *observe.Result) *hubHarness {
	t.Helper()
	h := &hubHarness{harness: newHarness(t), stub: &stubHub{}}
	h.app.Hub = h.stub
	h.app.RemoveAll = func(p string) error {
		h.removed = append(h.removed, p)
		return os.RemoveAll(p)
	}
	h.write("compose.yaml", "services:\n  web:\n    image: "+image+"\n")
	h.dock.have[image] = true
	h.dock.images[image] = &docker.ImageConfig{Architecture: "amd64"}
	h.app.Observe = func(ctx context.Context, o observe.Options) (*observe.Result, error) {
		h.observed = append(h.observed, o)
		sysroot := t.TempDir()
		h.made = append(h.made, sysroot)
		conf := filepath.Join(sysroot, "etc/nginx/nginx.conf")
		if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(conf, []byte(hubTestUserConf), 0o644); err != nil {
			return nil, err
		}
		return res(sysroot), nil
	}
	h.app.Write = func(p *plan.Plan, o output.Options) ([]string, error) {
		h.plan = p
		if len(p.Services) > 0 {
			b, _ := os.ReadFile(filepath.Join(p.Services[0].Sysroot, "etc/nginx/nginx.conf"))
			h.conf = string(b)
		}
		return []string{o.ComposePath}, nil
	}
	return h
}

func nginxObserved(children int) func(string) *observe.Result {
	return func(sysroot string) *observe.Result {
		return &observe.Result{Sysroot: sysroot, Program: "/usr/sbin/nginx", Argv: []string{"nginx", "-g", "daemon off;"}, Env: []string{"NGINX_VERSION=1.25.3"}, Children: children, UID: -1, GID: -1}
	}
}

func TestHubIsNotReadWhenTheProgramStarts(t *testing.T) {
	h := newHubHarness(t, "nginx:1.25.3", nginxObserved(0))
	h.mustRun(0, "convert")
	if len(h.stub.calls) != 0 {
		t.Errorf("the hub was read: %v", h.stub.calls)
	}
	if strings.Contains(h.stdout.String(), "hub") {
		t.Errorf("the report mentions the hub:\n%s", h.stdout)
	}
	if h.conf != hubTestUserConf {
		t.Errorf("the configuration was changed:\n%s", h.conf)
	}
}

func TestHubIsReadWhenTheProgramStartsChildren(t *testing.T) {
	h := newHubHarness(t, "nginx:1.25.3", nginxObserved(2))
	h.stub.pkgs = []hub.Package{hubPkg("eyberg", "nginx", "1.15.6"), hubPkg("eyberg", "nginx", "1.28.3")}
	h.stub.conf = hubTestHubConf
	h.mustRun(0, "convert")
	if !reflect.DeepEqual(h.stub.calls, []string{"manifest", "recipe eyberg/nginx 1.28.3"}) {
		t.Errorf("calls: %v", h.stub.calls)
	}
	for _, want := range []string{"master_process off;", "daemon off;", "worker_processes 1;", "listen 80;"} {
		if !strings.Contains(h.conf, want) {
			t.Errorf("%q is not in the configuration:\n%s", want, h.conf)
		}
	}
	if strings.Contains(h.conf, "8083") {
		t.Errorf("the configuration of the hub leaked in:\n%s", h.conf)
	}
	out := h.stdout.String()
	for _, want := range []string{"eyberg/nginx 1.28.3", hubTestSum, "published by eyberg", "the same major series", "files of the image are the ones observed from your image", "not run on nanos"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
	if len(h.plan.Services) != 1 || h.plan.Services[0].Manifest.Program != "/usr/sbin/nginx" {
		t.Errorf("the program is the one of the image: %+v", h.plan.Services)
	}
}

func TestHubIsReadWhenTheContainerStopped(t *testing.T) {
	h := newHubHarness(t, "nginx", func(sysroot string) *observe.Result {
		r := nginxObserved(0)(sysroot)
		r.StoppedEarly, r.ExitCode = true, 1
		return r
	})
	h.stub.pkgs = []hub.Package{hubPkg("eyberg", "nginx", "1.28.3")}
	h.mustRun(0, "convert")
	if len(h.stub.calls) != 2 {
		t.Errorf("calls: %v", h.stub.calls)
	}
	if !strings.Contains(h.stdout.String(), "the container stopped with status 1") {
		t.Errorf("the report does not say why:\n%s", h.stdout)
	}
	// the version comes from NGINX_VERSION: 1.25.3 against 1.28.3
	if !strings.Contains(h.stdout.String(), "your image has 1.25.3") {
		t.Errorf("the version of the image:\n%s", h.stdout)
	}
}

func TestNoHubNeverReadsTheHub(t *testing.T) {
	h := newHubHarness(t, "nginx:1.25.3", nginxObserved(2))
	h.stub.pkgs = []hub.Package{hubPkg("eyberg", "nginx", "1.28.3")}
	h.mustRun(0, "convert", "--no-hub")
	if len(h.stub.calls) != 0 {
		t.Errorf("the hub was read with --no-hub: %v", h.stub.calls)
	}
	if h.conf != hubTestUserConf {
		t.Errorf("the configuration was changed:\n%s", h.conf)
	}
	if !strings.Contains(h.stdout.String(), "--no-hub") {
		t.Errorf("the report does not say the hub was off:\n%s", h.stdout)
	}
}

func TestHubThatCannotBeReadDoesNotStopTheConversion(t *testing.T) {
	h := newHubHarness(t, "nginx:1.25.3", nginxObserved(2))
	h.stub.manifestErr = errors.New("connection refused")
	h.mustRun(0, "convert")
	if !strings.Contains(h.stdout.String(), "could not be read (connection refused)") {
		t.Errorf("the report:\n%s", h.stdout)
	}
	if h.conf != hubTestUserConf {
		t.Errorf("the configuration was changed:\n%s", h.conf)
	}
}

func TestHubPackageThatCannotBeFetchedDoesNotStopTheConversion(t *testing.T) {
	h := newHubHarness(t, "nginx:1.25.3", nginxObserved(2))
	h.stub.pkgs = []hub.Package{hubPkg("eyberg", "nginx", "1.28.3")}
	h.stub.fetchErr = errors.New("digest mismatch")
	h.mustRun(0, "convert")
	if !strings.Contains(h.stdout.String(), "could not be fetched or checked (digest mismatch)") {
		t.Errorf("the report:\n%s", h.stdout)
	}
	if h.conf != hubTestUserConf {
		t.Errorf("the configuration was changed:\n%s", h.conf)
	}
}

func TestHubWithoutAPackageForTheProgram(t *testing.T) {
	h := newHubHarness(t, "nginx:1.25.3", nginxObserved(2))
	h.stub.pkgs = []hub.Package{hubPkg("eyberg", "caddy", "2.7.6")}
	h.mustRun(0, "convert")
	if !reflect.DeepEqual(h.stub.calls, []string{"manifest"}) {
		t.Errorf("calls: %v", h.stub.calls)
	}
	if !strings.Contains(h.stdout.String(), "has no package named nginx for amd64") {
		t.Errorf("the report:\n%s", h.stdout)
	}
}

func TestHubIgnoresAPackageOfAnotherArchitecture(t *testing.T) {
	h := newHubHarness(t, "nginx:1.25.3", nginxObserved(2))
	p := hubPkg("eyberg", "nginx", "1.28.3")
	p.Arch = "arm64"
	h.stub.pkgs = []hub.Package{p}
	h.mustRun(0, "convert")
	if !reflect.DeepEqual(h.stub.calls, []string{"manifest"}) {
		t.Errorf("calls: %v", h.stub.calls)
	}
}

const hubTestRows = `
rows:
  - images: [nginx]
    programs: [nginx]
    packages:
      - eyberg/nginx
    reason: a test row
`

func TestRequiredBuildIsUsedWholeEvenWhenTheProgramStarted(t *testing.T) {
	h := newHubHarness(t, "nginx:1.28.0", nginxObserved(0))
	rows, err := hub.ParseRequired([]byte(hubTestRows))
	if err != nil {
		t.Fatal(err)
	}
	h.app.HubRequired = rows
	h.stub.pkgs = []hub.Package{hubPkg("eyberg", "nginx", "1.28.3")}
	h.stub.wholeFiles = map[string]string{"nginx": "ELF"}
	h.stub.args = []string{"./nginx", "-q"}
	var sysroot string
	write := h.app.Write
	h.app.Write = func(p *plan.Plan, o output.Options) ([]string, error) {
		sysroot = p.Services[0].Sysroot
		if _, err := os.Stat(filepath.Join(sysroot, "nginx")); err != nil {
			t.Errorf("the program of the hub is not in the sysroot: %v", err)
		}
		if _, err := os.Stat(filepath.Join(sysroot, "etc/nginx/nginx.conf")); err == nil {
			t.Errorf("the files of the image are in the package")
		}
		return write(p, o)
	}
	h.mustRun(0, "convert")
	if !reflect.DeepEqual(h.stub.calls, []string{"manifest", "whole eyberg/nginx 1.28.3"}) {
		t.Errorf("calls: %v", h.stub.calls)
	}
	m := h.plan.Services[0].Manifest
	if m.Program != "/nginx" || !reflect.DeepEqual(m.Args, []string{"./nginx", "-q"}) {
		t.Errorf("manifest: %+v", m)
	}
	out := h.stdout.String()
	for _, want := range []string{"the build of the hub whole", "eyberg/nginx 1.28.3", "a test row", "the same major series"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(sysroot); err == nil {
		t.Errorf("the directory of the hub package was not removed: %s", sysroot)
	}
	if _, err := os.Stat(h.made[0]); err == nil {
		t.Errorf("the sysroot of the observation was not removed: %s", h.made[0])
	}
}

func TestRequiredBuildOfAnotherSeriesSaysDataIsNotCompatible(t *testing.T) {
	h := newHubHarness(t, "nginx:0.9", nginxObserved(0))
	rows, _ := hub.ParseRequired([]byte(hubTestRows))
	h.app.HubRequired = rows
	h.stub.pkgs = []hub.Package{hubPkg("eyberg", "nginx", "1.28.3")}
	h.mustRun(0, "convert")
	if !strings.Contains(h.stdout.String(), "DATA VOLUMES ARE NOT COMPATIBLE") {
		t.Errorf("the report:\n%s", h.stdout)
	}
}

func TestRequiredBuildWithNoHubIsNotConvertible(t *testing.T) {
	h := newHubHarness(t, "nginx:1.28.0", nginxObserved(0))
	rows, _ := hub.ParseRequired([]byte(hubTestRows))
	h.app.HubRequired = rows
	h.stub.pkgs = []hub.Package{hubPkg("eyberg", "nginx", "1.28.3")}
	h.mustRun(1, "convert", "--no-hub")
	if len(h.stub.calls) != 0 {
		t.Errorf("the hub was read with --no-hub: %v", h.stub.calls)
	}
	out := h.stdout.String()
	for _, want := range []string{"--no-hub turns the hub off", "eyberg/nginx", "a test row", "it is not converted"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
	if h.plan != nil {
		t.Error("a package was written")
	}
}

func TestRequiredBuildWithAHubThatFailsIsNotConvertible(t *testing.T) {
	for _, fail := range []string{"manifest", "none", "fetch"} {
		h := newHubHarness(t, "nginx:1.28.0", nginxObserved(0))
		rows, _ := hub.ParseRequired([]byte(hubTestRows))
		h.app.HubRequired = rows
		h.stub.pkgs = []hub.Package{hubPkg("eyberg", "nginx", "1.28.3")}
		switch fail {
		case "manifest":
			h.stub.manifestErr = errors.New("down")
		case "none":
			h.stub.pkgs = nil
		case "fetch":
			h.stub.fetchErr = errors.New("bad digest")
		}
		h.mustRun(1, "convert")
		if !strings.Contains(h.stdout.String(), "it cannot be converted") {
			t.Errorf("%s: the report:\n%s", fail, h.stdout)
		}
		left, _ := os.ReadDir(h.tmp)
		if len(left) != 0 {
			t.Errorf("%s: a directory was left: %v", fail, left)
		}
	}
}

func TestNoHubFlagIsParsed(t *testing.T) {
	h := newHarness(t)
	o, err := h.app.parseConvert([]string{"--no-hub"})
	if err != nil || !o.noHub {
		t.Fatalf("%+v %v", o, err)
	}
	o, err = h.app.parseConvert(nil)
	if err != nil || o.noHub {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestHubTrigger(t *testing.T) {
	if got := hubTrigger(&observe.Result{}); got != "" {
		t.Errorf("a program that started: %q", got)
	}
	if got := hubTrigger(&observe.Result{Children: 3}); !strings.Contains(got, "3 child processes") {
		t.Errorf("children: %q", got)
	}
	if got := hubTrigger(&observe.Result{StoppedEarly: true, ExitCode: 137}); !strings.Contains(got, "status 137") {
		t.Errorf("stopped: %q", got)
	}
}

func TestHubArch(t *testing.T) {
	for in, want := range map[string]string{"amd64": "amd64", "arm64": "arm64", "arm": "", "riscv64": ""} {
		if got := hubArch(&docker.ImageConfig{Architecture: in}); got != want {
			t.Errorf("hubArch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTheShippedTableOfRequiredBuildsIsRead(t *testing.T) {
	a := newApp()
	if a.Hub == nil || a.MkdirTemp == nil {
		t.Error("the real app has no hub client")
	}
	if rows, err := hub.DefaultRequired(); err != nil || !reflect.DeepEqual(a.HubRequired, rows) {
		t.Errorf("%v %v", rows, err)
	}
}

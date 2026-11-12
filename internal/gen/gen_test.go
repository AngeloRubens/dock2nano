package gen

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AngeloRubens/dock2nano/internal/compose"
	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/dockerfile"
	"github.com/AngeloRubens/dock2nano/internal/observe"
	"github.com/AngeloRubens/dock2nano/internal/plan"
)

type fakeInfo struct{ dir bool }

func (fakeInfo) Name() string       { return "x" }
func (fakeInfo) Size() int64        { return 0 }
func (fakeInfo) Mode() fs.FileMode  { return 0 }
func (fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool      { return f.dir }
func (fakeInfo) Sys() any           { return nil }

// fake is a disk and an environment that live in memory.
type fake struct {
	dirs  map[string]bool
	files map[string]string
	host  map[string]string
}

func (f *fake) stat(p string) (fs.FileInfo, error) {
	if f.dirs[p] {
		return fakeInfo{dir: true}, nil
	}
	if _, ok := f.files[p]; ok {
		return fakeInfo{}, nil
	}
	return nil, fs.ErrNotExist
}

func (f *fake) read(p string) ([]byte, error) {
	if c, ok := f.files[p]; ok {
		return []byte(c), nil
	}
	return nil, fs.ErrNotExist
}

func (f *fake) lookup(name string) (string, bool) {
	v, ok := f.host[name]
	return v, ok
}

func svc(name string) *compose.Service { return &compose.Service{Name: name} }

func intp(n int) *int { return &n }

func observed(name string) *observe.Result {
	return &observe.Result{Sysroot: "/tmp/sys-" + name, Program: "/bin/" + name, Argv: []string{"/bin/" + name, "-x"}, WorkingDir: "/"}
}

// newInput builds an input in which every service was observed.
func newInput(f *fake, svcs ...*compose.Service) Input {
	in := Input{Project: &compose.Project{Name: "proj", Services: svcs}, Dir: "/work/app", Services: map[string]ServiceInput{}, Stat: f.stat, ReadFile: f.read, HostEnv: f.lookup}
	for _, s := range svcs {
		in.Services[s.Name] = ServiceInput{Observed: observed(s.Name)}
	}
	return in
}

func mustGen(t *testing.T, in Input) *plan.Plan {
	t.Helper()
	p, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return p
}

// one generates a plan for a single service.
func one(t *testing.T, f *fake, s *compose.Service) (*plan.Plan, plan.Service) {
	t.Helper()
	p := mustGen(t, newInput(f, s))
	if len(p.Services) != 1 {
		t.Fatalf("got %d services, want 1", len(p.Services))
	}
	return p, p.Services[0]
}

func itemsOf(p *plan.Plan, service string, kind plan.Kind, key string) []plan.Item {
	var out []plan.Item
	for _, it := range p.Report {
		if it.Service == service && it.Kind == kind && it.Key == key {
			out = append(out, it)
		}
	}
	return out
}

// want1 checks that exactly one item has this service, kind and key, and that
// its message holds substr.
func want1(t *testing.T, p *plan.Plan, service string, kind plan.Kind, key, substr string) {
	t.Helper()
	got := itemsOf(p, service, kind, key)
	if len(got) != 1 {
		t.Fatalf("items %s/%s/%q: got %d, want 1; report: %+v", service, kind, key, len(got), p.Report)
	}
	if !strings.Contains(got[0].Message, substr) {
		t.Errorf("item %s/%s/%q: message %q does not hold %q", service, kind, key, got[0].Message, substr)
	}
}

func wantNone(t *testing.T, p *plan.Plan, service string, kind plan.Kind, key string) {
	t.Helper()
	if got := itemsOf(p, service, kind, key); len(got) != 0 {
		t.Errorf("items %s/%s/%q: got %+v, want none", service, kind, key, got)
	}
}

func names(p *plan.Plan) []string {
	var out []string
	for _, s := range p.Services {
		out = append(out, s.Name)
	}
	return out
}

func TestOrderFollowsDependsOn(t *testing.T) {
	dep := func(names ...string) []compose.Dependency {
		var out []compose.Dependency
		for _, n := range names {
			out = append(out, compose.Dependency{Service: n, Condition: "service_started"})
		}
		return out
	}
	tests := []struct {
		name string
		deps map[string][]compose.Dependency
		want []string
	}{
		{"no dependencies keep the file order", nil, []string{"a", "b", "c"}},
		{"a dependency goes first", map[string][]compose.Dependency{"a": dep("b")}, []string{"b", "a", "c"}},
		{"a chain", map[string][]compose.Dependency{"a": dep("c"), "c": dep("b")}, []string{"b", "c", "a"}},
		{"two dependents keep the file order", map[string][]compose.Dependency{"a": dep("c"), "b": dep("c")}, []string{"c", "a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svcs := []*compose.Service{svc("a"), svc("b"), svc("c")}
			for _, s := range svcs {
				s.DependsOn = tt.deps[s.Name]
			}
			p := mustGen(t, newInput(&fake{}, svcs...))
			if got := names(p); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("services = %v, want %v", got, tt.want)
			}
			var pkgs []string
			for _, c := range p.Compose.Packages {
				pkgs = append(pkgs, c.Pkg)
			}
			if !reflect.DeepEqual(pkgs, tt.want) {
				t.Errorf("compose packages = %v, want %v", pkgs, tt.want)
			}
		})
	}
}

func TestOrderErrors(t *testing.T) {
	dep := func(n string) []compose.Dependency {
		return []compose.Dependency{{Service: n, Condition: "service_started"}}
	}
	alpha, beta, gamma, delta := svc("alpha"), svc("beta"), svc("gamma"), svc("delta")
	alpha.DependsOn = dep("beta")
	beta.DependsOn = dep("alpha")
	delta.DependsOn = dep("alpha")
	_, err := Generate(newInput(&fake{}, alpha, beta, gamma, delta))
	if err == nil {
		t.Fatal("a cycle gave no error")
	}
	if !strings.Contains(err.Error(), "alpha, beta") {
		t.Errorf("error %q does not name the services of the cycle", err)
	}
	for _, other := range []string{"gamma", "delta"} {
		if strings.Contains(err.Error(), other) {
			t.Errorf("error %q names %s, which is not in the cycle", err, other)
		}
	}

	self := svc("self")
	self.DependsOn = dep("self")
	if _, err := Generate(newInput(&fake{}, self)); err == nil || !strings.Contains(err.Error(), "self") {
		t.Errorf("a service that depends on itself: error = %v", err)
	}

	web := svc("web")
	web.DependsOn = dep("ghost")
	if _, err := Generate(newInput(&fake{}, web)); err == nil || !strings.Contains(err.Error(), `"ghost"`) {
		t.Errorf("a missing dependency: error = %v", err)
	}
}

func TestSkippedServices(t *testing.T) {
	prof := svc("prof")
	prof.Profiles = []string{"debug"}
	zero := svc("zero")
	zero.Replicas = intp(0)
	web := svc("web")
	web.DependsOn = []compose.Dependency{{Service: "zero", Condition: "service_started"}}
	p := mustGen(t, newInput(&fake{}, prof, zero, web))
	if got := names(p); !reflect.DeepEqual(got, []string{"web"}) {
		t.Errorf("services = %v, want [web]", got)
	}
	if len(p.Compose.Packages) != 1 {
		t.Errorf("compose packages = %+v, want only web", p.Compose.Packages)
	}
	want1(t, p, "prof", plan.Unsupported, "profiles", "debug")
	want1(t, p, "zero", plan.Unsupported, "replicas", "0 replicas")
	want1(t, p, "web", plan.Note, "depends_on.zero", "not converted")
}

func TestServiceNotObserved(t *testing.T) {
	web, db := svc("web"), svc("db")
	in := newInput(&fake{}, web, db)
	delete(in.Services, "db")
	in.Services["web"] = ServiceInput{}
	p, err := Generate(in)
	if err == nil {
		t.Fatal("no service could be converted and there was no error")
	}
	if p == nil {
		t.Fatal("the plan with the report must come back with the error")
	}
	if len(p.Services) != 0 {
		t.Errorf("services = %v, want none", names(p))
	}
	want1(t, p, "web", plan.Unsupported, "service", "not observed")
	want1(t, p, "db", plan.Unsupported, "service", "not observed")
}

func TestSkippedReason(t *testing.T) {
	web, db := svc("web"), svc("db")
	in := newInput(&fake{}, web, db)
	in.Skipped = map[string]string{"db": "the image cannot be built: no Dockerfile"}
	p := mustGen(t, in)
	if got := names(p); !reflect.DeepEqual(got, []string{"web"}) {
		t.Errorf("services = %v, want [web]", got)
	}
	want1(t, p, "db", plan.Unsupported, "service", "no Dockerfile")
	if m := itemsOf(p, "db", plan.Unsupported, "service")[0].Message; strings.Contains(m, "not observed") {
		t.Errorf("message %q must give the reason, not the generic text", m)
	}
}

func TestNoProgramIsAnError(t *testing.T) {
	web := svc("web")
	in := newInput(&fake{}, web)
	in.Services["web"].Observed.Program = ""
	if _, err := Generate(in); err == nil || !strings.Contains(err.Error(), "web") {
		t.Errorf("error = %v, want one that names web", err)
	}
	if _, err := Generate(Input{}); err == nil {
		t.Error("no project gave no error")
	}
}

func TestReplicas(t *testing.T) {
	tests := []struct {
		name     string
		replicas *int
		report   bool
	}{
		{"not set", nil, false},
		{"one", intp(1), false},
		{"three", intp(3), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := svc("web")
			s.Replicas = tt.replicas
			p, _ := one(t, &fake{}, s)
			if tt.report {
				want1(t, p, "web", plan.Unsupported, "replicas", "3 replicas")
			} else {
				wantNone(t, p, "web", plan.Unsupported, "replicas")
			}
		})
	}
}

func TestProgramArgsAndPackage(t *testing.T) {
	web := svc("web")
	p, got := one(t, &fake{}, web)
	m := got.Manifest
	if m.Program != "/bin/web" || !reflect.DeepEqual(m.Args, []string{"/bin/web", "-x"}) || m.Version != "" {
		t.Errorf("manifest = %+v", m)
	}
	if !m.DisableArgsCopy {
		t.Error("DisableArgsCopy is false: ops would copy a host file named like Args[1] into the image")
	}
	if got.Sysroot != "/tmp/sys-web" || got.Name != "web" || got.PackageName != "proj_web" {
		t.Errorf("service = %+v", got)
	}
	wantPkg := plan.ComposePackage{Pkg: "web", Name: "proj_web", Local: true, Arch: "amd64", BaseVolumeSz: "256m"}
	if len(p.Compose.Packages) != 1 || p.Compose.Packages[0] != wantPkg {
		t.Errorf("compose = %+v, want %+v", p.Compose.Packages, wantPkg)
	}
	if p.Arch != "amd64" {
		t.Errorf("Arch = %q, want amd64", p.Arch)
	}
	want1(t, p, "web", plan.Observed, "program", "program /bin/web and 1 arguments")
	if m := itemsOf(p, "web", plan.Observed, "program")[0].Message; strings.Contains(m, "-x") {
		t.Errorf("message %q shows an argument", m)
	}

	in := newInput(&fake{}, web)
	in.Arch = "arm64"
	p = mustGen(t, in)
	if p.Arch != "arm64" || p.Compose.Packages[0].Arch != "arm64" {
		t.Errorf("arch = %q / %q, want arm64", p.Arch, p.Compose.Packages[0].Arch)
	}
}

func TestArchDefaultIsTheMachine(t *testing.T) {
	old := goarch
	t.Cleanup(func() { goarch = old })
	goarch = "arm64"
	p := mustGen(t, newInput(&fake{}, svc("web")))
	if p.Arch != "arm64" || p.Compose.Packages[0].Arch != "arm64" {
		t.Errorf("arch = %q / %q, want arm64", p.Arch, p.Compose.Packages[0].Arch)
	}
	goarch = "riscv64"
	if _, err := Generate(newInput(&fake{}, svc("web"))); err == nil || !strings.Contains(err.Error(), "riscv64") {
		t.Errorf("error = %v, want one that names riscv64", err)
	}
	in := newInput(&fake{}, svc("web"))
	in.Arch = "amd64"
	if p := mustGen(t, in); p.Arch != "amd64" {
		t.Errorf("an explicit arch was changed to %q", p.Arch)
	}
}

func TestNames(t *testing.T) {
	good := []struct {
		name, project, dir, pkg, pkgName string
	}{
		{"Web", "proj", "/work/app", "web", "proj_web"},
		{"web-2", "My Proj", "/work/app", "web-2", "my-proj_web-2"},
		{"web", "", "/work/My App", "web", "my-app_web"},
		{"exactly15chars-", "p", "/w", "exactly15chars", "p_exactly15chars-"},
		{"my_app", "p", "/w", "my-app", "p_my_app"},
		{"web.app", "p", "/w", "web-app", "p_web.app"},
		{"web", "..", "/w", "web", "._web"},
		{"web", "a..b", "/w", "web", "a.b_web"},
	}
	for _, tt := range good {
		t.Run(tt.name+"/"+tt.project, func(t *testing.T) {
			in := newInput(&fake{}, svc(tt.name))
			in.Project.Name = tt.project
			in.Dir = tt.dir
			p := mustGen(t, in)
			if p.Compose.Packages[0].Pkg != tt.pkg || p.Services[0].PackageName != tt.pkgName || p.Compose.Packages[0].Name != tt.pkgName {
				t.Errorf("pkg %q / package name %q / compose name %q, want %q / %q", p.Compose.Packages[0].Pkg, p.Services[0].PackageName, p.Compose.Packages[0].Name, tt.pkg, tt.pkgName)
			}
		})
	}
	_, err := Generate(newInput(&fake{}, svc("")))
	if err == nil {
		t.Error("an empty service name gave no error")
	}
	_, err = Generate(newInput(&fake{}, svc("Web"), svc("web")))
	if err == nil || !strings.Contains(err.Error(), `"Web"`) || !strings.Contains(err.Error(), `"web"`) {
		t.Errorf("two services with the same package name: error = %v, want both named", err)
	}
}

func TestFolderNamesThatCollide(t *testing.T) {
	// The long names get different package names (the hash is of the original
	// name) but the same folder name, which is lower-cased.
	_, err := Generate(newInput(&fake{}, svc("Long-Service-Name-X"), svc("long-service-name-x")))
	if err == nil {
		t.Fatal("no error")
	}
	if !strings.Contains(err.Error(), `"Long-Service-Name-X"`) || !strings.Contains(err.Error(), `"long-service-name-x"`) || !strings.Contains(err.Error(), "folder") {
		t.Errorf("error %q must name both services and the folder", err)
	}
}

func TestPackageNames(t *testing.T) {
	long := "a-very-long-service-name"
	sum := sha1.Sum([]byte(long))
	wantLong := "a-very-l-" + hex.EncodeToString(sum[:])[:6]
	tests := []struct {
		service, want string
		note          bool
	}{
		{"web", "web", false},
		{"Web", "web", true},
		{"redis_cache", "redis-cache", true},
		{"a.b__c", "a-b-c", true},
		{"-x-", "x", true},
		{"dns", "svc-dns", true},
		{"DNS", "svc-dns", true},
		{long, wantLong, true},
	}
	for _, tt := range tests {
		t.Run(tt.service, func(t *testing.T) {
			m, items, err := PackageNames(&compose.Project{}, []string{tt.service})
			if err != nil {
				t.Fatal(err)
			}
			if m[tt.service] != tt.want {
				t.Errorf("package name = %q, want %q", m[tt.service], tt.want)
			}
			if len(m[tt.service]) > 15 {
				t.Errorf("package name %q is longer than 15", m[tt.service])
			}
			if tt.note != (len(items) == 1) {
				t.Fatalf("items = %+v, want a note: %v", items, tt.note)
			}
			if tt.note && (items[0].Kind != plan.Note || items[0].Service != tt.service || !strings.Contains(items[0].Message, "service "+tt.service+" is package "+tt.want+" because")) {
				t.Errorf("item = %+v", items[0])
			}
		})
	}
	if len(wantLong) != 15 {
		t.Fatalf("test data: %q has %d characters", wantLong, len(wantLong))
	}
	_, items, _ := PackageNames(&compose.Project{}, []string{"redis_cache"})
	if want := "service redis_cache is package redis-cache because a tap device name is at most 15 characters, a-z0-9-"; items[0].Message != want {
		t.Errorf("message = %q, want %q", items[0].Message, want)
	}
	for _, bad := range []string{"", "---", "..."} {
		if _, _, err := PackageNames(&compose.Project{}, []string{bad}); err == nil {
			t.Errorf("%q gave no error", bad)
		}
	}
	_, _, err := PackageNames(&compose.Project{}, []string{"redis_cache", "redis-cache"})
	if err == nil || !strings.Contains(err.Error(), "redis_cache") || !strings.Contains(err.Error(), "redis-cache") {
		t.Errorf("error = %v, want both services named", err)
	}
	_, _, err = PackageNames(&compose.Project{}, []string{"dns", "svc-dns"})
	if err == nil {
		t.Error("dns and svc-dns collide and gave no error")
	}
}

func TestLongNamesStayApart(t *testing.T) {
	p := mustGen(t, newInput(&fake{}, svc("a-very-long-service-one"), svc("a-very-long-service-two")))
	a, b := p.Compose.Packages[0].Pkg, p.Compose.Packages[1].Pkg
	if a == b || len(a) != 15 || len(b) != 15 {
		t.Errorf("packages %q and %q", a, b)
	}
	want1(t, p, "a-very-long-service-one", plan.Note, "package name", "is package "+a)
}

func TestEnvironment(t *testing.T) {
	f := &fake{files: map[string]string{}, host: map[string]string{"G": "fromhost", "HOME": "/root"}}
	f.files["/work/app/env.list"] = "# comment\n\nB=file\nexport C=\"quoted\"\nD='sq ${X}'\nE=val # trailing\nF=${HOME}\nBARE\n"
	f.files["/abs/other.env"] = "\xef\xbb\xbfK=v\r\nL=w\r\n"
	s := svc("web")
	s.EnvFile = []string{"env.list", "/abs/other.env", "missing.env"}
	s.Environment = []compose.EnvVar{
		{Name: "B", Value: "env", Set: true},
		{Name: "G"},
		{Name: "H"},
		{Name: "I", Value: "${Y:-1}", Set: true},
		{Name: "J", Value: "${NOPE}/x", Set: true},
	}
	in := newInput(f, s)
	in.Services["web"].Observed.Env = []string{"A=img", "B=img", "PATH=/bin", "noequals"}
	p := mustGen(t, in)
	want := map[string]string{"A": "img", "PATH": "/bin", "B": "env", "C": "quoted", "D": "sq ${X}", "E": "val", "F": "/root", "G": "fromhost", "I": "1", "J": "/x", "K": "v", "L": "w"}
	if got := p.Services[0].Manifest.Env; !reflect.DeepEqual(got, want) {
		t.Errorf("Env = %v, want %v", got, want)
	}
	want1(t, p, "web", plan.Note, "environment.H", "not set")
	want1(t, p, "web", plan.Note, "environment.J", "NOPE")
	want1(t, p, "web", plan.Note, "env_file[0]", "BARE")
	want1(t, p, "web", plan.Unsupported, "env_file[2]", "missing.env")
	wantNone(t, p, "web", plan.Unsupported, "environment.I")
	wantNone(t, p, "web", plan.Note, "environment.I")
	wantNone(t, p, "web", plan.Note, "environment.G")
	wantNone(t, p, "web", plan.Unsupported, "env_file[1]")
	wantNone(t, p, "web", plan.Note, "env_file[1]")
}

func TestServiceEnvIsOneSource(t *testing.T) {
	f := &fake{files: map[string]string{"/d/a.env": "A=file\nB=${H}\nQ=\"x y\"\n", "/d/b.env": "A=second\nBAD LINE\n"}, host: map[string]string{"H": "host"}}
	s := svc("web")
	s.EnvFile = []string{"a.env"}
	s.Environment = []compose.EnvVar{{Name: "A", Value: "env", Set: true}, {Name: "C", Value: "$$H ${H}", Set: true}}
	got, items := ServiceEnv(s, "/d", f.lookup, f.read)
	want := map[string]string{"A": "env", "B": "host", "Q": "x y", "C": "$H host"}
	if !reflect.DeepEqual(got, want) || len(items) != 0 {
		t.Errorf("env = %v, items = %+v; want %v and no items", got, items, want)
	}
	s.EnvFile = []string{"b.env"}
	s.Environment = nil
	got, items = ServiceEnv(s, "/d", f.lookup, f.read)
	if len(got) != 0 || len(items) != 1 || items[0].Kind != plan.Unsupported || items[0].Key != "env_file[0]" || items[0].Service != "web" || !strings.Contains(items[0].Message, "line 2") {
		t.Errorf("env = %v, items = %+v; want an unsupported item for line 2 and nothing set", got, items)
	}
	s.EnvFile = nil
	s.Environment = []compose.EnvVar{{Name: "R", Value: "${NOPE:?need NOPE}", Set: true}}
	got, items = ServiceEnv(s, "/d", f.lookup, f.read)
	if _, set := got["R"]; set || len(items) != 1 || items[0].Kind != plan.Unsupported || items[0].Key != "environment.R" {
		t.Errorf("env = %v, items = %+v", got, items)
	}
}

func TestEnvNeverShowsValues(t *testing.T) {
	f := &fake{files: map[string]string{"/work/app/e.env": "P=${NOPE}s3cretfile\n"}}
	web, db := svc("web"), svc("db")
	web.EnvFile = []string{"e.env"}
	web.Environment = []compose.EnvVar{{Name: "URL", Value: "postgres://u:s3cret@db/x", Set: true}, {Name: "U", Value: "${GONE}s3cret", Set: true}}
	web.Command = compose.Command{Shell: "run --requirepass s3cretpw", Set: true}
	web.Healthcheck = &compose.Healthcheck{Test: compose.Command{Shell: "redis-cli -a s3cretpw ping", Set: true}}
	in := newInput(f, db, web)
	in.Services["web"].Observed.Argv = []string{"/bin/web", "--requirepass", "s3cretpw"}
	p := mustGen(t, in)
	out := Render(p.Report)
	for _, bad := range []string{"s3cret", "requirepass", "postgres://"} {
		if strings.Contains(out, bad) {
			t.Errorf("Render holds %q:\n%s", bad, out)
		}
	}
	want1(t, p, "web", plan.Observed, "program", "program /bin/web and 2 arguments")
	want1(t, p, "web", plan.Unsupported, "healthcheck", "starts with redis-cli and has 4 words")
}

func TestEnvironmentEmptyIsNil(t *testing.T) {
	_, got := one(t, &fake{}, svc("web"))
	if got.Manifest.Env != nil {
		t.Errorf("Env = %v, want nil", got.Manifest.Env)
	}
}

func TestPorts(t *testing.T) {
	const reach = "reachable at web.service and at the address of the instance on the ops0 bridge; not forwarded to the host"
	tests := []struct {
		name  string
		port  compose.Port
		tcp   []string
		udp   []string
		items map[plan.Kind]string
	}{
		{"tcp", compose.Port{Published: "80", Target: "80", Protocol: "tcp"}, []string{"80"}, nil, map[plan.Kind]string{plan.Approximated: reach}},
		{"empty protocol is tcp", compose.Port{Published: "80", Target: "80"}, []string{"80"}, nil, map[plan.Kind]string{plan.Approximated: reach}},
		{"udp", compose.Port{Published: "53", Target: "53", Protocol: "udp"}, nil, []string{"53"}, map[plan.Kind]string{plan.Approximated: reach}},
		{"range", compose.Port{Published: "8000-8010", Target: "8000-8010", Protocol: "tcp"}, nil, nil, map[plan.Kind]string{plan.Unsupported: "port range 8000-8010:8000-8010 is not supported"}},
		{"range on the host side only", compose.Port{Published: "8000-8010", Target: "80", Protocol: "tcp"}, nil, nil, map[plan.Kind]string{plan.Unsupported: "port range"}},
		{"remapped", compose.Port{Published: "8080", Target: "80", Protocol: "tcp"}, []string{"80"}, nil, map[plan.Kind]string{plan.Unsupported: "the port mapping 8080:80 is not applied: the service is reachable on the container port 80, on the bridge ops0 at web.service and at the address of the instance; the published port 8080 does not exist (ops does not forward ports in compose mode)", plan.Approximated: reach}},
		{"not published", compose.Port{Target: "80", Protocol: "tcp"}, nil, nil, map[plan.Kind]string{plan.Note: "only from other services"}},
		{"sctp", compose.Port{Published: "80", Target: "80", Protocol: "sctp"}, nil, nil, map[plan.Kind]string{plan.Unsupported: "sctp"}},
		{"variable", compose.Port{Published: "${P}", Target: "80", Protocol: "tcp"}, nil, nil, map[plan.Kind]string{plan.Unsupported: "variable"}},
		{"variable in the target only", compose.Port{Published: "80", Target: "${P}", Protocol: "tcp"}, nil, nil, map[plan.Kind]string{plan.Unsupported: "the port holds a variable, which is not interpolated; it is not published"}},
		{"range on the target side only", compose.Port{Published: "80", Target: "8000-8010", Protocol: "tcp"}, nil, nil, map[plan.Kind]string{plan.Unsupported: "the port range 80:8000-8010 is not supported"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := svc("web")
			s.Ports = []compose.Port{tt.port}
			p, got := one(t, &fake{}, s)
			rc := got.Manifest.RunConfig
			if !reflect.DeepEqual(rc.Ports, tt.tcp) || !reflect.DeepEqual(rc.UDPPorts, tt.udp) {
				t.Errorf("Ports = %v, UDPPorts = %v, want %v and %v", rc.Ports, rc.UDPPorts, tt.tcp, tt.udp)
			}
			for _, k := range []plan.Kind{plan.Note, plan.Approximated, plan.Unsupported} {
				if substr, ok := tt.items[k]; ok {
					want1(t, p, "web", k, "ports[0]", substr)
				} else {
					wantNone(t, p, "web", k, "ports[0]")
				}
			}
		})
	}
}

func TestPortsTwoKindsAtOnce(t *testing.T) {
	s := svc("web")
	s.Ports = []compose.Port{{HostIP: "10.0.0.1", Published: "8080", Target: "80", Protocol: "tcp"}}
	p, got := one(t, &fake{}, s)
	if !reflect.DeepEqual(got.Manifest.RunConfig.Ports, []string{"80"}) {
		t.Errorf("Ports = %v", got.Manifest.RunConfig.Ports)
	}
	un := itemsOf(p, "web", plan.Unsupported, "ports[0]")
	if len(un) != 2 || !strings.Contains(un[0].Message, "10.0.0.1") || !strings.Contains(un[1].Message, "container port 80") {
		t.Errorf("unsupported items = %+v, want the host address and the remap", un)
	}
	want1(t, p, "web", plan.Approximated, "ports[0]", "not forwarded to the host")
}

func TestPortMessagesNameTheContainerPortAndTheProtocol(t *testing.T) {
	tests := []struct {
		name string
		port compose.Port
		kind plan.Kind
		msg  string
	}{
		{"unsupported protocol", compose.Port{Published: "8080", Target: "80", Protocol: "sctp"}, plan.Unsupported, "the protocol sctp is not supported by ops; the port 80 is not published"},
		{"not published", compose.Port{Target: "9090", Protocol: "udp"}, plan.Note, "port 9090/udp is not published; it is reachable only from other services"},
		{"not published, no protocol", compose.Port{Target: "9090"}, plan.Note, "port 9090/tcp is not published; it is reachable only from other services"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := svc("web")
			s.Ports = []compose.Port{tt.port}
			p, _ := one(t, &fake{}, s)
			got := itemsOf(p, "web", tt.kind, "ports[0]")
			if len(got) != 1 || got[0].Message != tt.msg {
				t.Errorf("items = %+v, want one with the message %q", got, tt.msg)
			}
		})
	}
}

func TestExposeIsNotPublished(t *testing.T) {
	s := svc("web")
	s.Expose = []string{"9000"}
	in := newInput(&fake{}, s)
	in.Services["web"] = ServiceInput{Observed: observed("web"), Image: &docker.ImageConfig{ExposedPorts: []string{"80/tcp"}}}
	p := mustGen(t, in)
	want1(t, p, "web", plan.Note, "expose", "9000, 80/tcp")
	if rc := p.Services[0].Manifest.RunConfig; len(rc.Ports) != 0 || len(rc.UDPPorts) != 0 {
		t.Errorf("RunConfig = %+v, want no ports", rc)
	}
	p, _ = one(t, &fake{}, svc("web"))
	wantNone(t, p, "web", plan.Note, "expose")
}

func TestVolumes(t *testing.T) {
	f := &fake{dirs: map[string]bool{"/work/app/data": true}, files: map[string]string{"/etc/app.conf": "x"}}
	tests := []struct {
		name   string
		vol    compose.Volume
		kind   plan.Kind
		substr string
	}{
		{"relative directory", compose.Volume{Type: "bind", Source: "./data", Target: "/data"}, plan.Approximated, "only the files the program opened"},
		{"absolute file read only", compose.Volume{Type: "bind", Source: "/etc/app.conf", Target: "/app.conf", ReadOnly: true}, plan.Approximated, "read-only"},
		{"bind options", compose.Volume{Type: "bind", Source: "./data", Target: "/data", Options: "z"}, plan.Approximated, "options z are ignored"},
		{"missing path", compose.Volume{Type: "bind", Source: "./nope", Target: "/n"}, plan.Unsupported, "/work/app/nope"},
		{"home directory", compose.Volume{Type: "bind", Source: "~/x", Target: "/x"}, plan.Unsupported, "starts with ~"},
		{"colon in the source", compose.Volume{Type: "bind", Source: "/data:x", Target: "/x"}, plan.Unsupported, "holds ':'"},
		{"named", compose.Volume{Type: "volume", Source: "db", Target: "/var/lib/db"}, plan.Unsupported, "named volume db"},
		{"anonymous", compose.Volume{Type: "volume", Target: "/cache"}, plan.Unsupported, "anonymous volume"},
		{"tmpfs", compose.Volume{Type: "tmpfs", Target: "/run"}, plan.Unsupported, "tmpfs at /run is not supported"},
		{"unresolved", compose.Volume{Type: "unresolved", Source: "${D}/x", Target: "/x"}, plan.Unsupported, "variable"},
		{"variable in a bind", compose.Volume{Type: "bind", Source: "${D}/x", Target: "/x"}, plan.Unsupported, "variable"},
		{"other type", compose.Volume{Type: "npipe", Source: "p", Target: "/p"}, plan.Unsupported, "npipe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := svc("web")
			s.Volumes = []compose.Volume{tt.vol}
			p, got := one(t, f, s)
			if got.Manifest.MapDirs != nil {
				t.Errorf("MapDirs = %v, want none", got.Manifest.MapDirs)
			}
			want1(t, p, "web", tt.kind, "volumes[0]", tt.substr)
		})
	}
}

func TestVolumesAndImageVolumes(t *testing.T) {
	f := &fake{dirs: map[string]bool{"/work/app/data": true}}
	s := svc("web")
	s.Volumes = []compose.Volume{{Type: "bind", Source: "./data", Target: "/a"}}
	in := newInput(f, s)
	in.Services["web"] = ServiceInput{Observed: observed("web"), Image: &docker.ImageConfig{Volumes: []string{"/var/lib/x"}}}
	p := mustGen(t, in)
	if got := p.Services[0].Manifest.MapDirs; got != nil {
		t.Errorf("MapDirs = %v, want none", got)
	}
	want1(t, p, "web", plan.Approximated, "volumes[0]", "/a")
	want1(t, p, "web", plan.Note, "volumes (image)", "/var/lib/x")
}

func TestRestart(t *testing.T) {
	tests := []struct {
		restart string
		reboot  bool
		kind    plan.Kind
	}{
		{"", false, ""},
		{"no", false, ""},
		{"always", true, plan.Approximated},
		{"unless-stopped", true, plan.Approximated},
		{"on-failure", true, plan.Approximated},
		{"on-failure:3", true, plan.Approximated},
		{"sometimes", false, plan.Unsupported},
	}
	for _, tt := range tests {
		t.Run(tt.restart, func(t *testing.T) {
			s := svc("web")
			s.Restart = tt.restart
			p, got := one(t, &fake{}, s)
			if got.Manifest.RebootOnExit != tt.reboot {
				t.Errorf("RebootOnExit = %v, want %v", got.Manifest.RebootOnExit, tt.reboot)
			}
			if tt.kind == "" {
				wantNone(t, p, "web", plan.Approximated, "restart")
				wantNone(t, p, "web", plan.Unsupported, "restart")
				return
			}
			want1(t, p, "web", tt.kind, "restart", tt.restart)
		})
	}
}

func TestDependsOnConditions(t *testing.T) {
	db, web := svc("db"), svc("web")
	web.DependsOn = []compose.Dependency{{Service: "db", Condition: "service_healthy"}}
	p := mustGen(t, newInput(&fake{}, db, web))
	want1(t, p, "web", plan.Approximated, "depends_on.db", "service_healthy")

	web.DependsOn = []compose.Dependency{{Service: "db", Condition: "service_started"}}
	p = mustGen(t, newInput(&fake{}, db, web))
	wantNone(t, p, "web", plan.Approximated, "depends_on.db")
}

func TestHealthcheck(t *testing.T) {
	s := svc("web")
	s.Healthcheck = &compose.Healthcheck{Test: compose.Command{Exec: []string{"CMD", "curl", "-f", "http://localhost"}, Set: true}}
	p, _ := one(t, &fake{}, s)
	want1(t, p, "web", plan.Unsupported, "healthcheck", "starts with curl and has 3 words")

	s.Healthcheck = &compose.Healthcheck{Test: compose.Command{Shell: "pg_isready -U x", Set: true}}
	p, _ = one(t, &fake{}, s)
	want1(t, p, "web", plan.Unsupported, "healthcheck", "starts with pg_isready and has 3 words")

	s.Healthcheck = &compose.Healthcheck{Disable: true}
	p, _ = one(t, &fake{}, s)
	wantNone(t, p, "web", plan.Unsupported, "healthcheck")

	plain := svc("web")
	in := newInput(&fake{}, plain)
	in.Services["web"] = ServiceInput{Observed: observed("web"), Stage: &dockerfile.Stage{Healthcheck: &dockerfile.Healthcheck{Test: dockerfile.Command{Shell: "wget -q localhost", Set: true}}}}
	p = mustGen(t, in)
	want1(t, p, "web", plan.Unsupported, "healthcheck", "the HEALTHCHECK of the Dockerfile")
	if m := itemsOf(p, "web", plan.Unsupported, "healthcheck")[0].Message; strings.Contains(m, "localhost") || !strings.Contains(m, "starts with wget and has 3 words") {
		t.Errorf("message = %q", m)
	}
}

func TestNetworks(t *testing.T) {
	s := svc("web")
	s.Networks = []string{"front", "default"}
	s.NetworkMode = "host"
	p, _ := one(t, &fake{}, s)
	want1(t, p, "web", plan.Unsupported, "networks", "front")
	want1(t, p, "web", plan.Unsupported, "network_mode", "host")
	if m := itemsOf(p, "web", plan.Unsupported, "networks")[0].Message; strings.Contains(m, "default") {
		t.Errorf("message %q lists the default network", m)
	}

	s.Networks = []string{"default"}
	s.NetworkMode = ""
	p, _ = one(t, &fake{}, s)
	wantNone(t, p, "web", plan.Unsupported, "networks")
	wantNone(t, p, "web", plan.Unsupported, "network_mode")
}

func TestServiceNamesNote(t *testing.T) {
	web, db := svc("web"), svc("db")
	web.Environment = []compose.EnvVar{{Name: "DB_HOST", Value: "db", Set: true}, {Name: "URL", Value: "postgres://db:5432/x", Set: true}, {Name: "SELF", Value: "web", Set: true}, {Name: "OTHER", Value: "dbx", Set: true}}
	p := mustGen(t, newInput(&fake{}, db, web))
	want1(t, p, "", plan.Note, "service names", "db -> db.service, web -> web.service")
	msg := itemsOf(p, "", plan.Note, "service names")[0].Message
	want := "a service reaches another as <pkg>.service, not by its compose name: db -> db.service, web -> web.service. Environment variables whose value names another service were not rewritten: web: DB_HOST, URL."
	if msg != want {
		t.Errorf("message = %q, want %q", msg, want)
	}
	for _, bad := range []string{"SELF", "OTHER", "postgres", "5432", "DB_HOST=", "=db"} {
		if strings.Contains(msg, bad) {
			t.Errorf("message %q holds %s", msg, bad)
		}
	}
	if got := p.Services[0].Manifest.Env["DB_HOST"]; got != "" {
		t.Errorf("db has DB_HOST = %q", got)
	}
	if got := p.Services[1].Manifest.Env["DB_HOST"]; got != "db" {
		t.Errorf("DB_HOST = %q, the value must not be rewritten", got)
	}

	p, _ = one(t, &fake{}, web)
	wantNone(t, p, "", plan.Note, "service names")
}

func TestEnvironmentSharedNote(t *testing.T) {
	web, db, cache := svc("web"), svc("db"), svc("cache")
	web.Environment = []compose.EnvVar{{Name: "B_VAR", Value: "s3cret", Set: true}, {Name: "A_VAR", Value: "v", Set: true}}
	cache.Environment = []compose.EnvVar{{Name: "C_VAR", Value: "v", Set: true}}
	p := mustGen(t, newInput(&fake{}, db, web, cache))
	want := "ops compose reads all packages into one configuration: environment variables of one service stay set for the services after it. Variables per service: web: A_VAR, B_VAR; cache: C_VAR."
	want1(t, p, "", plan.Note, "environment", want)
	if m := itemsOf(p, "", plan.Note, "environment")[0].Message; m != want {
		t.Errorf("message = %q, want %q", m, want)
	}
	p, _ = one(t, &fake{}, web)
	wantNone(t, p, "", plan.Note, "environment")
	p = mustGen(t, newInput(&fake{}, svc("a"), svc("b")))
	wantNone(t, p, "", plan.Note, "environment")
}

func TestUserAndWorkingDir(t *testing.T) {
	tests := []struct {
		name       string
		user, wd   string
		obsWD      string
		wantUser   bool
		wantWorkDr string
	}{
		{"nothing", "", "", "/", false, ""},
		{"root", "root", "/", "/", false, ""},
		{"zero with group", "0:0", "/", "/", false, ""},
		{"other user", "app", "/", "/", true, ""},
		{"numeric user and group", "1000:1000", "/", "/", true, ""},
		{"same directory", "", "/", "/", false, ""},
		{"other directory", "", "/srv", "/", false, "working_dir /srv of the file is not applied: the program was observed in /, and cwd / is set"},
		{"observed directory not known", "", "", "", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := svc("web")
			s.User, s.WorkingDir = tt.user, tt.wd
			in := newInput(&fake{}, s)
			in.Services["web"].Observed.WorkingDir = tt.obsWD
			p := mustGen(t, in)
			if tt.wantUser {
				want1(t, p, "web", plan.Unsupported, "user", tt.user)
			} else {
				wantNone(t, p, "web", plan.Unsupported, "user")
			}
			if tt.wantWorkDr != "" {
				want1(t, p, "web", plan.Unsupported, "working_dir", tt.wantWorkDr)
			} else {
				wantNone(t, p, "web", plan.Unsupported, "working_dir")
			}
		})
	}
	s := svc("web")
	s.WorkingDir = "/other"
	in := newInput(&fake{}, s)
	in.Services["web"].Observed.WorkingDir = "/app"
	p := mustGen(t, in)
	want1(t, p, "web", plan.Unsupported, "working_dir", "working_dir /other of the file is not applied")
}

func TestWorkingDirIsInTheManifest(t *testing.T) {
	tests := []struct {
		name      string
		file, obs string
		wantCwd   string
		kind      plan.Kind
		msg       string
	}{
		{"observed from the image", "", "/app", "/app", plan.Approximated, "cwd /app is set in the package manifest"},
		{"file and observation agree", "/app", "/app", "/app", plan.Converted, "cwd /app is set in the package manifest"},
		{"trailing slash is the same directory", "/app/", "/app", "/app", plan.Converted, "cwd /app is set"},
		{"observed with a trailing slash is the same directory", "/app", "/app/", "/app/", plan.Converted, "cwd /app/ is set"},
		{"root", "", "/", "/", "", ""},
		{"not known", "", "", "/", "", ""},
		{"file differs", "/srv", "/app", "/app", plan.Unsupported, "working_dir /srv of the file is not applied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := svc("web")
			s.WorkingDir = tt.file
			in := newInput(&fake{}, s)
			in.Services["web"].Observed.WorkingDir = tt.obs
			p := mustGen(t, in)
			want := map[string]any{"cwd": tt.wantCwd}
			if got := p.Services[0].Manifest.ManifestPassthrough; !reflect.DeepEqual(got, want) {
				t.Errorf("ManifestPassthrough = %v, want %v", got, want)
			}
			for _, k := range []plan.Kind{plan.Converted, plan.Approximated, plan.Unsupported} {
				if k == tt.kind {
					want1(t, p, "web", k, "working_dir", tt.msg)
				} else {
					wantNone(t, p, "web", k, "working_dir")
				}
			}
		})
	}
}

// Every service gets its own cwd, "/" included: ops compose merges the maps of
// all the manifests, so a service without one would inherit the previous cwd.
func TestEveryServiceHasACwd(t *testing.T) {
	in := newInput(&fake{}, svc("a"), svc("b"))
	in.Services["a"].Observed.WorkingDir = "/app"
	p := mustGen(t, in)
	if len(p.Services) != 2 {
		t.Fatalf("services = %d", len(p.Services))
	}
	if got := p.Services[0].Manifest.ManifestPassthrough["cwd"]; got != "/app" {
		t.Errorf("a: cwd = %v", got)
	}
	if got := p.Services[1].Manifest.ManifestPassthrough["cwd"]; got != "/" {
		t.Errorf("b: cwd = %v, want /", got)
	}
}

func TestArchIsChecked(t *testing.T) {
	for _, arch := range []string{"arm", "386", "riscv64", "x86_64", "ARM64"} {
		in := newInput(&fake{}, svc("web"))
		in.Arch = arch
		_, err := Generate(in)
		if err == nil || !strings.Contains(err.Error(), `"`+arch+`"`) {
			t.Errorf("Arch %q: err = %v, want an error naming the value", arch, err)
		}
	}
	for _, arch := range []string{"amd64", "arm64"} {
		in := newInput(&fake{}, svc("web"))
		in.Arch = arch
		p, err := Generate(in)
		if err != nil || p.Arch != arch || p.Compose.Packages[0].Arch != arch {
			t.Errorf("Arch %q: err = %v", arch, err)
		}
	}
}

func TestStoppedEarlyIsReported(t *testing.T) {
	in := newInput(&fake{}, svc("web"))
	o := in.Services["web"].Observed
	o.StoppedEarly, o.ExitCode = true, 3
	o.StoppedLog = "\nfirst line\nsecond line\n  third line\nfourth line\n"
	p := mustGen(t, in)
	want1(t, p, "web", plan.Unsupported, "observation", "stopped with status 3 before the end of the observation")
	it := itemsOf(p, "web", plan.Unsupported, "observation")[0]
	if !strings.Contains(it.Message, "Log: first line | second line | third line") || strings.Contains(it.Message, "fourth") {
		t.Errorf("message = %q, want the first 3 lines of the log", it.Message)
	}
	if !strings.Contains(it.Message, "the program chosen is the last one it started") {
		t.Errorf("message = %q", it.Message)
	}
	// A normal observation has no such item.
	p = mustGen(t, newInput(&fake{}, svc("web")))
	wantNone(t, p, "web", plan.Unsupported, "observation")
}

func TestStoppedEarlyWithoutALogHasNoLogSuffix(t *testing.T) {
	const want = "the container stopped with status 3 before the end of the observation, so what was observed is not a running server; the program chosen is the last one it started"
	for _, log := range []string{"", " \n\t\n"} {
		in := newInput(&fake{}, svc("web"))
		o := in.Services["web"].Observed
		o.StoppedEarly, o.ExitCode, o.StoppedLog = true, 3, log
		p := mustGen(t, in)
		got := itemsOf(p, "web", plan.Unsupported, "observation")
		if len(got) != 1 || got[0].Message != want {
			t.Errorf("log %q: items = %+v, want one with the message %q", log, got, want)
		}
	}
}

func TestUnknownIsReported(t *testing.T) {
	web := svc("web")
	web.Unknown = []string{"privileged", "deploy.resources"}
	in := newInput(&fake{}, web)
	in.Project.Unknown = []string{"configs"}
	in.Services["web"] = ServiceInput{Observed: observed("web"), Stage: &dockerfile.Stage{Unknown: []string{"line 3: ONBUILD"}}}
	p := mustGen(t, in)
	want1(t, p, "web", plan.Unsupported, "privileged", "not converted")
	want1(t, p, "web", plan.Unsupported, "deploy.resources", "not converted")
	want1(t, p, "web", plan.Unsupported, "dockerfile", "line 3: ONBUILD")
	want1(t, p, "", plan.Unsupported, "configs", "not converted")
}

func TestObservedItems(t *testing.T) {
	web := svc("web")
	in := newInput(&fake{}, web)
	o := in.Services["web"].Observed
	o.Warnings = []string{"could not carry /proc/x"}
	o.CreatedAtRuntime = []string{"/run/web.pid"}
	for i := 0; i < 25; i++ {
		o.ConfigurationFiles = append(o.ConfigurationFiles, fmt.Sprintf("/etc/f%02d", i))
	}
	p := mustGen(t, in)
	want1(t, p, "web", plan.Note, "observe", "could not carry /proc/x")
	want1(t, p, "web", plan.Observed, "created at runtime", "/run/web.pid")
	want1(t, p, "web", plan.Observed, "configuration files", "/etc/f19 and 5 more")
	msg := itemsOf(p, "web", plan.Observed, "configuration files")[0].Message
	if strings.Contains(msg, "/etc/f20") {
		t.Errorf("message %q lists more than 20 files", msg)
	}

	p, _ = one(t, &fake{}, web)
	wantNone(t, p, "web", plan.Observed, "configuration files")
	wantNone(t, p, "web", plan.Observed, "created at runtime")
}

func TestVanishedFilesAreANote(t *testing.T) {
	web := svc("web")
	in := newInput(&fake{}, web)
	in.Services["web"].Observed.Vanished = 3
	p := mustGen(t, in)
	want1(t, p, "web", plan.Note, "vanished", "3 files that the program created while it ran were gone when the observation ended (temporary files); they are not in the package")

	p, _ = one(t, &fake{}, web)
	wantNone(t, p, "web", plan.Note, "vanished")
}

func TestObservedUserIsSetInTheManifest(t *testing.T) {
	web := svc("web")
	in := newInput(&fake{}, web)
	in.Services["web"].Observed.UID, in.Services["web"].Observed.GID = 999, 998
	p := mustGen(t, in)
	got := itemsOf(p, "web", plan.Approximated, "user")
	if len(got) != 1 {
		t.Fatalf("got %d items, want 1; report: %+v", len(got), p.Report)
	}
	const want = "the program ran as uid 999 (gid 998) while it was observed; uid and gid are set in the package manifest, which a nanos that reads them from the manifest honours (nanovms/nanos#2189, in master since 2026-10-31, not in release 0.1.56); an older nanos runs the program as root, and a program that refuses to run as root, or that depends on who owns its files, may not work"
	if got[0].Message != want {
		t.Errorf("message %q, want %q", got[0].Message, want)
	}
	wantNone(t, p, "web", plan.Unsupported, "user")
	pass := p.Services[0].Manifest.ManifestPassthrough
	if pass["uid"] != "999" || pass["gid"] != "998" {
		t.Errorf("passthrough = %v, want uid \"999\" and gid \"998\" (strings: ops cannot encode numbers)", pass)
	}
}

func TestRootOrUnknownUserIsNotSetInTheManifest(t *testing.T) {
	for _, id := range []int{0, -1} {
		web := svc("web")
		in := newInput(&fake{}, web)
		in.Services["web"].Observed.UID, in.Services["web"].Observed.GID = id, id
		p := mustGen(t, in)
		pass := p.Services[0].Manifest.ManifestPassthrough
		if _, has := pass["uid"]; has {
			t.Errorf("uid %d: passthrough = %v", id, pass)
		}
		if _, has := pass["gid"]; has {
			t.Errorf("gid %d: passthrough = %v", id, pass)
		}
	}
}

func TestObservedUserRootOrUnknownHasNoItem(t *testing.T) {
	for _, id := range []int{0, -1} {
		web := svc("web")
		in := newInput(&fake{}, web)
		in.Services["web"].Observed.UID, in.Services["web"].Observed.GID = id, id
		p := mustGen(t, in)
		wantNone(t, p, "web", plan.Unsupported, "user")
	}
}

func TestObservedUserWithComposeUserIsOneItem(t *testing.T) {
	web := svc("web")
	web.User = "app"
	in := newInput(&fake{}, web)
	in.Services["web"].Observed.UID, in.Services["web"].Observed.GID = 999, 999
	p := mustGen(t, in)
	// the observed user is what the manifest says; the file's user is not a second item
	wantNone(t, p, "web", plan.Unsupported, "user")
	want1(t, p, "web", plan.Approximated, "user", "the program ran as uid 999")
}

func TestChildProcessesAreUnsupported(t *testing.T) {
	web := svc("web")
	in := newInput(&fake{}, web)
	in.Services["web"].Observed.Children = 3
	in.Services["web"].Observed.ChildCalls = []string{"fork", "clone"}
	p := mustGen(t, in)
	got := itemsOf(p, "web", plan.Unsupported, "processes")
	if len(got) != 1 {
		t.Fatalf("got %d items, want 1; report: %+v", len(got), p.Report)
	}
	const prefix = "the program started 3 child processes while it was observed (fork/clone); nanos runs a single process and has no fork, so the program may not start or may not work; threads are fine."
	if !strings.HasPrefix(got[0].Message, prefix) {
		t.Errorf("message %q does not start with %q", got[0].Message, prefix)
	}
	if !strings.Contains(got[0].Message, "run in one process") {
		t.Errorf("message %q does not point to the one-process option", got[0].Message)
	}

	p, _ = one(t, &fake{}, web)
	wantNone(t, p, "web", plan.Unsupported, "processes")
}

func TestLauncherProgramIsUnsupportedWithTheScript(t *testing.T) {
	sysroot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sysroot, "usr/src/app"), 0755); err != nil {
		t.Fatal(err)
	}
	pkg := `{"name":"x","scripts":{"start":"react-scripts start","dev":"nodemon index.js"}}`
	if err := os.WriteFile(filepath.Join(sysroot, "usr/src/app/package.json"), []byte(pkg), 0644); err != nil {
		t.Fatal(err)
	}
	web := svc("web")
	in := newInput(&fake{}, web)
	o := in.Services["web"].Observed
	o.Sysroot, o.WorkingDir = sysroot, "/usr/src/app"
	o.Program = "/usr/local/bin/node"
	o.Argv = []string{"node", "/usr/local/bin/npm", "run", "dev"}
	p := mustGen(t, in)
	got := itemsOf(p, "web", plan.Unsupported, "launcher")
	if len(got) != 1 {
		t.Fatalf("got %d items, want 1; report: %+v", len(got), p.Report)
	}
	for _, want := range []string{"the observed program is npm", "cannot run on nanos as it is", `script "dev"`, "read its command in package.json"} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("message %q does not hold %q", got[0].Message, want)
		}
	}
	// the files of the user are not touched
	data, err := os.ReadFile(filepath.Join(sysroot, "usr/src/app/package.json"))
	if err != nil || string(data) != pkg {
		t.Errorf("package.json changed: %q, %v", data, err)
	}
}

func TestLauncherProgramWithoutReadableScript(t *testing.T) {
	web := svc("web")
	in := newInput(&fake{}, web)
	o := in.Services["web"].Observed
	o.Program = "/bin/sh"
	o.Argv = []string{"sh", "-c", "yarn start"}
	p := mustGen(t, in)
	want1(t, p, "web", plan.Unsupported, "launcher", "the observed program runs yarn")
	want1(t, p, "web", plan.Unsupported, "launcher", "could not be read")
}

func TestNotALauncherHasNoLauncherItem(t *testing.T) {
	web := svc("web")
	in := newInput(&fake{}, web)
	o := in.Services["web"].Observed
	o.Program = "/usr/local/bin/node"
	o.Argv = []string{"node", "server.js"}
	p := mustGen(t, in)
	wantNone(t, p, "web", plan.Unsupported, "launcher")
}

// launcherInput makes a service whose observed program is argv, with package.json holding pkg (not
// written when pkg is empty) in /usr/src/app of a sysroot. It returns the input and the sysroot.
func launcherInput(t *testing.T, program string, argv []string, pkg string) (Input, string) {
	t.Helper()
	sysroot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sysroot, "usr/src/app"), 0755); err != nil {
		t.Fatal(err)
	}
	if pkg != "" {
		if err := os.WriteFile(filepath.Join(sysroot, "usr/src/app/package.json"), []byte(pkg), 0644); err != nil {
			t.Fatal(err)
		}
	}
	in := newInput(&fake{}, svc("web"))
	o := in.Services["web"].Observed
	o.Sysroot, o.WorkingDir, o.Program, o.Argv = sysroot, "/usr/src/app", program, argv
	return in, sysroot
}

var npmRunDev = []string{"node", "/usr/local/bin/npm", "run", "dev"}

func TestLauncherPackageJSONThroughASymlinkOutOfTheSysrootIsNotRead(t *testing.T) {
	in, sysroot := launcherInput(t, "/usr/local/bin/node", npmRunDev, "")
	host := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(host, []byte(`{"scripts":{"dev":"HOST-SECRET"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(host, filepath.Join(sysroot, "usr/src/app/package.json")); err != nil {
		t.Fatal(err)
	}
	p := mustGen(t, in)
	got := itemsOf(p, "web", plan.Unsupported, "launcher")
	if len(got) != 1 || strings.Contains(got[0].Message, "HOST-SECRET") || !strings.Contains(got[0].Message, "could not be read") {
		t.Errorf("items %+v: the file of the host was read or the message is wrong", got)
	}
}

func TestLauncherPackageJSONThroughALinkInsideTheSysrootIsRead(t *testing.T) {
	in, sysroot := launcherInput(t, "/usr/local/bin/node", npmRunDev, "")
	if err := os.WriteFile(filepath.Join(sysroot, "usr/src/app/real.json"), []byte(`{"scripts":{"dev":"node a.js"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/src/app/real.json", filepath.Join(sysroot, "usr/src/app/package.json")); err != nil {
		t.Fatal(err)
	}
	p := mustGen(t, in)
	want1(t, p, "web", plan.Unsupported, "launcher", "read its command in package.json")
	wantNoText(t, p, "web", plan.Unsupported, "launcher", "could not be read")
	wantNoText(t, p, "web", plan.Unsupported, "launcher", "node a.js")
}

// npmStart is what the observation of awesome-compose nginx-nodejs-redis saw: npm start, which ran
// no script because the .npmrc of the image says ignore-scripts=true, and a container that stopped
// with status 0.
var npmStart = []string{"node", "/usr/local/bin/npm", "start"}

// npmIgnoreCase is a service whose observed program is argv (npmStart when empty) run by program
// (node when empty), with npmrc in its working directory (not written when empty).
type npmIgnoreCase struct {
	name     string
	npmrc    string
	program  string
	argv     []string
	stopped  bool
	code     int
	children int
}

func (c npmIgnoreCase) input(t *testing.T) Input {
	t.Helper()
	program, argv := c.program, c.argv
	if program == "" {
		program = "/usr/local/bin/node"
	}
	if argv == nil {
		argv = npmStart
	}
	in, sysroot := launcherInput(t, program, argv, `{"scripts":{"start":"node server.js"}}`)
	if c.npmrc != "" {
		if err := os.WriteFile(filepath.Join(sysroot, "usr/src/app/.npmrc"), []byte(c.npmrc), 0644); err != nil {
			t.Fatal(err)
		}
	}
	o := in.Services["web"].Observed
	o.StoppedEarly, o.ExitCode, o.Children = c.stopped, c.code, c.children
	return in
}

func TestLauncherNpmIgnoreScriptsExplainsTheExit(t *testing.T) {
	for _, tc := range []struct{ name, npmrc string }{
		{"plain", "# hardening\nignore-scripts = true\n"},
		{"semicolon, quote, crlf", "; c\nignore-scripts=\"TRUE\"\r\n"},
		{"last wins", "ignore-scripts=false\nignore-scripts=true"},
		{"one", "ignore-scripts=1\n"},
		{"yes", "ignore-scripts=yes\n"},
		{"bare key", "ignore-scripts\n"},
		{"comment after", "ignore-scripts=true ; on purpose\n"},
		{"single quotes", "ignore-scripts='true'\n"},
		{"after other keys", "registry=x\n\nignore-scripts=true\n"},
	} {
		p := mustGen(t, npmIgnoreCase{npmrc: tc.npmrc, stopped: true}.input(t))
		got := itemsOf(p, "web", plan.Unsupported, "launcher")
		if len(got) != 1 {
			t.Errorf("%s: %d launcher items", tc.name, len(got))
			continue
		}
		for _, want := range []string{"sets ignore-scripts=true", "the node program as its command (node <file>), which nanos needs anyway", "likely reason", "6.14.13", "not the fix"} {
			if !strings.Contains(got[0].Message, want) {
				t.Errorf("%s: message %q does not hold %q", tc.name, got[0].Message, want)
			}
		}
		if strings.Contains(got[0].Message, "run 4") || strings.Contains(got[0].Message, "of the fork") {
			t.Errorf("%s: the message names a private run: %q", tc.name, got[0].Message)
		}
		if strings.Contains(got[0].Message, "the image itself runs nothing") {
			t.Errorf("%s: the message still claims the image runs nothing", tc.name)
		}
	}
}

func TestLauncherNpmIgnoreScriptsOnlyWhenItFits(t *testing.T) {
	yes := "ignore-scripts=true\n"
	for _, c := range []npmIgnoreCase{
		{name: "false", npmrc: "ignore-scripts=false\n", stopped: true},
		{name: "commented hash", npmrc: "#ignore-scripts=true\n", stopped: true},
		{name: "commented semicolon", npmrc: ";ignore-scripts=true\n", stopped: true},
		{name: "turned off after", npmrc: yes + "ignore-scripts=false\n", stopped: true},
		{name: "turned off by 0", npmrc: yes + "ignore-scripts=0\n", stopped: true},
		{name: "other value", npmrc: "ignore-scripts=maybe\n", stopped: true},
		{name: "empty value", npmrc: "ignore-scripts=\n", stopped: true},
		{name: "quoted false", npmrc: "ignore-scripts=\"false\" ; true\n", stopped: true},
		{name: "in a section", npmrc: "[sec]\nignore-scripts=true\n", stopped: true},
		{name: "another key", npmrc: "my-ignore-scripts=true\n", stopped: true},
		{name: "no npmrc", stopped: true},
		{name: "still ran", npmrc: yes},
		{name: "status 1", npmrc: yes, stopped: true, code: 1},
		{name: "had children", npmrc: yes, stopped: true, children: 1},
		{name: "yarn", npmrc: yes, stopped: true, program: "/usr/local/bin/yarn", argv: []string{"yarn", "start"}},
		{name: "npm install", npmrc: yes, stopped: true, argv: []string{"node", "/usr/local/bin/npm", "install"}},
		{name: "prefix elsewhere", npmrc: yes, stopped: true, argv: []string{"node", "/usr/local/bin/npm", "--prefix", "/other", "start"}},
	} {
		p := mustGen(t, c.input(t))
		got := itemsOf(p, "web", plan.Unsupported, "launcher")
		if len(got) != 1 || strings.Contains(got[0].Message, "ignore-scripts") {
			t.Errorf("%s: items %+v: the ignore-scripts text must not be there", c.name, got)
		}
	}
}

func TestLauncherReadablePackageJSONWithoutTheScript(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", npmRunDev, `{"scripts":{"start":"node a.js"}}`)
	p := mustGen(t, in)
	want1(t, p, "web", plan.Unsupported, "launcher", `which package.json does not have`)
	got := itemsOf(p, "web", plan.Unsupported, "launcher")
	if strings.Contains(got[0].Message, "could not be read") {
		t.Errorf("message %q says the file could not be read", got[0].Message)
	}
}

func TestLauncherEmptyScriptIsNotQuoted(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", npmRunDev, `{"scripts":{"dev":""}}`)
	p := mustGen(t, in)
	want1(t, p, "web", plan.Unsupported, "launcher", `which package.json does not have`)
	if got := itemsOf(p, "web", plan.Unsupported, "launcher"); strings.Contains(got[0].Message, "whose command is") {
		t.Errorf("message %q quotes an empty script", got[0].Message)
	}
}

func TestLauncherInvalidPackageJSON(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", npmRunDev, `{"scripts": `)
	want1(t, mustGen(t, in), "web", plan.Unsupported, "launcher", "could not be read")
}

func TestLauncherPackageJSONOverTheLimitIsUnreadable(t *testing.T) {
	big := `{"scripts":{"dev":"node a.js"},"x":"` + strings.Repeat("a", maxImageFile) + `"}`
	in, _ := launcherInput(t, "/usr/local/bin/node", npmRunDev, big)
	want1(t, mustGen(t, in), "web", plan.Unsupported, "launcher", "could not be read")
}

func TestLauncherNpmStartWithoutStartScriptRunsServerJS(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "/usr/local/bin/npm", "start"}, `{"scripts":{"dev":"x"}}`)
	want1(t, mustGen(t, in), "web", plan.Unsupported, "launcher", "runs node server.js")
}

func TestLauncherScriptInAnotherPackageIsNotRead(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "/usr/local/bin/npm", "--prefix", "/other", "run", "dev"}, `{"scripts":{"dev":"WRONG-PACKAGE"}}`)
	p := mustGen(t, in)
	want1(t, p, "web", plan.Unsupported, "launcher", "of another package or directory")
	if got := itemsOf(p, "web", plan.Unsupported, "launcher"); strings.Contains(got[0].Message, "WRONG-PACKAGE") {
		t.Errorf("message %q quotes the package.json of the working directory", got[0].Message)
	}
}

func TestLauncherAdviceSaysWhatToDoWithSeveralCommands(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", npmRunDev, `{"scripts":{"dev":"node a.js"}}`)
	want1(t, mustGen(t, in), "web", plan.Unsupported, "launcher", "needs the same at each step")
}

func TestLauncherWithBusyboxProgram(t *testing.T) {
	in, _ := launcherInput(t, "/bin/busybox", []string{"sh", "-c", "npm start"}, `{"scripts":{"start":"node a.js"}}`)
	want1(t, mustGen(t, in), "web", plan.Unsupported, "launcher", "the observed program runs npm")
}

func TestLauncherNoteOnACompoundShellCommandIsNotAdded(t *testing.T) {
	in, _ := launcherInput(t, "/bin/sh", []string{"sh", "-c", "npm run build && node server.js"}, "")
	wantNone(t, mustGen(t, in), "web", plan.Unsupported, "launcher")
}

func TestLauncherProgramOfTheUserNamedNpmJSIsNotALauncher(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "/app/npm.js"}, "")
	wantNone(t, mustGen(t, in), "web", plan.Unsupported, "launcher")
}

func TestPassedOverLauncherIsANote(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "server.js"}, "")
	in.Services["web"].Observed.PassedOver = []string{`npm (script "dev")`}
	p := mustGen(t, in)
	want1(t, p, "web", plan.Note, "launcher passed over", `the commands passed over were npm (script "dev") (outermost first)`)
	want1(t, p, "web", plan.Note, "launcher passed over", "the package runs the command the launcher started (in package.manifest)")
	if got := itemsOf(p, "web", plan.Note, "launcher passed over"); strings.Contains(got[0].Message, "server.js") {
		t.Errorf("message %q prints the command line of the program", got[0].Message)
	}
	want1(t, p, "web", plan.Note, "launcher passed over", "npm_* variables")
	wantNone(t, p, "web", plan.Unsupported, "launcher")
	in2, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "server.js"}, "")
	wantNone(t, mustGen(t, in2), "web", plan.Note, "launcher passed over")
}

func TestProcessesItemPointsToTheLauncher(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", npmRunDev, "")
	in.Services["web"].Observed.Children = 2
	want1(t, mustGen(t, in), "web", plan.Unsupported, "processes", "see the launcher item")
	in2, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "server.js"}, "")
	in2.Services["web"].Observed.Children = 2
	p := mustGen(t, in2)
	if got := itemsOf(p, "web", plan.Unsupported, "processes"); len(got) != 1 || strings.Contains(got[0].Message, "launcher") {
		t.Errorf("items %+v: a program that is not a launcher points to one", got)
	}
}

func TestLauncherPackageJSONThroughAChainOfFortyOneLinksIsNotRead(t *testing.T) {
	in, sysroot := launcherInput(t, "/usr/local/bin/node", npmRunDev, "")
	host := t.TempDir()
	if err := os.WriteFile(filepath.Join(host, "package.json"), []byte(`{"scripts":{"dev":"HOST-SECRET"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 41; i++ {
		target := fmt.Sprintf("l%d", i+1)
		if i == 41 {
			target = host
		}
		if err := os.Symlink(target, filepath.Join(sysroot, fmt.Sprintf("l%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	in.Services["web"].Observed.WorkingDir = "/l1"
	p := mustGen(t, in)
	got := itemsOf(p, "web", plan.Unsupported, "launcher")
	if len(got) != 1 || strings.Contains(got[0].Message, "HOST-SECRET") {
		t.Errorf("items %+v: the file of the host was read", got)
	}
}

func TestLauncherMissingScriptTexts(t *testing.T) {
	tests := []struct {
		name    string
		argv    []string
		pkg     string
		want    string
		notWant string
	}{
		{"npm start", []string{"node", "/usr/local/bin/npm", "start"}, `{"scripts":{}}`, "if that file exists", ""},
		{"npm restart", []string{"node", "/usr/local/bin/npm", "restart"}, `{"scripts":{}}`, "stop and start scripts", "which package.json does not have"},
		{"yarn script", []string{"node", "/opt/yarn-v1.22.19/bin/yarn.js", "foo"}, `{"scripts":{}}`, "binary of that name from node_modules/.bin", ""},
		{"yarn start", []string{"node", "/opt/yarn-v1.22.19/bin/yarn.js", "start"}, `{"scripts":{}}`, "may run node server.js if that file exists", "binary of that name"},
		{"pnpm script", []string{"node", "/usr/local/lib/node_modules/pnpm/bin/pnpm.cjs", "foo"}, `{"scripts":{}}`, "falls back to running a command of that name (pnpm exec)", ""},
		{"pnpm run script", []string{"node", "/usr/local/lib/node_modules/pnpm/bin/pnpm.cjs", "run", "foo"}, `{"scripts":{}}`, "pnpm run fails for it", "pnpm exec"},
		{"pnpm start", []string{"node", "/usr/local/lib/node_modules/pnpm/bin/pnpm.cjs", "start"}, `{"scripts":{}}`, "may run node server.js if that file exists", "binary of that name"},
		{"option value hint", []string{"node", "/usr/local/bin/npm", "run", "dev"}, `{"scripts":{}}`, "value of an option", ""},
	}
	for _, tt := range tests {
		in, _ := launcherInput(t, "/usr/local/bin/node", tt.argv, tt.pkg)
		p := mustGen(t, in)
		got := itemsOf(p, "web", plan.Unsupported, "launcher")
		if len(got) != 1 {
			t.Fatalf("%s: got %d items", tt.name, len(got))
		}
		if !strings.Contains(got[0].Message, tt.want) {
			t.Errorf("%s: message %q does not hold %q", tt.name, got[0].Message, tt.want)
		}
		if tt.notWant != "" && strings.Contains(got[0].Message, tt.notWant) {
			t.Errorf("%s: message %q holds %q", tt.name, got[0].Message, tt.notWant)
		}
		// every missing script but the restart of npm says that the word may be the value of an option
		if tt.name != "npm restart" && tt.name != "npm start" && !strings.Contains(got[0].Message, "value of an option") {
			t.Errorf("%s: message %q has no hint about the value of an option", tt.name, got[0].Message)
		}
	}
}

func TestLauncherNpxAdviceTalksAboutThePackage(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "/usr/local/lib/node_modules/npm/bin/npx-cli.js", "serve"}, "")
	p := mustGen(t, in)
	want1(t, p, "web", plan.Unsupported, "launcher", "the package it runs is a single node program")
	if got := itemsOf(p, "web", plan.Unsupported, "launcher"); strings.Contains(got[0].Message, "the script runs") {
		t.Errorf("message %q talks about a script", got[0].Message)
	}
}

func TestPassedOverNoteSaysPreAndPostIfTheLauncherRunsThem(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "server.js"}, "")
	in.Services["web"].Observed.PassedOver = []string{"npm start"}
	want1(t, mustGen(t, in), "web", plan.Note, "launcher passed over", "the pre and post scripts, if the launcher runs them")
}

func TestSupervisorIsNamedAndNotPassedOver(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "/usr/src/app/node_modules/.bin/nodemon", "server.js"}, "")
	p := mustGen(t, in)
	want1(t, p, "web", plan.Unsupported, "supervisor", "nodemon is a supervisor that starts your program as a child process")
	want1(t, p, "web", plan.Unsupported, "supervisor", "(for nodemon: node <file>)")
	wantNone(t, p, "web", plan.Unsupported, "launcher")
	in2, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "server.js"}, "")
	wantNone(t, mustGen(t, in2), "web", plan.Unsupported, "supervisor")
	in3, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "/app/node_modules/.bin/concurrently", "a", "b"}, "")
	want1(t, mustGen(t, in3), "web", plan.Unsupported, "supervisor", "need a service each")
}

func wantNoText(t *testing.T, p *plan.Plan, service string, kind plan.Kind, key, text string) {
	t.Helper()
	for _, it := range itemsOf(p, service, kind, key) {
		if strings.Contains(it.Message, text) {
			t.Errorf("item %s/%s/%q: message %q holds %q", service, kind, key, it.Message, text)
		}
	}
}

func TestReportNeverPrintsCommandText(t *testing.T) {
	const secret = "abc123"
	in, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "/usr/local/bin/npm", "run", "dev", "--token", secret}, `{"scripts":{"dev":"node a.js --api-key abc999 && node b.js"}}`)
	o := in.Services["web"].Observed
	o.PassedOver = []string{"sh -c", `npm (script "dev")`, "sh -c"}
	o.NotRun = []string{`npm (script "migrate")`, "node"}
	o.ChildLaunchers = []string{`npm (script "start")`}
	o.Children = 2
	p := mustGen(t, in)
	if len(itemsOf(p, "web", plan.Unsupported, "launcher")) == 0 || len(itemsOf(p, "web", plan.Note, "launcher passed over")) == 0 ||
		len(itemsOf(p, "web", plan.Unsupported, "commands not run")) == 0 || len(itemsOf(p, "web", plan.Unsupported, "launcher child")) == 0 {
		t.Fatalf("the items the test looks at are missing: %+v", p.Report)
	}
	for _, it := range p.Report {
		for _, word := range []string{secret, "abc999", "--token", "--api-key", "node a.js", "node b.js"} {
			if strings.Contains(it.Message, word) {
				t.Errorf("item %s/%s/%q prints %q: %q", it.Service, it.Kind, it.Key, word, it.Message)
			}
		}
	}
}

func TestPassedOverNoteNamesTheCommandsAndPointsToTheOriginal(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "server.js"}, "")
	in.Services["web"].Observed.PassedOver = []string{"sh -c", `npm (script "start")`, "sh -c"}
	p := mustGen(t, in)
	want1(t, p, "web", plan.Note, "launcher passed over", `sh -c, then npm (script "start"), then sh -c (outermost first)`)
	want1(t, p, "web", plan.Note, "launcher passed over", "CMD or ENTRYPOINT of the Dockerfile and in the scripts of package.json")
	wantNone(t, p, "web", plan.Unsupported, "commands not run")
}

func TestNotRunCommandsAreUnsupported(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "server.js"}, "")
	o := in.Services["web"].Observed
	o.PassedOver = []string{"sh -c", `npm (script "start")`}
	o.NotRun = []string{`npm (script "migrate")`}
	p := mustGen(t, in)
	want1(t, p, "web", plan.Unsupported, "commands not run", `npm (script "migrate")`)
	want1(t, p, "web", plan.Unsupported, "commands not run", "as a separate one-shot service")
	o.NotRun = nil
	wantNone(t, mustGen(t, in), "web", plan.Unsupported, "commands not run")
}

func TestChildLauncherIsUnsupported(t *testing.T) {
	in, _ := launcherInput(t, "/entrypoint.sh", []string{"/entrypoint.sh"}, "")
	in.Services["web"].Observed.ChildLaunchers = []string{`npm (script "start")`}
	want1(t, mustGen(t, in), "web", plan.Unsupported, "launcher child", `started npm (script "start") as a child process`)
	in2, _ := launcherInput(t, "/entrypoint.sh", []string{"/entrypoint.sh"}, "")
	wantNone(t, mustGen(t, in2), "web", plan.Unsupported, "launcher child")
}

func TestProcessesItemPointsToTheSupervisor(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "/usr/src/app/node_modules/.bin/nodemon", "server.js"}, "")
	in.Services["web"].Observed.Children = 2
	want1(t, mustGen(t, in), "web", plan.Unsupported, "processes", "see the supervisor item")
	in2, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "server.js"}, "")
	in2.Services["web"].Observed.Children = 2
	wantNoText(t, mustGen(t, in2), "web", plan.Unsupported, "processes", "supervisor")
}

func TestNotRunRepeatedLabelsAreCountedOnce(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "server.js"}, "")
	o := in.Services["web"].Observed
	o.PassedOver = []string{`npm (script "start")`}
	for i := 0; i < 30; i++ {
		o.NotRun = append(o.NotRun, "nc", "sleep")
	}
	o.NotRun = append(o.NotRun, "node")
	p := mustGen(t, in)
	want1(t, p, "web", plan.Unsupported, "commands not run", "nc (x30), sleep (x30), node")
	if got := itemsOf(p, "web", plan.Unsupported, "commands not run"); strings.Count(got[0].Message, "sleep") != 1 {
		t.Errorf("message %q lists sleep more than once", got[0].Message)
	}
}

func TestNotRunListIsCapped(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "server.js"}, "")
	o := in.Services["web"].Observed
	o.PassedOver = []string{"npm"}
	for i := 0; i < 25; i++ {
		o.NotRun = append(o.NotRun, fmt.Sprintf("cmd%d", i))
	}
	want1(t, mustGen(t, in), "web", plan.Unsupported, "commands not run", "and 5 more")
}

func TestNotRunWordingSaysACommandSubstitutionNeedsNothing(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "server.js"}, "")
	o := in.Services["web"].Observed
	o.PassedOver = []string{"npm"}
	o.NotRun = []string{"hostname", "id"}
	p := mustGen(t, in)
	want1(t, p, "web", plan.Unsupported, "commands not run", "needs nothing: its value is in the observed environment")
	want1(t, p, "web", plan.Unsupported, "commands not run", "A command with another effect is not run by the package")
}

func TestPassedOverRunsAreCountedInOrderAndCapped(t *testing.T) {
	in, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "server.js"}, "")
	o := in.Services["web"].Observed
	o.PassedOver = []string{"sh -c", "sh -c", "sh -c", `npm (script "start")`, "sh -c"}
	want1(t, mustGen(t, in), "web", plan.Note, "launcher passed over", `sh -c (x3), then npm (script "start"), then sh -c (outermost first)`)
	o.PassedOver = nil
	for i := 0; i < 25; i++ {
		o.PassedOver = append(o.PassedOver, fmt.Sprintf("cmd%d", i))
	}
	want1(t, mustGen(t, in), "web", plan.Note, "launcher passed over", "and 5 more")
}

func TestChildLauncherWordingTellsWhoStartedIt(t *testing.T) {
	labels := []string{`npm (script "start")`}
	in, _ := launcherInput(t, "/entrypoint.sh", []string{"/entrypoint.sh"}, "")
	in.Services["web"].Observed.ChildLaunchers = labels
	want1(t, mustGen(t, in), "web", plan.Unsupported, "launcher child", "the program, a script of the image or an init, started")

	in2, _ := launcherInput(t, "/sbin/tini", []string{"tini", "--", "npm", "start"}, "")
	in2.Services["web"].Observed.ChildLaunchers = labels
	want1(t, mustGen(t, in2), "web", plan.Unsupported, "launcher child", "the program (an init or a supervisor) started")

	in3, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "/usr/src/app/node_modules/.bin/nodemon", "--exec", "npm start"}, "")
	in3.Services["web"].Observed.ChildLaunchers = labels
	want1(t, mustGen(t, in3), "web", plan.Unsupported, "launcher child", "the program (an init or a supervisor) started")

	in4, _ := launcherInput(t, "/usr/local/bin/node", []string{"node", "server.js"}, "")
	in4.Services["web"].Observed.ChildLaunchers = labels
	wantNoText(t, mustGen(t, in4), "web", plan.Unsupported, "launcher child", "init or a supervisor")
}

func TestEffectiveCommand(t *testing.T) {
	exec := func(w ...string) compose.Command { return compose.Command{Exec: w, Set: true} }
	shell := func(s string) compose.Command { return compose.Command{Shell: s, Set: true} }
	img := &docker.ImageConfig{Entrypoint: []string{"/entry"}, Cmd: []string{"a", "b"}}
	f := &fake{host: map[string]string{"PORT": "8080", "SP": "two words", "EMPTY": ""}}
	tests := []struct {
		name  string
		ep    compose.Command
		cmd   compose.Command
		img   *docker.ImageConfig
		wantE string
		wantC string
	}{
		{"image only", compose.Command{}, compose.Command{}, img, "/entry", "a|b"},
		{"command replaces the cmd", compose.Command{}, exec("x"), img, "/entry", "x"},
		{"entrypoint clears the cmd", exec("/new"), compose.Command{}, img, "/new", ""},
		{"entrypoint and command", exec("/new"), exec("y", "z"), img, "/new", "y|z"},
		{"empty entrypoint clears both", exec(), compose.Command{}, img, "<none>", ""},
		{"empty command clears the cmd", compose.Command{}, exec(), img, "/entry", ""},
		{"no image", compose.Command{}, exec("run", "it"), nil, "<none>", "run|it"},
		{"simple shell form", compose.Command{}, shell(`nginx -g "daemon off;"`), img, "/entry", "nginx|-g|daemon off;"},
		{"single quotes keep a word together", compose.Command{}, shell(`echo 'a b' ''`), img, "/entry", "echo|a b|"},
		{"a dollar is interpolated inside single quotes, as Compose does", compose.Command{}, shell(`echo '$NOPE_UNSET'`), img, "/entry", "echo|"},
		{"an escaped dollar stays", compose.Command{}, shell(`echo '$$HOME'`), img, "/entry", "echo|$HOME"},
		{"pipe is an ordinary word", compose.Command{}, shell("echo hi | cat"), img, "/entry", "echo|hi|||cat"},
		{"glob is not expanded", compose.Command{}, shell("ls *.txt"), img, "/entry", "ls|*.txt"},
		{"assignment is an ordinary word", compose.Command{}, shell("A=1 run"), img, "/entry", "A=1|run"},
		{"option with equals", compose.Command{}, shell("run --a=1"), img, "/entry", "run|--a=1"},
		{"shell entrypoint", shell("/start.sh"), compose.Command{}, img, "/start.sh", ""},
		{"variable", compose.Command{}, shell("run --port $PORT --p2 ${PORT}"), img, "/entry", "run|--port|8080|--p2|8080"},
		{"interpolation comes before the split", compose.Command{}, shell("run $SP"), img, "/entry", "run|two|words"},
		{"quoted variable stays one word", compose.Command{}, shell(`run "$SP"`), img, "/entry", "run|two words"},
		{"no expansion after the split", compose.Command{}, shell(`run $$PORT "$$PORT"`), img, "/entry", "run|$PORT|$PORT"},
		{"default", compose.Command{}, shell("run ${NOPE:-dflt}"), img, "/entry", "run|dflt"},
		{"unset is an empty word only in quotes", compose.Command{}, shell(`run $NOPE ""`), img, "/entry", "run|"},
		{"backslash outside quotes", compose.Command{}, shell(`run a\ b c\"d`), img, "/entry", `run|a b|c"d`},
		{"backslash in double quotes", compose.Command{}, shell(`run "a\"b" "c\d"`), img, "/entry", `run|a"b|c\d`},
		{"tabs and newlines separate", compose.Command{}, shell("run\ta\nb"), img, "/entry", "run|a|b"},
		{"list is interpolated per element", compose.Command{}, exec("run", "--port=$PORT", "$SP", "a b"), img, "/entry", "run|--port=8080|two words|a b"},
		{"list is not split", compose.Command{}, exec("run $SP"), img, "/entry", "run two words"},
		{"entrypoint is interpolated", shell("/bin/$PORT -x"), compose.Command{}, img, "/bin/8080|-x", ""},
		{"image values are kept as they are", compose.Command{}, compose.Command{}, &docker.ImageConfig{Entrypoint: []string{"/e", "$PORT"}, Cmd: []string{"$SP", "'x"}}, "/e|$PORT", "$SP|'x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := svc("web")
			s.Entrypoint, s.Command = tt.ep, tt.cmd
			ep, cmd, err := EffectiveCommand(s, tt.img, f.lookup)
			if err != nil {
				t.Fatal(err)
			}
			gotE := "<none>"
			if ep != nil {
				gotE = strings.Join(*ep, "|")
			}
			if gotE != tt.wantE || strings.Join(cmd, "|") != tt.wantC {
				t.Errorf("entrypoint = %q, cmd = %q; want %q and %q", gotE, strings.Join(cmd, "|"), tt.wantE, tt.wantC)
			}
		})
	}
}

func TestEffectiveCommandErrors(t *testing.T) {
	f := &fake{}
	for _, tt := range []struct{ name, ep, cmd, want string }{
		{"open double quote", "", `run "x`, "command"},
		{"open single quote", "", "run 'x", "command"},
		{"open quote in the entrypoint", `/e "x`, "", "entrypoint"},
		{"required variable", "", "run ${NOPE:?must be set}", "NOPE"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := svc("web")
			if tt.ep != "" {
				s.Entrypoint = compose.Command{Shell: tt.ep, Set: true}
			}
			if tt.cmd != "" {
				s.Command = compose.Command{Shell: tt.cmd, Set: true}
			}
			_, _, err := EffectiveCommand(s, nil, f.lookup)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want one holding %q", err, tt.want)
			}
		})
	}
	s := svc("web")
	s.Command = compose.Command{Exec: []string{"run", "${NOPE:?x}"}, Set: true}
	if _, _, err := EffectiveCommand(s, nil, f.lookup); err == nil {
		t.Error("a required variable in a list gave no error")
	}
}

func TestEffectiveCommandDoesNotShareImageSlices(t *testing.T) {
	img := &docker.ImageConfig{Entrypoint: []string{"/entry"}, Cmd: []string{"a"}}
	ep, cmd, err := EffectiveCommand(svc("web"), img, (&fake{}).lookup)
	if err != nil {
		t.Fatal(err)
	}
	(*ep)[0] = "changed"
	cmd[0] = "changed"
	if img.Entrypoint[0] != "/entry" || img.Cmd[0] != "a" {
		t.Errorf("the image configuration was changed: %+v", img)
	}
}

func TestRender(t *testing.T) {
	items := []plan.Item{
		{Service: "a", Kind: plan.Observed, Key: "k1", Message: "m1"},
		{Service: "", Kind: plan.Note, Key: "pk", Message: "pm"},
		{Service: "a", Kind: plan.Note, Key: "k4", Message: "m4"},
		{Service: "a", Kind: plan.Unsupported, Key: "k2", Message: "m2"},
		{Service: "b", Kind: plan.Approximated, Key: "k3", Message: "m3"},
		{Service: "a", Kind: plan.Approximated, Key: "k5", Message: "m5"},
	}
	got := Render(items)
	lines := strings.Split(got, "\n")
	if !strings.Contains(lines[0], "observed") || !strings.Contains(lines[0], "review") {
		t.Errorf("first line %q does not say the output was observed and must be reviewed", lines[0])
	}
	rest := strings.TrimPrefix(got, lines[0]+"\n")
	want := "\nproject:\n  note pk: pm\n" +
		"\nservice a:\n  unsupported k2: m2\n  approximated k5: m5\n  note k4: m4\n  observed k1: m1\n" +
		"\nservice b:\n  approximated k3: m3\n"
	if rest != want {
		t.Errorf("Render =\n%s\nwant\n%s", rest, want)
	}
	if again := Render(items); again != got {
		t.Error("Render is not stable")
	}
	if strings.ContainsAny(got, "*#") {
		t.Errorf("Render holds markdown characters: %q", got)
	}
	empty := Render(nil)
	if strings.Count(empty, "\n") != 1 || !strings.Contains(empty, "review") {
		t.Errorf("Render(nil) = %q, want only the first line", empty)
	}
}

func TestDependsOnEmptyConditionIsSilent(t *testing.T) {
	db, web := svc("db"), svc("web")
	web.DependsOn = []compose.Dependency{{Service: "db", Condition: ""}}
	p := mustGen(t, newInput(&fake{}, db, web))
	wantNone(t, p, "web", plan.Approximated, "depends_on.db")
}

func TestDockerfileHealthcheckNoneGivesNoItem(t *testing.T) {
	in := newInput(&fake{}, svc("web"))
	in.Services["web"] = ServiceInput{Observed: observed("web"), Stage: &dockerfile.Stage{Healthcheck: &dockerfile.Healthcheck{None: true}}}
	p := mustGen(t, in)
	wantNone(t, p, "web", plan.Unsupported, "healthcheck")
}

func TestTestSummary(t *testing.T) {
	tests := []struct {
		name  string
		shell string
		exec  []string
		want  string
	}{
		{"cmd shell", "", []string{"CMD-SHELL", "curl -f http://localhost"}, "the test starts with curl and has 3 words (it is in the file)"},
		{"cmd shell with several words", "", []string{"CMD-SHELL", "pg_isready", "-U", "x"}, "the test starts with pg_isready and has 3 words (it is in the file)"},
		{"cmd", "", []string{"CMD", "curl", "-f"}, "the test starts with curl and has 2 words (it is in the file)"},
		{"shell form", "wget -q localhost", nil, "the test starts with wget and has 3 words (it is in the file)"},
		{"none", "", []string{"NONE"}, "the test is empty"},
		{"cmd without words", "", []string{"CMD"}, "the test is empty"},
		{"cmd shell without words", "", []string{"CMD-SHELL"}, "the test is empty"},
		{"nothing", "", nil, "the test is empty"},
		{"blank shell", "   ", nil, "the test is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := testSummary(tt.shell, tt.exec); got != tt.want {
				t.Errorf("testSummary(%q, %q) = %q, want %q", tt.shell, tt.exec, got, tt.want)
			}
		})
	}
}

func TestEmptyHealthcheckTestMessage(t *testing.T) {
	s := svc("web")
	s.Healthcheck = &compose.Healthcheck{Test: compose.Command{Exec: []string{"CMD"}, Set: true}}
	p, _ := one(t, &fake{}, s)
	want1(t, p, "web", plan.Unsupported, "healthcheck", "ops compose runs no healthchecks; the test is empty")
	s.Healthcheck = &compose.Healthcheck{Test: compose.Command{Exec: []string{"CMD-SHELL", "curl -f http://localhost"}, Set: true}}
	p, _ = one(t, &fake{}, s)
	want1(t, p, "web", plan.Unsupported, "healthcheck", "ops compose runs no healthchecks; the test starts with curl and has 3 words (it is in the file)")
}

func TestProjectNameFallback(t *testing.T) {
	if got := projectName(Input{Project: &compose.Project{}, Dir: "/"}); got != "project" {
		t.Errorf("projectName for the root directory = %q, want project", got)
	}
	in := newInput(&fake{}, svc("web"))
	in.Project.Name = ""
	in.Dir = "/"
	p := mustGen(t, in)
	if got := p.Services[0].PackageName; got != "project_web" {
		t.Errorf("package name = %q, want project_web", got)
	}
}

func TestFolderNameCollapsesEveryRun(t *testing.T) {
	tests := []struct{ project, service, want string }{
		{"...", "web", "._web"},
		{"....", "web", "._web"},
		{"a......b", "web", "a.b_web"},
		{"p", "..", "p_."},
	}
	for _, tt := range tests {
		if got := folderName(tt.project, tt.service); got != tt.want {
			t.Errorf("folderName(%q, %q) = %q, want %q", tt.project, tt.service, got, tt.want)
		}
	}
}

func TestServiceEnvNilFunctions(t *testing.T) {
	t.Setenv("D2N_NILFN_VAR", "real")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "e.env"), []byte("FROMFILE=1\nUSES=${D2N_NILFN_VAR}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := svc("web")
	s.EnvFile = []string{"e.env"}
	s.Environment = []compose.EnvVar{{Name: "D2N_NILFN_VAR"}}
	got, items := ServiceEnv(s, dir, nil, nil)
	want := map[string]string{"FROMFILE": "1", "USES": ""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("env = %v, want %v (a nil lookup finds nothing, a nil readFile reads the disk)", got, want)
	}
	wantItems := []plan.Item{
		{Service: "web", Kind: plan.Note, Key: "env_file[0]", Message: "variables used in the file are not set on this machine and became empty: D2N_NILFN_VAR"},
		{Service: "web", Kind: plan.Note, Key: "environment.D2N_NILFN_VAR", Message: "the variable has no value in the file and is not set on this machine; it is not set in the package"},
	}
	if !reflect.DeepEqual(items, wantItems) {
		t.Errorf("items = %+v, want %+v", items, wantItems)
	}
}

func TestServiceEnvFileUnsetNote(t *testing.T) {
	f := &fake{files: map[string]string{"/d/a.env": "A=$ONE-${TWO}\nB=${ONE}\nBARE\n"}}
	s := svc("web")
	s.EnvFile = []string{"a.env"}
	got, items := ServiceEnv(s, "/d", f.lookup, f.read)
	if want := map[string]string{"A": "-", "B": ""}; !reflect.DeepEqual(got, want) {
		t.Errorf("env = %v, want %v", got, want)
	}
	wantItems := []plan.Item{
		{Service: "web", Kind: plan.Note, Key: "env_file[0]", Message: "variables used in the file are not set on this machine and became empty: ONE, TWO"},
		{Service: "web", Kind: plan.Note, Key: "env_file[0]", Message: "variables named without a value are not set on this machine and are not set in the package: BARE"},
	}
	if !reflect.DeepEqual(items, wantItems) {
		t.Errorf("items = %+v, want %+v", items, wantItems)
	}
}

func TestPackageNameLengthBoundary(t *testing.T) {
	keep := "abcdefghijklmno"
	cut := "abcdefghijklmnop"
	if len(keep) != 15 || len(cut) != 16 {
		t.Fatalf("test data: %d and %d characters", len(keep), len(cut))
	}
	sum := sha1.Sum([]byte(cut))
	wantCut := "abcdefgh-" + hex.EncodeToString(sum[:])[:6]
	m, items, err := PackageNames(&compose.Project{}, []string{keep, cut})
	if err != nil {
		t.Fatal(err)
	}
	if m[keep] != keep || m[cut] != wantCut {
		t.Errorf("names = %v, want %q kept and %q shortened", m, keep, wantCut)
	}
	if len(items) != 1 || items[0].Service != cut {
		t.Errorf("items = %+v, want one note for %q only", items, cut)
	}
}

func TestPackageNameHashIsOfTheOriginalName(t *testing.T) {
	a, b := "Long-Service-Name-X", "long-service-name-x"
	m, _, err := PackageNames(&compose.Project{}, []string{a, b})
	if err != nil {
		t.Fatalf("two names that differ only in case must get different hashes: %v", err)
	}
	sa, sb := sha1.Sum([]byte(a)), sha1.Sum([]byte(b))
	wantA := "long-ser-" + hex.EncodeToString(sa[:])[:6]
	wantB := "long-ser-" + hex.EncodeToString(sb[:])[:6]
	if wantA == wantB {
		t.Fatal("test data: the hashes are equal")
	}
	if m[a] != wantA || m[b] != wantB {
		t.Errorf("names = %v, want %q and %q", m, wantA, wantB)
	}
}

func TestRenderPutsConvertedLast(t *testing.T) {
	items := []plan.Item{
		{Service: "a", Kind: plan.Converted, Key: "k1", Message: "m1"},
		{Service: "a", Kind: plan.Observed, Key: "k2", Message: "m2"},
		{Service: "a", Kind: plan.Note, Key: "k3", Message: "m3"},
		{Service: "a", Kind: plan.Approximated, Key: "k4", Message: "m4"},
		{Service: "a", Kind: plan.Unsupported, Key: "k5", Message: "m5"},
		{Service: "a", Kind: plan.Converted, Key: "k6", Message: "m6"},
	}
	got := Render(items)
	want := reviewLine + "\n\nservice a:\n  unsupported k5: m5\n  approximated k4: m4\n  note k3: m3\n  observed k2: m2\n  converted k1: m1\n  converted k6: m6\n"
	if got != want {
		t.Errorf("Render =\n%s\nwant\n%s", got, want)
	}
}

func TestListedLimit(t *testing.T) {
	var names []string
	for i := 1; i <= 21; i++ {
		names = append(names, fmt.Sprintf("n%d", i))
	}
	if got, want := listed(names[:20]), strings.Join(names[:20], ", "); got != want {
		t.Errorf("listed(20 names) = %q, want %q", got, want)
	}
	if got, want := listed(names), strings.Join(names[:20], ", ")+" and 1 more"; got != want {
		t.Errorf("listed(21 names) = %q, want %q", got, want)
	}
	if got := listed(names[:1]); got != "n1" {
		t.Errorf("listed(1 name) = %q", got)
	}
}

func TestArgv0StaysAsTheProgramSawIt(t *testing.T) {
	web := svc("web")
	in := newInput(&fake{}, web)
	in.Services["web"] = ServiceInput{Observed: &observe.Result{Sysroot: "/tmp/sys-web", Program: "/bin/busybox-extras", Argv: []string{"httpd", "-f", "-p", "8081"}, WorkingDir: "/"}}
	p, err := Generate(in)
	if err != nil {
		t.Fatal(err)
	}
	m := p.Services[0].Manifest
	if m.Program != "/bin/busybox-extras" || !reflect.DeepEqual(m.Args, []string{"httpd", "-f", "-p", "8081"}) {
		t.Errorf("manifest = %+v", m)
	}
}

func TestDockerfileVariableIsANote(t *testing.T) {
	web := svc("web")
	in := newInput(&fake{}, web)
	in.Services["web"] = ServiceInput{Observed: observed("web"), Stage: &dockerfile.Stage{Unknown: []string{
		"line 2: ENV X holds a variable (kept as written)",
		"line 3: ONBUILD",
	}}}
	p := mustGen(t, in)
	want1(t, p, "web", plan.Note, "dockerfile", "ENV X holds a variable; the value in the package is the one observed while the image ran")
	if m := itemsOf(p, "web", plan.Note, "dockerfile")[0].Message; strings.Contains(m, "line 2") {
		t.Errorf("message = %q", m)
	}
	want1(t, p, "web", plan.Unsupported, "dockerfile", "line 3: ONBUILD")
}

func TestHealthcheckOptionsFoldIntoItem(t *testing.T) {
	s := svc("web")
	s.Healthcheck = &compose.Healthcheck{Test: compose.Command{Exec: []string{"CMD-SHELL", "curl -f http://localhost"}, Set: true}}
	s.Unknown = []string{"healthcheck.interval", "healthcheck.timeout", "healthcheck.retries", "privileged"}
	p, _ := one(t, &fake{}, s)
	want1(t, p, "web", plan.Unsupported, "healthcheck", "options not converted: interval, timeout, retries")
	wantNone(t, p, "web", plan.Unsupported, "healthcheck.interval")
	wantNone(t, p, "web", plan.Unsupported, "healthcheck.timeout")
	wantNone(t, p, "web", plan.Unsupported, "healthcheck.retries")
	want1(t, p, "web", plan.Unsupported, "privileged", "not converted")

	// disabled healthcheck: no healthcheck item, so the keys keep their own items
	s = svc("web")
	s.Healthcheck = &compose.Healthcheck{Disable: true}
	s.Unknown = []string{"healthcheck.interval"}
	p, _ = one(t, &fake{}, s)
	wantNone(t, p, "web", plan.Unsupported, "healthcheck")
	want1(t, p, "web", plan.Unsupported, "healthcheck.interval", "not converted")
}

func TestObservedUserRows(t *testing.T) {
	tests := []struct {
		name      string
		user      string
		imageUser string
		uid       int
		want      string
	}{
		{"root in the file, entrypoint dropped to 999", "root", "", 999, "the program ran as uid 999"},
		{"zero in the file, entrypoint dropped to 999", "0", "", 999, "the program ran as uid 999"},
		{"zero with group in the file", "0:0", "", 999, "the program ran as uid 999"},
		{"no user in the file", "", "", 999, "the program ran as uid 999"},
		{"non-root user in the file", "app", "", 999, "the program ran as uid 999"},
		{"root observed", "", "", 0, ""},
		{"unknown, image user non-root", "", "mysql", -1, "the program was started as user mysql in the image; it was not possible to read which user it ran as; nanos runs everything as root"},
		{"unknown, image user numeric", "", "999:999", -1, "the numeric user of the image, uid 999"},
		{"unknown, file numeric over the image user", "1000:1000", "mysql", -1, "the numeric user of the file, uid 1000"},
		{"unknown, image user root", "", "root", -1, ""},
		{"unknown, no image user", "", "", -1, ""},
		{"unknown, file says root", "root", "mysql", -1, ""},
		{"unknown, file says non-root", "app", "mysql", -1, "user app is not applied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			web := svc("web")
			web.User = tt.user
			in := newInput(&fake{}, web)
			in.Services["web"].Observed.UID, in.Services["web"].Observed.GID = tt.uid, tt.uid
			si := in.Services["web"]
			si.Image = &docker.ImageConfig{User: tt.imageUser}
			in.Services["web"] = si
			p := mustGen(t, in)
			if tt.want == "" {
				wantNone(t, p, "web", plan.Unsupported, "user")
				wantNone(t, p, "web", plan.Approximated, "user")
				return
			}
			kind := plan.Unsupported
			if tt.uid > 0 || strings.Contains(tt.want, "numeric user") {
				kind = plan.Approximated
			}
			want1(t, p, "web", kind, "user", tt.want)
		})
	}
}

func TestVolumeSizeGivesRoomToWrite(t *testing.T) {
	dir := t.TempDir()
	if got := volumeSize(dir); got != "256m" {
		t.Errorf("empty sysroot: %q, want 256m", got)
	}
	if got := volumeSize(filepath.Join(dir, "missing")); got != "256m" {
		t.Errorf("missing sysroot: %q, want 256m", got)
	}
	// 8 MiB of files: 8 + 8/4 + 256
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "f"), make([]byte, 8<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := volumeSize(dir); got != "266m" {
		t.Errorf("8 MiB: %q, want 266m", got)
	}
}

func TestEveryPackageOfTheComposeFileHasAVolumeSize(t *testing.T) {
	p := mustGen(t, newInput(&fake{}, svc("web"), svc("app")))
	for _, c := range p.Compose.Packages {
		if c.BaseVolumeSz != "256m" {
			t.Errorf("%s: base_volume_sz %q, want 256m", c.Pkg, c.BaseVolumeSz)
		}
	}
	// "ops pkg load" does not read compose.yaml: the manifest has the size too
	for _, s := range p.Services {
		if s.Manifest.BaseVolumeSz != "256m" {
			t.Errorf("%s: manifest BaseVolumeSz %q, want 256m", s.Name, s.Manifest.BaseVolumeSz)
		}
	}
}

func TestNumericUserWhenTheObservedOneIsUnknownGoesInTheManifest(t *testing.T) {
	for _, tc := range []struct {
		name, file, image string
		uid, gid          any
	}{
		{"image uid and gid", "", "999:998", "999", "998"},
		{"image uid only", "", "999", "999", nil},
		{"the file wins", "1000", "999:999", "1000", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			web := svc("web")
			web.User = tc.file
			in := newInput(&fake{}, web)
			in.Services["web"].Observed.UID, in.Services["web"].Observed.GID = -1, -1
			si := in.Services["web"]
			si.Image = &docker.ImageConfig{User: tc.image}
			in.Services["web"] = si
			p := mustGen(t, in)
			pass := p.Services[0].Manifest.ManifestPassthrough
			if pass["uid"] != tc.uid {
				t.Errorf("uid = %v, want %v", pass["uid"], tc.uid)
			}
			if got, has := pass["gid"]; (tc.gid == nil) == has || (has && got != tc.gid) {
				t.Errorf("gid = %v (%v), want %v", got, has, tc.gid)
			}
		})
	}
}

func TestNumericUser(t *testing.T) {
	for _, tc := range []struct {
		in       string
		uid, gid int
		ok       bool
	}{
		{"999", 999, -1, true}, {"999:998", 999, 998, true}, {"0", 0, 0, false}, {"root", 0, 0, false},
		{"app:1", 0, 0, false}, {"1:app", 0, 0, false}, {"", 0, 0, false}, {"-5", 0, 0, false}, {"1:-2", 0, 0, false},
	} {
		uid, gid, ok := numericUser(tc.in)
		if ok != tc.ok || (ok && (uid != tc.uid || gid != tc.gid)) {
			t.Errorf("numericUser(%q) = %d %d %v, want %d %d %v", tc.in, uid, gid, ok, tc.uid, tc.gid, tc.ok)
		}
	}
}

// ops writes the file system of the image from the manifest and can encode strings, vectors and
// tuples, not numbers: every value we pass through is a string.
func TestEveryPassthroughValueIsAString(t *testing.T) {
	web := svc("web")
	in := newInput(&fake{}, web)
	in.Services["web"].Observed.UID, in.Services["web"].Observed.GID = 999, 998
	in.Services["web"].Observed.WorkingDir = "/srv"
	p := mustGen(t, in)
	for k, v := range p.Services[0].Manifest.ManifestPassthrough {
		if _, ok := v.(string); !ok {
			t.Errorf("%s: %T %v", k, v, v)
		}
	}
	if len(p.Services[0].Manifest.ManifestPassthrough) < 3 {
		t.Errorf("passthrough = %v, want cwd, uid and gid", p.Services[0].Manifest.ManifestPassthrough)
	}
}

func TestPackageWithOwnHostsFileIsNoted(t *testing.T) {
	web := svc("web")
	in := newInput(&fake{}, web)
	in.Services["web"].Observed.LocalNames = true
	p := mustGen(t, in)
	want1(t, p, "web", plan.Note, "hosts", "names localhost")
	in2 := newInput(&fake{}, svc("web"))
	wantNone(t, mustGen(t, in2), "web", plan.Note, "hosts")
}

func TestMachineFilesOfThePackageAreNoted(t *testing.T) {
	in := newInput(&fake{}, svc("db"))
	in.Services["db"].Observed.MachineFiles = []string{"/proc/cpuinfo", "/sys/devices/system/cpu/possible"}
	p := mustGen(t, in)
	want1(t, p, "db", plan.Note, "machine", "/sys/devices/system/cpu/possible")
	wantNone(t, mustGen(t, newInput(&fake{}, svc("db"))), "db", plan.Note, "machine")
}

func TestSecretBindSaysTheSecretIsInThePackage(t *testing.T) {
	db := svc("db")
	db.Volumes = []compose.Volume{{Type: "bind", Source: "pw.txt", Target: "/run/secrets/pw", ReadOnly: true, Secret: true}}
	in := newInput(&fake{files: map[string]string{"/work/app/pw.txt": "x"}}, db)
	p := mustGen(t, in)
	want1(t, p, "db", plan.Approximated, "volumes[0]", "the content of the secret is in the package")
}

func TestShortNamesWritesDNSSearchAndChangesTheNote(t *testing.T) {
	web, db := svc("web"), svc("db")
	web.Environment = []compose.EnvVar{{Name: "DB_HOST", Value: "db", Set: true}}
	web.Ports = []compose.Port{{Target: "80", Published: "80"}}
	in := newInput(&fake{}, db, web)
	in.ShortNames = true
	p := mustGen(t, in)
	if !reflect.DeepEqual(p.Compose.DNSSearch, []string{"service"}) {
		t.Errorf("DNSSearch = %v", p.Compose.DNSSearch)
	}
	msg := itemsOf(p, "", plan.Note, "service names")[0].Message
	if !strings.Contains(msg, "by its package name, or as <pkg>.service (dns_search: [service]") || !strings.Contains(msg, "db -> db (or db.service)") || strings.Contains(msg, "not rewritten") {
		t.Errorf("note = %q", msg)
	}
	want1(t, p, "web", plan.Approximated, "ports[0]", "reachable at web (or web.service) and at the address")
}

func TestWithoutShortNamesThereIsNoDNSSearch(t *testing.T) {
	p := mustGen(t, newInput(&fake{}, svc("db"), svc("web")))
	if len(p.Compose.DNSSearch) != 0 {
		t.Errorf("DNSSearch = %v", p.Compose.DNSSearch)
	}
	msg := itemsOf(p, "", plan.Note, "service names")[0].Message
	if !strings.Contains(msg, "as <pkg>.service, not by its compose name") {
		t.Errorf("note = %q", msg)
	}
}

func TestStartOrderNoteForAServiceThatNamesALaterOne(t *testing.T) {
	web, db := svc("web"), svc("db")
	web.Environment = []compose.EnvVar{{Name: "DB_HOST", Value: "db", Set: true}, {Name: "URL", Value: "mysql://db:3306/x", Set: true}, {Name: "OTHER", Value: "dbx", Set: true}}
	p := mustGen(t, newInput(&fake{}, web, db))
	want1(t, p, "web", plan.Note, "start order", "web names db in DB_HOST, URL, but compose.yaml starts web before db")
	wantNone(t, p, "db", plan.Note, "start order")
}

func TestStartOrderNoteIsNotMadeWhenTheOrderIsRight(t *testing.T) {
	web, db := svc("web"), svc("db")
	web.Environment = []compose.EnvVar{{Name: "DB_HOST", Value: "db", Set: true}}
	// db first: no note
	wantNone(t, mustGen(t, newInput(&fake{}, db, web)), "web", plan.Note, "start order")
	// declared: the order is already the right one, whatever the list
	web.DependsOn = []compose.Dependency{{Service: "db"}}
	wantNone(t, mustGen(t, newInput(&fake{}, web, db)), "web", plan.Note, "start order")
}

// nameInput builds proxy (observed, with the given configuration files) and a second service.
func nameInput(files map[string]string, short bool, proxyName, otherName string) Input {
	f := &fake{files: map[string]string{}}
	var names []string
	for n, c := range files {
		f.files["/tmp/sys-"+proxyName+n] = c
		names = append(names, n)
	}
	sort.Strings(names)
	in := newInput(f, svc(otherName), svc(proxyName))
	in.ShortNames = short
	in.Services[proxyName].Observed.ConfigurationFiles = names
	return in
}

func TestNameAtStartPackageFormInDefaultMode(t *testing.T) {
	in := nameInput(map[string]string{"/etc/n.conf": "server backend.service:80;"}, false, "proxy", "backend")
	p := mustGen(t, in)
	want1(t, p, "proxy", plan.Note, "name at start", "contains the word backend.service, which may name service backend")
	wantNone(t, p, "backend", plan.Note, "name at start")
	if m := itemsOf(p, "proxy", plan.Note, "name at start")[0].Message; !strings.Contains(m, "order of compose.yaml does not help") {
		t.Errorf("no timing text: %q", m)
	}
}

func TestNameAtStartBareNameInDefaultMode(t *testing.T) {
	p := mustGen(t, nameInput(map[string]string{"/etc/n.conf": "server Backend:80;"}, false, "proxy", "backend"))
	want1(t, p, "proxy", plan.Note, "name at start", "does not resolve under ops compose")
	if m := itemsOf(p, "proxy", plan.Note, "name at start")[0].Message; !strings.Contains(m, "backend.service") || strings.Contains(m, "order of compose.yaml") {
		t.Errorf("message: %q", m)
	}
}

func TestNameAtStartBareNameWithShortNames(t *testing.T) {
	p := mustGen(t, nameInput(map[string]string{"/etc/n.conf": "server backend:80;"}, true, "proxy", "backend"))
	want1(t, p, "proxy", plan.Note, "name at start", "order of compose.yaml does not help")
}

func TestNameAtStartPackageNameDiffersFromServiceName(t *testing.T) {
	p := mustGen(t, nameInput(map[string]string{"/etc/n.conf": "server my-app.service:80;"}, false, "proxy", "My_App"))
	want1(t, p, "proxy", plan.Note, "name at start", "may name service My_App")
}

func TestNameAtStartOwnNameIsNotReported(t *testing.T) {
	p := mustGen(t, nameInput(map[string]string{"/etc/n.conf": "server_name proxy proxy.service;"}, false, "proxy", "backend"))
	wantNone(t, p, "proxy", plan.Note, "name at start")
}

func TestNameAtStartExcludedFileIsNotRead(t *testing.T) {
	p := mustGen(t, nameInput(map[string]string{"/etc/nsswitch.conf": "backend.service"}, false, "proxy", "backend"))
	wantNone(t, p, "proxy", plan.Note, "name at start")
}

func TestNameAtStartCommentIsSkipped(t *testing.T) {
	p := mustGen(t, nameInput(map[string]string{"/etc/n.conf": "  # server backend.service:80;\nlisten 80;"}, false, "proxy", "backend"))
	wantNone(t, p, "proxy", plan.Note, "name at start")
}

func TestNameAtStartBinaryFileIsSkipped(t *testing.T) {
	p := mustGen(t, nameInput(map[string]string{"/etc/n.conf": "\x00 backend.service"}, false, "proxy", "backend"))
	wantNone(t, p, "proxy", plan.Note, "name at start")
}

func TestNameAtStartNoSysrootIsNotRead(t *testing.T) {
	in := nameInput(map[string]string{"/etc/n.conf": "backend.service"}, false, "proxy", "backend")
	in.Services["proxy"].Observed.Sysroot = ""
	in.ReadFile = func(p string) ([]byte, error) { return []byte("backend.service"), nil }
	wantNone(t, mustGen(t, in), "proxy", plan.Note, "name at start")
}

func TestNameAtStartPathSegmentIsNotAName(t *testing.T) {
	p := mustGen(t, nameInput(map[string]string{"/etc/n.conf": "proxy_cache_path /var/cache/nginx;"}, false, "proxy", "cache"))
	wantNone(t, p, "proxy", plan.Note, "name at start")
}

func TestNameAtStartPathDoesNotHideTheRealName(t *testing.T) {
	p := mustGen(t, nameInput(map[string]string{"/etc/n.conf": "root /srv/backend;\nproxy_pass http://backend.service:80;"}, false, "proxy", "backend"))
	want1(t, p, "proxy", plan.Note, "name at start", "contains the word backend.service")
}

func TestNameAtStartComposeNameWithShortNames(t *testing.T) {
	p := mustGen(t, nameInput(map[string]string{"/etc/n.conf": "server my_app:80;"}, true, "proxy", "My_App"))
	want1(t, p, "proxy", plan.Note, "name at start", "search line of resolv.conf")
	want1(t, p, "proxy", plan.Note, "name at start", "my-app.service")
	want1(t, p, "proxy", plan.Note, "name at start", "does not resolve under ops compose")
	if m := itemsOf(p, "proxy", plan.Note, "name at start")[0].Message; strings.Contains(m, "order of compose.yaml does not help") {
		t.Errorf("a compose name that does not resolve is given the timing text: %q", m)
	}
}

func TestNameAtStartTableOfNamesIsNotRead(t *testing.T) {
	wantNone(t, mustGen(t, nameInput(map[string]string{"/etc/services": "mysql 3306/tcp"}, false, "proxy", "mysql")), "proxy", plan.Note, "name at start")
	wantNone(t, mustGen(t, nameInput(map[string]string{"/etc/nginx/mime.types": "mysql"}, false, "proxy", "mysql")), "proxy", plan.Note, "name at start")
	wantNone(t, mustGen(t, nameInput(map[string]string{"/etc/passwd": "backend"}, false, "proxy", "backend")), "proxy", plan.Note, "name at start")
}

func TestNameAtStartTrailingDotIsTrimmed(t *testing.T) {
	p := mustGen(t, nameInput(map[string]string{"/etc/n.conf": "server backend.service.:80;"}, false, "proxy", "backend"))
	want1(t, p, "proxy", plan.Note, "name at start", "contains the word backend.service")
}

const nameHead = "server backend.service:80;\n"

func TestNameAtStartLargeFileIsSkipped(t *testing.T) {
	c := nameHead + strings.Repeat("a", 1<<20)
	wantNone(t, mustGen(t, nameInput(map[string]string{"/etc/n.conf": c}, false, "proxy", "backend")), "proxy", plan.Note, "name at start")
}

func TestNameAtStartFileOfExactly1MiBIsRead(t *testing.T) {
	c := nameHead + strings.Repeat("a", 1<<20-len(nameHead))
	want1(t, mustGen(t, nameInput(map[string]string{"/etc/n.conf": c}, false, "proxy", "backend")), "proxy", plan.Note, "name at start", "backend.service")
}

func TestNameAtStartNULAfterFirst4KBIsRead(t *testing.T) {
	c := strings.Repeat("a", 4096) + "\x00\nserver backend.service:80;"
	want1(t, mustGen(t, nameInput(map[string]string{"/etc/n.conf": c}, false, "proxy", "backend")), "proxy", plan.Note, "name at start", "backend.service")
}

func TestNameAtStartNULAtByte4095IsSkipped(t *testing.T) {
	c := strings.Repeat("a", 4095) + "\x00\nserver backend.service:80;"
	wantNone(t, mustGen(t, nameInput(map[string]string{"/etc/n.conf": c}, false, "proxy", "backend")), "proxy", plan.Note, "name at start")
}

func TestNameAtStartSameWordInTwoSpellingsIsOneHit(t *testing.T) {
	p := mustGen(t, nameInput(map[string]string{"/etc/n.conf": "server Backend.service:80;\nserver backend.service:81;"}, false, "proxy", "backend"))
	want1(t, p, "proxy", plan.Note, "name at start", "contains the word Backend.service")
	if m := itemsOf(p, "proxy", plan.Note, "name at start")[0].Message; strings.Count(m, "contains the word") != 1 {
		t.Errorf("one word, two reports: %q", m)
	}
}

func TestNameAtStartProtocolRelativeHost(t *testing.T) {
	p := mustGen(t, nameInput(map[string]string{"/etc/n.conf": "proxy_pass //backend.service:80;"}, false, "proxy", "backend"))
	want1(t, p, "proxy", plan.Note, "name at start", "contains the word backend.service")
}

func TestNameAtStartInlineCommentIsSkipped(t *testing.T) {
	p := mustGen(t, nameInput(map[string]string{"/etc/n.conf": "listen 80; # server backend.service:80\nroot /x;\t# backend.service"}, false, "proxy", "backend"))
	wantNone(t, p, "proxy", plan.Note, "name at start")
}

func TestNameAtStartEachExcludedFile(t *testing.T) {
	for _, f := range []string{"/etc/hosts", "/etc/resolv.conf", "/etc/group", "/etc/host.conf", "/etc/protocols", "/etc/os-release", "/etc/services", "/etc/passwd", "/etc/nsswitch.conf", "/etc/nginx/mime.types"} {
		p := mustGen(t, nameInput(map[string]string{f: "server backend.service:80;"}, false, "proxy", "backend"))
		if got := itemsOf(p, "proxy", plan.Note, "name at start"); len(got) != 0 {
			t.Errorf("%s is read: %+v", f, got)
		}
	}
}

func TestNetworkNoteWithShortNamesSaysPackageName(t *testing.T) {
	in := nameInput(map[string]string{"/etc/n.conf": "x"}, true, "proxy", "My_App")
	p := mustGen(t, in)
	want1(t, p, "", plan.Note, "service names", "by its package name")
	want1(t, p, "", plan.Note, "service names", "my-app (or my-app.service)")
}

package gen

import (
	"fmt"
	"io/fs"
	"reflect"
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
	dep := func(n string) []compose.Dependency { return []compose.Dependency{{Service: n, Condition: "service_started"}} }
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
	p := mustGen(t, in)
	if len(p.Services) != 0 {
		t.Errorf("services = %v, want none", names(p))
	}
	want1(t, p, "web", plan.Unsupported, "service", "not observed")
	want1(t, p, "db", plan.Unsupported, "service", "not observed")
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
	if got.Sysroot != "/tmp/sys-web" || got.Name != "web" || got.PackageName != "proj_web" {
		t.Errorf("service = %+v", got)
	}
	wantPkg := plan.ComposePackage{Pkg: "web", Name: "proj_web", Local: true, Arch: "amd64"}
	if len(p.Compose.Packages) != 1 || p.Compose.Packages[0] != wantPkg {
		t.Errorf("compose = %+v, want %+v", p.Compose.Packages, wantPkg)
	}
	if p.Arch != "amd64" {
		t.Errorf("Arch = %q, want amd64", p.Arch)
	}
	want1(t, p, "web", plan.Observed, "program", "/bin/web -x")

	in := newInput(&fake{}, web)
	in.Arch = "arm64"
	p = mustGen(t, in)
	if p.Arch != "arm64" || p.Compose.Packages[0].Arch != "arm64" {
		t.Errorf("arch = %q / %q, want arm64", p.Arch, p.Compose.Packages[0].Arch)
	}
}

func TestNames(t *testing.T) {
	good := []struct {
		name, project, dir, pkg, pkgName string
	}{
		{"Web", "proj", "/work/app", "web", "proj_web"},
		{"web-2", "My Proj", "/work/app", "web-2", "my-proj_web-2"},
		{"web", "", "/work/My App", "web", "my-app_web"},
		{"exactly15chars-", "p", "/w", "exactly15chars-", "p_exactly15chars-"},
	}
	for _, tt := range good {
		t.Run(tt.name, func(t *testing.T) {
			in := newInput(&fake{}, svc(tt.name))
			in.Project.Name = tt.project
			in.Dir = tt.dir
			p := mustGen(t, in)
			if p.Compose.Packages[0].Pkg != tt.pkg || p.Services[0].PackageName != tt.pkgName || p.Compose.Packages[0].Name != tt.pkgName {
				t.Errorf("pkg %q / package name %q / compose name %q, want %q / %q", p.Compose.Packages[0].Pkg, p.Services[0].PackageName, p.Compose.Packages[0].Name, tt.pkg, tt.pkgName)
			}
		})
	}
	bad := []string{"my_app", "web.app", "sixteen-chars-xx", ""}
	for _, name := range bad {
		t.Run("bad "+name, func(t *testing.T) {
			_, err := Generate(newInput(&fake{}, svc(name)))
			if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%q", name)) {
				t.Errorf("error = %v, want one that names %q", err, name)
			}
		})
	}
	_, err := Generate(newInput(&fake{}, svc("Web"), svc("web")))
	if err == nil || !strings.Contains(err.Error(), "already used") {
		t.Errorf("two services with the same package name: error = %v", err)
	}
}

func TestEnvironment(t *testing.T) {
	f := &fake{files: map[string]string{}, host: map[string]string{"G": "fromhost"}}
	f.files["/work/app/env.list"] = "# comment\n\nB=file\nexport C=\"quoted\"\nD='sq ${X}'\nE=val # trailing\nF=${HOME}\nBARE\n"
	f.files["/abs/other.env"] = "\xef\xbb\xbfK=v\r\nL=w\r\n"
	s := svc("web")
	s.EnvFile = []string{"env.list", "/abs/other.env", "missing.env"}
	s.Environment = []compose.EnvVar{
		{Name: "B", Value: "env", Set: true},
		{Name: "G"},
		{Name: "H"},
		{Name: "I", Value: "${Y:-1}", Set: true},
	}
	in := newInput(f, s)
	in.Services["web"].Observed.Env = []string{"A=img", "B=img", "PATH=/bin", "noequals"}
	p := mustGen(t, in)
	want := map[string]string{"A": "img", "PATH": "/bin", "B": "env", "C": "quoted", "D": "sq ${X}", "E": "val", "F": "${HOME}", "G": "fromhost", "I": "${Y:-1}", "K": "v", "L": "w"}
	if got := p.Services[0].Manifest.Env; !reflect.DeepEqual(got, want) {
		t.Errorf("Env = %v, want %v", got, want)
	}
	want1(t, p, "web", plan.Unsupported, "environment.H", "not set")
	want1(t, p, "web", plan.Unsupported, "environment.I", "not interpolated")
	want1(t, p, "web", plan.Unsupported, "env_file[0] line 7", "not interpolated")
	want1(t, p, "web", plan.Unsupported, "env_file[0] line 8", "not set")
	want1(t, p, "web", plan.Unsupported, "env_file[2]", "missing.env")
	wantNone(t, p, "web", plan.Unsupported, "environment.G")
	wantNone(t, p, "web", plan.Unsupported, "env_file[0] line 5")
	wantNone(t, p, "web", plan.Unsupported, "env_file[1]")
}

func TestEnvironmentEmptyIsNil(t *testing.T) {
	_, got := one(t, &fake{}, svc("web"))
	if got.Manifest.Env != nil {
		t.Errorf("Env = %v, want nil", got.Manifest.Env)
	}
}

func TestPorts(t *testing.T) {
	tests := []struct {
		name   string
		port   compose.Port
		tcp    []string
		udp    []string
		kind   plan.Kind
		substr string
	}{
		{"tcp", compose.Port{Published: "80", Target: "80", Protocol: "tcp"}, []string{"80"}, nil, "", ""},
		{"empty protocol is tcp", compose.Port{Published: "80", Target: "80"}, []string{"80"}, nil, "", ""},
		{"udp", compose.Port{Published: "53", Target: "53", Protocol: "udp"}, nil, []string{"53"}, "", ""},
		{"range", compose.Port{Published: "8000-8010", Target: "8000-8010", Protocol: "tcp"}, []string{"8000-8010"}, nil, "", ""},
		{"remapped", compose.Port{Published: "8080", Target: "80", Protocol: "tcp"}, []string{"8080"}, nil, plan.Approximated, "8080 is used, the container port is 80"},
		{"not published", compose.Port{Target: "80", Protocol: "tcp"}, nil, nil, plan.Note, "only from other services"},
		{"host address", compose.Port{HostIP: "127.0.0.1", Published: "80", Target: "80", Protocol: "tcp"}, []string{"80"}, nil, plan.Unsupported, "127.0.0.1"},
		{"sctp", compose.Port{Published: "80", Target: "80", Protocol: "sctp"}, nil, nil, plan.Unsupported, "sctp"},
		{"variable", compose.Port{Published: "${P}", Target: "80", Protocol: "tcp"}, nil, nil, plan.Unsupported, "variable"},
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
			if tt.kind == "" {
				for _, k := range []plan.Kind{plan.Note, plan.Approximated, plan.Unsupported} {
					wantNone(t, p, "web", k, "ports[0]")
				}
				return
			}
			want1(t, p, "web", tt.kind, "ports[0]", tt.substr)
		})
	}
}

func TestPortsTwoKindsAtOnce(t *testing.T) {
	s := svc("web")
	s.Ports = []compose.Port{{HostIP: "10.0.0.1", Published: "8080", Target: "80", Protocol: "tcp"}}
	p, got := one(t, &fake{}, s)
	if !reflect.DeepEqual(got.Manifest.RunConfig.Ports, []string{"8080"}) {
		t.Errorf("Ports = %v", got.Manifest.RunConfig.Ports)
	}
	want1(t, p, "web", plan.Unsupported, "ports[0]", "10.0.0.1")
	want1(t, p, "web", plan.Approximated, "ports[0]", "8080")
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
		mapDir map[string]string
		kind   plan.Kind
		substr string
	}{
		{"relative directory", compose.Volume{Type: "bind", Source: "./data", Target: "/data"}, map[string]string{"/work/app/data": "/data"}, plan.Approximated, "copied into the image at build time"},
		{"absolute file read only", compose.Volume{Type: "bind", Source: "/etc/app.conf", Target: "/app.conf", ReadOnly: true}, map[string]string{"/etc/app.conf": "/app.conf"}, plan.Approximated, "read_only is not enforced"},
		{"bind options", compose.Volume{Type: "bind", Source: "./data", Target: "/data", Options: "z"}, map[string]string{"/work/app/data": "/data"}, plan.Approximated, "options z are ignored"},
		{"missing path", compose.Volume{Type: "bind", Source: "./nope", Target: "/n"}, nil, plan.Unsupported, "/work/app/nope"},
		{"home directory", compose.Volume{Type: "bind", Source: "~/x", Target: "/x"}, nil, plan.Unsupported, "starts with ~"},
		{"named", compose.Volume{Type: "volume", Source: "db", Target: "/var/lib/db"}, nil, plan.Unsupported, "named volume db"},
		{"anonymous", compose.Volume{Type: "volume", Target: "/cache"}, nil, plan.Unsupported, "anonymous volume"},
		{"tmpfs", compose.Volume{Type: "tmpfs", Target: "/run"}, nil, plan.Unsupported, "tmpfs"},
		{"unresolved", compose.Volume{Type: "unresolved", Source: "${D}/x", Target: "/x"}, nil, plan.Unsupported, "variable"},
		{"variable in a bind", compose.Volume{Type: "bind", Source: "${D}/x", Target: "/x"}, nil, plan.Unsupported, "variable"},
		{"other type", compose.Volume{Type: "npipe", Source: "p", Target: "/p"}, nil, plan.Unsupported, "npipe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := svc("web")
			s.Volumes = []compose.Volume{tt.vol}
			p, got := one(t, f, s)
			if !reflect.DeepEqual(got.Manifest.MapDirs, tt.mapDir) {
				t.Errorf("MapDirs = %v, want %v", got.Manifest.MapDirs, tt.mapDir)
			}
			want1(t, p, "web", tt.kind, "volumes[0]", tt.substr)
		})
	}
}

func TestVolumeTargetConflictAndImageVolumes(t *testing.T) {
	f := &fake{dirs: map[string]bool{"/work/app/data": true}}
	s := svc("web")
	s.Volumes = []compose.Volume{{Type: "bind", Source: "./data", Target: "/a"}, {Type: "bind", Source: "./data", Target: "/b"}}
	in := newInput(f, s)
	in.Services["web"] = ServiceInput{Observed: observed("web"), Image: &docker.ImageConfig{Volumes: []string{"/var/lib/x"}}}
	p := mustGen(t, in)
	if got := p.Services[0].Manifest.MapDirs; !reflect.DeepEqual(got, map[string]string{"/work/app/data": "/a"}) {
		t.Errorf("MapDirs = %v", got)
	}
	want1(t, p, "web", plan.Approximated, "volumes[0]", "/a")
	want1(t, p, "web", plan.Unsupported, "volumes[1]", "second target")
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
	want1(t, p, "web", plan.Unsupported, "healthcheck", "CMD curl -f http://localhost")

	s.Healthcheck = &compose.Healthcheck{Test: compose.Command{Shell: "pg_isready -U x", Set: true}}
	p, _ = one(t, &fake{}, s)
	want1(t, p, "web", plan.Unsupported, "healthcheck", "pg_isready -U x")

	s.Healthcheck = &compose.Healthcheck{Disable: true}
	p, _ = one(t, &fake{}, s)
	wantNone(t, p, "web", plan.Unsupported, "healthcheck")

	plain := svc("web")
	in := newInput(&fake{}, plain)
	in.Services["web"] = ServiceInput{Observed: observed("web"), Stage: &dockerfile.Stage{Healthcheck: &dockerfile.Healthcheck{Test: dockerfile.Command{Shell: "wget -q localhost", Set: true}}}}
	p = mustGen(t, in)
	want1(t, p, "web", plan.Unsupported, "healthcheck", "wget -q localhost")
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
	want1(t, p, "", plan.Note, "service names", "web: DB_HOST=db; web: URL=postgres://db:5432/x.")
	msg := itemsOf(p, "", plan.Note, "service names")[0].Message
	for _, bad := range []string{"SELF=", "OTHER="} {
		if strings.Contains(msg, bad) {
			t.Errorf("message %q lists %s", msg, bad)
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

func TestUserAndWorkingDir(t *testing.T) {
	tests := []struct {
		name       string
		user, wd   string
		wantUser   bool
		wantWorkDr bool
	}{
		{"nothing", "", "", false, false},
		{"root", "root", "/", false, false},
		{"zero with group", "0:0", "/", false, false},
		{"other user", "app", "/", true, false},
		{"numeric user and group", "1000:1000", "/", true, false},
		{"same directory", "", "/", false, false},
		{"other directory", "", "/srv", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := svc("web")
			s.User, s.WorkingDir = tt.user, tt.wd
			p, _ := one(t, &fake{}, s)
			if tt.wantUser {
				want1(t, p, "web", plan.Unsupported, "user", tt.user)
			} else {
				wantNone(t, p, "web", plan.Unsupported, "user")
			}
			if tt.wantWorkDr {
				want1(t, p, "web", plan.Unsupported, "working_dir", "/srv")
			} else {
				wantNone(t, p, "web", plan.Unsupported, "working_dir")
			}
		})
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

func TestEffectiveCommand(t *testing.T) {
	exec := func(w ...string) compose.Command { return compose.Command{Exec: w, Set: true} }
	shell := func(s string) compose.Command { return compose.Command{Shell: s, Set: true} }
	img := &docker.ImageConfig{Entrypoint: []string{"/entry"}, Cmd: []string{"a", "b"}}
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
		{"single quotes are literal", compose.Command{}, shell(`echo '$HOME' ''`), img, "/entry", "echo|$HOME|"},
		{"pipe goes to sh", compose.Command{}, shell("echo hi | cat"), img, "/entry", "/bin/sh|-c|echo hi | cat"},
		{"variable goes to sh", compose.Command{}, shell("echo $HOME"), img, "/entry", "/bin/sh|-c|echo $HOME"},
		{"variable in double quotes goes to sh", compose.Command{}, shell(`echo "$HOME"`), img, "/entry", `/bin/sh|-c|echo "$HOME"`},
		{"open quote goes to sh", compose.Command{}, shell(`echo "hi`), img, "/entry", `/bin/sh|-c|echo "hi`},
		{"assignment goes to sh", compose.Command{}, shell("A=1 run"), img, "/entry", "/bin/sh|-c|A=1 run"},
		{"option with equals stays", compose.Command{}, shell("run --a=1"), img, "/entry", "run|--a=1"},
		{"glob goes to sh", compose.Command{}, shell("ls *.txt"), img, "/entry", "/bin/sh|-c|ls *.txt"},
		{"shell entrypoint", shell("/start.sh"), compose.Command{}, img, "/start.sh", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := svc("web")
			s.Entrypoint, s.Command = tt.ep, tt.cmd
			ep, cmd := EffectiveCommand(s, tt.img)
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

func TestEffectiveCommandDoesNotShareImageSlices(t *testing.T) {
	img := &docker.ImageConfig{Entrypoint: []string{"/entry"}, Cmd: []string{"a"}}
	ep, cmd := EffectiveCommand(svc("web"), img)
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

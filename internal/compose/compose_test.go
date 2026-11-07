package compose

import (
	"reflect"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) *Project {
	t.Helper()
	p, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return p
}

func TestParseTwoServices(t *testing.T) {
	p, err := ParseFile("testdata/two-services.yml")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "demo" || len(p.Services) != 2 {
		t.Fatalf("name %q, %d services", p.Name, len(p.Services))
	}
	web, app := p.Services[0], p.Services[1]
	if web.Name != "web" || app.Name != "app" {
		t.Fatalf("order: %s, %s", web.Name, app.Name)
	}
	if web.Image != "nginx:1.27" || web.Restart != "unless-stopped" {
		t.Errorf("web: %+v", web)
	}
	wantPorts := []Port{{Published: "8080", Target: "80", Protocol: "tcp"}}
	if !reflect.DeepEqual(web.Ports, wantPorts) {
		t.Errorf("ports %+v", web.Ports)
	}
	wantVol := []Volume{{Type: "bind", Source: "./site", Target: "/usr/share/nginx/html", ReadOnly: true}}
	if !reflect.DeepEqual(web.Volumes, wantVol) {
		t.Errorf("volumes %+v", web.Volumes)
	}
	if !reflect.DeepEqual(web.DependsOn, []Dependency{{"app", "service_started"}}) {
		t.Errorf("depends_on %+v", web.DependsOn)
	}
	if app.Build == nil || app.Build.Context != "./app" || app.Build.Dockerfile != "Dockerfile" {
		t.Errorf("build %+v", app.Build)
	}
	if !reflect.DeepEqual(app.Build.Args, []EnvVar{{"GO_VERSION", "1.22", true}}) {
		t.Errorf("args %+v", app.Build.Args)
	}
	wantEnv := []EnvVar{{"PORT", "9000", true}, {"DEBUG", "", false}}
	if !reflect.DeepEqual(app.Environment, wantEnv) {
		t.Errorf("env %+v", app.Environment)
	}
	if app.Healthcheck == nil || len(app.Healthcheck.Test.Exec) != 4 {
		t.Errorf("healthcheck %+v", app.Healthcheck)
	}
	if !reflect.DeepEqual(app.Unknown, []string{"privileged"}) {
		t.Errorf("unknown %v", app.Unknown)
	}
	if p.Service("app") != app || p.Service("nope") != nil {
		t.Error("Service lookup")
	}
}

func TestShortPorts(t *testing.T) {
	cases := []struct {
		in   string
		want Port
	}{
		{"80", Port{Target: "80", Protocol: "tcp"}},
		{"8080:80", Port{Published: "8080", Target: "80", Protocol: "tcp"}},
		{"127.0.0.1:8080:80", Port{HostIP: "127.0.0.1", Published: "8080", Target: "80", Protocol: "tcp"}},
		{"53:53/udp", Port{Published: "53", Target: "53", Protocol: "udp"}},
		{"8000-8010:80", Port{Published: "8000-8010", Target: "80", Protocol: "tcp"}},
		{"[::1]:8080:80", Port{HostIP: "::1", Published: "8080", Target: "80", Protocol: "tcp"}},
		{"[::1]:80", Port{HostIP: "::1", Target: "80", Protocol: "tcp"}},
	}
	for _, c := range cases {
		got, err := parseShortPort(c.in)
		if err != nil || got != c.want {
			t.Errorf("%q: got %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "a:b:c:d", "80/", "[::1:80", "8080:"} {
		if _, err := parseShortPort(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestLongPortAndVolume(t *testing.T) {
	p := mustParse(t, `
services:
  a:
    image: x
    ports:
      - target: 80
        published: 8080
        protocol: udp
        mode: host
    volumes:
      - type: volume
        source: data
        target: /data
        volume:
          nocopy: true
      - type: tmpfs
        target: /run
`)
	a := p.Services[0]
	if want := (Port{Published: "8080", Target: "80", Protocol: "udp"}); a.Ports[0] != want {
		t.Errorf("port %+v", a.Ports[0])
	}
	if a.Volumes[0] != (Volume{Type: "volume", Source: "data", Target: "/data"}) || a.Volumes[1].Type != "tmpfs" {
		t.Errorf("volumes %+v", a.Volumes)
	}
	want := []string{"ports[0].mode", "volumes[0].volume"}
	if !reflect.DeepEqual(a.Unknown, want) {
		t.Errorf("unknown %v, want %v", a.Unknown, want)
	}
}

func TestShortVolumes(t *testing.T) {
	cases := []struct {
		in   string
		want Volume
	}{
		{"/data", Volume{Type: "volume", Target: "/data"}},
		{"db:/var/lib/db", Volume{Type: "volume", Source: "db", Target: "/var/lib/db"}},
		{"./src:/app", Volume{Type: "bind", Source: "./src", Target: "/app"}},
		{"/etc/x:/x:ro", Volume{Type: "bind", Source: "/etc/x", Target: "/x", ReadOnly: true}},
		{"db:/d:rw,z", Volume{Type: "volume", Source: "db", Target: "/d"}},
		{"~/cfg:/cfg:ro,z", Volume{Type: "bind", Source: "~/cfg", Target: "/cfg", ReadOnly: true}},
	}
	for _, c := range cases {
		got, err := parseShortVolume(c.in)
		if err != nil || got != c.want {
			t.Errorf("%q: got %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	if _, err := parseShortVolume("a:b:c:d"); err == nil {
		t.Error("expected an error for four parts")
	}
}

func TestCommandForms(t *testing.T) {
	p := mustParse(t, `
services:
  a:
    image: x
    command: sh -c "echo hi"
    entrypoint: ["/bin/app", "--flag"]
  b:
    image: x
    entrypoint: []
`)
	a, b := p.Services[0], p.Services[1]
	if a.Command.Shell != `sh -c "echo hi"` || a.Command.Exec != nil {
		t.Errorf("command %+v", a.Command)
	}
	if !reflect.DeepEqual(a.Entrypoint.Exec, []string{"/bin/app", "--flag"}) {
		t.Errorf("entrypoint %+v", a.Entrypoint)
	}
	if b.Command.IsSet() || !b.Entrypoint.IsSet() {
		t.Errorf("an empty entrypoint list is set, an absent command is not: %+v %+v", b.Command, b.Entrypoint)
	}
}

func TestEnvironmentList(t *testing.T) {
	p := mustParse(t, `
services:
  a:
    image: x
    environment:
      - A=1
      - B=
      - C
      - D=x=y
`)
	want := []EnvVar{{"A", "1", true}, {"B", "", true}, {"C", "", false}, {"D", "x=y", true}}
	if got := p.Services[0].Environment; !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestDependsOnMapAndNetworks(t *testing.T) {
	p := mustParse(t, `
services:
  a:
    image: x
    depends_on:
      db:
        condition: service_healthy
        restart: true
      cache:
    networks:
      front:
        aliases: [www]
      back:
networks:
  front:
  back:
volumes:
  data:
`)
	a := p.Services[0]
	wantDeps := []Dependency{{"db", "service_healthy"}, {"cache", "service_started"}}
	if !reflect.DeepEqual(a.DependsOn, wantDeps) {
		t.Errorf("depends_on %+v", a.DependsOn)
	}
	if !reflect.DeepEqual(a.Networks, []string{"front", "back"}) {
		t.Errorf("networks %v", a.Networks)
	}
	wantUnknown := []string{"depends_on.db.restart", "networks.front.aliases"}
	if !reflect.DeepEqual(a.Unknown, wantUnknown) {
		t.Errorf("unknown %v", a.Unknown)
	}
	if !reflect.DeepEqual(p.Networks, []string{"front", "back"}) || !reflect.DeepEqual(p.Volumes, []string{"data"}) {
		t.Errorf("top level: %v %v", p.Networks, p.Volumes)
	}
}

func TestReplicasAndDeploy(t *testing.T) {
	p := mustParse(t, `
services:
  a:
    image: x
    deploy:
      replicas: 3
      resources:
        limits: {cpus: "1"}
  b:
    image: x
    scale: 2
`)
	a, b := p.Services[0], p.Services[1]
	if a.Replicas != 3 || b.Replicas != 2 {
		t.Errorf("replicas %d %d", a.Replicas, b.Replicas)
	}
	if !reflect.DeepEqual(a.Unknown, []string{"deploy.resources"}) {
		t.Errorf("unknown %v", a.Unknown)
	}
}

func TestBuildShortForm(t *testing.T) {
	p := mustParse(t, "services:\n  a:\n    build: ./dir\n")
	if b := p.Services[0].Build; b == nil || b.Context != "./dir" {
		t.Errorf("build %+v", b)
	}
}

func TestAnchorsAndMerge(t *testing.T) {
	p := mustParse(t, `
x-common: &common
  image: base
  restart: always
  environment:
    LEVEL: info
services:
  a:
    <<: *common
    restart: "no"
  b:
    <<: *common
    image: other
`)
	a, b := p.Services[0], p.Services[1]
	if a.Image != "base" || a.Restart != "no" || len(a.Environment) != 1 {
		t.Errorf("a: %+v", a)
	}
	if b.Image != "other" || b.Restart != "always" {
		t.Errorf("b: %+v", b)
	}
	if len(a.Unknown) != 0 || len(p.Unknown) != 0 {
		t.Errorf("extension keys must not be reported: %v %v", a.Unknown, p.Unknown)
	}
}

func TestTopLevelUnknown(t *testing.T) {
	p := mustParse(t, "version: '3.8'\nservices:\n  a:\n    image: x\nconfigs:\n  c:\n    file: ./c\n")
	if !reflect.DeepEqual(p.Unknown, []string{"configs"}) {
		t.Errorf("unknown %v", p.Unknown)
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"empty", "", "empty"},
		{"no services", "name: x\n", "no services"},
		{"not a map", "- a\n", "expected a mapping"},
		{"bad count", "services:\n  a:\n    image: x\n    scale: lots\n", "not a count"},
		{"dup key", "services:\n  a:\n    image: x\n    image: y\n", "twice"},
		{"bad port", "services:\n  a:\n    image: x\n    ports: ['a:b:c:d']\n", "not valid"},
		{"ports string", "services:\n  a:\n    image: x\n    ports: 80\n", "expected a list"},
		{"bad yaml", "services: [\n", "yaml"},
	}
	for _, c := range cases {
		_, err := Parse([]byte(c.src))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want it to contain %q", c.name, err, c.want)
		}
	}
}

func TestErrorNamesServiceAndLine(t *testing.T) {
	_, err := Parse([]byte("services:\n  web:\n    image: x\n    ports: 80\n"))
	if err == nil || !strings.Contains(err.Error(), `service "web"`) || !strings.Contains(err.Error(), "line 4") {
		t.Errorf("error %v", err)
	}
}

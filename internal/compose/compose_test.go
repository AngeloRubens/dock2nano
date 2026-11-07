package compose

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
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
	if app.Healthcheck == nil || !reflect.DeepEqual(app.Healthcheck.Test.Exec, []string{"CMD", "wget", "-q", "-O-", "http://localhost:9000/"}) {
		t.Errorf("healthcheck %+v", app.Healthcheck)
	}
	if !reflect.DeepEqual(app.Unknown, []string{"healthcheck.interval", "privileged"}) {
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
		{"127.0.0.1::80", Port{HostIP: "127.0.0.1", Target: "80", Protocol: "tcp"}},
		{"[::1]::80", Port{HostIP: "::1", Target: "80", Protocol: "tcp"}},
		{"[::1]:8080:80", Port{HostIP: "::1", Published: "8080", Target: "80", Protocol: "tcp"}},
	}
	for _, c := range cases {
		got, err := parseShortPort(c.in)
		if err != nil || got != c.want {
			t.Errorf("%q: got %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "a:b:c:d", "80/", "[::1:80", "8080:", "[::1]:80", "[::1]8080:80", "80/tcp/udp", "80/foo"} {
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
		{"db:/d:rw,z", Volume{Type: "volume", Source: "db", Target: "/d", Options: "z"}},
		{"~/cfg:/cfg:ro,z", Volume{Type: "bind", Source: "~/cfg", Target: "/cfg", ReadOnly: true, Options: "z"}},
		{"${PWD}:/app", Volume{Type: "unresolved", Source: "${PWD}", Target: "/app"}},
		{".data:/data", Volume{Type: "bind", Source: ".data", Target: "/data"}},
		{"C:\\data:/data", Volume{Type: "bind", Source: "C:\\data", Target: "/data"}},
		{"a:/b", Volume{Type: "volume", Source: "a", Target: "/b"}},
		{"a:/b:ro", Volume{Type: "volume", Source: "a", Target: "/b", ReadOnly: true}},
		{"C:/data:/data", Volume{Type: "bind", Source: "C:/data", Target: "/data"}},
		{"./${X}:/app", Volume{Type: "bind", Source: "./${X}", Target: "/app"}},
		{"db:/d:nocopy", Volume{Type: "volume", Source: "db", Target: "/d", Options: "nocopy"}},
	}
	for _, c := range cases {
		got, err := parseShortVolume(c.in)
		if err != nil || got != c.want {
			t.Errorf("%q: got %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"a:b:c:d", "/a:/b:/c", "a:/b:", ""} {
		if _, err := parseShortVolume(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
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
	if a.Replicas == nil || *a.Replicas != 3 || b.Replicas == nil || *b.Replicas != 2 {
		t.Errorf("replicas %v %v", a.Replicas, b.Replicas)
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
		{"two documents", "services:\n  a:\n    image: x\n---\nservices:\n  b:\n    image: y\n", "more than one"},
		{"scale disagrees", "services:\n  a:\n    image: x\n    scale: 2\n    deploy:\n      replicas: 3\n", "disagree"},
		{"self merge", "services:\n  a: &a\n    image: x\n    <<: *a\n", "contains it"},
		{"bad boolean", "services:\n  a:\n    image: x\n    volumes:\n      - {type: bind, source: ., target: /t, read_only: maybe}\n", "not a boolean"},
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

// mergeBomb builds a file where each level merges the level below fan times.
func mergeBomb(depth, fan int) string {
	var b strings.Builder
	b.WriteString("x-l0: &l0 {k: v}\n")
	for i := 1; i <= depth; i++ {
		fmt.Fprintf(&b, "x-l%d: &l%d {<<: [", i, i)
		for j := 0; j < fan; j++ {
			if j > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, "*l%d", i-1)
		}
		b.WriteString("]}\n")
	}
	fmt.Fprintf(&b, "services: {s: {<<: *l%d, image: x}}\n", depth)
	return b.String()
}

// parseWithin fails the test if Parse does not return within the limit.
func parseWithin(t *testing.T, src string, limit time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := Parse([]byte(src))
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatal("Parse did not return in time")
		return nil
	}
}

func TestMergeBombIsBounded(t *testing.T) {
	err := parseWithin(t, mergeBomb(5, 9), 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "too many merged") {
		t.Errorf("error %v", err)
	}
}

func TestDeepMergeBombIsRefused(t *testing.T) {
	if err := parseWithin(t, mergeBomb(9, 10), 10*time.Second); err == nil {
		t.Error("a merge bomb was accepted")
	}
}

func TestWideMergeIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("x-b: &b {")
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&b, "k%d: v,", i)
	}
	b.WriteString("}\nservices: {s: {image: x, <<: [")
	for i := 0; i < 200; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("*b")
	}
	b.WriteString("]}}\n")
	err := parseWithin(t, b.String(), 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "too many merged") {
		t.Errorf("error %v", err)
	}
}

func TestAliasAmplificationIsRefused(t *testing.T) {
	var b strings.Builder
	b.WriteString("x-big: &big [")
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&b, "%d,", i)
	}
	b.WriteString("]\nservices:\n")
	for i := 0; i < 1500; i++ {
		fmt.Fprintf(&b, "  s%d:\n    image: x\n    expose: *big\n", i)
	}
	err := parseWithin(t, b.String(), 20*time.Second)
	if err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("error %v", err)
	}
}

func TestMergeSequenceFirstWins(t *testing.T) {
	p := mustParse(t, `
x-a: &a
  image: from-a
  user: ua
x-b: &b
  image: from-b
  user: ub
  restart: always
services:
  s:
    <<: [*a, *b]
`)
	s := p.Services[0]
	if s.Image != "from-a" || s.User != "ua" || s.Restart != "always" {
		t.Errorf("service %+v", s)
	}
}

func TestHealthcheckUnknownKeys(t *testing.T) {
	p := mustParse(t, "services:\n  a:\n    image: x\n    healthcheck:\n      test: [CMD, 'true']\n      interval: 10s\n      disable: yes\n")
	a := p.Services[0]
	if !reflect.DeepEqual(a.Unknown, []string{"healthcheck.interval"}) || !a.Healthcheck.Disable {
		t.Errorf("unknown %v, healthcheck %+v", a.Unknown, a.Healthcheck)
	}
}

func TestEnvFileLongForm(t *testing.T) {
	p := mustParse(t, "services:\n  a:\n    image: x\n    env_file:\n      - path: ./a.env\n        required: false\n      - b.env\n  b:\n    image: x\n    env_file: one.env\n")
	a, b := p.Services[0], p.Services[1]
	if !reflect.DeepEqual(a.EnvFile, []string{"./a.env", "b.env"}) || !reflect.DeepEqual(a.Unknown, []string{"env_file[0].required"}) {
		t.Errorf("%v %v", a.EnvFile, a.Unknown)
	}
	if !reflect.DeepEqual(b.EnvFile, []string{"one.env"}) {
		t.Errorf("%v", b.EnvFile)
	}
}

func TestBooleanForms(t *testing.T) {
	for _, v := range []string{"true", "True", "TRUE", "yes", "on", `"true"`} {
		p := mustParse(t, "services:\n  a:\n    image: x\n    volumes:\n      - type: bind\n        source: ./s\n        target: /t\n        read_only: "+v+"\n")
		if !p.Services[0].Volumes[0].ReadOnly {
			t.Errorf("read_only: %s gave false", v)
		}
	}
	p := mustParse(t, "services:\n  a:\n    image: x\n    volumes:\n      - {type: bind, source: ./s, target: /t, read_only: no}\n")
	if p.Services[0].Volumes[0].ReadOnly {
		t.Error("read_only: no gave true")
	}
}

func TestEmptyCommandIsSetAndNullIsNot(t *testing.T) {
	p := mustParse(t, "services:\n  a:\n    image: x\n    entrypoint: \"\"\n    command: null\n")
	a := p.Services[0]
	if !a.Entrypoint.IsSet() || a.Command.IsSet() {
		t.Errorf("%+v %+v", a.Entrypoint, a.Command)
	}
}

func TestReplicasZeroIsKept(t *testing.T) {
	p := mustParse(t, "services:\n  a:\n    image: x\n    deploy:\n      replicas: 0\n  b:\n    image: x\n  c:\n    image: x\n    scale: 0\n")
	a, b, c := p.Services[0], p.Services[1], p.Services[2]
	if a.Replicas == nil || *a.Replicas != 0 || b.Replicas != nil || c.Replicas == nil || *c.Replicas != 0 {
		t.Errorf("replicas %v %v %v", a.Replicas, b.Replicas, c.Replicas)
	}
}

func TestTopLevelNetworkVolumeConfigListed(t *testing.T) {
	p := mustParse(t, "services:\n  a:\n    image: x\nvolumes:\n  data:\n    external: true\nnetworks:\n  front:\n    driver: bridge\n")
	if !reflect.DeepEqual(p.Unknown, []string{"volumes.data.external", "networks.front.driver"}) {
		t.Errorf("unknown %v", p.Unknown)
	}
}

func TestUnresolvedVolumeIsListed(t *testing.T) {
	p := mustParse(t, "services:\n  a:\n    image: x\n    volumes:\n      - ${PWD}:/app\n")
	a := p.Services[0]
	if a.Volumes[0].Type != "unresolved" || len(a.Unknown) != 1 || !strings.HasPrefix(a.Unknown[0], "volumes[0].source") {
		t.Errorf("%+v %v", a.Volumes, a.Unknown)
	}
}

func TestNamesStartingWithX(t *testing.T) {
	p := mustParse(t, "services:\n  a:\n    image: x\n    environment:\n      x-token: s\n    build:\n      context: .\n      args:\n        x-arg: v\n    depends_on:\n      x-db:\n    networks:\n      x-net:\n  x-db:\n    image: y\n")
	a := p.Services[0]
	if len(p.Services) != 2 || p.Service("x-db") == nil {
		t.Errorf("services %d", len(p.Services))
	}
	if !reflect.DeepEqual(a.Environment, []EnvVar{{"x-token", "s", true}}) || !reflect.DeepEqual(a.Build.Args, []EnvVar{{"x-arg", "v", true}}) {
		t.Errorf("env %+v args %+v", a.Environment, a.Build.Args)
	}
	if len(a.DependsOn) != 1 || a.DependsOn[0].Service != "x-db" || !reflect.DeepEqual(a.Networks, []string{"x-net"}) {
		t.Errorf("depends_on %+v networks %v", a.DependsOn, a.Networks)
	}
	const note = " (Compose reads it as an extension and drops it)"
	want := []string{"environment.x-token" + note, "build.args.x-arg" + note, "networks.x-net" + note}
	if !reflect.DeepEqual(a.Unknown, want) {
		t.Errorf("unknown %v", a.Unknown)
	}
}

func TestXNamesUnderServicesAndTopLevel(t *testing.T) {
	p := mustParse(t, "services:\n  x-db:\n    image: y\nvolumes:\n  x-data:\nnetworks:\n  x-net:\n")
	if p.Service("x-db") == nil || !reflect.DeepEqual(p.Volumes, []string{"x-data"}) || !reflect.DeepEqual(p.Networks, []string{"x-net"}) {
		t.Errorf("%+v", p)
	}
}

func TestInterpolationWithDefaultInShortSyntax(t *testing.T) {
	p, err := parseShortPort("${WEB_PORT:-8080}:80")
	if err != nil || p != (Port{Published: "${WEB_PORT:-8080}", Target: "80", Protocol: "tcp"}) {
		t.Errorf("port %+v %v", p, err)
	}
	v, err := parseShortVolume("${DATA:-./data}:/data:ro")
	if err != nil || v != (Volume{Type: "unresolved", Source: "${DATA:-./data}", Target: "/data", ReadOnly: true}) {
		t.Errorf("volume %+v %v", v, err)
	}
}

func TestVariablesInCountsAndBooleans(t *testing.T) {
	p := mustParse(t, "services:\n  a:\n    image: x\n    scale: ${N}\n    deploy:\n      replicas: ${N:-2}\n    volumes:\n      - {type: bind, source: ., target: /t, read_only: '${RO}'}\n    healthcheck:\n      test: [CMD, 'true']\n      disable: ${OFF}\n")
	a := p.Services[0]
	want := []string{"scale (holds a variable)", "deploy.replicas (holds a variable)", "volumes[0].read_only (holds a variable)", "healthcheck.disable (holds a variable)"}
	if a.Replicas != nil || !reflect.DeepEqual(a.Unknown, want) {
		t.Errorf("replicas %v unknown %v", a.Replicas, a.Unknown)
	}
}

func TestNullEntriesInLists(t *testing.T) {
	for _, src := range []string{
		"services:\n  a:\n    image: x\n    ports: [~]\n",
		"services:\n  a:\n    image: x\n    volumes: [null]\n",
		"services:\n  a:\n    image: x\n    expose: [~]\n",
	} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("accepted: %q", src)
		}
	}
}

func TestNestedNulls(t *testing.T) {
	p := mustParse(t, "services:\n  a:\n    image: x\n    ports: [{target: 80, protocol: null}]\n    depends_on: {db: {condition: null}}\n    build: {context: ., args: null}\n    deploy: {replicas: null}\n    volumes: [{type: bind, source: ., target: /t, read_only: null}]\n")
	a := p.Services[0]
	if a.Ports[0].Protocol != "tcp" || a.DependsOn[0].Condition != "service_started" || a.Replicas != nil || a.Volumes[0].ReadOnly || len(a.Unknown) != 0 {
		t.Errorf("service %+v", a)
	}
}

func TestLongVolumeTypes(t *testing.T) {
	if _, err := Parse([]byte("services:\n  a:\n    image: x\n    volumes:\n      - {source: ./src, target: /app}\n")); err == nil {
		t.Error("a long volume without a type was accepted")
	}
	p := mustParse(t, "services:\n  a:\n    image: x\n    volumes:\n      - {type: npipe, source: x, target: /y}\n")
	if !reflect.DeepEqual(p.Services[0].Unknown, []string{"volumes[0].type (npipe)"}) {
		t.Errorf("unknown %v", p.Services[0].Unknown)
	}
}

func TestLongPortProtocolIsChecked(t *testing.T) {
	if _, err := Parse([]byte("services:\n  a:\n    image: x\n    ports: [{target: 80, protocol: foo}]\n")); err == nil {
		t.Error("an unknown protocol was accepted")
	}
}

func TestHealthcheckNone(t *testing.T) {
	p := mustParse(t, "services:\n  a:\n    image: x\n    healthcheck:\n      test: [NONE]\n")
	if !p.Services[0].Healthcheck.Disable {
		t.Error("test NONE must disable the healthcheck")
	}
}

func TestNullValuesAreEmpty(t *testing.T) {
	p := mustParse(t, `
services:
  a:
    image: x
    build:
    ports:
    volumes:
    environment:
    env_file:
    depends_on:
    networks:
    expose:
    profiles:
    healthcheck:
    deploy:
    command:
volumes:
networks:
`)
	a := p.Services[0]
	if a.Build != nil || a.Ports != nil || a.Volumes != nil || a.Environment != nil || a.EnvFile != nil ||
		a.DependsOn != nil || a.Networks != nil || a.Expose != nil || a.Profiles != nil || a.Healthcheck != nil ||
		a.Replicas != nil || a.Command.IsSet() || len(a.Unknown) != 0 {
		t.Errorf("service %+v", a)
	}
	if p.Networks != nil || p.Volumes != nil {
		t.Errorf("top level %v %v", p.Networks, p.Volumes)
	}
}

func TestResetTagIsNull(t *testing.T) {
	p := mustParse(t, "services:\n  a:\n    image: !reset null\n    ports: !reset []\n")
	if p.Services[0].Image != "" || p.Services[0].Ports != nil {
		t.Errorf("image %q ports %v", p.Services[0].Image, p.Services[0].Ports)
	}
}

func TestPlainFields(t *testing.T) {
	p := mustParse(t, `
services:
  a:
    image: x
    working_dir: /srv
    user: "1000:1000"
    network_mode: host
    expose: ["80", 443]
    profiles: [debug]
    ports:
      - target: 53
        host_ip: 127.0.0.1
        protocol: udp
`)
	a := p.Services[0]
	if a.WorkingDir != "/srv" || a.User != "1000:1000" || a.NetworkMode != "host" {
		t.Errorf("service %+v", a)
	}
	if !reflect.DeepEqual(a.Expose, []string{"80", "443"}) || !reflect.DeepEqual(a.Profiles, []string{"debug"}) {
		t.Errorf("expose %v profiles %v", a.Expose, a.Profiles)
	}
	if a.Ports[0] != (Port{HostIP: "127.0.0.1", Target: "53", Protocol: "udp"}) {
		t.Errorf("port %+v", a.Ports[0])
	}
}

func TestBuildUnknownAndEmptyVarName(t *testing.T) {
	p := mustParse(t, "services:\n  a:\n    build:\n      context: .\n      network: host\n")
	if !reflect.DeepEqual(p.Services[0].Unknown, []string{"build.network"}) {
		t.Errorf("unknown %v", p.Services[0].Unknown)
	}
	if _, err := Parse([]byte("services:\n  a:\n    image: x\n    environment:\n      - =x\n")); err == nil {
		t.Error("an empty variable name was accepted")
	}
}

func TestRecursiveAliasIsRefused(t *testing.T) {
	_, err := Parse([]byte("x-a: &a {b: *a}\nservices: {s: {image: x}}\n"))
	if err == nil || !strings.Contains(err.Error(), "contains it") {
		t.Errorf("error %v", err)
	}
}

func TestCycleDoesNotHideAmplification(t *testing.T) {
	var b strings.Builder
	b.WriteString("x-a: &A\n")
	for i := 0; i < 40000; i++ {
		fmt.Fprintf(&b, "  k%d: v\n", i)
	}
	b.WriteString("  inner: &B\n    <<: *A\nservices:\n")
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&b, "  s%d: {<<: *B, image: x}\n", i)
	}
	if err := parseWithin(t, b.String(), 20*time.Second); err == nil {
		t.Error("accepted")
	}
}

func TestEnvFileNullEntries(t *testing.T) {
	for _, src := range []string{
		"services:\n  a:\n    image: x\n    env_file: [~]\n",
		"services:\n  a:\n    image: x\n    env_file: [{path: ~}]\n",
	} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("accepted: %q", src)
		}
	}
}

func TestTwoMergeKeysAreRefused(t *testing.T) {
	_, err := Parse([]byte("x-a: &a {image: a}\nx-b: &b {image: b}\nservices:\n  s: {<<: *a, <<: *b}\n"))
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("error %v", err)
	}
}

func TestExplicitKeyBeforeMergeWins(t *testing.T) {
	p := mustParse(t, "x-a: &a {image: other, user: ua}\nservices:\n  s: {image: mine, <<: *a}\n")
	if s := p.Services[0]; s.Image != "mine" || s.User != "ua" {
		t.Errorf("service %+v", s)
	}
}

func TestCountBases(t *testing.T) {
	p := mustParse(t, "services:\n  a:\n    image: x\n    scale: 010\n  b:\n    image: x\n    scale: 0x10\n  c:\n    image: x\n    scale: 1_000\n")
	a, b, c := p.Services[0], p.Services[1], p.Services[2]
	if *a.Replicas != 8 || *b.Replicas != 16 || *c.Replicas != 1000 {
		t.Errorf("replicas %d %d %d", *a.Replicas, *b.Replicas, *c.Replicas)
	}
}

func TestUnclosedVariableInShortSyntax(t *testing.T) {
	if _, err := parseShortPort("${X:80"); err == nil {
		t.Error("port accepted")
	}
	if _, err := parseShortVolume("${X:/data"); err == nil {
		t.Error("volume accepted")
	}
}

func TestResetRemovesEnvironmentVariable(t *testing.T) {
	p := mustParse(t, "services:\n  a:\n    image: x\n    environment:\n      A: !reset null\n      B: x\n")
	if got := p.Services[0].Environment; !reflect.DeepEqual(got, []EnvVar{{"B", "x", true}}) {
		t.Errorf("environment %+v", got)
	}
}

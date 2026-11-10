//go:build integration

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/AngeloRubens/dock2nano/internal/output"
	"github.com/AngeloRubens/dock2nano/internal/plan"
	"gopkg.in/yaml.v3"
)

const itNginxConf = `# user-nginx-conf-marker
worker_processes 1;
pid /tmp/nginx.pid;
error_log /dev/stderr;
events {
    worker_connections 64;
}
http {
    access_log off;
    default_type text/plain;
    server {
        listen 8080;
        location / {
            return 200 "served by the user config\n";
        }
    }
}
`

const itCompose = `name: it
services:
  nginx:
    image: nginx:1.27-alpine
    ports:
      - "8080:8080"
    volumes:
      - ./nginx.conf:/etc/nginx/nginx.conf:ro
    depends_on:
      - app
  app:
    build: ./app
`

const itDockerfile = `FROM alpine:3.20
RUN apk add --no-cache busybox-extras
RUN mkdir /www && echo hi > /www/index.html
EXPOSE 8081
CMD ["httpd","-f","-p","8081","-h","/www"]
`

func TestConvertTwoServices(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "version").CombinedOutput(); err != nil {
		t.Skipf("docker version failed: %v: %s", err, out)
	}
	dir := t.TempDir()
	home := t.TempDir()
	out := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("compose.yaml", itCompose)
	write("nginx.conf", itNginxConf)
	write("app/Dockerfile", itDockerfile)
	t.Cleanup(func() {
		exec.Command("docker", "rmi", "-f", ToolName+"/it/app:latest").Run()
	})

	var stdout, stderr bytes.Buffer
	app := newApp()
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Getwd = func() (string, error) { return dir, nil }
	code := app.Run(ctx, []string{"convert", "-f", filepath.Join(dir, "compose.yaml"), "--ops-home", home, "-o", out, "--pull", "--seconds", "20"})
	t.Logf("stderr:\n%s", stderr.String())
	t.Logf("report:\n%s", stdout.String())
	if code != 0 {
		t.Fatalf("exit status %d", code)
	}

	data, err := os.ReadFile(filepath.Join(out, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cf plan.ComposeFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		t.Fatal(err)
	}
	if len(cf.Packages) != 2 || cf.Packages[0].Pkg != "app" || cf.Packages[1].Pkg != "nginx" || cf.Packages[0].Name != "it_app" || cf.Packages[1].Name != "it_nginx" {
		t.Fatalf("compose.yaml:\n%s", data)
	}

	pkg := func(name string) string {
		m, err := filepath.Glob(filepath.Join(home, ".ops", "local_packages", "*", name))
		if err != nil || len(m) != 1 {
			t.Fatalf("package %s: %v %v", name, m, err)
		}
		return m[0]
	}
	for _, name := range []string{"it_app", "it_nginx"} {
		mf, err := os.ReadFile(filepath.Join(pkg(name), "package.manifest"))
		if err != nil {
			t.Fatal(err)
		}
		if err := output.ValidateManifestJSON(mf); err != nil {
			t.Errorf("%s: %v\n%s", name, err, mf)
		}
	}
	got, err := os.ReadFile(filepath.Join(pkg("it_nginx"), "sysroot", "etc", "nginx", "nginx.conf"))
	if err != nil {
		t.Fatal(err)
	}
	// the program forks, so the hub was consulted: the directives for one
	// process are added to the configuration of the user, which is otherwise
	// as it was; daemon off is on the command line (-g) and is not repeated
	want := "master_process off;\n" + itNginxConf
	if string(got) != want {
		t.Errorf("the nginx sysroot does not hold the configuration of the user plus the directives for one process:\n%s", got)
	}
	// nginx itself must accept the file with the arguments of the manifest
	mf, err := os.ReadFile(filepath.Join(pkg("it_nginx"), "package.manifest"))
	if err != nil {
		t.Fatal(err)
	}
	var man struct{ Args []string }
	if err := json.Unmarshal(mf, &man); err != nil || len(man.Args) < 1 {
		t.Fatalf("manifest: %v\n%s", err, mf)
	}
	confPath := filepath.Join(pkg("it_nginx"), "sysroot", "etc", "nginx", "nginx.conf")
	check := append([]string{"run", "--rm", "-v", confPath + ":/etc/nginx/nginx.conf:ro", "nginx:1.27-alpine", "nginx", "-t"}, man.Args[1:]...)
	if out, err := exec.CommandContext(ctx, "docker", check...).CombinedOutput(); err != nil {
		t.Errorf("nginx -t refuses the configuration with the arguments %v: %v\n%s", man.Args[1:], err, out)
	}
	if _, err := os.Stat(filepath.Join(pkg("it_app"), "sysroot", "www", "index.html")); err != nil {
		t.Errorf("the app sysroot has no /www/index.html: %v", err)
	}
	if !strings.Contains(stdout.String(), "a service reaches another as <pkg>.service") {
		t.Errorf("the report has no note about service names")
	}
}

const itDBCompose = `name: itdb
services:
  db:
    image: mysql:8.4
    environment:
      MYSQL_ROOT_PASSWORD: secret
      MYSQL_DATABASE: app
  client:
    build: ./client
    depends_on:
      - db
`

const itDBDockerfile = `FROM mysql:8.4
ENTRYPOINT []
CMD ["sh","-c","mysql -h db -uroot -psecret -e 'select 1' app && echo connected && sleep 60"]
`

// TestConvertServiceThatNeedsADatabase observes a client that connects to its
// database when it starts: it only runs when the database runs beside it on
// the network of the run, under its name.
func TestConvertServiceThatNeedsADatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "version").CombinedOutput(); err != nil {
		t.Skipf("docker version failed: %v: %s", err, out)
	}
	before := make([]map[string]bool, len(itLeftoverCommands))
	for i, c := range itLeftoverCommands {
		before[i] = map[string]bool{}
		for _, l := range itDockerLines(t, c) {
			before[i][l] = true
		}
	}
	dir := t.TempDir()
	home := t.TempDir()
	out := t.TempDir()
	for name, content := range map[string]string{"compose.yaml": itDBCompose, "client/Dockerfile": itDBDockerfile} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		exec.Command("docker", "rmi", "-f", ToolName+"/itdb/client:latest").Run()
	})

	var stdout, stderr bytes.Buffer
	app := newApp()
	app.Stdout = &stdout
	app.Stderr = &stderr
	app.Getwd = func() (string, error) { return dir, nil }
	code := app.Run(ctx, []string{"convert", "-f", filepath.Join(dir, "compose.yaml"), "--ops-home", home, "-o", out, "--pull", "--seconds", "40"})
	t.Logf("stderr:\n%s", stderr.String())
	t.Logf("report:\n%s", stdout.String())
	if code != 0 {
		t.Fatalf("exit status %d", code)
	}

	report := stdout.String()
	// the client ran until the end of the observation: it connected
	if strings.Contains(report, "stopped with status") || strings.Contains(stderr.String(), "stopped with status") {
		t.Errorf("a container stopped before the end of the observation")
	}
	if strings.Contains(report, "was not started for the observation") {
		t.Errorf("the database was not started for the client")
	}
	if !strings.Contains(stderr.String(), "db: starting mysql:8.4 for client") {
		t.Errorf("the database was not started beside the client")
	}
	if !regexp.MustCompile(`program \S*mysqld\b`).MatchString(report) {
		t.Errorf("the program of the database is not mysqld")
	}

	// nothing of the run is left behind (what existed before, from other tests, is not counted)
	for i, c := range itLeftoverCommands {
		now := itDockerLines(t, c)
		for _, l := range now {
			if !before[i][l] {
				t.Errorf("docker %v lists what the run left: %s", c, l)
			}
		}
	}
}

var itLeftoverCommands = [][]string{
	{"ps", "-a", "--format", "{{.Names}}", "--filter", "name=dock2nano-"},
	{"network", "ls", "--format", "{{.Name}}", "--filter", "name=dock2nano-"},
}

func itDockerLines(t *testing.T, args []string) []string {
	t.Helper()
	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		t.Fatalf("docker %v: %v", args, err)
	}
	return strings.Fields(string(out))
}

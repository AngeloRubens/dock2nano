//go:build integration

package cmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	if string(got) != itNginxConf {
		t.Errorf("the nginx sysroot does not hold the configuration of the user:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(pkg("it_app"), "sysroot", "www", "index.html")); err != nil {
		t.Errorf("the app sysroot has no /www/index.html: %v", err)
	}
	if !strings.Contains(stdout.String(), "a service reaches another as <pkg>.service") {
		t.Errorf("the report has no note about service names")
	}
}

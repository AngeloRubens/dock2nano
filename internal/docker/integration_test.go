//go:build integration

package docker_test

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AngeloRubens/dock2nano/internal/docker"
)

const (
	alpine = "alpine:3.20"
	nginx  = "nginx:1.27-alpine"
)

var unsafeName = regexp.MustCompile(`[^a-z0-9]+`)

// uniqueName is a lower case name that is safe for a container and an image.
func uniqueName(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	base := unsafeName.ReplaceAllString(strings.ToLower(t.Name()), "-")
	return "d2n-" + strings.Trim(base, "-") + "-" + hex.EncodeToString(b)
}

// needDocker skips the test when the docker command line does not work.
func needDocker(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "version").CombinedOutput(); err != nil {
		t.Skipf("docker version failed: %v: %s", err, out)
	}
}

// pulled makes sure image is on the machine.
func pulled(t *testing.T, c *docker.Client, image string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := c.Pull(ctx, image); err != nil {
		t.Fatalf("Pull(%s): %v", image, err)
	}
}

// removeContainer removes a container by name or id at the end of the test.
func removeContainer(t *testing.T, c *docker.Client, id string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = c.Remove(ctx, id, true)
	})
}

// removeImage removes an image at the end of the test.
func removeImage(t *testing.T, ref string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = exec.CommandContext(ctx, "docker", "rmi", "--force", ref).Run()
	})
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

// startSleeper creates and starts a container that sleeps, and removes it at
// the end of the test.
func startSleeper(t *testing.T, c *docker.Client, name string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ep := []string{"sleep"}
	id, err := c.Create(ctx, docker.CreateOptions{Image: alpine, Name: name, Entrypoint: &ep, Cmd: []string{"300"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	removeContainer(t, c, id)
	if err := c.Start(ctx, id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return id
}

func TestIntegrationPullHasImage(t *testing.T) {
	needDocker(t)
	c := docker.Default()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pulled(t, c, alpine)
	ok, err := c.HasImage(ctx, alpine)
	if err != nil || !ok {
		t.Fatalf("HasImage(%s) = %v, %v; want true, nil", alpine, ok, err)
	}

	missing := uniqueName(t) + ":nope"
	ok, err = c.HasImage(ctx, missing)
	if err != nil || ok {
		t.Fatalf("HasImage(%s) = %v, %v; want false, nil", missing, ok, err)
	}
}

func TestIntegrationInspect(t *testing.T) {
	needDocker(t)
	c := docker.Default()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pulled(t, c, alpine)

	cfg, err := c.Inspect(ctx, alpine)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("inspect: %+v", cfg)
	if len(cfg.Cmd) == 0 {
		t.Errorf("Cmd is empty")
	}
	if cfg.OS != "linux" {
		t.Errorf("OS = %q, want linux", cfg.OS)
	}
	if cfg.Architecture == "" {
		t.Errorf("Architecture is empty")
	}
	if cfg.ID == "" {
		t.Errorf("ID is empty")
	}

	if _, err := c.Inspect(ctx, uniqueName(t)+":nope"); err == nil {
		t.Errorf("Inspect of a missing image succeeded")
	}
}

func TestIntegrationBuild(t *testing.T) {
	needDocker(t)
	c := docker.Default()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	pulled(t, c, alpine)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Dockerfile"), `FROM alpine:3.20 AS app
ARG GREETING=none
ENV A=b
ENV GREETING=$GREETING
CMD ["sleep","300"]

FROM app AS other
ENV OTHER=1
`)
	tag := uniqueName(t) + ":latest"
	removeImage(t, tag)
	err := c.Build(ctx, docker.BuildOptions{
		Context: dir,
		Target:  "app",
		Args:    map[string]string{"GREETING": "hi"},
		Tag:     tag,
		Quiet:   true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	cfg, err := c.Inspect(ctx, tag)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cfg.Env, "A=b") {
		t.Errorf("Env = %v, want A=b in it", cfg.Env)
	}
	if !slices.Contains(cfg.Env, "GREETING=hi") {
		t.Errorf("Env = %v, want GREETING=hi in it (the build argument)", cfg.Env)
	}
	if slices.Contains(cfg.Env, "OTHER=1") {
		t.Errorf("Env = %v has OTHER=1: the target was not used", cfg.Env)
	}
	if !slices.Equal(cfg.Cmd, []string{"sleep", "300"}) {
		t.Errorf("Cmd = %v, want [sleep 300]", cfg.Cmd)
	}
}

func TestIntegrationBuildFailureRedactsSecret(t *testing.T) {
	needDocker(t)
	c := docker.Default()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	pulled(t, c, alpine)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Dockerfile"), "FROM alpine:3.20\nARG SECRET\nRUN false\n")
	tag := uniqueName(t) + ":latest"
	removeImage(t, tag)
	err := c.Build(ctx, docker.BuildOptions{Context: dir, Tag: tag, Args: map[string]string{"SECRET": "hunter2"}})
	if err == nil {
		t.Fatal("Build of a failing Dockerfile succeeded")
	}
	t.Logf("error: %v", err)
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the error shows the build argument value: %v", err)
	}
	if !strings.Contains(err.Error(), "SECRET=***") {
		t.Errorf("the error does not show the redacted argument: %v", err)
	}
	var ee *docker.ExitError
	if !errors.As(err, &ee) {
		t.Errorf("the error is not an *ExitError: %T", err)
	}
}

func TestIntegrationContainerLifecycle(t *testing.T) {
	needDocker(t)
	c := docker.Default()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pulled(t, c, alpine)

	bindDir := t.TempDir()
	writeFile(t, filepath.Join(bindDir, "mark"), "from the host\n")
	copyDir := t.TempDir()

	ep := []string{"sh", "-c"}
	name := uniqueName(t)
	id, err := c.Create(ctx, docker.CreateOptions{
		Image:       alpine,
		Name:        name,
		Entrypoint:  &ep,
		Cmd:         []string{"echo hello-log; exec sleep 300"},
		Binds:       []string{bindDir + ":/mnt/host:ro"},
		CapAdd:      []string{"SYS_PTRACE"},
		SecurityOpt: []string{"seccomp=unconfined"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	removeContainer(t, c, id)

	if running, err := c.Running(ctx, id); err != nil || running {
		t.Fatalf("Running before Start = %v, %v; want false, nil", running, err)
	}
	if err := c.Start(ctx, id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if running, err := c.Running(ctx, id); err != nil || !running {
		t.Fatalf("Running after Start = %v, %v; want true, nil", running, err)
	}

	out, err := c.Exec(ctx, id, []string{"cat", "/mnt/host/mark"})
	if err != nil || string(out) != "from the host\n" {
		t.Errorf("Exec cat of the bind = %q, %v", out, err)
	}
	out, err = c.Exec(ctx, id, []string{"echo", "hi"})
	if err != nil || strings.TrimSpace(string(out)) != "hi" {
		t.Errorf("Exec echo = %q, %v", out, err)
	}
	out, err = c.ExecUser(ctx, id, "root", []string{"id", "-un"})
	if err != nil || strings.TrimSpace(string(out)) != "root" {
		t.Errorf("ExecUser root id -un = %q, %v", out, err)
	}
	// a command that fails is an ExitError, not one of the container errors
	_, err = c.Exec(ctx, id, []string{"sh", "-c", "exit 3"})
	var ee *docker.ExitError
	if !errors.As(err, &ee) || ee.Code != 3 {
		t.Errorf("Exec of exit 3: err = %v, want an *ExitError with code 3", err)
	}
	if errors.Is(err, docker.ErrNotRunning) || errors.Is(err, docker.ErrNoContainer) {
		t.Errorf("Exec of exit 3 was taken for a container error: %v", err)
	}

	ip, err := c.IPAddress(ctx, id)
	if err != nil || net.ParseIP(ip) == nil {
		t.Errorf("IPAddress = %q, %v; want an IP address", ip, err)
	}

	if err := c.CopyFrom(ctx, id, "/etc/alpine-release", copyDir); err != nil {
		t.Fatalf("CopyFrom: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(copyDir, "alpine-release"))
	if err != nil || !strings.HasPrefix(string(got), "3.20") {
		t.Errorf("copied alpine-release = %q, %v; want 3.20...", got, err)
	}

	var tarBuf bytes.Buffer
	if err := c.Export(ctx, id, &tarBuf); err != nil {
		t.Fatalf("Export: %v", err)
	}
	tr := tar.NewReader(&tarBuf)
	first, err := tr.Next()
	if err != nil {
		t.Fatalf("Export: reading the first tar header: %v", err)
	}
	t.Logf("export: first entry %s, %d bytes in all", first.Name, tarBuf.Len())
	found := first.Name == "etc/alpine-release"
	for !found {
		h, err := tr.Next()
		if err != nil {
			break
		}
		found = h.Name == "etc/alpine-release"
	}
	if !found {
		t.Errorf("Export: etc/alpine-release is not in the tar")
	}

	logs, err := c.Logs(ctx, id)
	if err != nil || !strings.Contains(string(logs), "hello-log") {
		t.Errorf("Logs = %q, %v; want hello-log in it", logs, err)
	}
	tail, err := c.LogsTail(ctx, id, 1)
	if err != nil || !strings.Contains(string(tail), "hello-log") {
		t.Errorf("LogsTail = %q, %v; want hello-log in it", tail, err)
	}

	if err := c.Stop(ctx, id, 1); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if running, err := c.Running(ctx, id); err != nil || running {
		t.Errorf("Running after Stop = %v, %v; want false, nil", running, err)
	}
	_, err = c.Exec(ctx, id, []string{"echo", "x"})
	if !errors.Is(err, docker.ErrNotRunning) {
		t.Errorf("Exec on a stopped container: err = %v, want ErrNotRunning", err)
	}
	if errors.As(err, &ee) {
		t.Errorf("Exec on a stopped container is an *ExitError: %v", err)
	}
	code, err := c.Wait(ctx, id)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	t.Logf("exit code after Stop: %d", code)
	if code == 0 {
		t.Errorf("exit code after Stop = 0, want a non-zero code (the sleep was stopped)")
	}

	if err := c.Remove(ctx, id, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := c.Running(ctx, id); err == nil {
		t.Errorf("Running on a removed container succeeded")
	}
}

func TestIntegrationExecNoContainer(t *testing.T) {
	needDocker(t)
	c := docker.Default()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	_, err := c.Exec(ctx, uniqueName(t), []string{"echo", "x"})
	if !errors.Is(err, docker.ErrNoContainer) {
		t.Fatalf("Exec on an unknown container: err = %v, want ErrNoContainer", err)
	}
	var ee *docker.ExitError
	if errors.As(err, &ee) {
		t.Errorf("Exec on an unknown container is an *ExitError: %v", err)
	}
}

func TestIntegrationWaitDeadline(t *testing.T) {
	needDocker(t)
	c := docker.Default()
	pulled(t, c, alpine)
	id := startSleeper(t, c, uniqueName(t))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	_, err := c.Wait(ctx, id)
	elapsed := time.Since(start)
	t.Logf("Wait returned after %v: %v", elapsed, err)
	if elapsed > 5*time.Second {
		t.Errorf("Wait took %v, want less than 5s", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Wait: err = %v, want context.DeadlineExceeded", err)
	}
}

func TestIntegrationPullNginx(t *testing.T) {
	needDocker(t)
	c := docker.Default()
	pulled(t, c, nginx)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cfg, err := c.Inspect(ctx, nginx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cfg.ExposedPorts, "80/tcp") {
		t.Errorf("ExposedPorts = %v, want 80/tcp in it", cfg.ExposedPorts)
	}
	if len(cfg.Entrypoint) == 0 {
		t.Errorf("Entrypoint is empty")
	}
}

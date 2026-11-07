package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type reply struct {
	stdout string
	stderr string
	err    error
}

// fake records every call and answers with the queued replies.
type fake struct {
	calls   [][]string
	replies []reply
}

func (f *fake) Run(ctx context.Context, stdin io.Reader, args ...string) ([]byte, []byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	var r reply
	if len(f.replies) > 0 {
		r = f.replies[0]
		f.replies = f.replies[1:]
	}
	return []byte(r.stdout), []byte(r.stderr), r.err
}

// streamFake is a fake that is also a Streamer.
type streamFake struct {
	fake
	streamed [][]string
	data     string
	err      error
}

func (s *streamFake) Stream(ctx context.Context, w io.Writer, args ...string) ([]byte, error) {
	s.streamed = append(s.streamed, append([]string(nil), args...))
	io.WriteString(w, s.data)
	return []byte("streamerr"), s.err
}

func newFake(replies ...reply) (*Client, *fake) {
	f := &fake{replies: replies}
	return New(f), f
}

func wantCall(t *testing.T, f *fake, want ...string) {
	t.Helper()
	if len(f.calls) != 1 {
		t.Fatalf("got %d docker calls, want 1: %v", len(f.calls), f.calls)
	}
	if !reflect.DeepEqual(f.calls[0], want) {
		t.Fatalf("argv = %q, want %q", f.calls[0], want)
	}
}

func TestBuildArgs(t *testing.T) {
	buildArgMap := map[string]string{
		"K9": "9", "K3": "3", "K7": "7", "K1": "a b", "K5": "", "K0": "0",
		"K8": "8", "K2": "2", "K6": "6", "K4": "4", "KA": "10", "KB": "11",
	}
	got, env, err := buildArgs(BuildOptions{
		Context:    "./app",
		Dockerfile: "docker/Dockerfile.prod",
		Target:     "runtime",
		Args:       buildArgMap,
		Tag:        "app:latest",
		Platform:   "linux/amd64",
		Quiet:      true,
	})
	want := []string{
		"build", "--file", "docker/Dockerfile.prod", "--target", "runtime",
		"--platform", "linux/amd64", "--tag", "app:latest", "--quiet",
		"--build-arg", "K0", "--build-arg", "K1", "--build-arg", "K2",
		"--build-arg", "K3", "--build-arg", "K4", "--build-arg", "K5",
		"--build-arg", "K6", "--build-arg", "K7", "--build-arg", "K8",
		"--build-arg", "K9", "--build-arg", "KA", "--build-arg", "KB",
		"--", "./app",
	}
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	wantEnv := []string{"K0=0", "K1=a b", "K2=2", "K3=3", "K4=4", "K5=", "K6=6", "K7=7", "K8=8", "K9=9", "KA=10", "KB=11"}
	if !reflect.DeepEqual(env, wantEnv) {
		t.Fatalf("env = %q, want %q", env, wantEnv)
	}
	if got, env, err := buildArgs(BuildOptions{Context: "."}); err != nil || env != nil || !reflect.DeepEqual(got, []string{"build", "--", "."}) {
		t.Fatalf("minimal argv = %q, env %q, err %v", got, env, err)
	}
	if got, _, _ := buildArgs(BuildOptions{Context: "-evil"}); !reflect.DeepEqual(got, []string{"build", "--", "-evil"}) {
		t.Fatalf("option-like context argv = %q", got)
	}
}

func TestBuildArgsBadName(t *testing.T) {
	for _, k := range []string{"A=B", "A\x00B", ""} {
		if _, _, err := buildArgs(BuildOptions{Context: ".", Args: map[string]string{k: "v"}}); err == nil {
			t.Errorf("build argument name %q accepted", k)
		}
	}
}

func TestBuild(t *testing.T) {
	c, f := newFake()
	if err := c.Build(context.Background(), BuildOptions{Context: ".", Tag: "x"}); err != nil {
		t.Fatal(err)
	}
	wantCall(t, f, "build", "--tag", "x", "--", ".")

	if err := c.Build(context.Background(), BuildOptions{}); err == nil || !strings.Contains(err.Error(), "no build context") {
		t.Fatalf("empty context: err = %v", err)
	}
	err := c.Build(context.Background(), BuildOptions{Context: ".", Args: map[string]string{"": "x"}})
	if err == nil || !strings.Contains(err.Error(), "empty build argument name") {
		t.Fatalf("empty build-arg key: err = %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("docker was called for a bad build: %v", f.calls)
	}
}

func TestBuildErrorCarriesStderr(t *testing.T) {
	c, _ := newFake(reply{stderr: "failed to solve: no such file", err: errors.New("boom")})
	err := c.Build(context.Background(), BuildOptions{Context: "."})
	if err == nil {
		t.Fatal("no error")
	}
	for _, s := range []string{"docker build", "boom", "failed to solve: no such file"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q lacks %q", err, s)
		}
	}
}

func TestPull(t *testing.T) {
	c, f := newFake()
	if err := c.Pull(context.Background(), "nginx:1.27"); err != nil {
		t.Fatal(err)
	}
	wantCall(t, f, "pull", "nginx:1.27")
	if err := c.Pull(context.Background(), ""); err == nil {
		t.Fatal("empty image accepted")
	}
}

func TestHasImage(t *testing.T) {
	c, f := newFake(reply{stdout: "sha256:abc\n"})
	ok, err := c.HasImage(context.Background(), "nginx")
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	wantCall(t, f, "image", "inspect", "--format", "{{.Id}}", "nginx")

	for name, r := range map[string]reply{
		"docker, in ExitError.Stderr": {err: &ExitError{Code: 1, Stderr: "Error: No such image: nginx"}},
		"docker, in stderr bytes":     {stderr: "Error: No such image: nginx", err: &ExitError{Code: 1}},
		"podman, in ExitError.Stderr": {err: &ExitError{Code: 125, Stderr: "Error: nginx: image not known"}},
		"podman, in stderr bytes":     {stderr: "Error: nginx: image not known", err: &ExitError{Code: 125}},
	} {
		c, _ = newFake(r)
		ok, err = c.HasImage(context.Background(), "nginx")
		if err != nil || ok {
			t.Fatalf("missing image (%s): ok = %v, err = %v", name, ok, err)
		}
	}

	c, _ = newFake(reply{err: &ExitError{Code: 1, Stderr: "Cannot connect to the Docker daemon"}})
	ok, err = c.HasImage(context.Background(), "nginx")
	if err == nil || ok {
		t.Fatalf("daemon down must be an error: ok = %v, err = %v", ok, err)
	}
	if !strings.Contains(err.Error(), "Cannot connect to the Docker daemon") {
		t.Fatalf("error %q lacks the stderr", err)
	}
}

const inspectArray = `[
  {
    "Id": "sha256:0123456789abcdef",
    "Os": "linux",
    "Architecture": "arm64",
    "Variant": "v8",
    "Config": {
      "Entrypoint": ["/docker-entrypoint.sh"],
      "Cmd": ["nginx", "-g", "daemon off;"],
      "WorkingDir": "/srv",
      "User": "nginx",
      "Env": ["PATH=/usr/bin", "NGINX_VERSION=1.27.0"],
      "ExposedPorts": {"443/tcp": {}, "80/tcp": {}, "53/udp": {}},
      "Volumes": {"/var/log": {}, "/data": {}}
    }
  }
]`

func TestInspect(t *testing.T) {
	c, f := newFake(reply{stdout: inspectArray})
	got, err := c.Inspect(context.Background(), "nginx:1.27")
	if err != nil {
		t.Fatal(err)
	}
	wantCall(t, f, "image", "inspect", "--format", "{{json .}}", "nginx:1.27")
	want := &ImageConfig{
		ID:           "sha256:0123456789abcdef",
		Entrypoint:   []string{"/docker-entrypoint.sh"},
		Cmd:          []string{"nginx", "-g", "daemon off;"},
		WorkingDir:   "/srv",
		User:         "nginx",
		Env:          []string{"PATH=/usr/bin", "NGINX_VERSION=1.27.0"},
		ExposedPorts: []string{"443/tcp", "53/udp", "80/tcp"},
		Volumes:      []string{"/data", "/var/log"},
		OS:           "linux",
		Architecture: "arm64",
		Variant:      "v8",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestInspectNullEntrypointAndSingleObject(t *testing.T) {
	out := `{"Id":"sha256:ff","Os":"linux","Architecture":"amd64",
		"Config":{"Entrypoint":null,"Cmd":["/app"],"ExposedPorts":null,"Volumes":null,"Env":null}}`
	c, _ := newFake(reply{stdout: out + "\n"})
	got, err := c.Inspect(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if got.Entrypoint != nil {
		t.Errorf("Entrypoint = %#v, want nil", got.Entrypoint)
	}
	if !reflect.DeepEqual(got.Cmd, []string{"/app"}) {
		t.Errorf("Cmd = %q", got.Cmd)
	}
	if got.ExposedPorts != nil || got.Volumes != nil || got.Env != nil {
		t.Errorf("want nil ports, volumes and env: %+v", got)
	}
	if got.ID != "sha256:ff" || got.OS != "linux" || got.Architecture != "amd64" {
		t.Errorf("got %+v", got)
	}
}

func TestInspectNoConfig(t *testing.T) {
	c, _ := newFake(reply{stdout: `[{"Id":"sha256:1","Os":"linux","Architecture":"amd64","Config":null}]`})
	got, err := c.Inspect(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "sha256:1" || got.Cmd != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestInspectErrors(t *testing.T) {
	for name, r := range map[string]reply{
		"empty":     {stdout: " \n"},
		"empty-arr": {stdout: "[]"},
		"garbage":   {stdout: "{nope"},
		"failed":    {stderr: "Error: No such image: x", err: &ExitError{Code: 1, Stderr: "Error: No such image: x"}},
	} {
		c, _ := newFake(r)
		got, err := c.Inspect(context.Background(), "x")
		if err == nil || got != nil {
			t.Errorf("%s: got %+v, err %v", name, got, err)
		}
	}
	c, _ := newFake(reply{stdout: "{nope"})
	_, err := c.Inspect(context.Background(), "myimg")
	if err == nil || !strings.Contains(err.Error(), "myimg") || !strings.Contains(err.Error(), "reading the output") {
		t.Errorf("parse error text = %v", err)
	}
}

func TestCreateArgsFull(t *testing.T) {
	ep := []string{"/usr/bin/strace", "-f", "-o", "/trace/out"}
	got, env, err := createArgs(CreateOptions{
		Image:       "app:1",
		Name:        "probe",
		Entrypoint:  &ep,
		Cmd:         []string{"/app", "--serve"},
		Env:         []string{"B=2", "A=1"},
		WorkingDir:  "/srv",
		User:        "root",
		Labels:      []string{"k=v", "tool=x"},
		Binds:       []string{"/tmp/t:/trace", "/tmp/u:/u:ro"},
		CapAdd:      []string{"SYS_PTRACE"},
		SecurityOpt: []string{"seccomp=unconfined"},
		Ports:       []string{"8080:80"},
		Network:     "net1",
		AutoRemove:  true,
	})
	want := []string{
		"create", "--name", "probe", "--entrypoint", "/usr/bin/strace",
		"--user", "root", "--workdir", "/srv", "--env", "B", "--env", "A",
		"--label", "k=v", "--label", "tool=x",
		"--volume", "/tmp/t:/trace", "--volume", "/tmp/u:/u:ro",
		"--cap-add", "SYS_PTRACE", "--security-opt", "seccomp=unconfined",
		"--publish", "8080:80", "--network", "net1", "--rm",
		"app:1", "-f", "-o", "/trace/out", "/app", "--serve",
	}
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	if want := []string{"B=2", "A=1"}; !reflect.DeepEqual(env, want) {
		t.Fatalf("env = %q, want %q", env, want)
	}
}

func TestCreateArgsEntrypoint(t *testing.T) {
	// nil keeps the entrypoint of the image: no flag at all.
	got, _, _ := createArgs(CreateOptions{Image: "img", Cmd: []string{"sh"}})
	if want := []string{"create", "img", "sh"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("nil entrypoint: argv = %q, want %q", got, want)
	}
	// a pointer to an empty slice clears it.
	empty := []string{}
	got, _, _ = createArgs(CreateOptions{Image: "img", Entrypoint: &empty, Cmd: []string{"sh"}})
	if want := []string{"create", "--entrypoint", "", "img", "sh"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("empty entrypoint: argv = %q, want %q", got, want)
	}
	// a pointer to a nil slice is also a clear, not a keep.
	var nilSlice []string
	got, _, _ = createArgs(CreateOptions{Image: "img", Entrypoint: &nilSlice})
	if want := []string{"create", "--entrypoint", "", "img"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pointer to nil slice: argv = %q, want %q", got, want)
	}
}

func TestCreate(t *testing.T) {
	c, f := newFake(reply{stdout: "abc123\n"})
	id, err := c.Create(context.Background(), CreateOptions{Image: "img"})
	if err != nil || id != "abc123" {
		t.Fatalf("id = %q, err = %v", id, err)
	}
	wantCall(t, f, "create", "img")

	if _, err := c.Create(context.Background(), CreateOptions{}); err == nil {
		t.Error("empty image accepted")
	}
	c, _ = newFake(reply{stdout: "\n"})
	if _, err := c.Create(context.Background(), CreateOptions{Image: "img"}); err == nil {
		t.Error("empty id accepted")
	}
	c, _ = newFake(reply{err: &ExitError{Code: 125, Stderr: "Unable to find image"}})
	_, err = c.Create(context.Background(), CreateOptions{Image: "img"})
	if err == nil || !strings.Contains(err.Error(), "docker create") || !strings.Contains(err.Error(), "Unable to find image") {
		t.Errorf("err = %v", err)
	}
}

// envFake is a fake that is also an EnvRunner.
type envFake struct {
	fake
	envs [][]string
}

func (e *envFake) RunEnv(ctx context.Context, env []string, stdin io.Reader, args ...string) ([]byte, []byte, error) {
	e.envs = append(e.envs, append([]string(nil), env...))
	return e.fake.Run(ctx, stdin, args...)
}

func TestCreateSecretsStayOffTheCommandLine(t *testing.T) {
	f := &envFake{fake: fake{replies: []reply{{stdout: "cid\n"}}}}
	c := New(f)
	if _, err := c.Create(context.Background(), CreateOptions{Image: "img", Env: []string{"DB_PASSWORD=hunter2", "A=1", "A=2"}}); err != nil {
		t.Fatal(err)
	}
	wantCall(t, &f.fake, "create", "--env", "DB_PASSWORD", "--env", "A", "--env", "A", "img")
	for _, a := range f.calls[0] {
		if strings.Contains(a, "hunter2") {
			t.Errorf("a secret is in the argument %q", a)
		}
	}
	if len(f.envs) != 1 || !reflect.DeepEqual(f.envs[0], []string{"DB_PASSWORD=hunter2", "A=2"}) {
		t.Errorf("env passed = %q", f.envs)
	}
}

func TestBuildSecretsStayOffTheCommandLine(t *testing.T) {
	f := &envFake{}
	c := New(f)
	if err := c.Build(context.Background(), BuildOptions{Context: ".", Args: map[string]string{"TOKEN": "s3cr3t-value"}}); err != nil {
		t.Fatal(err)
	}
	wantCall(t, &f.fake, "build", "--build-arg", "TOKEN", "--", ".")
	if len(f.envs) != 1 || !reflect.DeepEqual(f.envs[0], []string{"TOKEN=s3cr3t-value"}) {
		t.Errorf("env passed = %q", f.envs)
	}
}

// A Runner that cannot pass an environment is refused for a call that has
// values: the values are not put on the command line instead.
func TestEnvNeedsEnvRunner(t *testing.T) {
	c, f := newFake()
	_, err := c.Create(context.Background(), CreateOptions{Image: "img", Env: []string{"A=1"}})
	if err == nil || !strings.Contains(err.Error(), "environment") {
		t.Errorf("create: err = %v", err)
	}
	err = c.Build(context.Background(), BuildOptions{Context: ".", Args: map[string]string{"A": "1"}})
	if err == nil || !strings.Contains(err.Error(), "environment") {
		t.Errorf("build: err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("docker was called: %v", f.calls)
	}
	// Without values the plain Runner is fine.
	if _, err := c.Create(context.Background(), CreateOptions{Image: "img"}); err != nil && !strings.Contains(err.Error(), "no container id") {
		t.Errorf("create without env: %v", err)
	}
}

func TestCreateBadEnvName(t *testing.T) {
	c := New(&envFake{})
	for _, e := range []string{"=x", "A\x00B=1", "A=b\x00c"} {
		if _, err := c.Create(context.Background(), CreateOptions{Image: "img", Env: []string{e}}); err == nil {
			t.Errorf("env entry %q accepted", e)
		}
	}
}

func TestBuildErrorHidesArgumentValues(t *testing.T) {
	stderr := "Step 3: RUN curl -H token=hunter-two-secret x; short=abc"
	c := New(&envFake{fake: fake{replies: []reply{{stderr: stderr, err: &ExitError{Code: 1, Stderr: stderr}}}}})
	err := c.Build(context.Background(), BuildOptions{Context: ".", Args: map[string]string{"T": "hunter-two-secret", "S": "abc"}})
	if err == nil {
		t.Fatal("no error")
	}
	if strings.Contains(err.Error(), "hunter-two-secret") {
		t.Errorf("secret in error: %v", err)
	}
	if !strings.Contains(err.Error(), "token=***") || !strings.Contains(err.Error(), "short=abc") {
		t.Errorf("error = %v", err)
	}
	// An error that is not an ExitError carries the stderr tail.
	c = New(&envFake{fake: fake{replies: []reply{{stderr: "oops hunter-two-secret", err: errors.New("boom")}}}})
	err = c.Build(context.Background(), BuildOptions{Context: ".", Args: map[string]string{"T": "hunter-two-secret"}})
	if err == nil || strings.Contains(err.Error(), "hunter-two-secret") || !strings.Contains(err.Error(), "oops ***") {
		t.Errorf("plain error = %v", err)
	}
}

func TestExecRunnerRunEnv(t *testing.T) {
	r := NewExecRunner("sh").(EnvRunner)
	out, _, err := r.RunEnv(context.Background(), []string{"D2N_SECRET=xyz"}, nil, "-c", "echo $D2N_SECRET; command -v sh >/dev/null && echo path-ok")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "xyz\npath-ok\n" {
		t.Errorf("out = %q (the variable is passed and the environment of the process is kept)", out)
	}
}

func TestSimpleCommands(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		call func(c *Client) error
		want []string
	}{
		{"start", func(c *Client) error { return c.Start(ctx, "c1") }, []string{"start", "c1"}},
		{"stop", func(c *Client) error { return c.Stop(ctx, "c1", 7) }, []string{"stop", "-t", "7", "c1"}},
		{"stop zero", func(c *Client) error { return c.Stop(ctx, "c1", 0) }, []string{"stop", "-t", "0", "c1"}},
		{"kill", func(c *Client) error { return c.Kill(ctx, "c1") }, []string{"kill", "c1"}},
		{"rm", func(c *Client) error { return c.Remove(ctx, "c1", false) }, []string{"rm", "c1"}},
		{"rm force", func(c *Client) error { return c.Remove(ctx, "c1", true) }, []string{"rm", "--force", "c1"}},
		{"cp", func(c *Client) error { return c.CopyFrom(ctx, "c1", "/etc", "/tmp/out") }, []string{"cp", "c1:/etc", "/tmp/out"}},
		{"cp relative", func(c *Client) error { return c.CopyFrom(ctx, "c1", "/etc", "out/dir") }, []string{"cp", "c1:/etc", "out/dir"}},
		{"cp dot", func(c *Client) error { return c.CopyFrom(ctx, "c1", "/etc", ".") }, []string{"cp", "c1:/etc", "."}},
		{"cp dash", func(c *Client) error { return c.CopyFrom(ctx, "c1", "/etc", "-") }, []string{"cp", "c1:/etc", "./-"}},
		{"cp option-like", func(c *Client) error { return c.CopyFrom(ctx, "c1", "/etc", "-x/out") }, []string{"cp", "c1:/etc", "./-x/out"}},
		{"cp colon", func(c *Client) error { return c.CopyFrom(ctx, "c1", "/etc", "a:b") }, []string{"cp", "c1:/etc", "./a:b"}},
		{"cp absolute colon", func(c *Client) error { return c.CopyFrom(ctx, "c1", "/etc", "/tmp/a:b") }, []string{"cp", "c1:/etc", "/tmp/a:b"}},
	}
	for _, tt := range tests {
		c, f := newFake()
		if err := tt.call(c); err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if len(f.calls) != 1 || !reflect.DeepEqual(f.calls[0], tt.want) {
			t.Errorf("%s: calls = %q, want %q", tt.name, f.calls, tt.want)
		}
	}
}

func TestSimpleCommandsRefuseEmptyArguments(t *testing.T) {
	ctx := context.Background()
	c, f := newFake()
	for name, err := range map[string]error{
		"start":  c.Start(ctx, ""),
		"stop":   c.Stop(ctx, "", 1),
		"stop-1": c.Stop(ctx, "c1", -1),
		"kill":   c.Kill(ctx, ""),
		"rm":     c.Remove(ctx, "", true),
		"cp":     c.CopyFrom(ctx, "c1", "", "/tmp"),
		"cp2":    c.CopyFrom(ctx, "c1", "/etc", ""),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("docker was called: %v", f.calls)
	}
}

func TestCommandErrorsAreWrapped(t *testing.T) {
	ctx := context.Background()
	cause := errors.New("daemon gone")
	c, _ := newFake(reply{err: cause, stderr: "some detail"})
	err := c.Start(ctx, "c1")
	if !errors.Is(err, cause) {
		t.Fatalf("cause lost: %v", err)
	}
	if !strings.HasPrefix(err.Error(), "docker start: ") || !strings.Contains(err.Error(), "some detail") {
		t.Fatalf("err = %q", err)
	}
	var ee *ExitError
	c, _ = newFake(reply{err: &ExitError{Args: []string{"kill", "c1"}, Code: 1, Stderr: "No such container"}})
	err = c.Kill(ctx, "c1")
	if !errors.As(err, &ee) || ee.Code != 1 {
		t.Fatalf("ExitError lost: %v", err)
	}
}

func TestRunning(t *testing.T) {
	for out, want := range map[string]bool{"true\n": true, "false\n": false} {
		c, f := newFake(reply{stdout: out})
		got, err := c.Running(context.Background(), "c1")
		if err != nil || got != want {
			t.Errorf("%q: got %v, err %v", out, got, err)
		}
		wantCall(t, f, "container", "inspect", "--format", "{{.State.Running}}", "c1")
	}
	c, _ := newFake(reply{stdout: "<no value>\n"})
	if _, err := c.Running(context.Background(), "c1"); err == nil {
		t.Error("garbage accepted")
	}
	c, _ = newFake(reply{err: errors.New("x")})
	if got, err := c.Running(context.Background(), "c1"); err == nil || got {
		t.Errorf("failure: got %v, err %v", got, err)
	}
}

func TestWait(t *testing.T) {
	c, f := newFake(reply{stdout: "137\n"})
	code, err := c.Wait(context.Background(), "c1")
	if err != nil || code != 137 {
		t.Fatalf("code = %d, err = %v", code, err)
	}
	wantCall(t, f, "wait", "c1")

	c, _ = newFake(reply{stdout: "soon\n"})
	if _, err := c.Wait(context.Background(), "c1"); err == nil {
		t.Error("non-number accepted")
	}
	c, _ = newFake(reply{err: errors.New("x")})
	if _, err := c.Wait(context.Background(), "c1"); err == nil {
		t.Error("failure not reported")
	}
}

func TestExec(t *testing.T) {
	c, f := newFake(reply{stdout: "line1\nline2\n"})
	out, err := c.Exec(context.Background(), "c1", []string{"cat", "/proc/net/tcp"})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "line1\nline2\n" {
		t.Fatalf("out = %q", out)
	}
	wantCall(t, f, "exec", "c1", "cat", "/proc/net/tcp")

	if _, err := c.Exec(context.Background(), "c1", nil); err == nil {
		t.Error("empty command accepted")
	}
}

func TestExecExitError(t *testing.T) {
	c, _ := newFake(reply{
		stdout: "partial",
		stderr: "cat: no such file",
		err:    &ExitError{Args: []string{"exec", "c1", "cat", "x"}, Code: 1},
	})
	out, err := c.Exec(context.Background(), "c1", []string{"cat", "x"})
	ee, ok := err.(*ExitError)
	if !ok {
		t.Fatalf("err = %T %v, want *ExitError", err, err)
	}
	if ee.Code != 1 || ee.Stderr != "cat: no such file" {
		t.Fatalf("ExitError = %+v", ee)
	}
	if string(out) != "partial" {
		t.Fatalf("out = %q", out)
	}

	// a wrapped ExitError from the Runner still comes back as itself.
	inner := &ExitError{Code: 3, Stderr: "boom"}
	c, _ = newFake(reply{err: errors.Join(errors.New("ctx"), inner)})
	_, err = c.Exec(context.Background(), "c1", []string{"x"})
	if err != error(inner) {
		t.Fatalf("err = %v, want the inner ExitError", err)
	}

	// any other error is wrapped.
	cause := errors.New("no docker")
	c, _ = newFake(reply{err: cause})
	_, err = c.Exec(context.Background(), "c1", []string{"x"})
	if _, isExit := err.(*ExitError); isExit || !errors.Is(err, cause) || !strings.HasPrefix(err.Error(), "docker exec: ") {
		t.Fatalf("err = %v", err)
	}
}

func TestExitErrorText(t *testing.T) {
	e := &ExitError{Args: []string{"exec", "c1", "false"}, Code: 2, Stderr: "  bad thing\n"}
	if got, want := e.Error(), "docker exec c1 false: exit status 2: bad thing"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got, want := (&ExitError{Args: []string{"ps"}, Code: 1}).Error(), "docker ps: exit status 1"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	long := strings.Repeat("x", 5000) + "END"
	got := (&ExitError{Code: 1, Stderr: long}).Error()
	if !strings.HasSuffix(got, "xxxEND") || !strings.Contains(got, ": ...x") || len(got) > 1100 {
		t.Fatalf("stderr not cut to its tail: len %d", len(got))
	}
}

func TestLogs(t *testing.T) {
	c, f := newFake(reply{stdout: "out\n", stderr: "err\n"})
	got, err := c.Logs(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "out\nerr\n" {
		t.Fatalf("logs = %q", got)
	}
	wantCall(t, f, "logs", "c1")

	c, _ = newFake(reply{err: errors.New("x")})
	if _, err := c.Logs(context.Background(), "c1"); err == nil {
		t.Error("failure not reported")
	}
}

func TestExportBuffered(t *testing.T) {
	c, f := newFake(reply{stdout: "TARDATA"})
	var buf bytes.Buffer
	if err := c.Export(context.Background(), "c1", &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "TARDATA" {
		t.Fatalf("written = %q", buf.String())
	}
	wantCall(t, f, "export", "c1")

	c, _ = newFake(reply{err: errors.New("x")})
	if err := c.Export(context.Background(), "c1", &buf); err == nil {
		t.Error("failure not reported")
	}
}

var errDiskFull = errors.New("disk full")

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errDiskFull }

func TestExportWriteError(t *testing.T) {
	c, _ := newFake(reply{stdout: "TAR"})
	err := c.Export(context.Background(), "c1", failWriter{})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
}

func TestExportStreams(t *testing.T) {
	s := &streamFake{data: "STREAMED"}
	var buf bytes.Buffer
	if err := New(s).Export(context.Background(), "c1", &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "STREAMED" {
		t.Fatalf("written = %q", buf.String())
	}
	if len(s.calls) != 0 {
		t.Fatalf("Run was used instead of Stream: %v", s.calls)
	}
	if !reflect.DeepEqual(s.streamed, [][]string{{"export", "c1"}}) {
		t.Fatalf("streamed = %q", s.streamed)
	}

	s = &streamFake{err: errors.New("pipe broke")}
	err := New(s).Export(context.Background(), "c1", &buf)
	if err == nil || !strings.Contains(err.Error(), "pipe broke") || !strings.Contains(err.Error(), "streamerr") {
		t.Fatalf("err = %v", err)
	}
}

func TestCopyTarBuffered(t *testing.T) {
	c, f := newFake(reply{stdout: "TARDATA"})
	var buf bytes.Buffer
	if err := c.CopyTar(context.Background(), "c1", "/usr/bin/app", &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "TARDATA" {
		t.Fatalf("written = %q", buf.String())
	}
	wantCall(t, f, "cp", "c1:/usr/bin/app", "-")

	c, _ = newFake(reply{stderr: "Error: No such container:path", err: errors.New("x")})
	err := c.CopyTar(context.Background(), "c1", "/nope", &buf)
	if err == nil || !strings.Contains(err.Error(), "docker cp") || !strings.Contains(err.Error(), "No such container:path") {
		t.Fatalf("err = %v", err)
	}

	c, _ = newFake(reply{stdout: "TAR"})
	err = c.CopyTar(context.Background(), "c1", "/etc", failWriter{})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
}

func TestCopyTarStreams(t *testing.T) {
	s := &streamFake{data: "STREAMED"}
	var buf bytes.Buffer
	if err := New(s).CopyTar(context.Background(), "c1", "/etc", &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "STREAMED" {
		t.Fatalf("written = %q", buf.String())
	}
	if len(s.calls) != 0 {
		t.Fatalf("Run was used instead of Stream: %v", s.calls)
	}
	if !reflect.DeepEqual(s.streamed, [][]string{{"cp", "c1:/etc", "-"}}) {
		t.Fatalf("streamed = %q", s.streamed)
	}

	s = &streamFake{err: errors.New("pipe broke")}
	err := New(s).CopyTar(context.Background(), "c1", "/etc", &buf)
	if err == nil || !strings.Contains(err.Error(), "docker cp") || !strings.Contains(err.Error(), "pipe broke") || !strings.Contains(err.Error(), "streamerr") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecRunner(t *testing.T) {
	ctx := context.Background()
	r := NewExecRunner("sh")

	stdout, stderr, err := r.Run(ctx, strings.NewReader("in"), "-c", "cat; echo oops >&2")
	if err != nil {
		t.Fatal(err)
	}
	if string(stdout) != "in" || string(stderr) != "oops\n" {
		t.Fatalf("stdout %q, stderr %q", stdout, stderr)
	}

	_, _, err = r.Run(ctx, nil, "-c", "echo bad >&2; exit 3")
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %T %v, want *ExitError", err, err)
	}
	if ee.Code != 3 || ee.Stderr != "bad\n" || !reflect.DeepEqual(ee.Args, []string{"-c", "echo bad >&2; exit 3"}) {
		t.Fatalf("ExitError = %+v", ee)
	}
	if !strings.Contains(err.Error(), "exit status 3") || !strings.Contains(err.Error(), "bad") || !strings.Contains(err.Error(), "echo bad") {
		t.Fatalf("text = %q", err)
	}

	var buf bytes.Buffer
	st, ok := r.(Streamer)
	if !ok {
		t.Fatal("exec runner is not a Streamer")
	}
	if _, err := st.Stream(ctx, &buf, "-c", "printf abc"); err != nil || buf.String() != "abc" {
		t.Fatalf("stream: %q, %v", buf.String(), err)
	}

	_, _, err = NewExecRunner("/nonexistent/docker-binary").Run(ctx, nil, "ps")
	if err == nil || !strings.Contains(err.Error(), "/nonexistent/docker-binary ps") {
		t.Fatalf("missing binary: %v", err)
	}
	var missing *ExitError
	if errors.As(err, &missing) {
		t.Fatalf("missing binary reported as exit error: %v", err)
	}
}

// A context that ends while the command runs must end Run, also when a child
// of the command keeps the stdout and stderr pipes open.
func TestExecRunnerCancelWhileRunning(t *testing.T) {
	r := NewExecRunner("sh")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := r.Run(ctx, nil, "-c", "sleep 10 & wait")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Run took %v after the deadline", d)
	}

	cctx, cancel2 := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel2)
	start = time.Now()
	_, err = r.(Streamer).Stream(cctx, io.Discard, "-c", "sleep 10 & wait")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stream err = %v, want Canceled", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Stream took %v after the cancel", d)
	}
}

// A command that ignores SIGTERM is killed, with its children, after the
// wait delay.
func TestExecRunnerCancelEscalatesToKill(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := NewExecRunner("sh").Run(ctx, nil, "-c", "trap '' TERM; sleep 10 & wait")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Fatalf("Run took %v", d)
	}
}

func TestExecRunnerSecretsAreRedacted(t *testing.T) {
	ctx := context.Background()
	r := NewExecRunner("sh")
	_, _, err := r.Run(ctx, nil, "-c", "exit 1", "--env", "DB_PASSWORD=hunter2", "--build-arg", "TOKEN=abc")
	if err == nil {
		t.Fatal("no error")
	}
	if want := `sh -c "exit 1" --env DB_PASSWORD=*** --build-arg TOKEN=***: exit status 1`; err.Error() != want {
		t.Fatalf("err = %q, want %q", err, want)
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Bin != "sh" {
		t.Fatalf("ExitError = %+v", ee)
	}

	_, _, err = NewExecRunner("/nonexistent/docker-binary").Run(ctx, nil, "run", "--env", "P=hunter2")
	if err == nil || strings.Contains(err.Error(), "hunter2") || !strings.Contains(err.Error(), "P=***") {
		t.Fatalf("missing binary: %v", err)
	}

	cctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_, _, err = r.Run(cctx, nil, "-c", "sleep 5", "-e", "P=hunter2")
	if err == nil || strings.Contains(err.Error(), "hunter2") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled: %v", err)
	}
}

func TestExitErrorRedaction(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"run", "--env", "A=hunter2", "img"}, "docker run --env A=*** img: exit status 1"},
		{[]string{"run", "-e", "A=hunter2", "img"}, "docker run -e A=*** img: exit status 1"},
		{[]string{"run", "-eA=hunter2", "img"}, "docker run -eA=*** img: exit status 1"},
		{[]string{"run", "--env=A=hunter2", "img"}, "docker run --env=A=*** img: exit status 1"},
		{[]string{"build", "--build-arg", "A=hunter2", "."}, "docker build --build-arg A=*** .: exit status 1"},
		{[]string{"build", "--build-arg=A=hunter2", "."}, "docker build --build-arg=A=*** .: exit status 1"},
		{[]string{"run", "--env", "A=", "img"}, "docker run --env A=*** img: exit status 1"},
		{[]string{"run", "--env", "A", "img"}, "docker run --env A img: exit status 1"},
		{[]string{"run", "--entrypoint", "", "a b", "x=y"}, `docker run --entrypoint "" "a b" x=y: exit status 1`},
	}
	for _, tt := range tests {
		e := &ExitError{Args: tt.args, Code: 1}
		if got := e.Error(); got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
		if strings.Contains(e.Error(), "hunter2") {
			t.Errorf("secret in %q", e.Error())
		}
	}
	// the raw arguments stay in the struct.
	e := &ExitError{Args: []string{"--env", "A=hunter2"}, Code: 1}
	_ = e.Error()
	if e.Args[1] != "A=hunter2" {
		t.Fatalf("Error() changed Args: %q", e.Args)
	}
	// the binary name is the real one.
	if got, want := (&ExitError{Bin: "/usr/bin/podman", Args: []string{"ps"}, Code: 2}).Error(), "/usr/bin/podman ps: exit status 2"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExitErrorSignal(t *testing.T) {
	_, _, err := NewExecRunner("sh").Run(context.Background(), nil, "-c", "kill -9 $$")
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %T %v, want *ExitError", err, err)
	}
	if ee.Code != -1 || ee.Signal != "signal: killed" {
		t.Fatalf("ExitError = %+v", ee)
	}
	if want := `sh -c "kill -9 $$": signal: killed`; err.Error() != want {
		t.Fatalf("err = %q, want %q", err, want)
	}
}

func TestStreamWriteErrorBeatsExitError(t *testing.T) {
	_, err := NewExecRunner("sh").(Streamer).Stream(context.Background(), failWriter{}, "-c", "exec yes")
	if !errors.Is(err, errDiskFull) {
		t.Fatalf("err = %v, want disk full", err)
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		t.Fatalf("write error reported as ExitError: %v", err)
	}
}

func TestExecRunnerKeepsTheTailOfStderr(t *testing.T) {
	script := `i=0; while [ $i -lt 200 ]; do printf '%01000d\n' $i >&2; i=$((i+1)); done; echo END >&2; exit 4`
	_, stderr, err := NewExecRunner("sh").Run(context.Background(), nil, "-c", script)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 4 {
		t.Fatalf("err = %v", err)
	}
	if len(stderr) != 64<<10 || len(ee.Stderr) != 64<<10 {
		t.Fatalf("kept %d and %d bytes of stderr, want %d", len(stderr), len(ee.Stderr), 64<<10)
	}
	if !strings.HasSuffix(ee.Stderr, "\nEND\n") {
		t.Fatalf("stderr does not end with the last line")
	}
}

func TestTailWriter(t *testing.T) {
	w := &tailWriter{max: 64 << 10}
	var all []byte
	for i := 0; i < 150; i++ {
		chunk := bytes.Repeat([]byte{byte('a' + i%26)}, 1000)
		all = append(all, chunk...)
		if n, err := w.Write(chunk); n != len(chunk) || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if got, want := w.Bytes(), all[len(all)-64<<10:]; !bytes.Equal(got, want) {
		t.Fatalf("kept %d bytes, differing from the last 64 KiB", len(got))
	}
	// one write larger than the limit.
	big := bytes.Repeat([]byte("0123456789"), 20000)
	w.Write(big)
	if got, want := w.Bytes(), big[len(big)-64<<10:]; !bytes.Equal(got, want) {
		t.Fatalf("big write: kept %d bytes", len(got))
	}
	// a small write after it still keeps the last bytes.
	w.Write([]byte("tail"))
	if got := w.Bytes(); len(got) != 64<<10 || !bytes.HasSuffix(got, []byte("789tail")) {
		t.Fatalf("after small write: len %d", len(got))
	}
}

func TestTailOfKeepsRunes(t *testing.T) {
	// 1202 bytes: the cut at 1202-1024 lands inside the second byte of a rune.
	s := "x" + strings.Repeat("é", 600) + "y"
	want := "..." + strings.Repeat("é", 511) + "y"
	if got := tailOf(s); got != want {
		t.Fatalf("tailOf cut inside a rune: got %d bytes, want %d", len(got), len(want))
	}
	if !utf8.ValidString(tailOf(s)) {
		t.Fatal("invalid UTF-8")
	}
}

func TestWrapExitErrorAppendsStderrOnce(t *testing.T) {
	ee := &ExitError{Args: []string{"kill", "c1"}, Code: 1, Stderr: "No such container: c1"}
	got := wrap("docker kill", ee, []byte("No such container: c1"))
	if want := "docker kill: docker kill c1: exit status 1: No such container: c1"; got.Error() != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if !errors.Is(got, ee) {
		t.Fatal("ExitError lost")
	}
	other := wrap("docker kill", errors.New("boom"), []byte(" detail \n"))
	if want := "docker kill: boom: detail"; other.Error() != want {
		t.Fatalf("got %q, want %q", other, want)
	}
}

func TestExecDaemonFailures(t *testing.T) {
	for name, tt := range map[string]struct {
		stderr string
		want   error
	}{
		"not running": {"Error response from daemon: container abc is not running", ErrNotRunning},
		"no such":     {"Error response from daemon: No such container: abc", ErrNoContainer},
	} {
		// the text may come in the ExitError or in the stderr bytes.
		for src, r := range map[string]reply{
			"exit error": {err: &ExitError{Code: 1, Stderr: tt.stderr}},
			"bytes":      {stderr: tt.stderr, err: &ExitError{Code: 1}},
		} {
			c, _ := newFake(r)
			_, err := c.Exec(context.Background(), "abc", []string{"true"})
			var ee *ExitError
			if !errors.Is(err, tt.want) || errors.As(err, &ee) {
				t.Errorf("%s/%s: err = %v", name, src, err)
			}
		}
	}
	// the command's own failure, even with similar words, stays an ExitError.
	c, _ := newFake(reply{err: &ExitError{Code: 1, Stderr: "service is not running"}})
	_, err := c.Exec(context.Background(), "abc", []string{"svc"})
	var ee *ExitError
	if !errors.As(err, &ee) || errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v", err)
	}
}

func TestOptionLikeArgumentsAreRefused(t *testing.T) {
	ctx := context.Background()
	c, f := newFake()
	var buf bytes.Buffer
	calls := map[string]func() error{
		"pull":          func() error { return c.Pull(ctx, "-x") },
		"hasimage":      func() error { _, err := c.HasImage(ctx, "-x"); return err },
		"inspect":       func() error { _, err := c.Inspect(ctx, "-x"); return err },
		"create":        func() error { _, err := c.Create(ctx, CreateOptions{Image: "-x"}); return err },
		"start":         func() error { return c.Start(ctx, "-x") },
		"stop":          func() error { return c.Stop(ctx, "-x", 1) },
		"kill":          func() error { return c.Kill(ctx, "-x") },
		"rm":            func() error { return c.Remove(ctx, "-x", false) },
		"running":       func() error { _, err := c.Running(ctx, "-x"); return err },
		"wait":          func() error { _, err := c.Wait(ctx, "-x"); return err },
		"exec":          func() error { _, err := c.Exec(ctx, "-x", []string{"true"}); return err },
		"execuser id":   func() error { _, err := c.ExecUser(ctx, "-x", "root", []string{"true"}); return err },
		"execuser user": func() error { _, err := c.ExecUser(ctx, "c1", "-root", []string{"true"}); return err },
		"ip":            func() error { _, err := c.IPAddress(ctx, "-x"); return err },
		"cp":            func() error { return c.CopyFrom(ctx, "-x", "/etc", "out") },
		"cptar":         func() error { return c.CopyTar(ctx, "-x", "/etc", &buf) },
		"logs":          func() error { _, err := c.Logs(ctx, "-x"); return err },
		"logs tail":     func() error { _, err := c.LogsTail(ctx, "-x", 5); return err },
		"export":        func() error { return c.Export(ctx, "-x", &buf) },
	}
	for name, call := range calls {
		if err := call(); err == nil || !strings.Contains(err.Error(), "starts with '-'") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("docker was called: %v", f.calls)
	}
}

func TestEmptyArgumentsAreRefused(t *testing.T) {
	ctx := context.Background()
	c, f := newFake()
	var buf bytes.Buffer
	calls := map[string]func() error{
		"pull":          func() error { return c.Pull(ctx, "") },
		"hasimage":      func() error { _, err := c.HasImage(ctx, ""); return err },
		"inspect":       func() error { _, err := c.Inspect(ctx, ""); return err },
		"create":        func() error { _, err := c.Create(ctx, CreateOptions{}); return err },
		"running":       func() error { _, err := c.Running(ctx, ""); return err },
		"wait":          func() error { _, err := c.Wait(ctx, ""); return err },
		"exec id":       func() error { _, err := c.Exec(ctx, "", []string{"true"}); return err },
		"exec cmd":      func() error { _, err := c.Exec(ctx, "c1", nil); return err },
		"execuser id":   func() error { _, err := c.ExecUser(ctx, "", "root", []string{"true"}); return err },
		"execuser user": func() error { _, err := c.ExecUser(ctx, "c1", "", []string{"true"}); return err },
		"execuser cmd":  func() error { _, err := c.ExecUser(ctx, "c1", "root", nil); return err },
		"ip":            func() error { _, err := c.IPAddress(ctx, ""); return err },
		"cp id":         func() error { return c.CopyFrom(ctx, "", "/etc", "out") },
		"cptar id":      func() error { return c.CopyTar(ctx, "", "/etc", &buf) },
		"cptar path":    func() error { return c.CopyTar(ctx, "c1", "", &buf) },
		"logs":          func() error { _, err := c.Logs(ctx, ""); return err },
		"logs tail":     func() error { _, err := c.LogsTail(ctx, "", 5); return err },
		"logs neg":      func() error { _, err := c.LogsTail(ctx, "c1", -1); return err },
		"export":        func() error { return c.Export(ctx, "", &buf) },
	}
	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("docker was called: %v", f.calls)
	}
}

func TestLogsTail(t *testing.T) {
	c, f := newFake(reply{stdout: "out\n", stderr: "err\n"})
	got, err := c.LogsTail(context.Background(), "c1", 20)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "out\nerr\n" {
		t.Fatalf("logs = %q", got)
	}
	wantCall(t, f, "logs", "--tail", "20", "c1")

	c, _ = newFake(reply{err: errors.New("x")})
	if _, err := c.LogsTail(context.Background(), "c1", 0); err == nil {
		t.Error("failure not reported")
	}
}

func TestDefault(t *testing.T) {
	c := Default()
	er, ok := c.r.(*execRunner)
	if !ok || er.bin != "docker" {
		t.Fatalf("runner = %#v", c.r)
	}
}

func TestExecUser(t *testing.T) {
	c, f := newFake(reply{stdout: "ok"})
	out, err := c.ExecUser(context.Background(), "c1", "root", []string{"cat", "/proc/1/environ"})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "ok" {
		t.Fatalf("out = %q", out)
	}
	wantCall(t, f, "exec", "--user", "root", "c1", "cat", "/proc/1/environ")

	// Exec itself does not get --user.
	c, f = newFake(reply{})
	if _, err := c.Exec(context.Background(), "c1", []string{"id"}); err != nil {
		t.Fatal(err)
	}
	wantCall(t, f, "exec", "c1", "id")
}

func TestExecUserErrors(t *testing.T) {
	ctx := context.Background()
	for name, tt := range map[string]struct {
		stderr string
		want   error
	}{
		"not running": {"Error response from daemon: container abc is not running", ErrNotRunning},
		"no such":     {"Error response from daemon: No such container: abc", ErrNoContainer},
	} {
		c, _ := newFake(reply{stderr: tt.stderr, err: &ExitError{Code: 1}})
		_, err := c.ExecUser(ctx, "abc", "root", []string{"true"})
		var ee *ExitError
		if !errors.Is(err, tt.want) || errors.As(err, &ee) {
			t.Errorf("%s: err = %v", name, err)
		}
	}

	c, _ := newFake(reply{stdout: "partial", stderr: "cat: no such file", err: &ExitError{Code: 1}})
	out, err := c.ExecUser(ctx, "abc", "root", []string{"cat", "x"})
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 1 || ee.Stderr != "cat: no such file" || string(out) != "partial" {
		t.Fatalf("out = %q, err = %v", out, err)
	}

	cause := errors.New("no docker")
	c, _ = newFake(reply{err: cause})
	_, err = c.ExecUser(ctx, "abc", "root", []string{"x"})
	if errors.As(err, &ee) || !errors.Is(err, cause) || !strings.HasPrefix(err.Error(), "docker exec: ") {
		t.Fatalf("err = %v", err)
	}
}

func TestIPAddress(t *testing.T) {
	for name, tt := range map[string]struct {
		json string
		want string
	}{
		"default bridge": {`{"IPAddress":"172.17.0.2","Networks":{"bridge":{"IPAddress":"172.17.0.2"}}}`, "172.17.0.2"},
		"top level wins": {`{"IPAddress":"172.17.0.2","Networks":{"a":{"IPAddress":"10.0.0.5"}}}`, "172.17.0.2"},
		"user network":   {`{"IPAddress":"","Networks":{"zeta":{"IPAddress":"10.0.2.2"},"alpha":{"IPAddress":"10.0.1.2"}}}`, "10.0.1.2"},
		"skips no ip":    {`{"IPAddress":"","Networks":{"alpha":{"IPAddress":""},"beta":{"IPAddress":"10.0.3.2"}}}`, "10.0.3.2"},
		"trailing nl":    {"{\"IPAddress\":\"172.17.0.9\"}\n", "172.17.0.9"},
	} {
		c, f := newFake(reply{stdout: tt.json})
		got, err := c.IPAddress(context.Background(), "c1")
		if err != nil || got != tt.want {
			t.Errorf("%s: got %q, %v, want %q", name, got, err, tt.want)
		}
		wantCall(t, f, "container", "inspect", "--format", "{{json .NetworkSettings}}", "c1")
	}
}

func TestIPAddressErrors(t *testing.T) {
	for name, out := range map[string]string{
		"no address":  `{"IPAddress":"","Networks":{"none":{"IPAddress":""}}}`,
		"no networks": `{"IPAddress":"","Networks":{}}`,
		"null":        "null",
		"malformed":   `{"IPAddress":`,
		"empty":       "",
	} {
		c, _ := newFake(reply{stdout: out})
		got, err := c.IPAddress(context.Background(), "abc123")
		if err == nil || got != "" || !strings.Contains(err.Error(), "abc123") {
			t.Errorf("%s: got %q, %v", name, got, err)
		}
	}

	cause := errors.New("no docker")
	c, _ := newFake(reply{err: cause})
	if _, err := c.IPAddress(context.Background(), "c1"); !errors.Is(err, cause) {
		t.Errorf("runner error lost: %v", err)
	}
}

func TestTailWriterExactMax(t *testing.T) {
	w := &tailWriter{max: 8}
	p := []byte("12345678")
	if n, err := w.Write(p); n != 8 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	p[0] = 'X'
	if got := string(w.Bytes()); got != "12345678" {
		t.Fatalf("after a write of exactly max bytes: %q, want 12345678 (a copy of the write)", got)
	}
	// data already there is dropped completely by a write of max bytes
	w = &tailWriter{max: 8}
	w.Write([]byte("abc"))
	if n, err := w.Write([]byte("ABCDEFGH")); n != 8 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if got := string(w.Bytes()); got != "ABCDEFGH" {
		t.Fatalf("exact max after data: %q, want ABCDEFGH", got)
	}
	// two writes that add up to max are kept whole, one more byte drops the first
	w = &tailWriter{max: 8}
	w.Write([]byte("12345"))
	w.Write([]byte("678"))
	if got := string(w.Bytes()); got != "12345678" {
		t.Fatalf("filled to max: %q, want 12345678", got)
	}
	w.Write([]byte("9"))
	if got := string(w.Bytes()); got != "23456789" {
		t.Fatalf("one byte over max: %q, want 23456789", got)
	}
	// one byte more than max in a single write keeps the last max bytes
	w = &tailWriter{max: 8}
	w.Write([]byte("123456789"))
	if got := string(w.Bytes()); got != "23456789" {
		t.Fatalf("max+1 in one write: %q, want 23456789", got)
	}
}

func TestIPAddressNetworkOrder(t *testing.T) {
	// More networks than a lucky map order would hide: the first name in sort order
	// that has an address is chosen, whatever order the map is read in.
	tests := map[string]struct {
		json string
		want string
	}{
		"all have an address": {
			`{"IPAddress":"","Networks":{"n2":{"IPAddress":"10.0.2.2"},"n10":{"IPAddress":"10.0.10.2"},"zeta":{"IPAddress":"10.0.99.2"},"mid":{"IPAddress":"10.0.50.2"},"alpha2":{"IPAddress":"10.0.3.2"},"alpha10":{"IPAddress":"10.0.4.2"}}}`,
			"10.0.4.2",
		},
		"the first ones have none": {
			`{"IPAddress":"","Networks":{"n2":{"IPAddress":"10.0.2.2"},"n10":{"IPAddress":"10.0.10.2"},"zeta":{"IPAddress":"10.0.99.2"},"mid":{"IPAddress":"10.0.50.2"},"alpha2":{"IPAddress":""},"alpha10":{"IPAddress":""}}}`,
			"10.0.50.2",
		},
	}
	for name, tt := range tests {
		for i := 0; i < 30; i++ {
			c, _ := newFake(reply{stdout: tt.json})
			got, err := c.IPAddress(context.Background(), "c1")
			if err != nil || got != tt.want {
				t.Fatalf("%s (try %d): got %q, %v, want %q", name, i, got, err, tt.want)
			}
		}
	}
}

func TestCreateArgsNetworkAliases(t *testing.T) {
	got, _, err := createArgs(CreateOptions{Image: "img", Network: "net1", NetworkAliases: []string{"db", "mysql"}, Cmd: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"create", "--network", "net1", "--network-alias", "db", "--network-alias", "mysql", "img", "x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

func TestCreateArgsNetworkGuards(t *testing.T) {
	tests := []struct {
		name string
		o    CreateOptions
	}{
		{"aliases without network", CreateOptions{Image: "img", NetworkAliases: []string{"db"}}},
		{"option-like network", CreateOptions{Image: "img", Network: "--privileged"}},
		{"option-like alias", CreateOptions{Image: "img", Network: "n", NetworkAliases: []string{"-x"}}},
		{"empty alias", CreateOptions{Image: "img", Network: "n", NetworkAliases: []string{""}}},
	}
	for _, tt := range tests {
		if _, _, err := createArgs(tt.o); err == nil {
			t.Errorf("%s: accepted", tt.name)
		}
	}
}

func TestNetworkCommands(t *testing.T) {
	ctx := context.Background()
	c, f := newFake()
	if err := c.NetworkCreate(ctx, "dock2nano-0a1b2c3d"); err != nil {
		t.Fatal(err)
	}
	wantCall(t, f, "network", "create", "dock2nano-0a1b2c3d")
	c, f = newFake()
	if err := c.NetworkRemove(ctx, "dock2nano-0a1b2c3d"); err != nil {
		t.Fatal(err)
	}
	wantCall(t, f, "network", "rm", "dock2nano-0a1b2c3d")
}

func TestNetworkCommandsRefuseBadNames(t *testing.T) {
	ctx := context.Background()
	c, f := newFake()
	for _, n := range []string{"", "-x", "--driver=bridge"} {
		if err := c.NetworkCreate(ctx, n); err == nil {
			t.Errorf("create %q accepted", n)
		}
		if err := c.NetworkRemove(ctx, n); err == nil {
			t.Errorf("rm %q accepted", n)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("docker was called: %v", f.calls)
	}
}

func TestNetworkCreateFailureIsWrapped(t *testing.T) {
	cause := errors.New("exists")
	c, _ := newFake(reply{err: cause, stderr: "network with name n already exists"})
	err := c.NetworkCreate(context.Background(), "n")
	if !errors.Is(err, cause) || !strings.HasPrefix(err.Error(), "docker network create: ") {
		t.Fatalf("err = %v", err)
	}
}

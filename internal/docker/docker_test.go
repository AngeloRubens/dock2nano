package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
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
	got := buildArgs(BuildOptions{
		Context:    "./app",
		Dockerfile: "docker/Dockerfile.prod",
		Target:     "runtime",
		Args:       map[string]string{"ZED": "1", "ALPHA": "a b", "MID": ""},
		Tag:        "app:latest",
		Platform:   "linux/amd64",
		Quiet:      true,
	})
	want := []string{
		"build", "--file", "docker/Dockerfile.prod", "--target", "runtime",
		"--platform", "linux/amd64", "--tag", "app:latest", "--quiet",
		"--build-arg", "ALPHA=a b", "--build-arg", "MID=", "--build-arg", "ZED=1",
		"./app",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	if got := buildArgs(BuildOptions{Context: "."}); !reflect.DeepEqual(got, []string{"build", "."}) {
		t.Fatalf("minimal argv = %q", got)
	}
}

func TestBuild(t *testing.T) {
	c, f := newFake()
	if err := c.Build(context.Background(), BuildOptions{Context: ".", Tag: "x"}); err != nil {
		t.Fatal(err)
	}
	wantCall(t, f, "build", "--tag", "x", ".")

	if err := c.Build(context.Background(), BuildOptions{}); err == nil || !strings.Contains(err.Error(), "no build context") {
		t.Fatalf("empty context: err = %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("docker was called for an empty context: %v", f.calls)
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

	c, _ = newFake(reply{stderr: "Error: No such image: nginx", err: &ExitError{Code: 1, Stderr: "Error: No such image: nginx"}})
	ok, err = c.HasImage(context.Background(), "nginx")
	if err != nil || ok {
		t.Fatalf("missing image: ok = %v, err = %v", ok, err)
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
	got := createArgs(CreateOptions{
		Image:       "app:1",
		Name:        "probe",
		Entrypoint:  &ep,
		Cmd:         []string{"/app", "--serve"},
		Env:         []string{"B=2", "A=1"},
		WorkingDir:  "/srv",
		User:        "root",
		Binds:       []string{"/tmp/t:/trace", "/tmp/u:/u:ro"},
		CapAdd:      []string{"SYS_PTRACE"},
		SecurityOpt: []string{"seccomp=unconfined"},
		Ports:       []string{"8080:80"},
		Network:     "net1",
		AutoRemove:  true,
	})
	want := []string{
		"create", "--name", "probe", "--entrypoint", "/usr/bin/strace",
		"--user", "root", "--workdir", "/srv", "--env", "B=2", "--env", "A=1",
		"--volume", "/tmp/t:/trace", "--volume", "/tmp/u:/u:ro",
		"--cap-add", "SYS_PTRACE", "--security-opt", "seccomp=unconfined",
		"--publish", "8080:80", "--network", "net1", "--rm",
		"app:1", "-f", "-o", "/trace/out", "/app", "--serve",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

func TestCreateArgsEntrypoint(t *testing.T) {
	// nil keeps the entrypoint of the image: no flag at all.
	got := createArgs(CreateOptions{Image: "img", Cmd: []string{"sh"}})
	if want := []string{"create", "img", "sh"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("nil entrypoint: argv = %q, want %q", got, want)
	}
	// a pointer to an empty slice clears it.
	empty := []string{}
	got = createArgs(CreateOptions{Image: "img", Entrypoint: &empty, Cmd: []string{"sh"}})
	if want := []string{"create", "--entrypoint", "", "img", "sh"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("empty entrypoint: argv = %q, want %q", got, want)
	}
	// a pointer to a nil slice is also a clear, not a keep.
	var nilSlice []string
	got = createArgs(CreateOptions{Image: "img", Entrypoint: &nilSlice})
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

func TestSimpleCommands(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		call func(c *Client) error
		want []string
	}{
		{"start", func(c *Client) error { return c.Start(ctx, "c1") }, []string{"start", "c1"}},
		{"stop", func(c *Client) error { return c.Stop(ctx, "c1", 7) }, []string{"stop", "--time", "7", "c1"}},
		{"stop zero", func(c *Client) error { return c.Stop(ctx, "c1", 0) }, []string{"stop", "--time", "0", "c1"}},
		{"kill", func(c *Client) error { return c.Kill(ctx, "c1") }, []string{"kill", "c1"}},
		{"rm", func(c *Client) error { return c.Remove(ctx, "c1", false) }, []string{"rm", "c1"}},
		{"rm force", func(c *Client) error { return c.Remove(ctx, "c1", true) }, []string{"rm", "--force", "c1"}},
		{"cp", func(c *Client) error { return c.CopyFrom(ctx, "c1", "/etc", "/tmp/out") }, []string{"cp", "c1:/etc", "/tmp/out"}},
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
		wantCall(t, f, "inspect", "--format", "{{.State.Running}}", "c1")
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

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

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

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = r.Run(cctx, nil, "-c", "sleep 5")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

func TestDefault(t *testing.T) {
	c := Default()
	er, ok := c.r.(*execRunner)
	if !ok || er.bin != "docker" {
		t.Fatalf("runner = %#v", c.r)
	}
}

// Package docker drives the docker command line.
//
// It is a thin wrapper: every method builds an argument list in a small pure
// function and hands it to a Runner. The tool therefore needs no Docker Go
// SDK, and tests can replace the Runner with a fake that records the argv.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// ErrNotRunning is returned (wrapped) by Exec when docker reports that the
// container is not running. It is not an *ExitError: the command never ran.
var ErrNotRunning = errors.New("container is not running")

// ErrNoContainer is returned (wrapped) by Exec when docker reports that the
// container does not exist. It is not an *ExitError: the command never ran.
var ErrNoContainer = errors.New("no such container")

// Runner runs `docker args...`, feeding stdin when it is not nil, and returns
// what the command wrote to stdout and stderr. A command that exits with a
// non-zero status must be reported as an *ExitError (possibly wrapped).
type Runner interface {
	Run(ctx context.Context, stdin io.Reader, args ...string) (stdout, stderr []byte, err error)
}

// Streamer is an optional extension of Runner: it copies stdout to w as the
// command produces it, instead of buffering it. Export uses it when the
// Runner has it and falls back to Run otherwise.
type Streamer interface {
	Stream(ctx context.Context, w io.Writer, args ...string) (stderr []byte, err error)
}

// ExitError is a docker command that ran and exited with a non-zero status.
type ExitError struct {
	// Bin is the binary that was run; empty means "docker".
	Bin string
	// Args are the arguments given to the binary, when known. They are the raw
	// arguments and may hold secrets (--env K=V): Error() redacts them, but
	// code that prints Args itself must do the same.
	Args []string
	// Code is the exit status of the command, or -1 when a signal ended it.
	Code int
	// Signal describes the signal that ended the command, such as
	// "signal: killed". It is empty for a normal exit.
	Signal string
	// Stderr is what the command wrote to stderr (the last 64 KiB at most).
	Stderr string
}

// stderrTail is how many bytes of stderr an error message shows.
const stderrTail = 1024

// stderrKeep is how many bytes of stderr the exec runner keeps.
const stderrKeep = 64 << 10

// waitDelay is how long the exec runner waits, after the context is done,
// for the command and its pipes before it kills them.
const waitDelay = 2 * time.Second

func (e *ExitError) Error() string {
	bin := e.Bin
	if bin == "" {
		bin = "docker"
	}
	var b strings.Builder
	b.WriteString(formatCmd(bin, e.Args))
	if e.Signal != "" {
		b.WriteString(": ")
		b.WriteString(e.Signal)
	} else {
		fmt.Fprintf(&b, ": exit status %d", e.Code)
	}
	if tail := tailOf(e.Stderr); tail != "" {
		b.WriteString(": ")
		b.WriteString(tail)
	}
	return b.String()
}

// tailOf trims s and keeps its last stderrTail bytes, without cutting inside
// a UTF-8 sequence.
func tailOf(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > stderrTail {
		i := len(s) - stderrTail
		for i < len(s) && !utf8.RuneStart(s[i]) {
			i++
		}
		s = "..." + s[i:]
	}
	return s
}

// redactValue turns K=V into K=***. A value without '=' is kept.
func redactValue(v string) string {
	if i := strings.IndexByte(v, '='); i >= 0 {
		return v[:i+1] + "***"
	}
	return v
}

// redactArgs returns a copy of args in which the values after --env, -e and
// --build-arg (also in the --flag=K=V and -eK=V spellings) are redacted.
func redactArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		switch {
		case i > 0 && (args[i-1] == "--env" || args[i-1] == "-e" || args[i-1] == "--build-arg"):
			out[i] = redactValue(a)
		case strings.HasPrefix(a, "--env="):
			out[i] = "--env=" + redactValue(a[len("--env="):])
		case strings.HasPrefix(a, "--build-arg="):
			out[i] = "--build-arg=" + redactValue(a[len("--build-arg="):])
		case strings.HasPrefix(a, "-e") && !strings.HasPrefix(a, "--") && strings.Contains(a, "="):
			out[i] = "-e" + redactValue(a[2:])
		default:
			out[i] = a
		}
	}
	return out
}

// quoteArg quotes an argument that is empty or has white space.
func quoteArg(a string) string {
	if a == "" || strings.ContainsAny(a, " \t\n\r") {
		return strconv.Quote(a)
	}
	return a
}

// formatCmd renders a command line for an error message, without secrets.
func formatCmd(bin string, args []string) string {
	var b strings.Builder
	b.WriteString(bin)
	for _, a := range redactArgs(args) {
		b.WriteByte(' ')
		b.WriteString(quoteArg(a))
	}
	return b.String()
}

// tailWriter keeps the last max bytes written to it.
type tailWriter struct {
	max int
	buf []byte
}

func (t *tailWriter) Write(p []byte) (int, error) {
	if len(p) >= t.max {
		t.buf = append(t.buf[:0], p[len(p)-t.max:]...)
		return len(p), nil
	}
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		n := copy(t.buf, t.buf[len(t.buf)-t.max:])
		t.buf = t.buf[:n]
	}
	return len(p), nil
}

func (t *tailWriter) Bytes() []byte { return t.buf }

// errWriter passes writes to w and remembers the first error. It calls
// onErr, when set, on that first error.
type errWriter struct {
	w     io.Writer
	err   error
	onErr func()
}

func (e *errWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	n, err := e.w.Write(p)
	if err != nil {
		e.err = err
		if e.onErr != nil {
			e.onErr()
		}
	}
	return n, err
}

type execRunner struct {
	bin string
}

// NewExecRunner returns a Runner that starts the binary bin (usually
// "docker") with os/exec. Errors carry the arguments and the tail of stderr.
func NewExecRunner(bin string) Runner {
	return &execRunner{bin: bin}
}

func (r *execRunner) Run(ctx context.Context, stdin io.Reader, args ...string) ([]byte, []byte, error) {
	var out bytes.Buffer
	stderr, err := r.run(ctx, stdin, &out, args)
	return out.Bytes(), stderr, err
}

// setupCancel makes the command stoppable. The command gets its own process
// group; when the context is done the group gets SIGTERM (docker forwards it
// to its children), and the group is killed if the command or a child that
// holds the pipes is still there waitDelay later. The returned function
// cancels the pending kill.
func setupCancel(cmd *exec.Cmd) (stop func()) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = waitDelay
	var timer *time.Timer
	cmd.Cancel = func() error {
		pid := cmd.Process.Pid
		timer = time.AfterFunc(waitDelay, func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
		if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	return func() {
		if timer != nil {
			timer.Stop()
		}
	}
}

// Stream implements Streamer.
func (r *execRunner) Stream(ctx context.Context, w io.Writer, args ...string) ([]byte, error) {
	return r.run(ctx, nil, w, args)
}

func (r *execRunner) run(ctx context.Context, stdin io.Reader, stdout io.Writer, args []string) ([]byte, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, r.bin, args...)
	stop := setupCancel(cmd)
	defer stop()
	cmd.Stdin = stdin
	// A failing stdout writer stops the command instead of leaving it
	// blocked on a pipe nobody reads.
	ew := &errWriter{w: stdout, onErr: cancel}
	cmd.Stdout = ew
	errBuf := &tailWriter{max: stderrKeep}
	cmd.Stderr = errBuf
	err := cmd.Run()
	if err == nil {
		return errBuf.Bytes(), nil
	}
	line := formatCmd(r.bin, args)
	if ew.err != nil {
		return errBuf.Bytes(), fmt.Errorf("%s: writing the output: %w", line, ew.err)
	}
	if ctx.Err() != nil {
		return errBuf.Bytes(), fmt.Errorf("%s: %w", line, ctx.Err())
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		x := &ExitError{Bin: r.bin, Args: args, Code: ee.ExitCode(), Stderr: string(errBuf.Bytes())}
		if x.Code < 0 {
			x.Signal = ee.String()
		}
		return errBuf.Bytes(), x
	}
	return errBuf.Bytes(), fmt.Errorf("%s: %w", line, err)
}

// Client drives docker through a Runner.
type Client struct {
	r Runner
}

// New returns a Client that uses r.
func New(r Runner) *Client {
	return &Client{r: r}
}

// Default returns a Client that runs the "docker" binary found in PATH.
func Default() *Client {
	return New(NewExecRunner("docker"))
}

// do runs docker and wraps a failure with the name of the operation.
func (c *Client) do(ctx context.Context, op string, args []string) ([]byte, error) {
	stdout, stderr, err := c.r.Run(ctx, nil, args...)
	if err != nil {
		return stdout, wrap(op, err, stderr)
	}
	return stdout, nil
}

// wrap adds the operation to err. An *ExitError already says everything, so
// it only gets the operation as a prefix. For other errors the tail of stderr
// is added when there is one.
func wrap(op string, err error, stderr []byte) error {
	var ee *ExitError
	if errors.As(err, &ee) {
		return fmt.Errorf("%s: %w", op, err)
	}
	if tail := tailOf(string(stderr)); tail != "" {
		return fmt.Errorf("%s: %w: %s", op, err, tail)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// checkArg refuses an empty value and one that docker would take for an
// option.
func checkArg(op, what, v string) error {
	if v == "" {
		return fmt.Errorf("%s: no %s given", op, what)
	}
	if v[0] == '-' {
		return fmt.Errorf("%s: %s %q starts with '-'", op, what, v)
	}
	return nil
}

// BuildOptions are the options of Build.
type BuildOptions struct {
	// Context is the build context directory. It is required.
	Context    string
	Dockerfile string
	Target     string
	// Args are the build arguments. They are passed sorted by name.
	Args     map[string]string
	Tag      string
	Platform string
	// Quiet suppresses the build output (docker build --quiet).
	Quiet bool
}

func buildArgs(o BuildOptions) []string {
	args := []string{"build"}
	if o.Dockerfile != "" {
		args = append(args, "--file", o.Dockerfile)
	}
	if o.Target != "" {
		args = append(args, "--target", o.Target)
	}
	if o.Platform != "" {
		args = append(args, "--platform", o.Platform)
	}
	if o.Tag != "" {
		args = append(args, "--tag", o.Tag)
	}
	if o.Quiet {
		args = append(args, "--quiet")
	}
	keys := make([]string, 0, len(o.Args))
	for k := range o.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--build-arg", k+"="+o.Args[k])
	}
	// "--" keeps a context that starts with '-' from being read as an option.
	return append(args, "--", o.Context)
}

// Build runs docker build.
func (c *Client) Build(ctx context.Context, o BuildOptions) error {
	if o.Context == "" {
		return errors.New("docker build: no build context given")
	}
	for k := range o.Args {
		if k == "" {
			return errors.New("docker build: empty build argument name")
		}
	}
	_, err := c.do(ctx, "docker build", buildArgs(o))
	return err
}

// Pull runs docker pull.
func (c *Client) Pull(ctx context.Context, image string) error {
	if err := checkArg("docker pull", "image", image); err != nil {
		return err
	}
	_, err := c.do(ctx, "docker pull", []string{"pull", image})
	return err
}

// HasImage reports whether the image is in the local store. Only docker's
// "No such image" (podman: "image not known") answer means false; any other failure, such as a daemon
// that is not running, is returned as an error.
func (c *Client) HasImage(ctx context.Context, image string) (bool, error) {
	if err := checkArg("docker image inspect", "image", image); err != nil {
		return false, err
	}
	_, stderr, err := c.r.Run(ctx, nil, "image", "inspect", "--format", "{{.Id}}", image)
	if err == nil {
		return true, nil
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		low := strings.ToLower(ee.Stderr + "\n" + string(stderr))
		if strings.Contains(low, "no such image") || strings.Contains(low, "image not known") {
			return false, nil
		}
	}
	return false, wrap("docker image inspect", err, stderr)
}

// ImageConfig is what the converter needs from docker image inspect.
type ImageConfig struct {
	ID string
	// Entrypoint and Cmd are nil when the image does not set them.
	Entrypoint []string
	Cmd        []string
	WorkingDir string
	User       string
	Env        []string
	// ExposedPorts are sorted, in docker's spelling such as "80/tcp".
	ExposedPorts []string
	// Volumes are the sorted VOLUME paths of the image.
	Volumes      []string
	OS           string
	Architecture string
	// Variant is the CPU variant such as "v8"; empty when the image has none.
	Variant string
}

// configJSON is the Config object of docker image inspect.
type configJSON struct {
	Entrypoint   []string            `json:"Entrypoint"`
	Cmd          []string            `json:"Cmd"`
	WorkingDir   string              `json:"WorkingDir"`
	User         string              `json:"User"`
	Env          []string            `json:"Env"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts"`
	Volumes      map[string]struct{} `json:"Volumes"`
}

// inspectJSON is the part of the docker image inspect output that is used.
type inspectJSON struct {
	ID           string      `json:"Id"`
	OS           string      `json:"Os"`
	Architecture string      `json:"Architecture"`
	Variant      string      `json:"Variant"`
	Config       *configJSON `json:"Config"`
}

func sortedKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// parseInspect reads the output of docker image inspect. It accepts the array
// that the command prints by default and the single object that
// --format {{json .}} prints.
func parseInspect(raw []byte) (*ImageConfig, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errors.New("empty output")
	}
	var info inspectJSON
	if raw[0] == '[' {
		var list []inspectJSON
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, errors.New("no image in the output")
		}
		info = list[0]
	} else if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	cfg := &ImageConfig{ID: info.ID, OS: info.OS, Architecture: info.Architecture, Variant: info.Variant}
	if info.Config != nil {
		c := info.Config
		cfg.Entrypoint = c.Entrypoint
		cfg.Cmd = c.Cmd
		cfg.WorkingDir = c.WorkingDir
		cfg.User = c.User
		cfg.Env = c.Env
		cfg.ExposedPorts = sortedKeys(c.ExposedPorts)
		cfg.Volumes = sortedKeys(c.Volumes)
	}
	return cfg, nil
}

// Inspect runs docker image inspect and returns the configuration of the
// image.
func (c *Client) Inspect(ctx context.Context, image string) (*ImageConfig, error) {
	if err := checkArg("docker image inspect", "image", image); err != nil {
		return nil, err
	}
	out, err := c.do(ctx, "docker image inspect", []string{"image", "inspect", "--format", "{{json .}}", image})
	if err != nil {
		return nil, err
	}
	cfg, err := parseInspect(out)
	if err != nil {
		return nil, fmt.Errorf("docker image inspect %s: reading the output: %w", image, err)
	}
	return cfg, nil
}

// CreateOptions are the options of Create.
type CreateOptions struct {
	// Image is required.
	Image string
	Name  string
	// Entrypoint nil keeps the entrypoint of the image; a pointer to an empty
	// slice clears it (--entrypoint ""). Docker takes only the program in
	// --entrypoint, so further elements are placed before Cmd.
	Entrypoint *[]string
	Cmd        []string
	Env        []string
	WorkingDir string
	User       string
	// Binds are host:container[:mode] volume specs.
	Binds       []string
	CapAdd      []string
	SecurityOpt []string
	// Ports are publish specs such as "8080:80".
	Ports      []string
	Network    string
	AutoRemove bool
}

func createArgs(o CreateOptions) []string {
	args := []string{"create"}
	if o.Name != "" {
		args = append(args, "--name", o.Name)
	}
	var tail []string
	if o.Entrypoint != nil {
		ep := *o.Entrypoint
		if len(ep) == 0 {
			args = append(args, "--entrypoint", "")
		} else {
			args = append(args, "--entrypoint", ep[0])
			tail = append(tail, ep[1:]...)
		}
	}
	if o.User != "" {
		args = append(args, "--user", o.User)
	}
	if o.WorkingDir != "" {
		args = append(args, "--workdir", o.WorkingDir)
	}
	for _, e := range o.Env {
		args = append(args, "--env", e)
	}
	for _, b := range o.Binds {
		args = append(args, "--volume", b)
	}
	for _, c := range o.CapAdd {
		args = append(args, "--cap-add", c)
	}
	for _, s := range o.SecurityOpt {
		args = append(args, "--security-opt", s)
	}
	for _, p := range o.Ports {
		args = append(args, "--publish", p)
	}
	if o.Network != "" {
		args = append(args, "--network", o.Network)
	}
	if o.AutoRemove {
		args = append(args, "--rm")
	}
	args = append(args, o.Image)
	args = append(args, tail...)
	return append(args, o.Cmd...)
}

// Create runs docker create and returns the id of the new container.
//
// Docker drops the CMD of the image whenever --entrypoint is given, even when
// the entrypoint is only changed or cleared. A caller that sets Entrypoint
// and wants the image CMD must pass it explicitly in Cmd.
func (c *Client) Create(ctx context.Context, o CreateOptions) (string, error) {
	if err := checkArg("docker create", "image", o.Image); err != nil {
		return "", err
	}
	out, err := c.do(ctx, "docker create", createArgs(o))
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return "", errors.New("docker create: no container id in the output")
	}
	return id, nil
}

// simple runs a command that takes one container id after fixed arguments.
func (c *Client) simple(ctx context.Context, op, id string, args ...string) error {
	if err := checkArg(op, "container id", id); err != nil {
		return err
	}
	_, err := c.do(ctx, op, append(args, id))
	return err
}

// Start runs docker start.
func (c *Client) Start(ctx context.Context, id string) error {
	return c.simple(ctx, "docker start", id, "start")
}

// Stop runs docker stop, waiting seconds before the container is killed.
func (c *Client) Stop(ctx context.Context, id string, seconds int) error {
	if seconds < 0 {
		return fmt.Errorf("docker stop: negative timeout %d", seconds)
	}
	return c.simple(ctx, "docker stop", id, "stop", "-t", strconv.Itoa(seconds))
}

// Kill runs docker kill.
func (c *Client) Kill(ctx context.Context, id string) error {
	return c.simple(ctx, "docker kill", id, "kill")
}

// Remove runs docker rm, with --force when force is true.
func (c *Client) Remove(ctx context.Context, id string, force bool) error {
	if force {
		return c.simple(ctx, "docker rm", id, "rm", "--force")
	}
	return c.simple(ctx, "docker rm", id, "rm")
}

// Running reports whether the container is running.
func (c *Client) Running(ctx context.Context, id string) (bool, error) {
	if err := checkArg("docker container inspect", "container id", id); err != nil {
		return false, err
	}
	out, err := c.do(ctx, "docker container inspect", []string{"container", "inspect", "--format", "{{.State.Running}}", id})
	if err != nil {
		return false, err
	}
	switch s := strings.TrimSpace(string(out)); s {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("docker container inspect %s: unexpected State.Running %q", id, s)
	}
}

// Wait blocks until the container stops and returns its exit code.
func (c *Client) Wait(ctx context.Context, id string) (int, error) {
	if err := checkArg("docker wait", "container id", id); err != nil {
		return 0, err
	}
	out, err := c.do(ctx, "docker wait", []string{"wait", id})
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(out))
	code, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("docker wait %s: unexpected exit code %q", id, s)
	}
	return code, nil
}

// Exec runs cmd in the running container and returns what it wrote to
// stdout. When the command exits with a non-zero status, the error is an
// *ExitError with the exit code and the stderr of the command. When docker
// itself says that the container is not running or does not exist, the
// command never ran: the error then wraps ErrNotRunning or ErrNoContainer and
// is not an *ExitError.
func (c *Client) Exec(ctx context.Context, id string, cmd []string) ([]byte, error) {
	if err := checkArg("docker exec", "container id", id); err != nil {
		return nil, err
	}
	if len(cmd) == 0 {
		return nil, errors.New("docker exec: no command given")
	}
	args := append([]string{"exec", id}, cmd...)
	stdout, stderr, err := c.r.Run(ctx, nil, args...)
	if err != nil {
		var ee *ExitError
		if errors.As(err, &ee) {
			if ee.Stderr == "" {
				ee.Stderr = string(stderr)
			}
			low := strings.ToLower(ee.Stderr)
			if strings.Contains(low, "no such container") {
				return stdout, fmt.Errorf("docker exec %s: %w: %s", id, ErrNoContainer, tailOf(ee.Stderr))
			}
			if strings.Contains(low, "container") && strings.Contains(low, "is not running") {
				return stdout, fmt.Errorf("docker exec %s: %w: %s", id, ErrNotRunning, tailOf(ee.Stderr))
			}
			return stdout, ee
		}
		return stdout, wrap("docker exec", err, stderr)
	}
	return stdout, nil
}

// CopyFrom runs docker cp, copying containerPath of the container into the
// host directory hostDir.
func (c *Client) CopyFrom(ctx context.Context, id, containerPath, hostDir string) error {
	if err := checkArg("docker cp", "container id", id); err != nil {
		return err
	}
	if containerPath == "" || hostDir == "" {
		return errors.New("docker cp: container id, container path and host directory are all required")
	}
	// A relative host path that starts with '-' (a lone "-" means stdout) or
	// has a ':' (read as container:path) gets "./" in front.
	if !filepath.IsAbs(hostDir) && (hostDir[0] == '-' || strings.Contains(hostDir, ":")) {
		hostDir = "./" + hostDir
	}
	_, err := c.do(ctx, "docker cp", []string{"cp", id + ":" + containerPath, hostDir})
	return err
}

// Logs returns the output of the container: its stdout followed by its
// stderr, since docker logs writes each stream to the matching stream.
func (c *Client) Logs(ctx context.Context, id string) ([]byte, error) {
	if err := checkArg("docker logs", "container id", id); err != nil {
		return nil, err
	}
	return c.logs(ctx, "logs", id)
}

// LogsTail is Logs limited to the last lines lines (docker logs --tail).
func (c *Client) LogsTail(ctx context.Context, id string, lines int) ([]byte, error) {
	if err := checkArg("docker logs", "container id", id); err != nil {
		return nil, err
	}
	if lines < 0 {
		return nil, fmt.Errorf("docker logs: negative line count %d", lines)
	}
	return c.logs(ctx, "logs", "--tail", strconv.Itoa(lines), id)
}

func (c *Client) logs(ctx context.Context, args ...string) ([]byte, error) {
	stdout, stderr, err := c.r.Run(ctx, nil, args...)
	if err != nil {
		return nil, wrap("docker logs", err, stderr)
	}
	return append(stdout, stderr...), nil
}

// Export runs docker export and writes the filesystem tar of the container
// to w. It streams when the Runner is a Streamer.
func (c *Client) Export(ctx context.Context, id string, w io.Writer) error {
	if err := checkArg("docker export", "container id", id); err != nil {
		return err
	}
	args := []string{"export", id}
	if s, ok := c.r.(Streamer); ok {
		stderr, err := s.Stream(ctx, w, args...)
		if err != nil {
			return wrap("docker export", err, stderr)
		}
		return nil
	}
	stdout, stderr, err := c.r.Run(ctx, nil, args...)
	if err != nil {
		return wrap("docker export", err, stderr)
	}
	if _, err := w.Write(stdout); err != nil {
		return fmt.Errorf("docker export: %w", err)
	}
	return nil
}

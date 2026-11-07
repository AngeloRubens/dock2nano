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
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

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
	// Args are the arguments given to docker, when known.
	Args []string
	// Code is the exit status of the command.
	Code int
	// Stderr is everything the command wrote to stderr.
	Stderr string
}

// stderrTail is how many bytes of stderr an error message shows.
const stderrTail = 1024

func (e *ExitError) Error() string {
	var b strings.Builder
	b.WriteString("docker")
	for _, a := range e.Args {
		b.WriteByte(' ')
		b.WriteString(a)
	}
	fmt.Fprintf(&b, ": exit status %d", e.Code)
	if tail := tailOf(e.Stderr); tail != "" {
		b.WriteString(": ")
		b.WriteString(tail)
	}
	return b.String()
}

// tailOf trims s and keeps its last stderrTail bytes.
func tailOf(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > stderrTail {
		s = "..." + s[len(s)-stderrTail:]
	}
	return s
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

// Stream implements Streamer.
func (r *execRunner) Stream(ctx context.Context, w io.Writer, args ...string) ([]byte, error) {
	return r.run(ctx, nil, w, args)
}

func (r *execRunner) run(ctx context.Context, stdin io.Reader, stdout io.Writer, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.bin, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err == nil {
		return errBuf.Bytes(), nil
	}
	if ctx.Err() != nil {
		return errBuf.Bytes(), fmt.Errorf("%s %s: %w", r.bin, strings.Join(args, " "), ctx.Err())
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return errBuf.Bytes(), &ExitError{Args: args, Code: ee.ExitCode(), Stderr: errBuf.String()}
	}
	return errBuf.Bytes(), fmt.Errorf("%s %s: %w", r.bin, strings.Join(args, " "), err)
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
	return append(args, o.Context)
}

// Build runs docker build.
func (c *Client) Build(ctx context.Context, o BuildOptions) error {
	if o.Context == "" {
		return errors.New("docker build: no build context given")
	}
	_, err := c.do(ctx, "docker build", buildArgs(o))
	return err
}

// Pull runs docker pull.
func (c *Client) Pull(ctx context.Context, image string) error {
	if image == "" {
		return errors.New("docker pull: no image given")
	}
	_, err := c.do(ctx, "docker pull", []string{"pull", image})
	return err
}

// HasImage reports whether the image is in the local store. Only docker's
// "No such image" answer means false; any other failure, such as a daemon
// that is not running, is returned as an error.
func (c *Client) HasImage(ctx context.Context, image string) (bool, error) {
	if image == "" {
		return false, errors.New("docker image inspect: no image given")
	}
	_, stderr, err := c.r.Run(ctx, nil, "image", "inspect", "--format", "{{.Id}}", image)
	if err == nil {
		return true, nil
	}
	var ee *ExitError
	if errors.As(err, &ee) && strings.Contains(strings.ToLower(ee.Stderr+string(stderr)), "no such image") {
		return false, nil
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
	cfg := &ImageConfig{ID: info.ID, OS: info.OS, Architecture: info.Architecture}
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
	if image == "" {
		return nil, errors.New("docker image inspect: no image given")
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
func (c *Client) Create(ctx context.Context, o CreateOptions) (string, error) {
	if o.Image == "" {
		return "", errors.New("docker create: no image given")
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
	if id == "" {
		return fmt.Errorf("%s: no container id given", op)
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
	return c.simple(ctx, "docker stop", id, "stop", "--time", strconv.Itoa(seconds))
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
	if id == "" {
		return false, errors.New("docker inspect: no container id given")
	}
	out, err := c.do(ctx, "docker inspect", []string{"inspect", "--format", "{{.State.Running}}", id})
	if err != nil {
		return false, err
	}
	switch s := strings.TrimSpace(string(out)); s {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("docker inspect %s: unexpected State.Running %q", id, s)
	}
}

// Wait blocks until the container stops and returns its exit code.
func (c *Client) Wait(ctx context.Context, id string) (int, error) {
	if id == "" {
		return 0, errors.New("docker wait: no container id given")
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
// *ExitError with the exit code and the stderr of the command.
func (c *Client) Exec(ctx context.Context, id string, cmd []string) ([]byte, error) {
	if id == "" {
		return nil, errors.New("docker exec: no container id given")
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
			return stdout, ee
		}
		return stdout, wrap("docker exec", err, stderr)
	}
	return stdout, nil
}

// CopyFrom runs docker cp, copying containerPath of the container into the
// host directory hostDir.
func (c *Client) CopyFrom(ctx context.Context, id, containerPath, hostDir string) error {
	if id == "" || containerPath == "" || hostDir == "" {
		return errors.New("docker cp: container id, container path and host directory are all required")
	}
	_, err := c.do(ctx, "docker cp", []string{"cp", id + ":" + containerPath, hostDir})
	return err
}

// Logs returns the output of the container: its stdout followed by its
// stderr, since docker logs writes each stream to the matching stream.
func (c *Client) Logs(ctx context.Context, id string) ([]byte, error) {
	if id == "" {
		return nil, errors.New("docker logs: no container id given")
	}
	stdout, stderr, err := c.r.Run(ctx, nil, "logs", id)
	if err != nil {
		return nil, wrap("docker logs", err, stderr)
	}
	return append(stdout, stderr...), nil
}

// Export runs docker export and writes the filesystem tar of the container
// to w. It streams when the Runner is a Streamer.
func (c *Client) Export(ctx context.Context, id string, w io.Writer) error {
	if id == "" {
		return errors.New("docker export: no container id given")
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

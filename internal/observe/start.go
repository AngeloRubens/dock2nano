package observe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/AngeloRubens/dock2nano/internal/docker"
)

const (
	// defaultStartWait is how long Start waits for the container to be ready.
	defaultStartWait = 60 * time.Second
	// startPollEvery is the pause between two checks of the readiness.
	startPollEvery = time.Second
	// stayUpFor is how long a container of an image that exposes no port must stay up to be
	// ready.
	stayUpFor = 3 * time.Second
	// startLogBytes is the size of the end of the log put in the error of a container that
	// stopped before it was ready.
	startLogBytes = 1024
)

// StartOptions are the options of Start: a container that runs a service the way the image starts
// it, without tracing, so that a service under observation can reach it.
type StartOptions struct {
	// Image is the image to run. It is required.
	Image string
	// Pull fetches the image when this machine does not have it.
	Pull bool
	// Entrypoint, Cmd, Env, WorkingDir, User and Binds are as in Options. Binds are not checked
	// for the directory of the tracer, which is not used here.
	Entrypoint *[]string
	Cmd        []string
	Env        []string
	WorkingDir string
	User       string
	Binds      []string
	// Network is the docker network the container joins and NetworkAliases are its names there.
	Network        string
	NetworkAliases []string
	// Wait is how long the container may take to be ready. The default is 60 seconds.
	Wait time.Duration
	// Log receives the messages, in the manner of fmt.Printf. nil drops them.
	Log func(format string, args ...any)

	// now, sleep and dial are the clock and the connection; tests replace them.
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
	dial  func(ctx context.Context, address string) error
}

// Running is a container started by Start.
type Running struct {
	d  Docker
	id string
}

// ID is the id of the container.
func (r *Running) ID() string { return r.id }

// Stop stops and removes the container, with a context of its own so that it also works after
// the context of the run has ended. It is safe to call more than once.
func (r *Running) Stop(ctx context.Context) error {
	if r == nil || r.id == "" {
		return nil
	}
	id := r.id
	r.id = ""
	cctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	var errs []error
	if err := r.d.Stop(cctx, id, stopSeconds); err != nil {
		errs = append(errs, fmt.Errorf("stopping container %s: %w", id, err))
	}
	if err := r.d.Remove(cctx, id, true); err != nil {
		errs = append(errs, fmt.Errorf("removing container %s: %w", id, err))
	}
	return errors.Join(errs...)
}

// Start runs the image as a plain container (no tracing) and returns when it is ready: when every
// tcp port the image exposes accepts a connection at the address of the container, or, for an
// image that exposes none, when the container is still running after 3 seconds. A container that
// stops before it is ready, or is not ready within o.Wait, is an error that includes the end of
// its output (1 KiB, the values of o.Env hidden). The container is removed on every error.
func Start(ctx context.Context, d Docker, o StartOptions) (*Running, error) {
	if o.Image == "" {
		return nil, errors.New("observe: start: no image given")
	}
	if o.Wait <= 0 {
		o.Wait = defaultStartWait
	}
	if o.now == nil {
		o.now = time.Now
	}
	if o.sleep == nil {
		o.sleep = sleepContext
	}
	if o.dial == nil {
		o.dial = dialTCP
	}
	r := &run{d: d, o: Options{Image: o.Image, Env: o.Env, Log: o.Log, now: o.now, sleep: o.sleep}, once: map[string]bool{}}

	have, err := d.HasImage(ctx, o.Image)
	if err != nil {
		return nil, fmt.Errorf("observe: start: looking for image %s: %w", o.Image, err)
	}
	if !have {
		if !o.Pull {
			return nil, fmt.Errorf("observe: start: image %s is not on this machine and pulling is off", o.Image)
		}
		r.logf("pulling %s\n", o.Image)
		if err := d.Pull(ctx, o.Image); err != nil {
			return nil, fmt.Errorf("observe: start: pulling %s: %w", o.Image, err)
		}
	}
	cfg, err := d.Inspect(ctx, o.Image)
	if err != nil {
		return nil, fmt.Errorf("observe: start: inspecting %s: %w", o.Image, err)
	}
	r.imageValues = map[string]bool{}
	for _, e := range cfg.Env {
		if _, v, ok := strings.Cut(e, "="); ok {
			r.imageValues[v] = true
		}
	}
	ports := exposedPorts(cfg.ExposedPorts)

	id, err := r.create(ctx, docker.CreateOptions{
		Image:          o.Image,
		Entrypoint:     o.Entrypoint,
		Cmd:            o.Cmd,
		Env:            o.Env,
		WorkingDir:     o.WorkingDir,
		User:           o.User,
		Binds:          o.Binds,
		Network:        o.Network,
		NetworkAliases: o.NetworkAliases,
	})
	if err != nil {
		return nil, fmt.Errorf("observe: start: creating the container: %w", err)
	}
	started := &Running{d: d, id: id}
	fail := func(err error) (*Running, error) {
		if serr := started.Stop(ctx); serr != nil {
			err = errors.Join(err, serr)
		}
		return nil, err
	}
	if err := d.Start(ctx, id); err != nil {
		return fail(fmt.Errorf("observe: start: starting the container: %w", err))
	}
	if err := r.ready(ctx, id, ports, o); err != nil {
		return fail(err)
	}
	return started, nil
}

// ready waits for the container to be ready, as Start describes.
func (r *run) ready(ctx context.Context, id string, ports []int, o StartOptions) error {
	begin := o.now()
	deadline := begin.Add(o.Wait)
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("observe: start: waiting for %s: %w", o.Image, err)
		}
		up, err := r.d.Running(ctx, id)
		if err != nil {
			return fmt.Errorf("observe: start: asking whether the container of %s runs: %w", o.Image, err)
		}
		if !up {
			return fmt.Errorf("observe: start: the container of %s stopped before it was ready%s", o.Image, r.startLogTail(id))
		}
		if len(ports) == 0 {
			if !o.now().Before(begin.Add(stayUpFor)) {
				return nil
			}
		} else if r.portsOpen(ctx, id, ports, o) {
			return nil
		}
		if !o.now().Before(deadline) {
			what := "did not stay up"
			if len(ports) > 0 {
				what = "did not accept connections on " + portList(ports)
			}
			return fmt.Errorf("observe: start: the container of %s %s within %s%s", o.Image, what, o.Wait, r.startLogTail(id))
		}
		if err := o.sleep(ctx, startPollEvery); err != nil {
			return fmt.Errorf("observe: start: waiting for %s: %w", o.Image, err)
		}
	}
}

// portsOpen tells whether every port accepts a connection at the address of the container. A
// container that has no address yet is not ready.
func (r *run) portsOpen(ctx context.Context, id string, ports []int, o StartOptions) bool {
	a, ok := r.d.(Addresser)
	if !ok {
		return false
	}
	host, err := a.IPAddress(ctx, id)
	if err != nil || host == "" {
		return false
	}
	for _, p := range ports {
		if err := o.dial(ctx, net.JoinHostPort(host, strconv.Itoa(p))); err != nil {
			return false
		}
	}
	return true
}

func portList(ports []int) string {
	var s []string
	for _, p := range ports {
		s = append(s, strconv.Itoa(p))
	}
	return strings.Join(s, ", ")
}

// startLogTail is ": " and the last 1 KiB of the output of the container, secrets hidden, or a
// remark when there is none.
func (r *run) startLogTail(id string) string {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	out, err := r.d.LogsTail(ctx, id, logLines)
	if err != nil {
		return fmt.Sprintf(" (the output of the container is not available: %s)", r.redact(err.Error()))
	}
	text := strings.TrimSpace(r.redact(string(out)))
	if len(text) > startLogBytes {
		text = strings.ToValidUTF8(text[len(text)-startLogBytes:], "")
	}
	if text == "" {
		return " (the container wrote nothing)"
	}
	return ": " + text
}

// dialTCP connects to the address and closes the connection.
func dialTCP(ctx context.Context, address string) error {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	return conn.Close()
}

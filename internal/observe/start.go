package observe

import (
	"context"
	"errors"
	"fmt"
	"sort"
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

	// now and sleep are the clock and the pause; tests replace them.
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
}

// Running is a container started by Start.
type Running struct {
	d  Docker
	id string
	// Notes are things to tell the user about how the container came up, for instance that it was
	// running at the deadline but listened on no port.
	Notes []string
}

// ID is the id of the container.
func (r *Running) ID() string { return r.id }

// NoteList is Notes, for a caller that holds the container through an interface.
func (r *Running) NoteList() []string { return r.Notes }

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

// Start runs the image as a plain container (no tracing) and returns when it is ready: when the
// container runs and every tcp port that the image exposes is in the listening state inside it (or, after 10 seconds with some port listening, the report says which exposed ports did not) (read from
// /proc/net/tcp and tcp6 of the container, so nothing is dialed from this machine), or, for an
// image that exposes no port, when the container is still running after 3 seconds. If the
// deadline o.Wait comes while the container still runs, it is returned as running with a note in
// Running.Notes. A container that stops before it is ready is an error that includes the end of
// its output (1 KiB, the values of o.Env hidden), and so is an image that exposes no port and does
// not stay up within o.Wait. The container is removed on every error.
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
	notes, err := r.ready(ctx, id, ports, o)
	if err != nil {
		return fail(err)
	}
	started.Notes = notes
	return started, nil
}

// otherPortsGrace is how long Start waits, after one port listens, for the other ports that the
// image exposes.
const otherPortsGrace = 10 * time.Second

// ready waits for the container to be ready, as Start describes, and gives the notes to keep.
func (r *run) ready(ctx context.Context, id string, ports []int, o StartOptions) ([]string, error) {
	begin := o.now()
	deadline := begin.Add(o.Wait)
	var first time.Time
	var unreadable error
	var partial []int
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("observe: start: waiting for %s: %w", o.Image, err)
		}
		up, err := r.d.Running(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("observe: start: asking whether the container of %s runs: %w", o.Image, err)
		}
		if !up {
			return nil, fmt.Errorf("observe: start: the container of %s stopped before it was ready%s", o.Image, r.startLogTail(id))
		}
		if len(ports) == 0 {
			if !o.now().Before(begin.Add(stayUpFor)) {
				return nil, nil
			}
		} else {
			got, rerr := r.listeningTables(ctx, id)
			unreadable = rerr
			switch {
			case rerr != nil:
				// no way to see the sockets (an image without cat): the container that stays up
				// is taken as ready, and the report says so
				if !o.now().Before(begin.Add(stayUpFor)) {
					note := fmt.Sprintf("%v; the container was taken as ready because it stayed up for %d seconds, not because a port listens", rerr, int(stayUpFor.Seconds()))
					r.logf("%s\n", note)
					return []string{note}, nil
				}
			case len(got) > 0:
				// a server may open one port before the one a client uses (mysqld opens its
				// X port first): wait for every port the image exposes, but not for ever
				missing := missingPorts(got, ports)
				partial = got
				if len(missing) == 0 {
					return nil, nil
				}
				if first.IsZero() {
					first = o.now()
				}
				if !o.now().Before(first.Add(otherPortsGrace)) {
					note := fmt.Sprintf("the container listens on %s but not on %s, which the image exposes", portNumbers(got), portNumbers(missing))
					r.logf("%s\n", note)
					return []string{note}, nil
				}
			}
		}
		if !o.now().Before(deadline) {
			if len(ports) == 0 {
				return nil, fmt.Errorf("observe: start: the container of %s did not stay up within %s%s", o.Image, o.Wait, r.startLogTail(id))
			}
			note := fmt.Sprintf("the container was running but listens on no port after %d seconds", int(o.Wait.Seconds()))
			switch {
			case unreadable != nil:
				note = fmt.Sprintf("%v; whether it listens is not known", unreadable)
			case len(partial) > 0:
				note = fmt.Sprintf("the container listens on %s but not on %s, which the image exposes", portNumbers(partial), portNumbers(missingPorts(partial, ports)))
			}
			r.logf("%s\n", note)
			return []string{note}, nil
		}
		if err := o.sleep(ctx, startPollEvery); err != nil {
			return nil, fmt.Errorf("observe: start: waiting for %s: %w", o.Image, err)
		}
	}
}

// missingPorts are the ports of want that are not in got.
func missingPorts(got, want []int) []int {
	have := map[int]bool{}
	for _, p := range got {
		have[p] = true
	}
	var out []int
	for _, p := range want {
		if !have[p] {
			out = append(out, p)
		}
	}
	return out
}

// portNumbers is the ports as a list "80, 443", sorted and without repeats.
func portNumbers(ports []int) string {
	seen := map[int]bool{}
	var list []int
	for _, p := range ports {
		if !seen[p] {
			seen[p] = true
			list = append(list, p)
		}
	}
	sort.Ints(list)
	text := make([]string, len(list))
	for i, p := range list {
		text[i] = strconv.Itoa(p)
	}
	return strings.Join(text, ", ")
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

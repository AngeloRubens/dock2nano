// Package observe runs a Docker image under strace the way the image starts, and builds a sysroot
// directory that holds only the files the program reached.
//
// It is the part of nanovms/ops#1802 that calls Docker. The parts that read the trace and decide
// which files count live in internal/trace; Docker is reached only through the Docker interface of
// this package, which *docker.Client implements.
//
// The run is sequential. The only goroutine is the one that streams a tar out of Docker for one
// path (see fetch); it is always waited for before fetch returns. Every wait is a poll loop that
// checks the context first, so a cancelled context ends the run at the next step. Both containers
// (the one that fetches strace and the traced one) are stopped and removed in deferred code that
// uses a context of its own, so cleanup also runs after an error or a cancelled context.
//
// Security. The tracer directory (strace, the musl loader and the list of files) is made with
// os.MkdirTemp, so it is mode 0700, and so is the sysroot. The traced container starts as root;
// strace writes the list as root and then runs the program as the user of the image (strace -u),
// so the program cannot open the list. strace and its libraries are mounted a second time,
// read-only, over the directory out. The residual risk is that anyone who can talk to the Docker
// socket is root on this machine anyway, and that the program in the container runs with
// SYS_PTRACE and without seccomp, which is what strace needs.
package observe

import (
	"archive/tar"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/trace"
)

// Docker is what Observe needs from Docker. *docker.Client implements it.
type Docker interface {
	Pull(ctx context.Context, image string) error
	HasImage(ctx context.Context, image string) (bool, error)
	Inspect(ctx context.Context, image string) (*docker.ImageConfig, error)
	Create(ctx context.Context, o docker.CreateOptions) (string, error)
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string, seconds int) error
	Remove(ctx context.Context, id string, force bool) error
	Running(ctx context.Context, id string) (bool, error)
	Wait(ctx context.Context, id string) (int, error)
	ExecUser(ctx context.Context, id, user string, cmd []string) ([]byte, error)
	CopyFrom(ctx context.Context, id, containerPath, hostDir string) error
	CopyTar(ctx context.Context, id, containerPath string, w io.Writer) error
	Logs(ctx context.Context, id string) ([]byte, error)
}

var _ Docker = (*docker.Client)(nil)

// Addresser is an optional extension of Docker: the IP address of a container on the bridge
// network of the host. Without it the ports of the program cannot be asked for their front page,
// and Observe says so in Result.Warnings. *docker.Client implements it.
type Addresser interface {
	IPAddress(ctx context.Context, id string) (string, error)
}

// Prober asks a server for its front page, so that the files it opens only when it answers show
// up in the trace.
type Prober interface {
	// Get returns an error only when the server could not be reached. A reply that is not valid
	// HTTP is not an error: the port was there and has been asked.
	Get(ctx context.Context, url string) error
}

// Options are the options of Observe.
type Options struct {
	// Image is the image to observe. It is required.
	Image string
	// Pull fetches the image when this machine does not have it. When it is false and the image
	// is missing, Observe fails.
	Pull bool
	// TargetExecutable (--file), when set, replaces the entrypoint and cmd of the image: it
	// followed by Args is run.
	TargetExecutable string
	// Args are added after the entrypoint and cmd of the image, or after TargetExecutable.
	Args []string
	// Env is added to the environment of the image.
	Env []string
	// WorkingDir replaces the working directory of the image when not empty.
	WorkingDir string
	// Seconds is how long the program is watched. The default is 30.
	Seconds int
	// Verbose logs what is found and decided, through Log.
	Verbose bool
	// Log receives the messages, in the manner of fmt.Printf. nil drops them.
	Log func(format string, args ...any)
	// Probe asks the ports for their front page. The default is an HTTP GET.
	Probe Prober
	// KeepTrace, when not empty, is a file path where the raw strace output is written, to be
	// kept as a test fixture. A failure to write it is a warning.
	KeepTrace string

	// now and sleep are the clock; tests replace them.
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
}

// Result is what Observe found. The caller owns Sysroot and removes it.
type Result struct {
	// Sysroot is a directory on this machine with the files the program reached.
	Sysroot string

	// Program is the path of the program inside the image.
	Program string

	// Argv is the command line of the program, starting with Program.
	Argv []string

	// Env is the environment of the program, without what the container runtime adds.
	Env []string

	// WorkingDir is the directory the program runs in.
	WorkingDir string

	// ExposedPorts are the tcp ports the image says it listens on, sorted.
	ExposedPorts []int

	// CreatedAtRuntime are the files the program created while it ran (pid files, logs, a config
	// an entrypoint script generated). They are carried when the program opened them, as they
	// were at the end of the observation; Warnings lists them too.
	CreatedAtRuntime []string

	// ConfigurationFiles are the carried files a user is likely to replace.
	ConfigurationFiles []string

	// Image is the configuration of the image.
	Image *docker.ImageConfig

	// Warnings are things that did not stop the run: files that could not be carried, cleanup
	// that failed.
	Warnings []string
}

const (
	defaultSeconds = 30
	pollEvery      = 2 * time.Second
	settleFor      = 5 * time.Second
	quietFor       = 10 * time.Second
	cleanupTimeout = 2 * time.Minute
	stopSeconds    = 2
	logTail        = 2048
)

// tracerScript fetches strace, the musl loader and the libraries strace needs into /out.
const tracerScript = `set -e
apk add --no-cache strace >/dev/null
mkdir -p /out
cp -L /usr/bin/strace /out/strace
cp -L /lib/ld-musl-*.so.1 /out/
for lib in $(ldd /usr/bin/strace | awk '/=>/ {print $3}'); do cp -Ln "$lib" /out/ 2>/dev/null || true; done`

type run struct {
	d       Docker
	o       Options
	warn    []string
	cleanup []error
	once    map[string]bool
}

func (r *run) logf(format string, args ...any) {
	if r.o.Log != nil {
		r.o.Log(format, args...)
	}
}

// redact hides the values of Options.Env in text that comes from outside (the output of the
// container, errors of Docker). The names are not secret and are left alone.
func (r *run) redact(text string) string {
	for _, e := range r.o.Env {
		if _, v, ok := strings.Cut(e, "="); ok && v != "" {
			text = strings.ReplaceAll(text, v, "***")
		}
	}
	return text
}

func (r *run) vlogf(format string, args ...any) {
	if r.o.Verbose {
		r.logf(format, args...)
	}
}

// warnOnce records a warning the first time its text is seen.
func (r *run) warnOnce(text string) {
	if r.once[text] {
		return
	}
	r.once[text] = true
	r.warn = append(r.warn, text)
	r.logf("warning: %s\n", text)
}

// discard stops (when stop is true) and removes a container with a context of its own, so that it
// works after the context of the run is cancelled. Failures are kept for the end of Observe.
func (r *run) discard(id string, stop bool) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	if stop {
		if err := r.d.Stop(ctx, id, stopSeconds); err != nil {
			r.cleanup = append(r.cleanup, fmt.Errorf("stopping container %s: %w", id, err))
		}
	}
	if err := r.d.Remove(ctx, id, true); err != nil {
		r.cleanup = append(r.cleanup, fmt.Errorf("removing container %s: %w", id, err))
	}
}

// Observe runs o.Image under strace and returns the sysroot of the files its program reached.
func Observe(ctx context.Context, d Docker, o Options) (res *Result, err error) {
	if o.Image == "" {
		return nil, errors.New("observe: no image given")
	}
	if o.Seconds <= 0 {
		o.Seconds = defaultSeconds
	}
	if o.now == nil {
		o.now = time.Now
	}
	if o.sleep == nil {
		o.sleep = sleepContext
	}
	if o.Probe == nil {
		o.Probe = httpProber{}
	}
	r := &run{d: d, o: o, once: map[string]bool{}}

	sysroot, err := os.MkdirTemp("", "dock2nano-sysroot-")
	if err != nil {
		return nil, fmt.Errorf("observe: making the sysroot directory: %w", err)
	}
	// registered first, so it runs last: after the containers are gone
	defer func() {
		if err != nil {
			os.RemoveAll(sysroot)
			res = nil
			if len(r.cleanup) > 0 {
				err = errors.Join(append([]error{err}, r.cleanup...)...)
			}
			return
		}
		for _, c := range r.cleanup {
			res.Warnings = append(res.Warnings, "cleanup: "+c.Error())
		}
	}()

	res, err = r.observe(ctx, sysroot)
	return res, err
}

func (r *run) observe(ctx context.Context, sysroot string) (*Result, error) {
	image := r.o.Image

	have, err := r.d.HasImage(ctx, image)
	if err != nil {
		return nil, fmt.Errorf("observe: looking for image %s: %w", image, err)
	}
	if !have {
		if !r.o.Pull {
			return nil, fmt.Errorf("observe: image %s is not on this machine and pulling is off", image)
		}
		r.logf("pulling %s\n", image)
		if err := r.d.Pull(ctx, image); err != nil {
			return nil, fmt.Errorf("observe: pulling %s: %w", image, err)
		}
	}

	cfg, err := r.d.Inspect(ctx, image)
	if err != nil {
		return nil, fmt.Errorf("observe: inspecting %s: %w", image, err)
	}
	if cfg.OS != "" && cfg.OS != "linux" {
		return nil, fmt.Errorf("observe: %s is an image for %s, and a unikernel here runs what was made for linux", image, cfg.OS)
	}
	exposed := exposedPorts(cfg.ExposedPorts)

	var argv []string
	if r.o.TargetExecutable != "" {
		argv = append([]string{r.o.TargetExecutable}, r.o.Args...)
	} else {
		argv = append(append(append([]string{}, cfg.Entrypoint...), cfg.Cmd...), r.o.Args...)
	}
	if len(argv) == 0 {
		return nil, fmt.Errorf("observe: %s starts nothing: say what to run with --file", image)
	}
	workingDir := cfg.WorkingDir
	if r.o.WorkingDir != "" {
		workingDir = r.o.WorkingDir
	}
	env := append(append([]string{}, cfg.Env...), r.o.Env...)

	tracerDir, loader, err := r.prepareTracer(ctx)
	if tracerDir != "" {
		defer os.RemoveAll(tracerDir)
	}
	if err != nil {
		return nil, err
	}

	if len(r.o.Env) > 0 {
		names := make([]string, 0, len(r.o.Env))
		for _, e := range r.o.Env {
			name, _, _ := strings.Cut(e, "=")
			names = append(names, name)
		}
		r.vlogf("environment variables given: %s\n", strings.Join(names, ", "))
	}

	// The container starts as root, so that strace can write the list where the program cannot
	// read it; strace then runs the program as the user of the image.
	// the second bind makes strace and its libraries read-only to everything in the container
	binds := []string{tracerDir + ":" + trace.TraceMount, filepath.Join(tracerDir, "out") + ":" + trace.TraceMount + "/out:ro"}
	wrapped := trace.WrapCommandAs(trace.TraceMount, loader, straceUser(cfg.User), argv)
	id, err := r.d.Create(ctx, docker.CreateOptions{
		Image:       image,
		Entrypoint:  &wrapped,
		Env:         r.o.Env,
		WorkingDir:  r.o.WorkingDir,
		User:        "0",
		Binds:       binds,
		CapAdd:      []string{"SYS_PTRACE"},
		SecurityOpt: []string{"seccomp=unconfined"},
	})
	if err != nil {
		return nil, fmt.Errorf("observe: creating the traced container: %w", err)
	}
	defer r.discard(id, true)

	if err := r.d.Start(ctx, id); err != nil {
		return nil, fmt.Errorf("observe: starting the traced container: %w", err)
	}

	if err := r.watch(ctx, id, tracerDir, exposed); err != nil {
		return nil, err
	}

	// the list is written as the program goes, so what it has opened so far is in it
	raw, err := os.ReadFile(filepath.Join(tracerDir, "files"))
	if err != nil {
		return nil, fmt.Errorf("observe: reading the trace: %w", err)
	}
	if r.o.KeepTrace != "" {
		if err := os.WriteFile(r.o.KeepTrace, raw, 0600); err != nil {
			r.warnOnce(fmt.Sprintf("could not keep the trace in %s: %v", r.o.KeepTrace, err))
		}
	}
	final, found := trace.FinalExec(raw)
	if !found {
		return nil, fmt.Errorf("observe: %s started no program in %d seconds%s", image, r.o.Seconds, r.logTail(id))
	}
	pid, program, from := final.Pid, final.Path, final.Line
	r.vlogf("the program is %s, process %s\n", program, pid)

	// What the process says about itself, while it is there to ask. The command line comes from
	// the execve call: a program such as nginx rewrites /proc/<pid>/cmdline, so that is only
	// compared with it.
	cmdline := slices.Clone(final.Argv)
	cwd, exe := "", ""
	running, err := r.d.Running(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("observe: checking that the traced container runs: %w", err)
	}
	if running {
		base := "/proc/" + pid + "/"
		if out, ok := r.procRead(ctx, id, "command line", []string{"cat", base + "cmdline"}); ok {
			if parts := trace.SplitNul(out); len(parts) > 0 {
				switch {
				case len(cmdline) == 0:
					cmdline = parts
				case !slices.Equal(parts, cmdline):
					r.warnOnce(r.redact(fmt.Sprintf("the command line in /proc %q differs from the one execve was given %q: the one execve was given is used", parts, cmdline)))
				}
			}
		}
		if out, ok := r.procRead(ctx, id, "environment", []string{"cat", base + "environ"}); ok {
			if parts := trace.SplitNul(out); len(parts) > 0 {
				env = parts
			}
		}
		if out, ok := r.procRead(ctx, id, "working directory", []string{"readlink", base + "cwd"}); ok {
			cwd = strings.TrimSpace(string(out))
		}
		if out, ok := r.procRead(ctx, id, "executable", []string{"readlink", base + "exe"}); ok {
			exe = strings.TrimSpace(string(out))
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("observe: reading /proc of the program: %w", err)
		}
	} else {
		r.warnOnce("the program had stopped before /proc could be read: the command line, environment and directory are those of the image")
	}
	if len(cmdline) == 0 {
		cmdline = argv
	}
	if cwd == "" {
		cwd = workingDir
	}
	var runningEnv []string
	for _, e := range env {
		if name, _, ok := strings.Cut(e, "="); ok && !slices.Contains(trace.BelongsToTheContainer, name) {
			runningEnv = append(runningEnv, e)
		}
	}

	touched, executed := trace.TouchedPaths(raw, cwd, from)
	r.vlogf("the program opened %d files\n", len(touched))
	created := trace.CreatedPaths(raw, cwd, from)
	if len(created) > 0 {
		r.warnOnce("the program created these files while it ran; they are carried as they were at the end of the observation, and a fresh start may not make them: " + strings.Join(created, ", "))
	}

	if err := r.copyTouched(ctx, id, sysroot, touched); err != nil {
		return nil, err
	}
	var note func(string, ...any)
	if r.o.Verbose {
		note = r.logf
	}
	err = trace.CarryInterpreters(sysroot, executed, func(interp string) error {
		return r.copyTouched(ctx, id, sysroot, []string{interp})
	}, note)
	if err != nil {
		return nil, fmt.Errorf("observe: carrying interpreters: %w", err)
	}

	if !strings.HasPrefix(program, "/") {
		program = trace.WhereIs(sysroot, program, env)
	}
	// the file the program was started through can be a link, and a program finds its libraries
	// beside the file it really is
	if exe != "" && exe != program {
		if _, err := os.Stat(filepath.Join(sysroot, filepath.FromSlash(exe))); err == nil {
			program = exe
		}
	}
	if _, err := os.Stat(filepath.Join(sysroot, filepath.FromSlash(program))); err != nil {
		return nil, fmt.Errorf("observe: %s did not open %s: say it with --file", image, program)
	}

	return &Result{
		Sysroot:            sysroot,
		Program:            program,
		Argv:               cmdline,
		Env:                runningEnv,
		WorkingDir:         cwd,
		ExposedPorts:       exposed,
		CreatedAtRuntime:   created,
		ConfigurationFiles: trace.ConfigurationFiles(sysroot),
		Image:              cfg,
		Warnings:           r.warn,
	}, nil
}

// straceUser is the user strace runs the program as (-u): the name part of the USER of the image,
// or "" for root. strace looks the name up in /etc/passwd of the image.
func straceUser(imageUser string) string {
	name, _, _ := strings.Cut(imageUser, ":")
	if name == "" || name == "root" || name == "0" {
		return ""
	}
	return name
}

// procRead runs a command in the container, as root, to read /proc. A failure is a warning, since the
// program can have stopped meanwhile.
func (r *run) procRead(ctx context.Context, id string, what string, cmd []string) ([]byte, bool) {
	out, err := r.d.ExecUser(ctx, id, "root", cmd)
	if err != nil {
		r.warnOnce(r.redact(fmt.Sprintf("could not read the %s of the program: %v", what, err)))
		return nil, false
	}
	return out, true
}

// logTail is the end of the output of the container, for an error message.
func (r *run) logTail(id string) string {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	out, err := r.d.Logs(ctx, id)
	if err != nil {
		return fmt.Sprintf(" (the output of the container is not available: %s)", r.redact(err.Error()))
	}
	text := strings.TrimSpace(r.redact(string(out)))
	if len(text) > logTail {
		text = "..." + text[len(text)-logTail:]
	}
	if text == "" {
		return " (the container wrote nothing)"
	}
	return ": " + text
}

// exposedPorts are the tcp ports the image names, sorted.
func exposedPorts(specs []string) []int {
	var ports []int
	for _, s := range specs {
		number, protocol, _ := strings.Cut(s, "/")
		if n, err := strconv.Atoi(number); err == nil && (protocol == "" || protocol == "tcp") {
			ports = append(ports, n)
		}
	}
	sort.Ints(ports)
	return ports
}

// prepareTracer fetches strace and what it is linked against into a directory of its own (mode
// 0700). The image of strace is pulled when it is missing, or when Options.Pull is set. The
// directory is returned even with an error, so that the caller removes it.
func (r *run) prepareTracer(ctx context.Context) (dir string, loader string, err error) {
	dir, err = os.MkdirTemp("", "dock2nano-trace-")
	if err != nil {
		return "", "", fmt.Errorf("observe: making the tracer directory: %w", err)
	}
	// the directory stays 0700 from MkdirTemp: strace runs as root and writes the list, and the
	// program, which runs as the user of the image, cannot reach it

	have, err := r.d.HasImage(ctx, trace.StraceImage)
	if err != nil {
		return dir, "", fmt.Errorf("observe: looking for image %s: %w", trace.StraceImage, err)
	}
	if !have || r.o.Pull {
		if err := r.d.Pull(ctx, trace.StraceImage); err != nil {
			return dir, "", fmt.Errorf("observe: fetching %s for strace: %w", trace.StraceImage, err)
		}
	}
	id, err := r.d.Create(ctx, docker.CreateOptions{Image: trace.StraceImage, Cmd: []string{"sh", "-c", tracerScript}})
	if err != nil {
		return dir, "", fmt.Errorf("observe: creating the strace container: %w", err)
	}
	defer r.discard(id, false)

	if err := r.d.Start(ctx, id); err != nil {
		return dir, "", fmt.Errorf("observe: starting the strace container: %w", err)
	}
	code, err := r.d.Wait(ctx, id)
	if err != nil {
		return dir, "", fmt.Errorf("observe: waiting for the strace container: %w", err)
	}
	if code != 0 {
		return dir, "", fmt.Errorf("observe: getting strace out of %s stopped with %d%s", trace.StraceImage, code, r.logTail(id))
	}
	// strace and what it is linked against, in the directory out under dir
	if err := r.d.CopyFrom(ctx, id, "/out", dir); err != nil {
		return dir, "", fmt.Errorf("observe: copying strace out of the container: %w", err)
	}

	loaders, _ := filepath.Glob(filepath.Join(dir, "out", "ld-musl-*.so.1"))
	if len(loaders) == 0 {
		return dir, "", errors.New("observe: no musl loader came with strace")
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "strace")); err != nil {
		return dir, "", fmt.Errorf("observe: strace did not come out of %s", trace.StraceImage)
	}

	// created ahead, so that the host side owns it; strace writes to it as root
	if err := os.WriteFile(filepath.Join(dir, "files"), nil, 0600); err != nil {
		return dir, "", fmt.Errorf("observe: making the trace file: %w", err)
	}
	return dir, filepath.Base(loaders[0]), nil
}

// watch lets the program run for up to Seconds. A server opens some of what it needs only when it
// answers, so every port it listens on, or the image says it does, is asked for its front page, and
// the time is cut short once they have been asked, nothing new has shown up in the list and it has
// not grown for a while. It returns when the container stops, the time is up, or ctx ends (an
// error).
func (r *run) watch(ctx context.Context, id string, tracerDir string, exposed []int) error {
	deadline := r.o.now().Add(time.Duration(r.o.Seconds) * time.Second)
	asked := map[int]bool{}
	var settled time.Time
	var size int64 = -1
	grew := r.o.now()
	host := ""
	hostTried := false

	for r.o.now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("observe: watching the traced container: %w", err)
		}
		running, err := r.d.Running(ctx, id)
		if err != nil {
			return fmt.Errorf("observe: checking that the traced container runs: %w", err)
		}
		if !running {
			r.vlogf("the traced container stopped\n")
			return nil
		}

		ports := append(r.listening(ctx, id), exposed...)
		fresh := false
		for _, port := range ports {
			if asked[port] {
				continue
			}
			if !hostTried {
				hostTried = true
				host = r.address(ctx, id)
			}
			if host == "" {
				break
			}
			url := "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/"
			if err := r.o.Probe.Get(ctx, url); err != nil {
				continue
			}
			asked[port] = true
			fresh = true
			r.vlogf("asked %s\n", url)
		}
		if fresh {
			settled = r.o.now().Add(settleFor)
		}
		if st, err := os.Stat(filepath.Join(tracerDir, "files")); err == nil && st.Size() != size {
			size = st.Size()
			grew = r.o.now()
		}
		if len(asked) > 0 && r.o.now().After(settled) && r.o.now().Sub(grew) > quietFor {
			return nil
		}
		if err := r.o.sleep(ctx, pollEvery); err != nil {
			return fmt.Errorf("observe: watching the traced container: %w", err)
		}
	}
	return nil
}

// address is the IP of the container, or "" with a warning when it cannot be found.
func (r *run) address(ctx context.Context, id string) string {
	a, ok := r.d.(Addresser)
	if !ok {
		r.warnOnce("the Docker client cannot give the address of a container: the ports of the program were not asked for their front page")
		return ""
	}
	host, err := a.IPAddress(ctx, id)
	if err != nil || host == "" {
		r.warnOnce(fmt.Sprintf("no address for the traced container (%v): the ports of the program were not asked for their front page", err))
		return ""
	}
	return host
}

// listening are the tcp ports the container has a server on.
func (r *run) listening(ctx context.Context, id string) []int {
	var ports []int
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		out, err := r.d.ExecUser(ctx, id, "root", []string{"cat", table})
		if err != nil {
			if ctx.Err() == nil {
				r.warnOnce(r.redact(fmt.Sprintf("could not read %s: %v", table, err)))
			}
			continue
		}
		ports = append(ports, trace.ListeningPorts(string(out))...)
	}
	return ports
}

type fetchKind int

const (
	// kindNothing: docker cp made no entry for the path (a socket, for one).
	kindNothing fetchKind = iota
	kindDir
	kindFile
	kindLink
	// kindOther: a device, a fifo or another entry that is not carried.
	kindOther
)

// fetched says what the path is. For a file or a directory it is already in the sysroot.
type fetched struct {
	kind fetchKind
	// link is the text of the link, for kindLink.
	link string
}

// hostError is a failure to write to this machine, as opposed to a failure of Docker for one path.
type hostError struct{ err error }

func (e *hostError) Error() string { return e.err.Error() }
func (e *hostError) Unwrap() error { return e.err }

// errStopped ends the stream of a copy when its first entry is all that is needed.
var errStopped = errors.New("the rest of the copy is not needed")

// hostWriter remembers that a write failed, to tell it from a failure of the stream being read.
type hostWriter struct {
	w   io.Writer
	err error
}

func (h *hostWriter) Write(p []byte) (int, error) {
	n, err := h.w.Write(p)
	if err != nil && h.err == nil {
		h.err = err
	}
	return n, err
}

// fetch copies one path of the container into the sysroot. It streams the tar of docker cp and
// reads only its first entry, which is the path itself: a directory is made empty (what is in it
// is carried when the program opened it), a regular file is written with its mode (owner write and
// read added, exec bits kept), and a link is only reported. Once the first entry is dealt with the
// stream is cancelled, so the copy of a whole directory such as /usr stops at once, and the docker
// process has ended when fetch returns.
//
// A failure to write to the sysroot is a *hostError. Any other error is a failure of Docker for this
// path (it is not there, the stream broke): the caller makes it a warning. A path that makes no
// entry at all (a socket) is kindNothing and no error.
func (r *run) fetch(ctx context.Context, id string, sysroot string, p string) (fetched, error) {
	cctx, cancel := context.WithCancel(ctx)
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := r.d.CopyTar(cctx, id, p, pw)
		pw.CloseWithError(err) // nil reads as the end of the stream
		done <- err
	}()
	var copyErr error
	finished := false
	stop := func() {
		if !finished {
			finished = true
			cancel()
			pr.CloseWithError(errStopped)
			copyErr = <-done
		}
	}
	defer stop()

	tr := tar.NewReader(pr)
	hdr, err := tr.Next()
	if err == io.EOF {
		stop()
		return fetched{}, copyErr // nil: nothing came
	}
	if err != nil {
		return fetched{}, err
	}
	target := filepath.Join(sysroot, filepath.FromSlash(p))
	mode := hdr.FileInfo().Mode()

	switch {
	case mode&os.ModeSymlink != 0:
		stop()
		return fetched{kind: kindLink, link: hdr.Linkname}, nil
	case mode.IsDir():
		stop()
		if err := os.MkdirAll(target, 0755); err != nil {
			return fetched{}, &hostError{fmt.Errorf("making %s: %w", p, err)}
		}
		return fetched{kind: kindDir}, nil
	case mode.IsRegular():
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fetched{}, &hostError{fmt.Errorf("making the directory of %s: %w", p, err)}
		}
		// a name that an earlier copy left as something else
		os.Remove(target)
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fetched{}, &hostError{fmt.Errorf("creating %s: %w", p, err)}
		}
		hw := &hostWriter{w: f}
		_, readErr := io.Copy(hw, tr)
		closeErr := f.Close()
		switch {
		case hw.err != nil:
			os.Remove(target)
			return fetched{}, &hostError{fmt.Errorf("writing %s: %w", p, hw.err)}
		case readErr != nil:
			// half of a file is not a file
			os.Remove(target)
			return fetched{}, readErr
		case closeErr != nil:
			os.Remove(target)
			return fetched{}, &hostError{fmt.Errorf("writing %s: %w", p, closeErr)}
		}
		if err := os.Chmod(target, mode.Perm()|0600); err != nil {
			return fetched{}, &hostError{fmt.Errorf("setting the mode of %s: %w", p, err)}
		}
		return fetched{kind: kindFile}, nil
	}
	stop()
	return fetched{kind: kindOther}, nil
}

// copyTouched copies the files that were opened out of the container into sysroot. A package does
// not carry a symbolic link: an absolute one names a path of the machine that builds the image, not
// of the image. What a link leads to is carried instead, under the name of the link and under its
// own. A path that Docker cannot give (a file that was there at the time and is not any more, a
// socket, a device) is a warning; a failure to write to the sysroot, or the end of ctx, is an
// error.
func (r *run) copyTouched(ctx context.Context, id string, sysroot string, paths []string) error {
	done := map[string]bool{}
	// the names a path was reached by, through links
	aliases := map[string][]string{}
	queue := slices.Clone(paths)

	notCarried := func(p string, why string) {
		text := r.redact(fmt.Sprintf("not carried %s: %s", p, why))
		r.warn = append(r.warn, text)
		r.vlogf("%s\n", text)
	}

	for len(queue) > 0 {
		p := path.Clean(queue[0])
		queue = queue[1:]
		if done[p] || p == "/" || trace.NotCarried(p) {
			continue
		}
		done[p] = true

		got, err := r.fetch(ctx, id, sysroot, p)
		if err != nil {
			var host *hostError
			if errors.As(err, &host) {
				return fmt.Errorf("observe: copying %s out of the container: %w", p, err)
			}
			if ctx.Err() != nil {
				return fmt.Errorf("observe: copying %s out of the container: %w", p, ctx.Err())
			}
			notCarried(p, err.Error())
			continue
		}

		switch got.kind {
		case kindLink:
			link := trace.ResolveLink(p, got.link)
			names := append([]string{p}, aliases[p]...)
			aliases[link] = append(aliases[link], names...)
			switch {
			case trace.IsConsoleLink(link):
				// a log that is a link to the console of the container: an empty file takes its
				// place, so that opening it works
				for _, name := range names {
					if err := trace.CarryConsoleLink(sysroot, name); err != nil {
						return fmt.Errorf("observe: carrying the console link %s: %w", name, err)
					}
				}
			case done[link]:
				// it was carried already, under its own name
				for _, name := range names {
					if err := trace.CopyHostFile(sysroot, link, name); err != nil {
						return fmt.Errorf("observe: copying %s to %s: %w", link, name, err)
					}
				}
			default:
				queue = append(queue, link)
			}
		case kindDir, kindFile:
			for _, name := range aliases[p] {
				if err := trace.CopyHostFile(sysroot, p, name); err != nil {
					return fmt.Errorf("observe: copying %s to %s: %w", p, name, err)
				}
			}
		case kindNothing:
			notCarried(p, "docker cp made nothing of it (a socket, for one)")
		default:
			notCarried(p, "it is not a file, a directory or a link")
		}
	}
	return nil
}

// sleepContext waits d or until ctx ends.
func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// httpProber is the default Prober: it connects to the address and asks for the page.
type httpProber struct{}

func (httpProber) Get(ctx context.Context, url string) error {
	rest := strings.TrimPrefix(url, "http://")
	address, _, _ := strings.Cut(rest, "/")
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	conn.Close()

	// The probe asks for / of our own container, which can answer with a redirect to https on the
	// same address and a certificate no one signed for it. Nothing is sent but a GET of /, and
	// nothing of the answer is used, so the certificate is not verified. A redirect to another
	// host is not followed.
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer transport.CloseIdleConnections()
	client := http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Hostname() != via[0].URL.Hostname() || len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	if resp, err := client.Do(req); err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	return nil
}

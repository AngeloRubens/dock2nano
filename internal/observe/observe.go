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
// strace writes the list as root and then runs the program as the user of the image (strace -u).
// For a user that is not root the program cannot open the list; for an image whose user is root
// (or whose uid has no name in /etc/passwd, so that it is observed as root) it can, and could
// replace the list with a symbolic link: the list is read only when it is still a regular file
// (Lstat, then read; the check and the read are not one step, which the Docker socket owner could
// race anyway). strace and its libraries are mounted a second time, read-only, over the directory
// out. The residual risk is that anyone who can talk to the Docker socket is root on this machine
// anyway, and that the program in the container runs with SYS_PTRACE and without seccomp, which
// is what strace needs.
package observe

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
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
	LogsTail(ctx context.Context, id string, lines int) ([]byte, error)
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
	// HTTP is not an error: the port was there and has been asked (the status is 0 then). host,
	// when not empty, is sent as the Host header in place of the address of the url. status is the
	// status code of the last answer, and body its first ProbeBodyHead bytes.
	Get(ctx context.Context, url, host string) (status int, body []byte, err error)
}

// Options are the options of Observe.
type Options struct {
	// Image is the image to observe. It is required.
	Image string
	// Pull fetches the image when this machine does not have it. When it is false and the image
	// is missing, Observe fails.
	Pull bool
	// TargetExecutable (--file), when set, replaces the entrypoint and cmd of the image: it
	// followed by Args is run. It wins over Entrypoint and Cmd.
	TargetExecutable string
	// Entrypoint replaces the entrypoint of the image, as the entrypoint of a compose file or
	// docker run --entrypoint does. nil keeps the one of the image; a pointer to an empty slice
	// clears it, as --entrypoint "" does. Setting it (even to an empty slice) also drops the
	// CMD of the image, as Docker does, unless Cmd is given.
	Entrypoint *[]string
	// Cmd replaces the CMD of the image. nil keeps the CMD of the image, unless Entrypoint is
	// set, in which case the CMD of the image is dropped and nothing takes its place.
	Cmd []string
	// Args are added after the entrypoint and cmd (the effective ones), or after
	// TargetExecutable.
	Args []string
	// Env is added to the environment of the image; an entry replaces the variable of the image
	// with the same name.
	Env []string
	// WorkingDir replaces the working directory of the image when not empty.
	WorkingDir string
	// User replaces the user of the image when not empty (the user: of a compose file). A name is
	// used as it is; a uid, or uid:gid, is looked up in /etc/passwd of the image, because strace
	// runs the program as a user name. A uid with no name is observed as root, with a warning.
	User string
	// Binds are bind mounts in docker syntax, "host:container" or "host:container:ro", with
	// absolute paths; a bare absolute path is an anonymous volume at that path of the container. The traced container gets them in addition to its own, so that the program
	// sees the files the user mounts; the files it opens under them are carried like any other.
	// A container path under the directory of the tracer is an error.
	Binds []string
	// Network, when not empty, is the docker network the traced container joins, and
	// NetworkAliases are its names on it. Other containers of the network (see Start) reach the
	// program by them.
	Network        string
	NetworkAliases []string
	// Seconds is how long the program is watched. The default is 30.
	Seconds int
	// Verbose logs what is found and decided, through Log.
	Verbose bool
	// Log receives the messages, in the manner of fmt.Printf. nil drops them.
	Log func(format string, args ...any)
	// Probe asks the ports for their front page. The default is an HTTP GET.
	Probe Prober
	// Exercise are requests made to the program once it is up, so that what it opens only to
	// answer them is in the trace (the certificate on the first connection, the template of a
	// page, a plugin). A path is asked of the first port the image exposes (the first one that
	// answered when it exposes none); an address with the host "container" is asked of the
	// container, at the port it names. A request that fails is a warning.
	Exercise []string
	// ExerciseHost, when not empty, is the Host header of the requests of Exercise, in place of
	// the address of the container: for a program that answers 400 to a host it does not allow
	// before it loads the code that serves a valid request. The front page request and
	// ExerciseCmd are not touched.
	ExerciseHost string

	// Hostname is the base of the machine name: the package name of the service (unique among the
	// services, at most 15 characters). The machine is named after it, with "-host" added
	// (lower-cased and reduced to letters, digits and "-", at most 63 characters in all; "dock2nano"
	// when nothing is left): that name is in /etc/hostname and on 127.0.0.1 in /etc/hosts, and the
	// service name itself is not, as peers resolve it to the address of the machine. The hostname:
	// key of a compose file is not read. An /etc/hostname that the sysroot has as a regular file is
	// replaced.
	Hostname string
	// TakenNames are the names that the services of the project use (service name, package name),
	// lower case, of every service of the file, converted or not. The name of the machine is none of them: when "<service>-host" is one,
	// "-host2", "-host3" and so on are tried. Only these names are checked.
	TakenNames []string

	// ServiceName is the name of the service in the compose file. It is used only in the text of a
	// warning that names --exercise-host; "SVC" is written when it is empty.
	ServiceName string

	// ExerciseCmd is a command run on this side once the program is up, for one that does not
	// speak HTTP. It sees DOCK2NANO_HOST and DOCK2NANO_PORT, the address of the container.
	ExerciseCmd string
	// RunCommand runs ExerciseCmd. The default is sh -c, stopped after a minute.
	RunCommand func(ctx context.Context, command string, env []string) ([]byte, error)
	// KeepTrace, when not empty, is a file path where the raw strace output is written, to be
	// kept as a test fixture. A failure to write it is a warning.
	KeepTrace string

	// now and sleep are the clock; tests replace them.
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
}

// Result is what Observe found. The caller owns Sysroot and removes it.
type Result struct {
	// LocalNames says that the package was given an /etc/hosts that names localhost: the one of the
	// container is not carried.
	LocalNames bool
	// HostnameMade is the name of the machine when /etc/hostname of the package has it and
	// /etc/hosts names it on 127.0.0.1. It is empty when a file was not a regular file, could not
	// be read or could not be written (a warning says it).
	HostnameMade string
	// TmpMade says that the package was given an empty /tmp: nanos has none.
	TmpMade bool
	// MachineFiles lists the files of /proc and /sys that the program opened to learn what machine
	// it runs on (and /proc/self/stat for node, opened or not) and that the package carries as
	// files of ours (the processors), since nanos does not answer them.
	MachineFiles []string

	// Sysroot is a directory on this machine with the files the program reached.
	Sysroot string

	// Program is the path of the program inside the image.
	Program string

	// Argv is the command line of the program, starting with Program.
	Argv []string

	// Env is the environment of the program, without what the container runtime adds.
	Env []string

	// UID and GID are the effective user and group ids the program ran as while it was observed,
	// or -1 when they are not known (the process was gone, /proc could not be read or parsed).
	UID int
	GID int

	// WorkingDir is the directory the program runs in.
	WorkingDir string

	// ExposedPorts are the tcp ports the image says it listens on, sorted.
	ExposedPorts []int

	// CreatedAtRuntime are the files the program created while it ran (pid files, logs, a config
	// an entrypoint script generated) and that were still there at the end. They are carried when
	// the program opened them, as they were at the end of the observation; Warnings lists the
	// first ten. A created file that was gone at the end is not in it: see Vanished.
	CreatedAtRuntime []string

	// Vanished is how many files the program created while it ran that docker cp could not find at
	// the end (temporary files that were renamed or deleted). They are not carried and not warned
	// about one by one.
	Vanished int

	// PassedOver are the labels (trace.Label: names, never arguments) of the commands the image
	// started and that the observation passed over for the program they started, the outermost first:
	// the package manager launchers (npm, yarn, pnpm) and the shells around them. Empty when no
	// launcher was passed over.
	PassedOver []string

	// Beside are the labels of the programs that ran under the shell the observation passed over for
	// the program, were not chosen and are not below the program ("celery worker" of "celery worker
	// & gunicorn app"). The package does not run them.
	Beside []string
	// BesideNear: one of them ran at the same distance from the shell as the program (see trace.Exec).
	BesideNear bool
	// BeforeWaiter: the program is a command that only waits or copies, and these ran under it.
	BeforeWaiter []string

	// NotRun are the labels of the commands that ran before the program or beside it, and ended
	// ("npm (script "migrate")" of "npm run migrate && npm start"): the package does not run them.
	NotRun []string

	// ChildLaunchers are the labels of the launchers that the program started as children: the
	// program is a script of the image that runs npm, which cannot run on nanos.
	ChildLaunchers []string

	// Children is the number of child processes (fork, vfork, clone or clone3 without
	// CLONE_THREAD) the program started while it was observed. Threads are not counted. Nanos runs
	// one process, so the caller reports a program that has any.
	Children int

	// ChildCalls are the distinct calls that started those processes, in the order they were first
	// seen.
	ChildCalls []string

	// ConfigurationFiles are the carried files a user is likely to replace.
	ConfigurationFiles []string

	// Image is the configuration of the image.
	Image *docker.ImageConfig

	// StoppedEarly says the traced container was no longer running before Seconds had elapsed.
	// The program chosen is then the last one it started, which is not necessarily a server (an
	// entrypoint that refuses to start without a password ends this way).
	StoppedEarly bool

	// ExitCode is the exit status of the container; valid when StoppedEarly. It is -1 when the
	// status could not be read.
	ExitCode int

	// StoppedLog is the end of the output of the container (at most 1 KiB, secrets hidden); set
	// when StoppedEarly.
	StoppedLog string

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
	// logLines is how many lines of the output of a container are asked for.
	logLines = 100
	// stoppedLogBytes is the size of Result.StoppedLog.
	stoppedLogBytes = 1024
	// passwdLimit is the most that is read of /etc/passwd of an image.
	passwdLimit = 1 << 20
	// ProbeBodyHead is how much of the body of an answer a Prober gives back.
	ProbeBodyHead = 2048

	// probeBodyLimit is the most that is read of the answer to a probe.
	probeBodyLimit = 1 << 20
	// namePrefix and ownLabel mark the containers of the tool.
	namePrefix = "dock2nano-"
	ownLabel   = "dock2nano=1"
	// passwdProbeEntrypoint is the entrypoint of the container that is only created, to read
	// /etc/passwd of the image from it. It never starts.
	passwdProbeEntrypoint = "/dock2nano-not-started"
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
	// localNames: the package got an /etc/hosts of its own.
	localNames bool
	// hostnameMade: the name that /etc/hostname of the package has and /etc/hosts names on 127.0.0.1.
	hostnameMade string
	once         map[string]bool

	// created are the paths the program created while it ran.
	created map[string]bool
	// gone are the created paths that docker cp could not find.
	gone map[string]bool
	// links is where each link that was met leads to (cleaned, absolute), by the path it was met at.
	links map[string]string
	// notCarried counts the paths that could not be carried and are warned about.
	notCarried int
	// imageValues are the values of the environment of the image, which redact leaves alone.
	imageValues map[string]bool

	stopped    bool
	exitCode   int
	stoppedLog string
}

// maxWarnedPaths is how many paths of one kind are named in the warnings; the rest is counted.
const maxWarnedPaths = 10

// The beginnings of the notes of an observation that describe the files of the
// image (what was carried, made or not carried). They are constants because the
// notes are made here and in cmd, and gen drops them for a package of the hub
// that is used whole: a rewording that changed one without the other would not
// compile.
const (
	NoteEntrypointMade    = "the entrypoint made "
	NoteCreatedWhileRan   = "the program created these files while it ran"
	NoteNotCarried        = "not carried "
	NoteMorePrefix        = "and "
	NoteMoreSuffix        = " more paths were not carried"
	NoteBind              = "the bind "
	NoteMachineFiles      = "could not write the machine files"
	NoteHosts             = "could not make /etc/hosts"
	NoteHostname          = "could not make /etc/hostname"
	NoteProcCmdline       = "the command line in /proc"
	NoteStoppedBeforeProc = "the program had stopped before /proc could be read"
	NoteUserGroup         = "could not read the user and group"
	NoteImageRuns         = "the image runs as "
	// NoteObservedBeside is in the middle of the note that names the services
	// observed while this one ran.
	NoteObservedBeside = " was observed while this service was running; "
)

// FileNotePrefixes are the beginnings of the notes that describe the files of
// the image. NoteMorePrefix, NoteMoreSuffix and NoteObservedBeside are not in
// it: they are matched on their own.
var FileNotePrefixes = []string{
	NoteEntrypointMade, NoteCreatedWhileRan, NoteNotCarried, NoteBind, NoteMachineFiles,
	NoteHosts, NoteHostname, NoteProcCmdline, NoteStoppedBeforeProc, NoteUserGroup, NoteImageRuns,
}

// isNotFound tells a path that docker cp could not find from another failure of docker cp.
func isNotFound(err error) bool {
	text := err.Error()
	return strings.Contains(text, "Could not find the file") || strings.Contains(text, "No such container:path")
}

func (r *run) logf(format string, args ...any) {
	if r.o.Log != nil {
		r.o.Log(format, args...)
	}
}

// redact hides secrets of Options.Env in text that comes from outside (the output of the container,
// errors of Docker): the value of a variable whose name looks like a secret (see secretNames); for
// any variable, the user and password of a URL in its value (jdbc: URLs too) and the value of
// password= and pwd= pairs in it. The names are not secret and are left alone, and so are the
// values of the other variables (DB_NAME=petclinic), which would garble the text around them.
func (r *run) redact(text string) string {
	for _, e := range r.o.Env {
		// a short value (DEBUG=1) or one the image has anyway is not a secret, and replacing it
		// would damage the text around it
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		if secretName(k) && len(v) >= minSecret && !r.imageValues[v] {
			text = strings.ReplaceAll(text, v, "***")
		}
		if info := urlUserinfo(v); info != "" {
			text = strings.ReplaceAll(text, info+"@", "***@")
		}
		for _, m := range passwordPair.FindAllStringSubmatch(v, -1) {
			text = strings.ReplaceAll(text, m[0], m[1]+"=***")
			if len(m[2]) >= minSecret && !r.imageValues[m[2]] {
				text = strings.ReplaceAll(text, m[2], "***")
			}
		}
	}
	return text
}

// passwordPair matches password=value and pwd=value in a query string or a string separated by
// semicolons; the name is group 1 and the value group 2.
var passwordPair = regexp.MustCompile(`(?i)(password|pwd)=([^&;\s"']+)`)

// urlUserinfo gives the user (and password) of a URL value as it is written in it, or "" when the
// value is not a URL with a user. A leading jdbc: is dropped first.
func urlUserinfo(value string) string {
	value = strings.TrimPrefix(value, "jdbc:")
	_, rest, found := strings.Cut(value, "://")
	if !found {
		return ""
	}
	if end := strings.IndexAny(rest, " \t\r\n\"'<>"); end >= 0 {
		rest = rest[:end]
	}
	authority := rest
	if end := strings.IndexAny(authority, "/?#"); end >= 0 {
		authority = authority[:end]
	}
	if at := strings.LastIndex(authority, "@"); at > 0 {
		return authority[:at]
	}
	// A password with a '#', a '/', a '?' or a bad '%' escape ends the authority early: take
	// everything up to the last '@' of the value when it looks like user:password. The price is
	// that "https://host:443/@scope/pkg" loses its host in the log: the two cannot be told apart
	// by their syntax, and hiding too much is the safer mistake.
	if at := strings.LastIndex(rest, "@"); at > 0 && strings.Contains(rest[:at], ":") {
		return rest[:at]
	}
	return ""
}

// secretNames are the parts of the name of a variable (compared in upper case) that make redact
// hide its value.
var secretNames = []string{"PASS", "SECRET", "TOKEN", "KEY", "CRED", "AUTH", "PRIVATE", "CERT", "SIGN", "SALT", "DSN"}

// secretName tells whether the name of a variable looks like the name of a secret. PWD and OLDPWD
// are the directory the shell is in, not a password, though MYSQL_PWD is one.
func secretName(name string) bool {
	name = strings.ToUpper(name)
	if name != "PWD" && name != "OLDPWD" && strings.Contains(name, "PWD") {
		return true
	}
	for _, part := range secretNames {
		if strings.Contains(name, part) {
			return true
		}
	}
	return false
}

// minSecret is the shortest value of Options.Env that redact hides.
const minSecret = 6

// containerName is a name for a container of the tool: dock2nano- and 8 random hex digits.
func (r *run) containerName() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s%08x", namePrefix, uint32(r.o.now().UnixNano()))
	}
	return namePrefix + hex.EncodeToString(b)
}

// create makes a container with a name and a label of the tool. When Create fails without giving
// an id (a cancelled context can leave the container made), it is removed by name, as best effort,
// with a context of its own; the answer of that is not an error, since usually nothing was made.
func (r *run) create(ctx context.Context, o docker.CreateOptions) (string, error) {
	o.Name = r.containerName()
	o.Labels = []string{ownLabel}
	id, err := r.d.Create(ctx, o)
	if err != nil || id == "" {
		cctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_ = r.d.Remove(cctx, o.Name, true)
		if err == nil {
			err = errors.New("Docker gave no container id")
		}
		return "", err
	}
	return id, nil
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
	if err := checkBinds(o.Binds); err != nil {
		return nil, err
	}
	r := &run{d: d, o: o, once: map[string]bool{}, created: map[string]bool{}, gone: map[string]bool{}, links: map[string]string{}}

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

// effectiveArgv is the command line Docker would run: the entrypoint (the one in the options, or
// the image's) followed by the cmd (the one in the options; the image's only when the options do
// not set an entrypoint, because Docker drops the image cmd then), followed by o.Args.
// checkBinds refuses a bind that is not "host:container[:ro]" with absolute paths, or whose
// container path is the directory of the tracer or under it.
func checkBinds(binds []string) error {
	for _, b := range binds {
		parts := strings.Split(b, ":")
		if len(parts) == 1 && path.IsAbs(b) {
			// an anonymous volume: only the path in the container
			t := path.Clean(b)
			if t == trace.TraceMount || strings.HasPrefix(t, trace.TraceMount+"/") {
				return fmt.Errorf("observe: the volume %q is under %s, which the tracer uses", b, trace.TraceMount)
			}
			continue
		}
		if len(parts) < 2 || len(parts) > 3 {
			return fmt.Errorf("observe: the bind %q is not host:container[:ro]", b)
		}
		host, target := parts[0], parts[1]
		if len(parts) == 3 && parts[2] != "ro" && parts[2] != "rw" {
			return fmt.Errorf("observe: the bind %q has the option %q; only ro and rw are known", b, parts[2])
		}
		if !filepath.IsAbs(host) {
			return fmt.Errorf("observe: the host path of the bind %q is not absolute", b)
		}
		if !path.IsAbs(target) {
			return fmt.Errorf("observe: the container path of the bind %q is not absolute", b)
		}
		t := path.Clean(target)
		if t == trace.TraceMount || strings.HasPrefix(t, trace.TraceMount+"/") {
			return fmt.Errorf("observe: the bind %q is under %s, which the tracer uses", b, trace.TraceMount)
		}
	}
	return nil
}

func effectiveArgv(imageEntrypoint, imageCmd []string, o Options) []string {
	entrypoint := imageEntrypoint
	cmd := imageCmd
	if o.Entrypoint != nil {
		entrypoint = *o.Entrypoint
		cmd = nil
	}
	if o.Cmd != nil {
		cmd = o.Cmd
	}
	argv := append([]string{}, entrypoint...)
	argv = append(argv, cmd...)
	return append(argv, o.Args...)
}

// mergeEnv returns base with the entries of add put in: an entry replaces the variable of base with
// the same name, in its place; the others follow.
func mergeEnv(base, add []string) []string {
	out := append([]string{}, base...)
	for _, e := range add {
		name, _, _ := strings.Cut(e, "=")
		replaced := false
		for i, b := range out {
			if n, _, _ := strings.Cut(b, "="); n == name {
				out[i] = e
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, e)
		}
	}
	return out
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
	r.imageValues = map[string]bool{}
	for _, e := range cfg.Env {
		if _, v, ok := strings.Cut(e, "="); ok {
			r.imageValues[v] = true
		}
	}

	var argv []string
	if r.o.TargetExecutable != "" {
		argv = append([]string{r.o.TargetExecutable}, r.o.Args...)
	} else {
		argv = effectiveArgv(cfg.Entrypoint, cfg.Cmd, r.o)
	}
	if len(argv) == 0 {
		return nil, fmt.Errorf("observe: %s starts nothing: say what to run with --file", image)
	}
	workingDir := cfg.WorkingDir
	if r.o.WorkingDir != "" {
		workingDir = r.o.WorkingDir
	}
	// The trace is read from the directory the container starts in, and follows chdir from there.
	// The directory /proc gives at the end is where the program ended up, not where it began.
	startDir := workingDir
	if startDir == "" {
		startDir = "/"
	}
	env := mergeEnv(cfg.Env, r.o.Env)

	tracerDir, loader, err := r.prepareTracer(ctx)
	if tracerDir != "" {
		defer func() {
			if rerr := os.RemoveAll(tracerDir); rerr != nil {
				r.cleanup = append(r.cleanup, fmt.Errorf("the directory of the tracer %s was not removed: %w", tracerDir, rerr))
			}
		}()
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
	binds = append(binds, r.o.Binds...)
	imageUser := cfg.User
	if r.o.User != "" {
		imageUser = r.o.User
	}
	asUser, home, err := r.resolveUser(ctx, image, imageUser, env)
	if err != nil {
		return nil, err
	}
	// The container starts as root, so docker sets HOME=/root; the program runs as asUser, and is
	// given the home of that user unless the image or the options say another HOME.
	createEnv := r.o.Env
	if asUser != "" && home != "" && !hasHome(env) {
		createEnv = append(slices.Clone(r.o.Env), "HOME="+home)
		r.vlogf("HOME is %s, the home of %s\n", home, asUser)
	}
	wrapped := trace.WrapCommandAs(trace.TraceMount, loader, asUser, argv)
	id, err := r.create(ctx, docker.CreateOptions{
		Image:       image,
		Entrypoint:  &wrapped,
		Env:         createEnv,
		WorkingDir:  r.o.WorkingDir,
		User:        "0",
		Binds:       binds,
		CapAdd:      []string{"SYS_PTRACE"},
		SecurityOpt: []string{"seccomp=unconfined"},

		Network:        r.o.Network,
		NetworkAliases: r.o.NetworkAliases,
	})
	if err != nil {
		return nil, fmt.Errorf("observe: creating the traced container: %w", err)
	}
	defer r.discard(id, true)

	if err := r.d.Start(ctx, id); err != nil {
		return nil, fmt.Errorf("observe: starting the traced container: %w", err)
	}

	stopped, err := r.watch(ctx, id, tracerDir, exposed)
	if err != nil {
		return nil, err
	}
	if stopped {
		if err := r.noteStopped(ctx, id); err != nil {
			return nil, err
		}
	}

	// the list is written as the program goes, so what it has opened so far is in it
	raw, err := r.readList(filepath.Join(tracerDir, "files"))
	if err != nil {
		return nil, fmt.Errorf("observe: reading the trace: %w", err)
	}
	if r.o.KeepTrace != "" {
		if err := keepTrace(r.o.KeepTrace, raw); err != nil {
			r.warnOnce(fmt.Sprintf("could not keep the trace in %s: %v", r.o.KeepTrace, err))
		}
	}
	final, found := trace.FinalExec(raw)
	if !found {
		return nil, fmt.Errorf("observe: %s started no program in %d seconds%s", image, r.o.Seconds, r.logTail(id))
	}
	pid, program, from := final.Pid, final.Path, final.Line
	var passedOver []string
	for _, l := range final.PassedOver {
		passedOver = append(passedOver, trace.Label(l.Path, l.Argv))
	}
	r.vlogf("the program is %s, process %s\n", program, pid)

	// What the process says about itself, while it is there to ask. The command line comes from
	// the execve call: a program such as nginx rewrites /proc/<pid>/cmdline, so that is only
	// compared with it.
	cmdline := slices.Clone(final.Argv)
	cwd, exe := "", ""
	uid, gid := -1, -1
	running, err := r.d.Running(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("observe: checking that the traced container runs: %w", err)
	}
	if running {
		base := "/proc/" + pid + "/"
		if out, ok := r.procRead(ctx, id, "command line", []string{"cat", base + "cmdline"}); ok {
			// a program that rewrites its command line, nginx for one, pads it with NULs
			parts := trimEmpty(trace.SplitNul(out))
			// the same trim for the copy of execve that is compared, so that a trailing empty
			// argument (app "") is not taken for a difference
			asked := trimEmpty(slices.Clone(cmdline))
			if len(parts) > 0 {
				switch {
				case len(cmdline) == 0:
					cmdline = parts
				case startedThroughShebang(parts, asked, program):
					r.warnOnce(r.redact(fmt.Sprintf("the program was started through a shebang: the command line of /proc %q is used, not the one execve was given %q", parts, cmdline)))
					cmdline = parts
				case !slices.Equal(parts, asked):
					r.warnOnce(r.redact(fmt.Sprintf(NoteProcCmdline+" %q differs from the one execve was given %q: the one execve was given is used", parts, cmdline)))
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
		if out, ok := r.procRead(ctx, id, "user and group", []string{"cat", base + "status"}); ok {
			var good bool
			if uid, gid, good = parseStatusIDs(string(out)); !good {
				r.warnOnce(NoteUserGroup + " of the program from /proc: they are not known")
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("observe: reading /proc of the program: %w", err)
		}
	} else {
		r.warnOnce(NoteStoppedBeforeProc + ": the command line, environment and directory are those of the image")
	}
	// The program may change its directory while it runs (rr goes to the folder of its config):
	// /proc says where it is now, and the package starts it where it started, which the trace
	// knows even when the program had stopped before /proc was read.
	if dir := trace.CwdAtExec(raw, startDir, final.Line); dir != "" && dir != cwd {
		r.vlogf("the program started in %s; %q is where it was at the end; %s is used\n", dir, cwd, dir)
		cwd = dir
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

	touched, executed := trace.TouchedPaths(raw, startDir, from)
	r.vlogf("the program opened %d files\n", len(touched))
	created := trace.CreatedPaths(raw, startDir, from)
	for _, c := range created {
		r.created[c] = true
	}
	// What the entrypoint made before it handed over to the program is the state the package starts
	// from: a package has no entrypoint to make it again (the database the first start of a server
	// image creates, the keys it generates). It is carried like the rest, as it was at the end.
	var early []string
	for _, e := range trace.CreatedPaths(raw, startDir, 0) {
		if r.created[e] || slices.Contains(touched, e) {
			continue
		}
		r.created[e] = true
		touched = append(touched, e)
		early = append(early, e)
	}

	children := trace.ChildProcesses(raw, from)
	var childCalls []string
	for _, c := range children {
		if !slices.Contains(childCalls, c.Call) {
			childCalls = append(childCalls, c.Call)
		}
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

	// nanos fails the exec when the directory the program starts in is not in the package, and the
	// program may open nothing there
	if wd := path.Clean(cwd); path.IsAbs(wd) && wd != "/" {
		if err := os.MkdirAll(filepath.Join(sysroot, filepath.FromSlash(wd)), 0755); err != nil {
			return nil, fmt.Errorf("observe: making the working directory %s: %w", wd, err)
		}
	}

	// what was made before the program started and is still there
	var earlyHere []string
	for _, e := range early {
		if _, err := os.Lstat(filepath.Join(sysroot, filepath.FromSlash(e))); err == nil && !r.gone[e] {
			earlyHere = append(earlyHere, e)
		}
	}
	if len(earlyHere) > 0 {
		list := earlyHere
		more := ""
		if len(list) > maxWarnedPaths {
			more = fmt.Sprintf(" and %d more", len(list)-maxWarnedPaths)
			list = list[:maxWarnedPaths]
		}
		r.warnOnce(fmt.Sprintf(NoteEntrypointMade+"%d files before the program started (the state the image starts from, such as the data of a database); they are carried as they were at the end of the observation: %s%s", len(earlyHere), strings.Join(list, ", "), more))
	}

	// the created files that were gone at the end are counted, not listed
	var stillThere []string
	for _, c := range created {
		if !r.gone[c] {
			stillThere = append(stillThere, c)
		}
	}
	created = stillThere
	if len(created) > 0 {
		list := created
		more := ""
		if len(list) > maxWarnedPaths {
			more = fmt.Sprintf(" and %d more", len(list)-maxWarnedPaths)
			list = list[:maxWarnedPaths]
		}
		r.warnOnce(NoteCreatedWhileRan + "; they are carried as they were at the end of the observation, and a fresh start may not make them: " + strings.Join(list, ", ") + more)
	}
	if r.notCarried > maxWarnedPaths {
		r.warn = append(r.warn, fmt.Sprintf(NoteMorePrefix+"%d"+NoteMoreSuffix, r.notCarried-maxWarnedPaths))
	}

	if !strings.HasPrefix(program, "/") {
		program = trace.WhereIs(sysroot, program, env)
	}
	// the file the program was started through can be a link, and a program finds its libraries
	// beside the file it really is
	if exe == "" {
		// /proc could not be read (an image without cat and readlink, a distroless one): the links
		// that were met on the way to the program tell where it really is. java of such an image is
		// /usr/bin/java -> /etc/alternatives/java -> /usr/lib/jvm/.../bin/java, and the JVM finds
		// libjli.so at ../lib of the last, not of the first.
		exe = r.endOfLinks(path.Clean(program))
	}
	if exe != "" && exe != program {
		if _, err := os.Stat(filepath.Join(sysroot, filepath.FromSlash(exe))); err == nil {
			program = exe
		}
	}
	if _, err := os.Stat(filepath.Join(sysroot, filepath.FromSlash(program))); err != nil {
		return nil, fmt.Errorf("observe: %s did not open %s: say it with --file", image, program)
	}

	r.writeLocalNames(sysroot)
	r.writeHostname(sysroot)
	machine, err := writeMachineFiles(sysroot, cfg.Architecture, program, touched)
	if err != nil {
		r.warnOnce(fmt.Sprintf(NoteMachineFiles+" for the package: %v", err))
	}

	tmpMade, err := carryTmp(sysroot)
	if err != nil {
		// the reason without the path: it is a path of this machine
		r.warnOnce(fmt.Sprintf("could not make /tmp for the package: %s", errReason(err)))
	}

	return &Result{
		TmpMade:            tmpMade,
		MachineFiles:       machine,
		LocalNames:         r.localNames,
		HostnameMade:       r.hostnameMade,
		Sysroot:            sysroot,
		Program:            program,
		Argv:               cmdline,
		Env:                runningEnv,
		UID:                uid,
		GID:                gid,
		WorkingDir:         cwd,
		ExposedPorts:       exposed,
		CreatedAtRuntime:   created,
		Vanished:           len(r.gone),
		PassedOver:         passedOver,
		NotRun:             final.NotRun,
		Beside:             final.Beside,
		BesideNear:         final.BesideNear,
		BeforeWaiter:       final.BeforeWaiter,
		ChildLaunchers:     final.ChildLaunchers,
		Children:           len(children),
		ChildCalls:         childCalls,
		ConfigurationFiles: trace.ConfigurationFiles(sysroot),
		Image:              cfg,
		StoppedEarly:       r.stopped,
		ExitCode:           r.exitCode,
		StoppedLog:         r.stoppedLog,
		Warnings:           r.warn,
	}, nil
}

// parseStatusIDs reads the effective uid and gid (the second column) from the text of
// /proc/<pid>/status. Both are -1 and ok is false when either line is missing or malformed.
func parseStatusIDs(text string) (uid, gid int, ok bool) {
	uid, gid = -1, -1
	for _, line := range strings.Split(text, "\n") {
		name, rest, found := strings.Cut(line, ":")
		if !found || (name != "Uid" && name != "Gid") {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 2 {
			return -1, -1, false
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			return -1, -1, false
		}
		// a negative number is left in uid or gid, and the check below refuses it
		if name == "Uid" {
			uid = n
		} else {
			gid = n
		}
	}
	if uid < 0 || gid < 0 {
		return -1, -1, false
	}
	return uid, gid, true
}

// writeLocalNames gives the package an /etc/hosts that names localhost, unless the sysroot has one.
// The /etc/hosts of the container is not carried (docker writes it with the name and the address
// of that container), and without any, a program that looks up "localhost" asks the resolver and
// does not get an answer: caddy and etcd stop at "lookup localhost: no such host".
func (r *run) writeLocalNames(sysroot string) {
	path := filepath.Join(sysroot, "etc", "hosts")
	if _, err := os.Lstat(path); err == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.warnOnce(fmt.Sprintf(NoteHosts+" for the package: %s", errReason(err)))
		return
	}
	const hosts = "127.0.0.1\tlocalhost\n::1\tlocalhost ip6-localhost ip6-loopback\n"
	if err := os.WriteFile(path, []byte(hosts), 0o644); err != nil {
		r.warnOnce(fmt.Sprintf(NoteHosts+" for the package: %s", errReason(err)))
		return
	}
	r.localNames = true
}

// defaultHostname is the name of the machine when the one asked for has nothing usable in it.
const defaultHostname = "dock2nano"

// hostSuffix ends the name of the machine. The name is checked against TakenNames, the names that
// the converted services have (service name, package name, network alias), and a number is added
// when one of them is the same. A name that is not in the compose file, and is used by something
// else on the network of the program, is not checked.
const hostSuffix = "-host"

// sanitizePart makes a part of a name of a machine of s: lower case, a character that is not a
// letter, a digit or "-" turned into "-", repeated "-" collapsed into one (as the package names
// are), at most limit characters, no "-" at either end. It is empty when nothing is left.
func sanitizePart(s string, limit int) string {
	var b []byte
	dash := false
	for _, c := range []byte(strings.ToLower(s)) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b = append(b, c)
			dash = false
		} else if !dash {
			b = append(b, '-')
			dash = true
		}
	}
	if len(b) > limit {
		b = b[:limit]
	}
	return strings.Trim(string(b), "-")
}

// machineName is the name of the machine of the service: its package name (service is the base
// that cmd gives: unique, at most 15 characters), sanitized, and "-host" (the base is cut so that
// the whole is at most 63 characters). It is not the name of the
// service: the peers use that name for the address of the service, and a program that is
// configured with its own service name must not find 127.0.0.1 behind it. When the name is one of
// taken (without regard to case), "-host2", "-host3" and so on are tried until one is not. When
// nothing is left of the service name, the name is "dock2nano" when that is not taken, and else
// "dock2nano-host", "dock2nano-host2" and so on.
func machineName(service string, taken []string) string {
	free := func(name string) bool {
		for _, t := range taken {
			if strings.EqualFold(t, name) {
				return false
			}
		}
		return true
	}
	part := sanitizePart(service, 63-len(hostSuffix))
	if part == "" {
		if free(defaultHostname) {
			return defaultHostname
		}
		part = defaultHostname
	}
	for n := 1; ; n++ {
		suffix := hostSuffix
		if n > 1 {
			suffix += strconv.Itoa(n)
		}
		name := sanitizePart(part, 63-len(suffix)) + suffix
		if free(name) {
			return name
		}
	}
}

// errReason is the text of err without the path of this machine, when err is a path error.
func errReason(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// hostsNames says whether a line of the hosts file has name as a name on 127.0.0.1 (not as the
// address, not on another address, and not in a comment), without regard to case. A line that has
// the name on another address does not count: the line on 127.0.0.1 is appended after it. (glibc
// takes the first match, so that line would win for a lookup by name, but the hosts file of the
// sysroot is the one of this tool: it is made by writeLocalNames or comes from the image, and a
// name of the machine on another address there is not expected.)
func hostsNames(hosts []byte, name string) bool {
	for _, line := range strings.Split(string(hosts), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "127.0.0.1" {
			continue
		}
		for _, f := range fields[1:] {
			if strings.EqualFold(f, name) {
				return true
			}
		}
	}
	return false
}

// writeHostname gives the package a name of the machine that resolves. nanos reads /etc/hostname
// of the package at boot, the whole file less the CR and LF at its end; without it the machine is
// named after its address (192-168-33-209) and nothing resolves that name, so a program that
// looks up its own host name fails (WildFly: UnknownHostException in InetAddress.getLocalHost();
// ASP.NET: SocketException in Dns.GetHostEntry). The name is machineName of the package name of
// the service, and only that name is put on 127.0.0.1: the service name stays the name that peers
// resolve to the address of the machine. /etc/hosts must be a regular file that can be read, and
// /etc/hostname must not be a link or a directory: both are checked before anything is written,
// and when one cannot be used nothing is written. Then the line "127.0.0.1<TAB>name" is appended
// to /etc/hosts when no line of it has the name on 127.0.0.1, and only after that /etc/hostname is
// written, so that no step has to be undone: a failure of the append leaves /etc/hostname as it
// was, and a failure of the write of /etc/hostname leaves a hosts file that gained only a
// loopback line for a name nothing else uses. An /etc/hostname of the sysroot that is a regular
// file is replaced (it is almost never there). It runs after writeLocalNames. hostnameMade is set
// when both files have the name at the end; a file that cannot be used or a write that fails is
// one warning and no claim.
func (r *run) writeHostname(sysroot string) {
	name := machineName(r.o.Hostname, r.o.TakenNames)

	hostsPath := filepath.Join(sysroot, "etc", "hosts")
	fi, err := os.Lstat(hostsPath)
	if errors.Is(err, fs.ErrNotExist) {
		r.warnOnce(NoteHostname + ": the /etc/hosts of the package does not exist")
		return
	}
	if err != nil {
		r.warnOnce(NoteHostname + ": the /etc/hosts of the package could not be read: " + errReason(err))
		return
	}
	if !fi.Mode().IsRegular() {
		r.warnOnce(NoteHostname + ": the /etc/hosts of the package could not be used: it is not a regular file")
		return
	}
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		r.warnOnce(NoteHostname + ": the /etc/hosts of the package could not be read: " + errReason(err))
		return
	}

	hostnamePath := filepath.Join(sysroot, "etc", "hostname")
	switch fi, err := os.Lstat(hostnamePath); {
	case err == nil && !fi.Mode().IsRegular():
		// a link or a directory: not touched (a link may lead out of the sysroot)
		r.warnOnce(NoteHostname + ": /etc/hostname of the package is not a regular file")
		return
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		r.warnOnce(NoteHostname + " for the package: " + errReason(err))
		return
	}

	if err := os.WriteFile(hostnamePath, []byte(name+"\n"), 0o644); err != nil {
		r.warnOnce(NoteHostname + " for the package: " + errReason(err) + " (the /etc/hosts of the package only gained a loopback line)")
		return
	}
	if !hostsNames(data, name) {
		add := "127.0.0.1\t" + name + "\n"
		if len(data) > 0 && data[len(data)-1] != '\n' {
			add = "\n" + add
		}
		f, err := os.OpenFile(hostsPath, os.O_WRONLY|os.O_APPEND, 0)
		if err == nil {
			_, err = f.WriteString(add)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			r.warnOnce(NoteHostname + ": the /etc/hosts of the package could not be written: " + errReason(err))
			return
		}
	}
	r.hostnameMade = name
}

// resolveUser gives the user name strace -u runs the program as, or "" for root, and the home
// directory the image's /etc/passwd gives that user ("" when it is not known). strace looks the
// name up in /etc/passwd of the image, so a uid (or uid:gid) is turned into a name by reading
// that file out of a container that is created from the image and never started. A name is used as
// it is. A uid with no name, or a file that cannot be read, means the program is observed as root,
// and that is a warning, but only for a uid. An error is only the end of ctx.
//
// The home matters because the traced container starts as root: docker sets HOME to /root, and
// strace -u does not change it, while a container started as that user has the home of the user
// (pm2 and many programs write under HOME and fail with EACCES under /root).
func (r *run) resolveUser(ctx context.Context, image, user string, env []string) (name, home string, err error) {
	who, _, _ := strings.Cut(user, ":")
	if who == "" || who == "root" {
		return "", "", nil
	}
	uid, perr := strconv.ParseUint(who, 10, 32)
	isUID := perr == nil
	if perr != nil {
		if strings.Trim(who, "0123456789") == "" {
			// only digits, but too many for a uid
			r.warnOnce(fmt.Sprintf(NoteImageRuns+"%q, which is not a uid; it was observed as root", who))
			return "", "", nil
		}
		name = who
	} else if uid == 0 {
		return "", "", nil
	}

	why := ""
	ep := []string{passwdProbeEntrypoint}
	probe, err := r.create(ctx, docker.CreateOptions{Image: image, Entrypoint: &ep})
	if err == nil {
		data, rerr := r.readFirstFile(ctx, probe, "/etc/passwd", passwdLimit)
		r.discard(probe, false)
		if rerr == nil {
			if isUID {
				name = passwdName(data, uid)
			}
			home = passwdHome(data, name)
		} else {
			why = rerr.Error()
		}
	} else {
		why = err.Error()
	}
	if cerr := ctx.Err(); cerr != nil {
		return "", "", fmt.Errorf("observe: looking up the user %s of the image: %w", who, cerr)
	}
	if name != "" {
		if isUID {
			r.vlogf("the user of the image, uid %d, is %s\n", uid, name)
		}
		if home == "" && !hasHome(env) {
			// docker would have given the program the home of its user; here HOME stays /root
			text := fmt.Sprintf(NoteImageRuns+"%s, whose home directory is not known (%s); HOME was left as the container has it (/root), where that user may not be able to write", name, homeWhy(why))
			r.warnOnce(r.redact(text))
		}
		return name, home, nil
	}
	text := fmt.Sprintf(NoteImageRuns+"uid %d, which has no name in /etc/passwd; it was observed as root", uid)
	if why != "" {
		text += " (/etc/passwd could not be read: " + why + ")"
	}
	r.warnOnce(r.redact(text))
	return "", "", nil
}

// hasHome says whether the environment (the image's and the options') sets HOME.
func hasHome(env []string) bool {
	return slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, "HOME=") })
}

// homeWhy says why the home of a user is not known: /etc/passwd could not be read, or has no usable
// entry.
func homeWhy(readError string) string {
	if readError != "" {
		return "/etc/passwd could not be read: " + readError
	}
	return "/etc/passwd has no entry with an absolute home for it"
}

// passwdHome is the home directory (the sixth field) of the first entry of /etc/passwd named name,
// or "" when there is none or the field is not an absolute path.
func passwdHome(data []byte, name string) string {
	if name == "" {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 6 || strings.HasPrefix(f[0], "#") || f[0] != name {
			continue
		}
		if h := strings.TrimSpace(f[5]); strings.HasPrefix(h, "/") {
			return h
		}
		return ""
	}
	return ""
}

// passwdName is the first name in the text of /etc/passwd whose uid is uid.
func passwdName(data []byte, uid uint64) string {
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 3 || f[0] == "" || strings.HasPrefix(f[0], "#") {
			continue
		}
		if n, err := strconv.ParseUint(strings.TrimSpace(f[2]), 10, 32); err == nil && n == uid {
			return f[0]
		}
	}
	return ""
}

// readFirstFile reads the first entry of the tar of docker cp for one path, which must be a
// regular file of at most limit bytes. The stream is cancelled and waited for before it returns.
func (r *run) readFirstFile(ctx context.Context, id, p string, limit int64) ([]byte, error) {
	cctx, cancel := context.WithCancel(ctx)
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := r.d.CopyTar(cctx, id, p, pw)
		pw.CloseWithError(err) // nil reads as the end of the stream
		done <- err
	}()
	defer func() {
		cancel()
		pr.CloseWithError(errStopped)
		<-done
	}()

	tr := tar.NewReader(pr)
	hdr, err := tr.Next()
	if err == io.EOF {
		return nil, fmt.Errorf("docker cp made no entry for %s", p)
	}
	if err != nil {
		return nil, err
	}
	if !hdr.FileInfo().Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	data, err := io.ReadAll(io.LimitReader(tr, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", p, limit)
	}
	return data, nil
}

// keepTrace writes the trace to a new file of mode 0600: a file that is there is removed first (a
// link is removed, not followed, and an old mode is not kept), and the new one is made with O_EXCL.
func keepTrace(name string, raw []byte) error {
	if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// readList reads the list strace wrote. The program runs as root when the image does, and could
// have put a link in the place of the file; only a regular file is read, and anything else is a
// warning and an empty list.
func (r *run) readList(p string) ([]byte, error) {
	st, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		r.warnOnce("the trace file is not a regular file (mode " + st.Mode().String() + "): it was not read")
		return nil, nil
	}
	return os.ReadFile(p)
}

// noteStopped records that the traced container ended before the time was up: its exit status, the
// end of its output, and a warning that the program chosen is not necessarily a server.
func (r *run) noteStopped(ctx context.Context, id string) error {
	code, err := r.d.Wait(ctx, id)
	status := strconv.Itoa(code)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("observe: waiting for the traced container: %w", cerr)
		}
		code, status = -1, "unknown ("+r.redact(err.Error())+")"
	}
	r.stopped, r.exitCode = true, code

	lctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	if out, lerr := r.d.LogsTail(lctx, id, logLines); lerr == nil {
		text := strings.TrimSpace(r.redact(string(out)))
		if len(text) > stoppedLogBytes {
			text = text[len(text)-stoppedLogBytes:]
			// the cut can fall inside a line: that line is dropped
			if i := strings.Index(text, "\n"); i >= 0 && i < len(text)-1 {
				text = text[i+1:]
			}
			text = strings.ToValidUTF8(text, "")
		}
		r.stoppedLog = text
	}
	var first []string
	for _, line := range strings.Split(r.stoppedLog, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			first = append(first, line)
		}
		if len(first) == 3 {
			break
		}
	}
	text := fmt.Sprintf("the container stopped with status %s before the end of the observation; the program chosen is the last one it started, not necessarily a server", status)
	if len(first) > 0 {
		text += ": " + strings.Join(first, " | ")
	}
	r.warnOnce(text)
	return nil
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
	out, err := r.d.LogsTail(ctx, id, logLines)
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
	id, err := r.create(ctx, docker.CreateOptions{Image: trace.StraceImage, Cmd: []string{"sh", "-c", tracerScript}})
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
// not grown for a while. It returns when the container stops (stopped is true), the time is up, or
// ctx ends (an error). A port that could not be asked is a warning that names it.
func (r *run) watch(ctx context.Context, id string, tracerDir string, exposed []int) (stopped bool, err error) {
	deadline := r.o.now().Add(time.Duration(r.o.Seconds) * time.Second)
	asked := map[int]bool{}
	var settled time.Time
	var size int64 = -1
	grew := r.o.now()
	host := ""
	hostTried := false
	exercised := false
	// ports that were not asked, and why
	failed := map[int]string{}
	defer func() {
		var left []int
		for port := range failed {
			if !asked[port] {
				left = append(left, port)
			}
		}
		sort.Ints(left)
		for _, port := range left {
			r.warnOnce(r.redact(fmt.Sprintf("port %d could not be asked for /: %s", port, failed[port])))
		}
	}()

	for r.o.now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("observe: watching the traced container: %w", err)
		}
		running, err := r.d.Running(ctx, id)
		if err != nil {
			return false, fmt.Errorf("observe: checking that the traced container runs: %w", err)
		}
		if !running {
			r.vlogf("the traced container stopped\n")
			return true, nil
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
				failed[port] = "no address for the container"
				continue
			}
			url := "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/"
			if !r.o.now().Before(deadline) {
				break
			}
			// the question may not take the observation past its end
			pctx, stop := context.WithTimeout(ctx, deadline.Sub(r.o.now()))
			_, _, err := r.o.Probe.Get(pctx, url, "")
			stop()
			if err != nil {
				failed[port] = err.Error()
				continue
			}
			asked[port] = true
			fresh = true
			r.vlogf("asked %s\n", url)
		}
		if fresh {
			settled = r.o.now().Add(settleFor)
		}
		if len(asked) > 0 && !exercised && (len(r.o.Exercise) > 0 || r.o.ExerciseCmd != "") {
			exercised = true
			r.exercise(ctx, host, firstPort(exposed, asked), deadline)
			settled = r.o.now().Add(settleFor)
			grew = r.o.now()
		}
		if st, err := os.Lstat(filepath.Join(tracerDir, "files")); err == nil && st.Size() != size {
			size = st.Size()
			grew = r.o.now()
		}
		if len(asked) > 0 && r.o.now().After(settled) && r.o.now().Sub(grew) > quietFor {
			return false, nil
		}
		if err := r.o.sleep(ctx, pollEvery); err != nil {
			return false, fmt.Errorf("observe: watching the traced container: %w", err)
		}
	}
	return false, nil
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
	ports, _ := r.listeningTables(ctx, id)
	return ports
}

// listeningTables is listening, and says whether neither table of /proc could be read (an
// image without cat), with the error of the last one.
func (r *run) listeningTables(ctx context.Context, id string) ([]int, error) {
	var ports []int
	var last error
	failed := 0
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		out, err := r.d.ExecUser(ctx, id, "root", []string{"cat", table})
		if err != nil {
			failed++
			last = fmt.Errorf("could not read %s: %s", table, r.redact(err.Error()))
			if ctx.Err() == nil {
				r.warnOnce(r.redact(fmt.Sprintf("could not read %s: %v", table, err)))
			}
			continue
		}
		ports = append(ports, trace.ListeningPorts(string(out))...)
	}
	if failed < 2 {
		last = nil
	}
	return ports, last
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
	if !path.IsAbs(p) {
		return fetched{}, fmt.Errorf("the path %q is not absolute", p)
	}
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
		if !trace.InsideSysroot(sysroot, target) {
			return fetched{}, fmt.Errorf("it is, through a link of the image, outside the package")
		}
		if err := os.MkdirAll(target, 0755); err != nil {
			return fetched{}, &hostError{fmt.Errorf("making %s: %w", p, err)}
		}
		return fetched{kind: kindDir}, nil
	case mode.IsRegular():
		if !trace.InsideSysroot(sysroot, filepath.Dir(target)) {
			return fetched{}, fmt.Errorf("its directory is, through a link of the image, outside the package")
		}
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
// not carry an absolute symbolic link: it names a path of the machine that builds the image, not
// of the image. What such a link leads to is carried instead, under the name of the link and under
// its own. A relative link is kept as a link when what it leads to is carried: a program finds its
// own files beside the file it really is (npm is a link, and its modules are found from the file
// the link leads to, which a copy under the name of the link would not have beside it). A path that Docker cannot give (a file that was there at the time and is not any more, a
// socket, a device) is a warning; a failure to write to the sysroot, or the end of ctx, is an
// error.
func (r *run) copyTouched(ctx context.Context, id string, sysroot string, paths []string) error {
	done := map[string]bool{}
	// the paths that are in the sysroot now; done also holds the ones that Docker could not give
	carried := map[string]bool{}
	// the names a path was reached by, through links
	aliases := map[string][]string{}
	// the text of the relative links, by the name they were reached by
	relative := map[string]string{}
	queue := slices.Clone(paths)
	// place puts what link leads to under name: a link with the text it had, or a copy
	// a link or a copy that was left out on purpose is a warning, never an error
	skipped := func(err error) error {
		var left *trace.LinkSkipped
		if errors.As(err, &left) {
			r.warnOnce(r.redact(NoteNotCarried + left.Error()))
			return nil
		}
		return err
	}
	place := func(link, name string) error {
		if text, ok := relative[name]; ok {
			err := trace.LinkHostFile(sysroot, text, name)
			if !errors.Is(err, trace.ErrTopLevelLink) {
				return skipped(err)
			}
			// a link at the top of the image (/lib, /lib64) is carried as a copy, as an absolute one is
		}
		return skipped(trace.CopyHostFile(sysroot, link, name))
	}

	notCarried := func(p string, why string) {
		text := r.redact(fmt.Sprintf(NoteNotCarried+"%s: %s", p, why))
		r.notCarried++
		if r.notCarried <= maxWarnedPaths {
			r.warn = append(r.warn, text)
		}
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
			if r.created[p] && isNotFound(err) {
				// a temporary file the program made and removed again
				r.gone[p] = true
				r.vlogf("gone at the end: %s\n", p)
				continue
			}
			notCarried(p, err.Error())
			continue
		}

		switch got.kind {
		case kindLink:
			link := trace.ResolveLink(p, got.link)
			r.links[p] = link
			if !strings.HasPrefix(got.link, "/") {
				relative[p] = got.link
			}
			names := append([]string{p}, aliases[p]...)
			aliases[link] = append(aliases[link], names...)
			switch {
			case trace.IsConsoleLink(link):
				// a log that is a link to the console of the container: an empty file takes its
				// place, so that opening it works
				for _, name := range names {
					if err := skipped(trace.CarryConsoleLink(sysroot, name)); err != nil {
						return fmt.Errorf("observe: carrying the console link %s: %w", name, err)
					}
				}
			case done[link] && !carried[link]:
				// it was asked for and Docker could not give it
				for _, name := range names {
					notCarried(name, "it leads to "+link+", which was not carried")
				}
			case done[link]:
				// it was carried already, under its own name
				for _, name := range names {
					if err := place(link, name); err != nil {
						return fmt.Errorf("observe: copying %s to %s: %w", link, name, err)
					}
				}
			default:
				queue = append(queue, link)
			}
		case kindDir, kindFile:
			carried[p] = true
			for _, name := range aliases[p] {
				if err := place(p, name); err != nil {
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

// endOfLinks follows the links that copyTouched met from p (a clean path), on the name itself and on
// each directory of it, and gives the path where they end, or p itself when none is a link. With
// /usr/bin/java -> /opt/jdk/bin/java and /opt/jdk -> jdk-17 it gives /opt/jdk-17/bin/java. It stops
// at 40 links, as the kernel does.
func (r *run) endOfLinks(p string) string {
	for i := 0; i < 40; i++ {
		next := r.stepOfLinks(p)
		if next == p {
			return p
		}
		p = next
	}
	return p
}

// stepOfLinks replaces the first prefix of p (from the top) that is a link met by copyTouched with
// what it leads to, and keeps the rest of p. It gives p when no prefix is a link.
func (r *run) stepOfLinks(p string) string {
	for i := 1; i <= len(p); i++ {
		if i < len(p) && p[i] != '/' {
			continue
		}
		if next, ok := r.links[p[:i]]; ok {
			return path.Join(next, p[i:])
		}
	}
	return p
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

// firstPort is the first port the image exposes, or the lowest of those that answered.
func firstPort(exposed []int, asked map[int]bool) int {
	if len(exposed) > 0 {
		return exposed[0]
	}
	first := 0
	for p := range asked {
		if first == 0 || p < first {
			first = p
		}
	}
	return first
}

// exerciseURL is the address a request is made to: raw taken against the container at host, and at
// port when it names none.
func exerciseURL(raw, host string, port int) (string, error) {
	if strings.HasPrefix(raw, "/") {
		return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + raw, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("%q is neither a path nor an address", raw)
	}
	if u.Scheme != "http" {
		return "", fmt.Errorf("%q is not http: only http is asked", raw)
	}
	if u.Hostname() != "container" {
		return "", fmt.Errorf("%q does not name the container: use the host \"container\" or a path", raw)
	}
	p := u.Port()
	if p == "" {
		p = strconv.Itoa(port)
	}
	u.Host = net.JoinHostPort(host, p)
	return u.String(), nil
}

// hostRejections are the texts of the answers of frameworks that refuse a request for its Host
// header: Django (DisallowedHost, Invalid HTTP_HOST header), the dev servers of webpack and Vite
// (Invalid Host header; Vite: Blocked request. This host) and Rails (Blocked host, Blocked hosts).
var hostRejections = []string{"DisallowedHost", "Invalid HTTP_HOST header", "Invalid Host header", "Blocked host", "Blocked request. This host"}

// rejectedNote is the warning for a request of --exercise that was answered with a status of 400 or
// more. When the status is 400 or 403 and the start of the body is one of the answers of a framework
// that refuses the Host header, it says so and names the option; the body itself is not printed.
func (r *run) rejectedNote(raw string, status int, body []byte) string {
	service := r.o.ServiceName
	if service == "" {
		service = "SVC"
	}
	if status == 400 || status == 403 {
		for _, m := range hostRejections {
			if bytes.Contains(body, []byte(m)) {
				if r.o.ExerciseHost != "" {
					return fmt.Sprintf("exercise: the program rejected the Host header of the request for %s (status %d): the Host %s given with --exercise-host was rejected; give a name the program accepts (for Django one in ALLOWED_HOSTS)", raw, status, r.o.ExerciseHost)
				}
				return fmt.Sprintf("exercise: the program rejected the Host header of the request for %s (status %d): code that is loaded only to answer a valid request may be missing from the package; run again with --exercise-host %s=localhost (or the host name the program accepts)", raw, status, service)
			}
		}
	}
	msg := fmt.Sprintf("exercise: the request for %s was answered with status %d", raw, status)
	if status == 400 && bytes.Contains(body, []byte("Bad Request")) {
		// Django with DEBUG off answers a bad Host with a page that does not say why
		if r.o.ExerciseHost == "" {
			msg += fmt.Sprintf("; if the program checks the Host header, try --exercise-host %s=localhost", service)
		} else {
			msg += fmt.Sprintf("; the Host %s given with --exercise-host may have been rejected", r.o.ExerciseHost)
		}
	}
	return msg
}

// exercise makes the requests and runs the command once the program is up. What fails is a
// warning: the package is made from whatever was seen. Nothing goes past the end of the time.
func (r *run) exercise(ctx context.Context, host string, port int, deadline time.Time) {
	for _, raw := range r.o.Exercise {
		address, err := exerciseURL(raw, host, port)
		if err != nil {
			r.warnOnce("exercise: " + err.Error())
			continue
		}
		left := deadline.Sub(r.o.now())
		if left <= 0 {
			r.warnOnce("exercise: the time was up before " + raw + " was asked")
			continue
		}
		pctx, stop := context.WithTimeout(ctx, left)
		status, body, err := r.o.Probe.Get(pctx, address, r.o.ExerciseHost)
		stop()
		if err != nil {
			r.warnOnce(r.redact("exercise: calling " + raw + ": " + err.Error()))
			continue
		}
		if status >= 400 {
			r.warnOnce(r.redact(r.rejectedNote(raw, status, body)))
		}
		if r.o.ExerciseHost != "" {
			r.vlogf("exercised %s with the Host %s\n", address, r.o.ExerciseHost)
		} else {
			r.vlogf("exercised %s\n", address)
		}
	}
	if r.o.ExerciseCmd == "" {
		return
	}
	env := append(os.Environ(), "DOCK2NANO_HOST="+host, "DOCK2NANO_PORT="+strconv.Itoa(port))
	run := r.o.RunCommand
	if run == nil {
		run = runShell
	}
	out, err := run(ctx, r.o.ExerciseCmd, env)
	if err != nil {
		r.warnOnce(r.redact("exercise: running the command: " + err.Error()))
		return
	}
	r.vlogf("exercise command: %s\n", strings.TrimSpace(string(out)))
}

// runShell runs command with sh -c and gives up after a minute.
func runShell(ctx context.Context, command string, env []string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	c := exec.CommandContext(cctx, "sh", "-c", command)
	c.Env = env
	return c.CombinedOutput()
}

// httpProber is the default Prober: it connects to the address and asks for the page.
type httpProber struct{}

// ValidHost tells whether host can be the Host header of a request: a host name or address, with
// an optional port, and no path, query, fragment, user name or space.
func ValidHost(host string) bool {
	u, err := url.Parse("http://" + host)
	return err == nil && host != "" && u.Host == host && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil && !strings.ContainsAny(host, " \t")
}

// redirectAddress is the address to reach the container at when a redirect names the host given
// for the Host header: the address of the first request. When the scheme is another (a redirect to
// https), the port of the redirect is kept, as the container does not speak it on the first port.
func redirectAddress(first, target *url.URL) string {
	if first.Scheme == target.Scheme {
		return first.Host
	}
	name := first.Hostname()
	if strings.Contains(name, ":") {
		name = "[" + name + "]"
	}
	if port := target.Port(); port != "" {
		return name + ":" + port
	}
	return name
}

func (httpProber) Get(ctx context.Context, url, host string) (int, []byte, error) {
	if host != "" && !ValidHost(host) {
		return 0, nil, fmt.Errorf("%q is not a host a Host header can hold", host)
	}
	rest := strings.TrimPrefix(url, "http://")
	address, _, _ := strings.Cut(rest, "/")
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return 0, nil, err
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
			if len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			// Go keeps a Host given by hand only for a relative Location. A redirect to the
			// name that was given as Host (an application that redirects to its own domain) is
			// followed to the container and still carries that Host.
			if host != "" && strings.EqualFold(req.URL.Hostname(), hostName(host)) {
				req.URL.Host = redirectAddress(via[0].URL, req.URL)
				req.Host = host
				return nil
			}
			if req.URL.Hostname() != via[0].URL.Hostname() {
				return http.ErrUseLastResponse
			}
			if host != "" {
				req.Host = host
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, nil
	}
	if host != "" {
		req.Host = host
	}
	resp, err := client.Do(req)
	if err != nil {
		if host != "" && strings.Contains(err.Error(), "invalid Host header") {
			return 0, nil, err
		}
		return 0, nil, nil
	}
	limited := io.LimitReader(resp.Body, probeBodyLimit)
	head, _ := io.ReadAll(io.LimitReader(limited, ProbeBodyHead))
	io.Copy(io.Discard, limited)
	resp.Body.Close()
	return resp.StatusCode, head, nil
}

// hostName is the name of a host that ValidHost accepted, without its port.
func hostName(host string) string {
	u, err := url.Parse("http://" + host)
	if err != nil {
		return host
	}
	return u.Hostname()
}

// trimEmpty drops the empty strings at the end of list.
func trimEmpty(list []string) []string {
	for len(list) > 0 && list[len(list)-1] == "" {
		list = list[:len(list)-1]
	}
	return list
}

// startedThroughShebang tells whether the command line in /proc is the one execve was given with
// the kernel's shebang expansion in front: the interpreter, optionally its one argument, then the
// script in place of argv[0], then the rest of the arguments unchanged. The kernel puts the file
// name given to execve (path) there, not argv[0], so the script must be path itself or have its
// name. Anything else (a program that rewrote its own arguments, a launcher) is not taken for a
// shebang. The check that the interpreter is the one of /proc/PID/exe is not made: that link is
// read after the command line, and the interpreter can be reached through a link.
func startedThroughShebang(proc, execve []string, path string) bool {
	if len(execve) == 0 || path == "" {
		return false
	}
	// the interpreter and the script replace argv[0]; the interpreter argument is optional
	lead := len(proc) - len(execve) + 1
	if lead != 2 && lead != 3 {
		return false
	}
	if !slices.Equal(proc[lead:], execve[1:]) || !strings.HasPrefix(proc[0], "/") {
		return false
	}
	script := proc[lead-1]
	return script == path || filepath.Base(script) == filepath.Base(path)
}

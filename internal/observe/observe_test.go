package observe

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/trace"
)

const (
	tracerID = "tracer-1"
	appID    = "app-1"
	probeID  = "probe-1"
	loader   = "ld-musl-x86_64.so.1"
)

// the strace log of the scene: an entrypoint script starts the program (process 11), which opens
// its files. The line "11 execve" is where the final program starts.
const sceneTrace = `10 execve("/entry.sh", ["/entry.sh"], 0x7ffd /* 5 vars */) = 0
10 openat(AT_FDCWD, "/only/the/script", O_RDONLY) = 3
11 execve("/usr/bin/app", ["app", "--serve"], 0x7ffd /* 4 vars */) = 0
11 openat(AT_FDCWD, "/etc/app.conf", O_RDONLY) = 3
11 openat(AT_FDCWD, "/data/missing", O_RDONLY) = -1 ENOENT (No such file or directory)
11 openat(AT_FDCWD, "/proc/self/maps", O_RDONLY) = 4
11 openat(AT_FDCWD, "/lib/libc.so", O_RDONLY) = 5
11 openat(AT_FDCWD, "rel.txt", O_RDONLY) = 6
11 openat(AT_FDCWD, "/var/log/app.log", O_WRONLY) = 7
`

const tcpHeader = "  sl  local_address rem_address   st tx_queue\n"

type entry struct {
	data string
	mode os.FileMode
	link string
	dir  bool
	// zero is a file whose mode is 0000
	zero bool
	// fifo is an entry that is none of file, directory and link
	fifo bool
	// socket makes docker cp write nothing and succeed
	socket bool
	// partial is a file whose stream breaks after 3 of its 10 bytes
	partial bool
}

type fakeDocker struct {
	t *testing.T

	hasImage bool

	cfg *docker.ImageConfig

	traceTxt string

	files map[string]entry

	exec map[string]string

	ip string

	logs string

	startErr map[string]error

	rmErr map[string]error

	onStart func()

	pulled []string

	creates []docker.CreateOptions

	started []string

	stopped []string

	removed []string

	removeCtx []error

	copied []string

	bindDir string

	dirMode fs.FileMode

	listMode fs.FileMode

	listSizeAtStart int64

	// notRunning makes Running answer false for every container.
	notRunning bool

	// waitCode is what the strace container exits with; tracedCode what the traced one exits with.
	waitCode   int
	tracedCode int

	noLoader bool

	noStrace bool

	users []string

	// cpErr makes docker cp of a path fail with this error.
	cpErr map[string]error

	// onTar runs at the start of every CopyTar, with the path.
	onTar func(p string)

	// tarStopped and tarCancelled tell, per path, that the writer of the tar was refused and
	// that its context was cancelled by then.
	tarStopped   map[string]bool
	tarCancelled map[string]bool

	// tailLines are the line counts LogsTail was asked for.
	tailLines []int

	// createErr, when set, is asked for every Create and can fail it.
	createErr func(o docker.CreateOptions) error
}

var containerName = regexp.MustCompile(`^dock2nano-[0-9a-f]{8}$`)

func (f *fakeDocker) Pull(ctx context.Context, image string) error {
	f.pulled = append(f.pulled, image)
	return nil
}

func (f *fakeDocker) HasImage(ctx context.Context, image string) (bool, error) {
	return f.hasImage, nil
}

func (f *fakeDocker) Inspect(ctx context.Context, image string) (*docker.ImageConfig, error) {
	return f.cfg, nil
}

func (f *fakeDocker) Create(ctx context.Context, o docker.CreateOptions) (string, error) {
	f.creates = append(f.creates, o)
	if f.createErr != nil {
		if err := f.createErr(o); err != nil {
			return "", err
		}
	}
	if o.Image == trace.StraceImage {
		return tracerID, nil
	}
	if o.Entrypoint != nil && len(*o.Entrypoint) == 1 && (*o.Entrypoint)[0] == passwdProbeEntrypoint {
		return probeID, nil
	}
	return appID, nil
}

func (f *fakeDocker) Start(ctx context.Context, id string) error {
	f.started = append(f.started, id)
	if err := f.startErr[id]; err != nil {
		return err
	}
	if id == appID {
		bind := f.creates[len(f.creates)-1].Binds[0]
		dir, _, _ := strings.Cut(bind, ":")
		f.bindDir = dir
		if st, err := os.Stat(dir); err == nil {
			f.dirMode = st.Mode().Perm()
		}
		if st, err := os.Stat(filepath.Join(dir, "files")); err == nil {
			f.listMode = st.Mode().Perm()
			f.listSizeAtStart = st.Size()
		}
		if err := os.WriteFile(filepath.Join(dir, "files"), []byte(f.traceTxt), 0666); err != nil {
			f.t.Fatal(err)
		}
		if f.onStart != nil {
			f.onStart()
		}
	}
	return nil
}

func (f *fakeDocker) Stop(ctx context.Context, id string, seconds int) error {
	f.stopped = append(f.stopped, id)
	return nil
}

func (f *fakeDocker) Remove(ctx context.Context, id string, force bool) error {
	f.removed = append(f.removed, id)
	f.removeCtx = append(f.removeCtx, ctx.Err())
	return f.rmErr[id]
}

func (f *fakeDocker) Running(ctx context.Context, id string) (bool, error) {
	return !f.notRunning, nil
}

func (f *fakeDocker) Wait(ctx context.Context, id string) (int, error) {
	if id == tracerID {
		return f.waitCode, nil
	}
	return f.tracedCode, nil
}

func (f *fakeDocker) ExecUser(ctx context.Context, id, user string, cmd []string) ([]byte, error) {
	f.users = append(f.users, user)
	if v, ok := f.exec[strings.Join(cmd, " ")]; ok {
		return []byte(v), nil
	}
	return nil, errors.New("fake exec: no answer for " + strings.Join(cmd, " "))
}

// CopyFrom is only used for the strace container.
func (f *fakeDocker) CopyFrom(ctx context.Context, id, containerPath, hostDir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if id != tracerID {
		f.t.Errorf("CopyFrom of %s %s", id, containerPath)
		return errors.New("unexpected CopyFrom")
	}
	out := filepath.Join(hostDir, "out")
	if err := os.MkdirAll(out, 0755); err != nil {
		return err
	}
	for _, name := range []string{"strace", loader} {
		if (name == "strace" && f.noStrace) || (name == loader && f.noLoader) {
			continue
		}
		if err := os.WriteFile(filepath.Join(out, name), []byte("x"), 0755); err != nil {
			return err
		}
	}
	return nil
}

// CopyTar writes what docker cp id:path - writes: one entry named after the last element.
func (f *fakeDocker) CopyTar(ctx context.Context, id, containerPath string, w io.Writer) error {
	f.copied = append(f.copied, containerPath)
	if f.onTar != nil {
		f.onTar(containerPath)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.cpErr[containerPath]; err != nil {
		return err
	}
	e, ok := f.files[containerPath]
	if !ok {
		return errors.New("docker cp: exit status 1: Error response from daemon: Could not find the file " + containerPath + " in container " + id)
	}
	name := path.Base(containerPath)
	tw := tar.NewWriter(w)
	switch {
	case e.socket:
		return nil
	case e.link != "":
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: name, Linkname: e.link, Mode: 0777}); err != nil {
			return err
		}
	case e.fifo:
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeFifo, Name: name, Mode: 0644}); err != nil {
			return err
		}
	case e.dir:
		// a write that fails after the first header means the reader went away
		refused := func(err error) error {
			if f.tarStopped == nil {
				f.tarStopped = map[string]bool{}
				f.tarCancelled = map[string]bool{}
			}
			f.tarStopped[containerPath] = true
			f.tarCancelled[containerPath] = ctx.Err() != nil
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: 0755}); err != nil {
			return err
		}
		// what is in the directory: more than the pipe holds, so the writer blocks until the
		// reader goes away
		const size = 8 << 20
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name + "/big", Mode: 0644, Size: size}); err != nil {
			return refused(err)
		}
		chunk := make([]byte, 64<<10)
		for n := 0; n < size; n += len(chunk) {
			if _, err := tw.Write(chunk); err != nil {
				return refused(err)
			}
		}
	case e.partial:
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0644, Size: 10}); err != nil {
			return err
		}
		if _, err := tw.Write([]byte("abc")); err != nil {
			return err
		}
		return errors.New("connection lost")
	default:
		mode := e.mode
		if mode == 0 && !e.zero {
			mode = 0644
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: int64(mode), Size: int64(len(e.data))}); err != nil {
			return err
		}
		if _, err := tw.Write([]byte(e.data)); err != nil {
			return err
		}
	}
	return tw.Close()
}

func (f *fakeDocker) LogsTail(ctx context.Context, id string, lines int) ([]byte, error) {
	f.tailLines = append(f.tailLines, lines)
	all := strings.Split(strings.TrimRight(f.logs, "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return []byte(strings.Join(all, "\n")), nil
}

func (f *fakeDocker) IPAddress(ctx context.Context, id string) (string, error) {
	return f.ip, nil
}

type fakeClock struct {
	t      time.Time
	sleeps int
}

func (c *fakeClock) now() time.Time { return c.t }

func (c *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.t = c.t.Add(d)
	c.sleeps++
	return nil
}

type fakeProber struct {
	urls []string
}

func (p *fakeProber) Get(ctx context.Context, url string) error {
	p.urls = append(p.urls, url)
	return nil
}

type scene struct {
	d     *fakeDocker
	clock *fakeClock
	probe *fakeProber
	o     Options
	start time.Time
	tmp   string
}

func newScene(t *testing.T) *scene {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := &fakeClock{t: start}
	probe := &fakeProber{}
	d := &fakeDocker{t: t}
	d.cfg = &docker.ImageConfig{ID: "sha256:abc", Entrypoint: []string{"/entry.sh"}, Cmd: []string{"--serve"}, WorkingDir: "/app", Env: []string{"PATH=/usr/bin", "HOME=/root"}, OS: "linux"}
	d.traceTxt = sceneTrace
	d.files = map[string]entry{}
	d.files["/etc/app.conf"] = entry{data: "port=80\n"}
	d.files["/lib/libc.so"] = entry{link: "libc.so.6"}
	d.files["/lib/libc.so.6"] = entry{data: "libc", mode: 0755}
	d.files["/app/rel.txt"] = entry{data: "relative"}
	d.files["/usr/bin/app"] = entry{data: "#!/bin/sh\necho hi\n", mode: 0755}
	d.files["/var/log/app.log"] = entry{link: "/dev/stderr"}
	d.files["/bin/sh"] = entry{data: "shell", mode: 0755}
	d.files["/only/the/script"] = entry{data: "must not be copied"}
	d.exec = map[string]string{}
	d.exec["cat /proc/net/tcp"] = tcpHeader
	d.exec["cat /proc/net/tcp6"] = tcpHeader
	d.exec["cat /proc/11/cmdline"] = "app\x00--serve\x00"
	d.exec["cat /proc/11/environ"] = "A=1\x00HOSTNAME=abc\x00PATH=/usr/bin\x00_=/x\x00"
	d.exec["readlink /proc/11/cwd"] = "/srv\n"
	d.exec["readlink /proc/11/exe"] = "/usr/bin/app\n"
	d.exec["cat /proc/11/status"] = statusText(999, 999)
	d.ip = "172.17.0.2"
	o := Options{Image: "img:1", Pull: true, Probe: probe, now: clock.now, sleep: clock.sleep}
	return &scene{d: d, clock: clock, probe: probe, o: o, start: start, tmp: tmp}
}

func (s *scene) observe(t *testing.T, ctx context.Context) (*Result, error) {
	t.Helper()
	return Observe(ctx, s.d, s.o)
}

// tree lists the files and links under dir, as slash paths relative to it.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func equal(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %#v\nwant %#v", what, got, want)
	}
}

func TestObserveSuccess(t *testing.T) {
	s := newScene(t)
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}

	equal(t, "pulled", s.d.pulled, []string{"img:1", trace.StraceImage})

	// the traced container, exactly
	if len(s.d.creates) != 2 {
		t.Fatalf("created %d containers, want 2", len(s.d.creates))
	}
	equal(t, "tracer container image", s.d.creates[0].Image, trace.StraceImage)
	equal(t, "tracer container cmd", s.d.creates[0].Cmd[:2], []string{"sh", "-c"})
	wrapped := []string{
		"/.ops-trace/out/" + loader, "--library-path", "/.ops-trace/out", "/.ops-trace/out/strace",
		"-f", "-q", "-s", "4096", "-e", "trace=" + trace.TraceSet, "-e", "signal=none",
		"-o", "/.ops-trace/files", "--",
		"/entry.sh", "--serve",
	}
	if !containerName.MatchString(s.d.creates[1].Name) || s.d.creates[0].Name == s.d.creates[1].Name {
		t.Errorf("container names %q and %q", s.d.creates[0].Name, s.d.creates[1].Name)
	}
	equal(t, "tracer container labels", s.d.creates[0].Labels, []string{"dock2nano=1"})
	want := docker.CreateOptions{
		Name:        s.d.creates[1].Name,
		Labels:      []string{"dock2nano=1"},
		Image:       "img:1",
		Entrypoint:  &wrapped,
		User:        "0",
		Binds:       []string{s.d.bindDir + ":/.ops-trace", s.d.bindDir + "/out:/.ops-trace/out:ro"},
		CapAdd:      []string{"SYS_PTRACE"},
		SecurityOpt: []string{"seccomp=unconfined"},
	}
	equal(t, "traced container options", s.d.creates[1], want)
	if s.d.bindDir == "" {
		t.Fatal("the traced container was never started")
	}

	// the tracer directory as the program sees it
	equal(t, "tracer dir mode", s.d.dirMode, fs.FileMode(0700))
	equal(t, "trace file mode", s.d.listMode, fs.FileMode(0600))
	equal(t, "trace file size at start", s.d.listSizeAtStart, int64(0))
	if _, err := os.Stat(s.d.bindDir); !os.IsNotExist(err) {
		t.Errorf("the tracer directory is still there: %v", err)
	}

	// both containers are removed, the traced one stopped first, with a context that was alive
	equal(t, "started", s.d.started, []string{tracerID, appID})
	equal(t, "stopped", s.d.stopped, []string{appID})
	equal(t, "removed", s.d.removed, []string{tracerID, appID})

	// only the files of the trace after the final program started, plus the interpreter
	equal(t, "copied", s.d.copied, []string{
		"/app/rel.txt", "/etc/app.conf", "/lib/libc.so", "/usr/bin/app", "/var/log/app.log",
		"/lib/libc.so.6", "/bin/sh",
	})
	equal(t, "sysroot files", tree(t, res.Sysroot), []string{
		"app/rel.txt", "bin/sh", "etc/app.conf", "lib/libc.so", "lib/libc.so.6", "usr/bin/app", "var/log/app.log",
	})
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(res.Sysroot, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	equal(t, "alias is a copy of the link target", read("lib/libc.so"), "libc")
	equal(t, "console link is empty", read("var/log/app.log"), "")
	equal(t, "relative path read against the start directory of the container", read("app/rel.txt"), "relative")
	if st, err := os.Lstat(filepath.Join(res.Sysroot, "lib/libc.so")); err != nil || !st.Mode().IsRegular() {
		t.Errorf("lib/libc.so is not a regular file: %v %v", st, err)
	}

	// the program and its command line come from /proc
	equal(t, "program", res.Program, "/usr/bin/app")
	equal(t, "argv", res.Argv, []string{"app", "--serve"})
	equal(t, "env", res.Env, []string{"A=1", "PATH=/usr/bin"})
	equal(t, "created at run time", res.CreatedAtRuntime, []string(nil))
	equal(t, "children", res.Children, 0)
	equal(t, "child calls", res.ChildCalls, []string(nil))
	// every read of /proc and of the tables is made as root
	for _, u := range s.d.users {
		if u != "root" {
			t.Errorf("exec as %q, want root", u)
		}
	}
	if len(s.d.users) == 0 {
		t.Error("nothing was read through exec")
	}
	// the modes of the carried files: exec bits stay, owner read and write are added
	for name, want := range map[string]fs.FileMode{"usr/bin/app": 0755, "lib/libc.so.6": 0755, "etc/app.conf": 0644} {
		st, err := os.Stat(filepath.Join(res.Sysroot, name))
		if err != nil {
			t.Fatal(err)
		}
		equal(t, "mode of "+name, st.Mode().Perm(), want)
	}
	equal(t, "working dir", res.WorkingDir, "/srv")
	equal(t, "configuration files", res.ConfigurationFiles, []string{"/etc/app.conf"})
	equal(t, "image", res.Image, s.d.cfg)
	equal(t, "exposed ports", res.ExposedPorts, []int(nil))
	equal(t, "warnings", res.Warnings, []string(nil))

	// the sysroot is the only thing left in the temporary directory
	entries, _ := os.ReadDir(s.tmp)
	if len(entries) != 1 || filepath.Join(s.tmp, entries[0].Name()) != res.Sysroot {
		t.Errorf("temporary directory holds %v, want only the sysroot %s", entries, res.Sysroot)
	}
}

func TestObserveProgramFromProcExe(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt += `11 openat(AT_FDCWD, "/usr/bin/app-real", O_RDONLY) = 8` + "\n"
	s.d.files["/usr/bin/app-real"] = entry{data: "real", mode: 0755}
	s.d.exec["readlink /proc/11/exe"] = "/usr/bin/app-real\n"
	s.d.exec["cat /proc/11/cmdline"] = "app\x00-v\x00"
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// the file is the one /proc says, the command line the one execve was given
	equal(t, "program", res.Program, "/usr/bin/app-real")
	equal(t, "argv", res.Argv, []string{"app", "--serve"})
	equal(t, "warnings", res.Warnings, []string{
		`the command line in /proc ["app" "-v"] differs from the one execve was given ["app" "--serve"]: the one execve was given is used`,
	})
}

func TestObserveNginxRewritesItsCommandLine(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt = strings.Replace(sceneTrace, `"/usr/bin/app", ["app", "--serve"]`, `"/usr/sbin/nginx", ["nginx", "-g", "daemon off;"]`, 1)
	s.d.files["/usr/sbin/nginx"] = entry{data: "nginx", mode: 0755}
	s.d.exec["cat /proc/11/cmdline"] = "nginx: master process nginx -g daemon off;\x00\x00\x00"
	s.d.exec["readlink /proc/11/exe"] = "/usr/sbin/nginx\n"
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "program", res.Program, "/usr/sbin/nginx")
	equal(t, "argv", res.Argv, []string{"nginx", "-g", "daemon off;"})
	equal(t, "warnings", res.Warnings, []string{
		`the command line in /proc ["nginx: master process nginx -g daemon off;"] differs from the one execve was given ["nginx" "-g" "daemon off;"]: the one execve was given is used`,
	})
}

func TestObserveUserOfTheImageRunsTheProgram(t *testing.T) {
	for _, tc := range []struct {
		user string
		want []string
	}{
		{"app:staff", []string{"-u", "app", "--"}},
		{"app", []string{"-u", "app", "--"}},
		{"", []string{"--"}},
		{"root", []string{"--"}},
		{"0", []string{"--"}},
		{"0:0", []string{"--"}},
	} {
		s := newScene(t)
		s.d.cfg.User = tc.user
		if _, err := s.observe(t, context.Background()); err != nil {
			t.Fatal(err)
		}
		wrapped := trace.WrapCommandAs(trace.TraceMount, loader, strings.Split(tc.user, ":")[0], []string{"/entry.sh", "--serve"})
		if tc.want[0] == "--" {
			wrapped = trace.WrapCommandAs(trace.TraceMount, loader, "", []string{"/entry.sh", "--serve"})
		}
		got := s.d.creates[1]
		equal(t, "entrypoint for user "+tc.user, *got.Entrypoint, wrapped)
		equal(t, "container user for "+tc.user, got.User, "0")
		if tc.want[0] == "-u" && !strings.Contains(strings.Join(*got.Entrypoint, " "), " -u app -- ") {
			t.Errorf("user %q: no -u app in %q", tc.user, *got.Entrypoint)
		}
		if tc.want[0] == "--" && slices.Contains(*got.Entrypoint, "-u") {
			t.Errorf("user %q: -u in %q", tc.user, *got.Entrypoint)
		}
	}
}

func TestObserveTargetExecutableAndArgs(t *testing.T) {
	s := newScene(t)
	s.o.TargetExecutable = "/usr/bin/app"
	s.o.Args = []string{"-x", "1"}
	s.o.Env = []string{"K=V"}
	s.o.WorkingDir = "/w"
	if _, err := s.observe(t, context.Background()); err != nil {
		t.Fatal(err)
	}
	got := *s.d.creates[1].Entrypoint
	equal(t, "wrapped program", got[len(got)-3:], []string{"/usr/bin/app", "-x", "1"})
	equal(t, "env", s.d.creates[1].Env, []string{"K=V"})
	equal(t, "working dir", s.d.creates[1].WorkingDir, "/w")
}

// wrappedArgv returns the program argv inside the entrypoint of the traced container.
func wrappedArgv(t *testing.T, s *scene, argv []string) []string {
	t.Helper()
	return trace.WrapCommandAs(trace.TraceMount, loader, "", argv)
}

func TestObserveCommandOverrides(t *testing.T) {
	empty := []string{}
	for _, tc := range []struct {
		name       string
		entrypoint *[]string
		cmd        []string
		args       []string
		want       []string
	}{
		{"image entrypoint and cmd", nil, nil, nil, []string{"/entry.sh", "--serve"}},
		{"entrypoint replaces and drops the image cmd", &[]string{"/usr/bin/app"}, nil, nil, []string{"/usr/bin/app"}},
		{"empty entrypoint clears it and drops the image cmd", &empty, []string{"/usr/bin/app", "-x"}, nil, []string{"/usr/bin/app", "-x"}},
		{"cmd replaces the image cmd, entrypoint kept", nil, []string{"--other"}, nil, []string{"/entry.sh", "--other"}},
		{"entrypoint and cmd both set", &[]string{"/usr/bin/app", "-a"}, []string{"-b"}, nil, []string{"/usr/bin/app", "-a", "-b"}},
		{"args last", &[]string{"/usr/bin/app"}, []string{"-b"}, []string{"-z"}, []string{"/usr/bin/app", "-b", "-z"}},
		{"args last after the image command", nil, nil, []string{"-z"}, []string{"/entry.sh", "--serve", "-z"}},
	} {
		s := newScene(t)
		s.o.Entrypoint = tc.entrypoint
		s.o.Cmd = tc.cmd
		s.o.Args = tc.args
		_, err := s.observe(t, context.Background())
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		equal(t, tc.name+": traced entrypoint", *s.d.creates[1].Entrypoint, wrappedArgv(t, s, tc.want))
	}
}

func TestObserveEmptyEntrypointWithoutCmdStartsNothing(t *testing.T) {
	s := newScene(t)
	s.o.Entrypoint = &[]string{}
	if _, err := s.observe(t, context.Background()); err == nil || !strings.Contains(err.Error(), "starts nothing") {
		t.Fatalf("err = %v, want it to say the image starts nothing", err)
	}
}

func TestObserveEnvReplacesTheVariableOfTheImage(t *testing.T) {
	got := mergeEnv([]string{"PATH=/usr/bin", "HOME=/root"}, []string{"HOME=/h", "NEW=1"})
	equal(t, "merged env", got, []string{"PATH=/usr/bin", "HOME=/h", "NEW=1"})

	s := newScene(t)
	s.o.Env = []string{"HOME=/h", "NEW=1"}
	if _, err := s.observe(t, context.Background()); err != nil {
		t.Fatal(err)
	}
	// docker create applies the same rule: it is given only the entries of the options
	equal(t, "env given to create", s.d.creates[1].Env, []string{"HOME=/h", "NEW=1"})
}

func TestObserveTimeoutEndsTheWatch(t *testing.T) {
	s := newScene(t)
	s.o.Seconds = 7
	if _, err := s.observe(t, context.Background()); err != nil {
		t.Fatal(err)
	}
	// polls at 0, 2, 4, 6 seconds; at 8 the time is up
	equal(t, "sleeps", s.clock.sleeps, 4)
	equal(t, "clock", s.clock.t, s.start.Add(8*time.Second))
}

func TestObserveDefaultSeconds(t *testing.T) {
	s := newScene(t)
	if _, err := s.observe(t, context.Background()); err != nil {
		t.Fatal(err)
	}
	equal(t, "clock", s.clock.t, s.start.Add(30*time.Second))
}

func TestObserveProbesEveryPortOnceAndStopsEarly(t *testing.T) {
	s := newScene(t)
	s.d.cfg.ExposedPorts = []string{"80/tcp", "53/udp", "8080/tcp"}
	// 0x1F90 is 8080: listening, and also exposed, so asked once
	s.d.exec["cat /proc/net/tcp"] = tcpHeader + "   0: 00000000:1F90 00000000:0000 0A 00000000:00000000\n" +
		"   1: 00000000:0016 00000000:0000 01 00000000:00000000\n"
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "urls", s.probe.urls, []string{"http://172.17.0.2:8080/", "http://172.17.0.2:80/"})
	equal(t, "exposed ports", res.ExposedPorts, []int{80, 8080})
	// asked at 0s, the list stopped growing at 0s: the watch ends at the first poll after 10s
	equal(t, "clock", s.clock.t, s.start.Add(12*time.Second))
}

func TestObserveWithoutAddressWarns(t *testing.T) {
	s := newScene(t)
	s.d.cfg.ExposedPorts = []string{"80/tcp"}
	s.o.Seconds = 3
	s.d.ip = ""
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "urls", s.probe.urls, []string(nil))
	equal(t, "warnings", len(res.Warnings), 2)
	if !strings.Contains(res.Warnings[0], "no address") {
		t.Errorf("warnings = %q", res.Warnings)
	}
	equal(t, "port warning", res.Warnings[1], "port 80 could not be asked for /: no address for the container")
}

func TestObserveStartFailureRemovesBoth(t *testing.T) {
	s := newScene(t)
	s.d.startErr = map[string]error{appID: errors.New("boom")}
	res, err := s.observe(t, context.Background())
	if err == nil || res != nil {
		t.Fatalf("res = %v, err = %v", res, err)
	}
	if !strings.Contains(err.Error(), "starting the traced container") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v", err)
	}
	equal(t, "removed", s.d.removed, []string{tracerID, appID})
	equal(t, "stopped", s.d.stopped, []string{appID})
	entries, _ := os.ReadDir(s.tmp)
	if len(entries) != 0 {
		t.Errorf("temporary directory still holds %v", entries)
	}
}

func TestObserveTracerStartFailure(t *testing.T) {
	s := newScene(t)
	s.d.startErr = map[string]error{tracerID: errors.New("no strace")}
	_, err := s.observe(t, context.Background())
	if err == nil || !strings.Contains(err.Error(), "starting the strace container") {
		t.Fatalf("err = %v", err)
	}
	equal(t, "removed", s.d.removed, []string{tracerID})
	entries, _ := os.ReadDir(s.tmp)
	if len(entries) != 0 {
		t.Errorf("temporary directory still holds %v", entries)
	}
}

func TestObserveCancelledContextCleansUpWithFreshContext(t *testing.T) {
	s := newScene(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.d.onStart = cancel
	res, err := s.observe(t, ctx)
	if res != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("res = %v, err = %v", res, err)
	}
	if !strings.Contains(err.Error(), "watching the traced container") {
		t.Errorf("error does not name the step: %v", err)
	}
	equal(t, "removed", s.d.removed, []string{tracerID, appID})
	// ctx was cancelled, the contexts used for removal were not
	equal(t, "context errors at removal", s.d.removeCtx, []error{nil, nil})
	equal(t, "stopped", s.d.stopped, []string{appID})
	entries, _ := os.ReadDir(s.tmp)
	if len(entries) != 0 {
		t.Errorf("temporary directory still holds %v", entries)
	}
}

func TestObserveNoProgramStarted(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt = ""
	s.d.logs = "exec format error"
	_, err := s.observe(t, context.Background())
	if err == nil || !strings.Contains(err.Error(), "started no program") || !strings.Contains(err.Error(), "exec format error") {
		t.Fatalf("err = %v", err)
	}
	equal(t, "removed", s.d.removed, []string{tracerID, appID})
}

func TestObserveCleanupErrorIsAWarning(t *testing.T) {
	s := newScene(t)
	s.d.rmErr = map[string]error{appID: errors.New("busy")}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "removing container app-1") || !strings.Contains(res.Warnings[0], "busy") {
		t.Errorf("warnings = %q", res.Warnings)
	}
}

func TestObserveCleanupErrorJoinsTheError(t *testing.T) {
	s := newScene(t)
	s.d.startErr = map[string]error{appID: errors.New("boom")}
	s.d.rmErr = map[string]error{appID: errors.New("busy")}
	_, err := s.observe(t, context.Background())
	if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("err = %v", err)
	}
}

func TestObserveMissingFileIsAWarning(t *testing.T) {
	s := newScene(t)
	delete(s.d.files, "/app/rel.txt")
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], "not carried /app/rel.txt") {
		t.Errorf("warnings = %q", res.Warnings)
	}
}

func TestObserveImageMissingWithoutPull(t *testing.T) {
	s := newScene(t)
	s.o.Pull = false
	_, err := s.observe(t, context.Background())
	if err == nil || !strings.Contains(err.Error(), "pulling is off") {
		t.Fatalf("err = %v", err)
	}
	equal(t, "pulled", s.d.pulled, []string(nil))
	equal(t, "created", len(s.d.creates), 0)
}

func TestObserveNothingIsPulledWhenNothingIsMissing(t *testing.T) {
	s := newScene(t)
	s.d.hasImage = true
	s.o.Pull = false
	if _, err := s.observe(t, context.Background()); err != nil {
		t.Fatal(err)
	}
	equal(t, "pulled", s.d.pulled, []string(nil))
}

func TestObserveImageOnThisMachineIsNotPulled(t *testing.T) {
	s := newScene(t)
	s.d.hasImage = true
	if _, err := s.observe(t, context.Background()); err != nil {
		t.Fatal(err)
	}
	equal(t, "pulled", s.d.pulled, []string{trace.StraceImage})
}

func TestObserveRefusesOtherSystems(t *testing.T) {
	s := newScene(t)
	s.d.cfg.OS = "windows"
	_, err := s.observe(t, context.Background())
	if err == nil || !strings.Contains(err.Error(), "windows") {
		t.Fatalf("err = %v", err)
	}
	equal(t, "created", len(s.d.creates), 0)
}

func TestObserveImageStartsNothing(t *testing.T) {
	s := newScene(t)
	s.d.cfg.Entrypoint = nil
	s.d.cfg.Cmd = nil
	_, err := s.observe(t, context.Background())
	if err == nil || !strings.Contains(err.Error(), "starts nothing") {
		t.Fatalf("err = %v", err)
	}
}

func (s *scene) sysrootNow(t *testing.T) string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(s.tmp, "dock2nano-sysroot-*"))
	if len(m) != 1 {
		// not Fatalf: this runs in the goroutine that streams the tar
		t.Errorf("sysroots in the temporary directory: %v", m)
		return ""
	}
	return m[0]
}

func TestObserveCreatedAtRuntimeIsReported(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt += `11 openat(AT_FDCWD, "/etc/generated.conf", O_WRONLY|O_CREAT|O_TRUNC, 0644) = 8` + "\n"
	s.d.files["/etc/generated.conf"] = entry{data: "made"}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "created", res.CreatedAtRuntime, []string{"/etc/generated.conf"})
	equal(t, "warnings", res.Warnings, []string{
		"the program created these files while it ran; they are carried as they were at the end of the observation, and a fresh start may not make them: /etc/generated.conf",
	})
	equal(t, "carried", slices.Contains(tree(t, res.Sysroot), "etc/generated.conf"), true)
}

func TestObserveCreatedAndGoneIsCountedNotWarned(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt += `11 openat(AT_FDCWD, "/tmp/x.pyc.123", O_WRONLY|O_CREAT|O_EXCL, 0644) = 8` + "\n" +
		`11 openat(AT_FDCWD, "/tmp/y.pyc.456", O_WRONLY|O_CREAT|O_EXCL, 0644) = 9` + "\n"
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "vanished", res.Vanished, 2)
	equal(t, "created", res.CreatedAtRuntime, []string(nil))
	equal(t, "warnings", res.Warnings, []string(nil))
}

func TestObserveGoneButNotCreatedStillWarns(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt += `11 openat(AT_FDCWD, "/tmp/read.txt", O_RDONLY) = 8` + "\n"
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "vanished", res.Vanished, 0)
	if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], "not carried /tmp/read.txt") {
		t.Errorf("warnings = %q", res.Warnings)
	}
}

func TestObserveOtherCopyErrorOfCreatedFileStillWarns(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt += `11 openat(AT_FDCWD, "/tmp/z.log", O_WRONLY|O_CREAT, 0644) = 8` + "\n"
	s.d.files["/tmp/z.log"] = entry{data: "x"}
	s.d.cpErr = map[string]error{"/tmp/z.log": errors.New("docker cp: exit status 1: permission denied")}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "vanished", res.Vanished, 0)
	equal(t, "warnings", res.Warnings, []string{
		"not carried /tmp/z.log: docker cp: exit status 1: permission denied",
		"the program created these files while it ran; they are carried as they were at the end of the observation, and a fresh start may not make them: /tmp/z.log",
	})
}

func TestObserveNotCarriedWarningsAreAggregated(t *testing.T) {
	s := newScene(t)
	for i := 0; i < 25; i++ {
		s.d.traceTxt += fmt.Sprintf(`11 openat(AT_FDCWD, "/gone/f%02d", O_RDONLY) = 8`+"\n", i)
	}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 11 {
		t.Fatalf("got %d warnings: %q", len(res.Warnings), res.Warnings)
	}
	for i := 0; i < 10; i++ {
		if !strings.HasPrefix(res.Warnings[i], fmt.Sprintf("not carried /gone/f%02d: ", i)) {
			t.Errorf("warning %d = %q", i, res.Warnings[i])
		}
	}
	equal(t, "last", res.Warnings[10], "and 15 more paths were not carried")
}

func TestObserveCreatedWarningListsTenPaths(t *testing.T) {
	s := newScene(t)
	var want []string
	for i := 0; i < 12; i++ {
		p := fmt.Sprintf("/var/c%02d", i)
		want = append(want, p)
		s.d.traceTxt += fmt.Sprintf(`11 openat(AT_FDCWD, "%s", O_WRONLY|O_CREAT, 0644) = 8`+"\n", p)
		s.d.files[p] = entry{data: "x"}
	}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "created", res.CreatedAtRuntime, want)
	equal(t, "warnings", res.Warnings, []string{
		"the program created these files while it ran; they are carried as they were at the end of the observation, and a fresh start may not make them: " + strings.Join(want[:10], ", ") + " and 2 more",
	})
}

func TestObserveCountsChildProcesses(t *testing.T) {
	s := newScene(t)
	// two forks, a vfork and a clone3 that makes a process, one that makes a thread and a fork that
	// failed; a fork of the entrypoint script (process 10) comes before the program and is not
	// the program's
	s.d.traceTxt = strings.Replace(sceneTrace, "11 execve(", "10 fork() = 20\n11 execve(", 1) +
		"11 fork() = 12\n" +
		"11 fork( <unfinished ...>\n" +
		"11 <... fork resumed>) = 13\n" +
		"11 clone3({flags=CLONE_VM|CLONE_THREAD, exit_signal=0}, 88) = 14\n" +
		"11 fork() = -1 EAGAIN (Resource temporarily unavailable)\n" +
		"12 vfork() = 15\n" +
		"11 clone3({flags=CLONE_VM|CLONE_VFORK, exit_signal=SIGCHLD}, 88) = 16\n"
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "children", res.Children, 4)
	equal(t, "child calls", res.ChildCalls, []string{"fork", "vfork", "clone3"})
	equal(t, "warnings", res.Warnings, []string(nil))
}

func TestObserveKeepTrace(t *testing.T) {
	s := newScene(t)
	s.o.KeepTrace = filepath.Join(t.TempDir(), "trace.txt")
	if _, err := s.observe(t, context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(s.o.KeepTrace)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "kept trace", string(got), sceneTrace)

	s = newScene(t)
	s.o.KeepTrace = filepath.Join(t.TempDir(), "no", "such", "dir", "trace.txt")
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], "could not keep the trace in "+s.o.KeepTrace+": ") {
		t.Errorf("warnings = %q", res.Warnings)
	}
}

func TestObserveSocketIsNotCarried(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt += `11 openat(AT_FDCWD, "/run/x.sock", O_RDONLY) = 8` + "\n"
	s.d.files["/run/x.sock"] = entry{socket: true}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "warnings", res.Warnings, []string{"not carried /run/x.sock: docker cp made nothing of it (a socket, for one)"})
}

func TestObserveOtherEntryIsNotCarried(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt += `11 openat(AT_FDCWD, "/data/pipe", O_RDONLY) = 8` + "\n"
	s.d.files["/data/pipe"] = entry{fifo: true}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "warnings", res.Warnings, []string{"not carried /data/pipe: it is not a file, a directory or a link"})
}

func TestObserveDirectoryStopsTheStream(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt += `11 openat(AT_FDCWD, "/var/lib/dir", O_RDONLY|O_DIRECTORY) = 8` + "\n"
	s.d.files["/var/lib/dir"] = entry{dir: true}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(res.Sysroot, "var/lib/dir"))
	if err != nil || !st.IsDir() {
		t.Fatalf("the directory is not in the sysroot: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(res.Sysroot, "var/lib/dir")); len(entries) != 0 {
		t.Errorf("the directory is not empty: %v", entries)
	}
	// the writer of the tar was stopped, and its context was cancelled by then
	equal(t, "stopped", s.d.tarStopped, map[string]bool{"/var/lib/dir": true})
	equal(t, "cancelled", s.d.tarCancelled, map[string]bool{"/var/lib/dir": true})
	equal(t, "warnings", res.Warnings, []string(nil))
}

func TestObserveModes(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt += `11 openat(AT_FDCWD, "/etc/locked", O_RDONLY) = 8` + "\n"
	s.d.files["/etc/locked"] = entry{data: "secret", zero: true}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(res.Sysroot, "etc/locked"))
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "mode of a 0000 file", st.Mode().Perm(), fs.FileMode(0600))
}

func TestObserveLinkToRootIsSkipped(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt += `11 openat(AT_FDCWD, "/lnk", O_RDONLY) = 8` + "\n"
	s.d.files["/lnk"] = entry{link: "/"}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(s.d.copied, "/") {
		t.Errorf("/ was asked for: %v", s.d.copied)
	}
	equal(t, "warnings", res.Warnings, []string(nil))
}

func TestObserveHalfWrittenFileIsRemoved(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt += `11 openat(AT_FDCWD, "/etc/half", O_RDONLY) = 8` + "\n"
	s.d.files["/etc/half"] = entry{partial: true}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "warnings", res.Warnings, []string{"not carried /etc/half: connection lost"})
	if _, err := os.Lstat(filepath.Join(res.Sysroot, "etc/half")); !os.IsNotExist(err) {
		t.Errorf("the half-written file is still there: %v", err)
	}
}

func TestObserveHostFailureIsAnError(t *testing.T) {
	s := newScene(t)
	s.d.onTar = func(p string) {
		if p == "/etc/app.conf" {
			// a file where the directory has to go
			if err := os.WriteFile(filepath.Join(s.sysrootNow(t), "etc"), nil, 0600); err != nil {
				t.Error(err)
			}
		}
	}
	res, err := s.observe(t, context.Background())
	if err == nil || res != nil {
		t.Fatalf("res = %v, err = %v", res, err)
	}
	if !strings.Contains(err.Error(), "copying /etc/app.conf out of the container: making the directory of /etc/app.conf") {
		t.Errorf("err = %v", err)
	}
	equal(t, "removed", s.d.removed, []string{tracerID, appID})
	entries, _ := os.ReadDir(s.tmp)
	if len(entries) != 0 {
		t.Errorf("temporary directory still holds %v", entries)
	}
}

func TestObserveCancelWhileCopying(t *testing.T) {
	s := newScene(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.d.onTar = func(p string) {
		if p == "/etc/app.conf" {
			cancel()
		}
	}
	res, err := s.observe(t, ctx)
	if res != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("res = %v, err = %v", res, err)
	}
	if !strings.Contains(err.Error(), "copying /etc/app.conf out of the container") {
		t.Errorf("err = %v", err)
	}
	equal(t, "removed", s.d.removed, []string{tracerID, appID})
	entries, _ := os.ReadDir(s.tmp)
	if len(entries) != 0 {
		t.Errorf("temporary directory still holds %v", entries)
	}
}

func TestObserveStoppedBeforeProcCouldBeRead(t *testing.T) {
	s := newScene(t)
	s.d.notRunning = true
	s.d.files["/app/rel.txt"] = entry{data: "relative"}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "sleeps", s.clock.sleeps, 0)
	equal(t, "warnings", res.Warnings, []string{
		"the container stopped with status 0 before the end of the observation; the program chosen is the last one it started, not necessarily a server",
		"the program had stopped before /proc could be read: the command line, environment and directory are those of the image",
	})
	equal(t, "program", res.Program, "/usr/bin/app")
	equal(t, "argv", res.Argv, []string{"app", "--serve"})
	equal(t, "working dir", res.WorkingDir, "/app")
	equal(t, "env", res.Env, []string{"PATH=/usr/bin", "HOME=/root"})
	equal(t, "users", s.d.users, []string(nil))
}

func statusText(uid, gid int) string {
	return fmt.Sprintf("Name:\tmysqld\nUmask:\t0022\nState:\tS (sleeping)\nPid:\t11\nUid:\t%d\t%d\t%d\t%d\nGid:\t%d\t%d\t%d\t%d\nGroups:\t\n", uid, uid, uid, uid, gid, gid, gid, gid)
}

func TestObserveUserAndGroup(t *testing.T) {
	s := newScene(t)
	s.d.files["/app/rel.txt"] = entry{data: "relative"}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "uid", res.UID, 999)
	equal(t, "gid", res.GID, 999)
	equal(t, "warnings", res.Warnings, []string(nil))
}

func TestObserveUserIsEffectiveColumn(t *testing.T) {
	s := newScene(t)
	s.d.exec["cat /proc/11/status"] = "Name:\tx\nUid:\t1\t2\t3\t4\nGid:\t5\t6\t7\t8\n"
	s.d.files["/app/rel.txt"] = entry{data: "relative"}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "uid", res.UID, 2)
	equal(t, "gid", res.GID, 6)
}

func TestObserveUserRoot(t *testing.T) {
	s := newScene(t)
	s.d.exec["cat /proc/11/status"] = statusText(0, 0)
	s.d.files["/app/rel.txt"] = entry{data: "relative"}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "uid", res.UID, 0)
	equal(t, "gid", res.GID, 0)
}

func TestObserveUserUnparsable(t *testing.T) {
	s := newScene(t)
	s.d.exec["cat /proc/11/status"] = "Name:\tx\nUid:\tabc\tdef\nGid:\t1\t1\t1\t1\n"
	s.d.files["/app/rel.txt"] = entry{data: "relative"}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "uid", res.UID, -1)
	equal(t, "gid", res.GID, -1)
	equal(t, "warnings", res.Warnings, []string{"could not read the user and group of the program from /proc: they are not known"})
}

func TestObserveUserUnreadable(t *testing.T) {
	s := newScene(t)
	delete(s.d.exec, "cat /proc/11/status")
	s.d.files["/app/rel.txt"] = entry{data: "relative"}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "uid", res.UID, -1)
	equal(t, "gid", res.GID, -1)
	equal(t, "warnings", res.Warnings, []string{"could not read the user and group of the program: fake exec: no answer for cat /proc/11/status"})
}

func TestObserveUserProgramGone(t *testing.T) {
	s := newScene(t)
	s.d.notRunning = true
	s.d.files["/app/rel.txt"] = entry{data: "relative"}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "uid", res.UID, -1)
	equal(t, "gid", res.GID, -1)
}

func TestObserveProcReadFailuresAreWarnings(t *testing.T) {
	s := newScene(t)
	for _, k := range []string{"cat /proc/11/cmdline", "cat /proc/11/environ", "readlink /proc/11/cwd", "readlink /proc/11/exe"} {
		delete(s.d.exec, k)
	}
	s.d.files["/app/rel.txt"] = entry{data: "relative"}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "warnings", res.Warnings, []string{
		"could not read the command line of the program: fake exec: no answer for cat /proc/11/cmdline",
		"could not read the environment of the program: fake exec: no answer for cat /proc/11/environ",
		"could not read the working directory of the program: fake exec: no answer for readlink /proc/11/cwd",
		"could not read the executable of the program: fake exec: no answer for readlink /proc/11/exe",
	})
	equal(t, "argv", res.Argv, []string{"app", "--serve"})
	equal(t, "working dir", res.WorkingDir, "/app")
	equal(t, "env", res.Env, []string{"PATH=/usr/bin", "HOME=/root"})
}

func TestObservePrepareTracerFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(d *fakeDocker)
		want  string
	}{
		{"exit code", func(d *fakeDocker) { d.waitCode = 3; d.logs = "apk: no network" }, "getting strace out of alpine:3.20 stopped with 3: apk: no network"},
		{"no loader", func(d *fakeDocker) { d.noLoader = true }, "no musl loader came with strace"},
		{"no strace", func(d *fakeDocker) { d.noStrace = true }, "strace did not come out of alpine:3.20"},
	} {
		s := newScene(t)
		tc.setup(s.d)
		res, err := s.observe(t, context.Background())
		if err == nil || res != nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: res = %v, err = %v, want %q", tc.name, res, err, tc.want)
		}
		equal(t, tc.name+": removed", s.d.removed, []string{tracerID})
		equal(t, tc.name+": created", len(s.d.creates), 1)
		entries, _ := os.ReadDir(s.tmp)
		if len(entries) != 0 {
			t.Errorf("%s: temporary directory still holds %v", tc.name, entries)
		}
	}
}

func TestObserveSecretsStayOutOfErrorsAndLogs(t *testing.T) {
	s := newScene(t)
	s.o.Env = []string{"TOKEN=s3cret"}
	s.o.Verbose = true
	var logged strings.Builder
	s.o.Log = func(format string, args ...any) { logged.WriteString(fmt.Sprintf(format, args...)) }
	s.d.traceTxt = ""
	s.d.logs = "leak s3cret leak"
	_, err := s.observe(t, context.Background())
	if err == nil || !strings.Contains(err.Error(), "started no program") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "s3cret") || !strings.Contains(err.Error(), "leak *** leak") {
		t.Errorf("err = %v", err)
	}
	if strings.Contains(logged.String(), "s3cret") {
		t.Errorf("log = %q", logged.String())
	}
	if !strings.Contains(logged.String(), "environment variables given: TOKEN") {
		t.Errorf("the names are not logged: %q", logged.String())
	}
}

func TestObserveLogTailIsTheLast2KiB(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt = ""
	s.d.logs = strings.Repeat("a", 5000) + "END"
	_, err := s.observe(t, context.Background())
	if err == nil {
		t.Fatal("no error")
	}
	if !strings.Contains(err.Error(), ": ..."+strings.Repeat("a", 2045)+"END") || strings.Contains(err.Error(), strings.Repeat("a", 2046)) {
		t.Errorf("tail is not 2048 bytes: %d bytes of error", len(err.Error()))
	}
}

func TestHTTPProberFollowsARedirectToHTTPSOnTheSameHost(t *testing.T) {
	hits := 0
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer secure.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secure.URL+"/", http.StatusFound)
	}))
	defer plain.Close()
	if err := (httpProber{}).Get(context.Background(), plain.URL+"/"); err != nil {
		t.Fatal(err)
	}
	equal(t, "requests that reached the https server", hits, 1)

	// a closed port is the only failure
	closed := httptest.NewServer(http.NotFoundHandler())
	url := closed.URL + "/"
	closed.Close()
	if err := (httpProber{}).Get(context.Background(), url); err == nil {
		t.Error("a closed port was not an error")
	}
}

func TestObserveBindsFollowTheTracerBinds(t *testing.T) {
	s := newScene(t)
	s.o.Binds = []string{"/srv/site/app.conf:/etc/app.conf:ro", "/srv/site/www:/www"}
	if _, err := s.observe(t, context.Background()); err != nil {
		t.Fatal(err)
	}
	equal(t, "binds", s.d.creates[1].Binds, []string{
		s.d.bindDir + ":/.ops-trace", s.d.bindDir + "/out:/.ops-trace/out:ro",
		"/srv/site/app.conf:/etc/app.conf:ro", "/srv/site/www:/www",
	})
}

func TestObserveBindUnderTheTracerIsAnError(t *testing.T) {
	for _, b := range []string{
		"/h:/.ops-trace", "/h:/.ops-trace/out", "/h:/.ops-trace/../.ops-trace/x:ro",
		"h:/x", "/h:x", "/h", "/h:/x:zz", "/h:/x:ro:more",
	} {
		s := newScene(t)
		s.o.Binds = []string{b}
		if _, err := s.observe(t, context.Background()); err == nil {
			t.Errorf("bind %q: no error", b)
		}
		equal(t, "created for "+b, len(s.d.creates), 0)
		equal(t, "pulled for "+b, s.d.pulled, []string(nil))
	}
}

func TestObserveBindMountedFileAndDirectoryAreCarried(t *testing.T) {
	s := newScene(t)
	s.o.Binds = []string{"/srv/site/app.conf:/etc/mine.conf:ro", "/srv/site/www:/www"}
	s.d.traceTxt += `11 openat(AT_FDCWD, "/etc/mine.conf", O_RDONLY) = 8` + "\n" +
		`11 openat(AT_FDCWD, "/www/index.html", O_RDONLY) = 9` + "\n" +
		`11 openat(AT_FDCWD, "/www/sub", O_RDONLY|O_DIRECTORY) = 10` + "\n"
	// docker cp reads through a bind mount like any other path
	s.d.files["/etc/mine.conf"] = entry{data: "mine\n"}
	s.d.files["/www/index.html"] = entry{data: "<p>hi</p>"}
	s.d.files["/www/sub"] = entry{dir: true}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"etc/mine.conf": "mine\n", "www/index.html": "<p>hi</p>"} {
		b, err := os.ReadFile(filepath.Join(res.Sysroot, name))
		if err != nil || string(b) != want {
			t.Errorf("%s = %q, %v; want %q", name, b, err, want)
		}
	}
	if st, err := os.Stat(filepath.Join(res.Sysroot, "www/sub")); err != nil || !st.IsDir() {
		t.Errorf("the bind-mounted directory is not in the sysroot: %v", err)
	}
	equal(t, "stopped", s.d.tarStopped, map[string]bool{"/www/sub": true})
	equal(t, "warnings", res.Warnings, []string(nil))
}

func TestObserveProgramWithoutASlashIsFoundOnThePath(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt = strings.Replace(sceneTrace, `"/usr/bin/app", ["app", "--serve"]`, `"app", ["app", "--serve"]`, 1)
	s.d.traceTxt += `11 openat(AT_FDCWD, "/opt/bin/app", O_RDONLY) = 8` + "\n"
	s.d.files["/opt/bin/app"] = entry{data: "real", mode: 0755}
	s.d.exec["cat /proc/11/environ"] = "A=1\x00PATH=/opt/bin:/usr/bin\x00"
	// no answer for the executable: only the PATH of the program can tell where it is
	delete(s.d.exec, "readlink /proc/11/exe")
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "program", res.Program, "/opt/bin/app")
	equal(t, "argv", res.Argv, []string{"app", "--serve"})
}

func TestObserveProgramNotOpenedIsAnError(t *testing.T) {
	s := newScene(t)
	delete(s.d.files, "/usr/bin/app")
	_, err := s.observe(t, context.Background())
	want := "observe: img:1 did not open /usr/bin/app: say it with --file"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestObserveProbesInTheOrderOfThePorts(t *testing.T) {
	s := newScene(t)
	s.d.cfg.ExposedPorts = []string{"8080/tcp", "443/tcp", "80/tcp"}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "urls", s.probe.urls, []string{"http://172.17.0.2:80/", "http://172.17.0.2:443/", "http://172.17.0.2:8080/"})
	equal(t, "exposed ports", res.ExposedPorts, []int{80, 443, 8080})
}

func TestHTTPProberDoesNotFollowARedirectToAnotherHost(t *testing.T) {
	var other atomic.Int32
	l, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Skipf("no second loopback address: %v", err)
	}
	far := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { other.Add(1) }))
	far.Listener.Close()
	far.Listener = l
	far.Start()
	defer far.Close()
	var first atomic.Int32
	near := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first.Add(1)
		http.Redirect(w, r, far.URL+"/", http.StatusFound)
	}))
	defer near.Close()
	if err := (httpProber{}).Get(context.Background(), near.URL+"/"); err != nil {
		t.Fatal(err)
	}
	equal(t, "requests to the first server", first.Load(), int32(1))
	equal(t, "requests to the other host", other.Load(), int32(0))
}

func TestHTTPProberStopsAtTenRequests(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 50 {
			http.Redirect(w, r, "/again", http.StatusFound)
		}
	}))
	defer srv.Close()
	if err := (httpProber{}).Get(context.Background(), srv.URL+"/"); err != nil {
		t.Fatal(err)
	}
	equal(t, "requests made to a server that always redirects to itself", hits.Load(), int32(10))
}

func TestObserveTraceStartsInTheDirectoryOfTheContainer(t *testing.T) {
	s := newScene(t)
	s.d.cfg.WorkingDir = "/srv"
	s.d.traceTxt = `10 execve("/entry.sh", ["/entry.sh"], 0x7ffd /* 5 vars */) = 0
10 chdir("sub") = 0
11 execve("/usr/bin/app", ["app"], 0x7ffd /* 4 vars */) = 0
11 openat(AT_FDCWD, "data.txt", O_RDONLY) = 3
`
	// where the program ended up, which is not where it started
	s.d.exec["readlink /proc/11/cwd"] = "/srv/sub\n"
	s.d.files["/srv/sub/data.txt"] = entry{data: "data"}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.d.copied, "/srv/sub/data.txt") {
		t.Errorf("copied = %v, want /srv/sub/data.txt in it", s.d.copied)
	}
	if got, err := os.ReadFile(filepath.Join(res.Sysroot, "srv/sub/data.txt")); err != nil || string(got) != "data" {
		t.Errorf("srv/sub/data.txt = %q, %v", got, err)
	}
	equal(t, "working dir", res.WorkingDir, "/srv/sub")
}

func TestObserveRelativePathWithoutAWorkingDirIsReadAgainstRoot(t *testing.T) {
	s := newScene(t)
	s.d.cfg.WorkingDir = ""
	delete(s.d.exec, "readlink /proc/11/cwd")
	delete(s.d.files, "/app/rel.txt")
	s.d.files["/rel.txt"] = entry{data: "relative"}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.d.copied, "/rel.txt") {
		t.Errorf("copied = %v, want /rel.txt in it", s.d.copied)
	}
	if _, err := os.Stat(filepath.Join(res.Sysroot, "rel.txt")); err != nil {
		t.Error(err)
	}
}

func TestObserveUserIsResolvedThroughPasswd(t *testing.T) {
	passwd := "root:x:0:0:root:/root:/bin/sh\n# comment:x:9:9\ngrafana:x:472:0:g:/h:/sbin/nologin\n"
	for _, tc := range []struct {
		name      string
		imageUser string
		optUser   string
		noPasswd  bool
		want      string
		warn      string
		read      bool
	}{
		{"uid", "472", "", false, "grafana", "", true},
		{"uid and gid", "472:0", "", false, "grafana", "", true},
		{"uid without a name", "65532:65532", "", false, "", "the image runs as uid 65532, which has no name in /etc/passwd; it was observed as root", true},
		{"uid and no passwd", "472", "", true, "", "the image runs as uid 472, which has no name in /etc/passwd; it was observed as root (/etc/passwd could not be read: ", true},
		{"name", "app", "", false, "app", "", false},
		{"name and group", "app:staff", "", false, "app", "", false},
		{"root", "root", "", false, "", "", false},
		{"zero", "0", "", false, "", "", false},
		{"zero and group", "0:0", "", false, "", "", false},
		{"empty", "", "", false, "", "", false},
		{"option replaces a name", "app", "472", false, "grafana", "", true},
		{"option replaces a uid", "472", "web", false, "web", "", false},
		{"option root over a uid", "472", "root", false, "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newScene(t)
			s.d.cfg.User = tc.imageUser
			s.o.User = tc.optUser
			if !tc.noPasswd {
				s.d.files["/etc/passwd"] = entry{data: passwd}
			}
			res, err := s.observe(t, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			traced := s.d.creates[len(s.d.creates)-1]
			if traced.Image != "img:1" || traced.User != "0" {
				t.Fatalf("last container = %+v", traced)
			}
			got := ""
			if i := slices.Index(*traced.Entrypoint, "-u"); i >= 0 {
				got = (*traced.Entrypoint)[i+1]
			}
			equal(t, "strace -u", got, tc.want)
			equal(t, "passwd read", slices.Contains(s.d.copied, "/etc/passwd"), tc.read)
			if tc.read {
				probe := s.d.creates[len(s.d.creates)-2]
				if !containerName.MatchString(probe.Name) || probe.Name == traced.Name || !slices.Equal(probe.Labels, []string{"dock2nano=1"}) {
					t.Errorf("probe container = %+v", probe)
				}
				if !slices.Contains(s.d.removed, probeID) || slices.Contains(s.d.started, probeID) {
					t.Errorf("probe container: removed %v, started %v", s.d.removed, s.d.started)
				}
			}
			var userWarnings []string
			for _, w := range res.Warnings {
				if strings.Contains(w, "the image runs as") {
					userWarnings = append(userWarnings, w)
				}
			}
			if tc.warn == "" {
				equal(t, "user warnings", userWarnings, []string(nil))
			} else if len(userWarnings) != 1 || !strings.HasPrefix(userWarnings[0], tc.warn) {
				t.Errorf("user warnings = %q, want one starting with %q", userWarnings, tc.warn)
			}
		})
	}
}

func TestObserveStoppedEarlyIsReported(t *testing.T) {
	s := newScene(t)
	s.o.Env = []string{"PASSWORD=s3cret-pw"}
	s.d.notRunning = true
	s.d.tracedCode = 3
	s.d.logs = "first\nsecond\nthird\nfourth s3cret-pw\n"
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.StoppedEarly || res.ExitCode != 3 {
		t.Errorf("StoppedEarly = %v, ExitCode = %d", res.StoppedEarly, res.ExitCode)
	}
	equal(t, "stopped log", res.StoppedLog, "first\nsecond\nthird\nfourth ***")
	equal(t, "log lines asked for", s.d.tailLines, []int{100})
	want := "the container stopped with status 3 before the end of the observation; the program chosen is the last one it started, not necessarily a server: first | second | third"
	if len(res.Warnings) == 0 || res.Warnings[0] != want {
		t.Errorf("warnings = %q, want the first to be %q", res.Warnings, want)
	}
}

func TestObserveStoppedLogIsTheLastKiB(t *testing.T) {
	s := newScene(t)
	s.d.notRunning = true
	s.d.logs = strings.Repeat(strings.Repeat("x", 99)+"\n", 30)
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.StoppedLog) > 1024 || len(res.StoppedLog) < 900 || strings.HasPrefix(res.StoppedLog, "\n") {
		t.Errorf("stopped log has %d bytes", len(res.StoppedLog))
	}
	if strings.Trim(res.StoppedLog, "x\n") != "" {
		t.Errorf("stopped log = %q", res.StoppedLog)
	}
}

func TestObserveRunningUntilTheEndIsNotStoppedEarly(t *testing.T) {
	s := newScene(t)
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.StoppedEarly || res.ExitCode != 0 || res.StoppedLog != "" {
		t.Errorf("StoppedEarly = %v, ExitCode = %d, StoppedLog = %q", res.StoppedEarly, res.ExitCode, res.StoppedLog)
	}
	equal(t, "log lines asked for", s.d.tailLines, []int(nil))
}

type failProber struct{}

func (failProber) Get(ctx context.Context, url string) error { return errors.New("connection refused") }

func TestObserveProbeFailureWarnsOncePerPort(t *testing.T) {
	s := newScene(t)
	s.o.Probe = failProber{}
	s.o.Seconds = 6
	s.d.cfg.ExposedPorts = []string{"8080/tcp"}
	s.d.exec["cat /proc/net/tcp"] = tcpHeader + "   0: 00000000:1F90 00000000:0000 0A 00000000:00000000\n"
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "warnings", res.Warnings, []string{"port 8080 could not be asked for /: connection refused"})
}

func TestObserveRedactLeavesShortAndImageValuesAlone(t *testing.T) {
	s := newScene(t)
	s.d.cfg.Env = append(s.d.cfg.Env, "LANG_AUTH=en_US.UTF-8")
	s.o.Env = []string{"DEBUG_KEY=1", "LANG_AUTH=en_US.UTF-8", "TOKEN=secret-token"}
	s.d.traceTxt = ""
	s.d.logs = "/lib/x1.so en_US.UTF-8 secret-token"
	_, err := s.observe(t, context.Background())
	if err == nil || !strings.Contains(err.Error(), ": /lib/x1.so en_US.UTF-8 ***") {
		t.Fatalf("err = %v", err)
	}
}

func TestObserveLogTailAsksForHundredLines(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt = ""
	s.d.logs = "a\nb"
	if _, err := s.observe(t, context.Background()); err == nil {
		t.Fatal("no error")
	}
	equal(t, "log lines asked for", s.d.tailLines, []int{100})
}

func TestObserveFailedCreateIsRemovedByName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		image string
		want  func(name string) []string
	}{
		{"strace container", trace.StraceImage, func(name string) []string { return []string{name} }},
		{"traced container", "img:1", func(name string) []string { return []string{tracerID, name} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newScene(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.d.createErr = func(o docker.CreateOptions) error {
				if o.Image == tc.image {
					cancel()
					return context.Canceled
				}
				return nil
			}
			res, err := s.observe(t, ctx)
			if res != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("res = %v, err = %v", res, err)
			}
			name := s.d.creates[len(s.d.creates)-1].Name
			if !containerName.MatchString(name) {
				t.Fatalf("name = %q", name)
			}
			equal(t, "removed", s.d.removed, tc.want(name))
			equal(t, "context errors at removal", s.d.removeCtx[len(s.d.removeCtx)-1], error(nil))
		})
	}
}

func TestObserveTraceFileThatIsALinkIsNotRead(t *testing.T) {
	s := newScene(t)
	var logged strings.Builder
	s.o.Log = func(format string, args ...any) { logged.WriteString(fmt.Sprintf(format, args...)) }
	s.d.onStart = func() {
		real := filepath.Join(s.tmp, "elsewhere")
		if err := os.WriteFile(real, []byte(sceneTrace), 0600); err != nil {
			t.Fatal(err)
		}
		list := filepath.Join(s.d.bindDir, "files")
		os.Remove(list)
		if err := os.Symlink(real, list); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.observe(t, context.Background())
	if err == nil || !strings.Contains(err.Error(), "started no program") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(logged.String(), "the trace file is not a regular file") {
		t.Errorf("log = %q", logged.String())
	}
}

func TestFetchRefusesARelativePath(t *testing.T) {
	s := newScene(t)
	s.d.files["etc/x"] = entry{data: "x"}
	r := &run{d: s.d}
	if _, err := r.fetch(context.Background(), appID, t.TempDir(), "etc/x"); err == nil {
		t.Error("no error")
	}
	equal(t, "copied", s.d.copied, []string(nil))
}

func TestHTTPProberReadsAtMostOneMiBOfTheBody(t *testing.T) {
	var failed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 1<<20)
		for i := 0; i < 64; i++ {
			if _, err := w.Write(chunk); err != nil {
				failed.Store(true)
				return
			}
		}
	}))
	if err := (httpProber{}).Get(context.Background(), srv.URL+"/"); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	if !failed.Load() {
		t.Error("the whole body was read")
	}
}

func TestObserveAliasOfAPathThatWasNotCarriedIsAWarning(t *testing.T) {
	s := newScene(t)
	// /aa-sock is asked for first and Docker makes nothing of it; /zz is a link to it
	s.d.traceTxt += `11 openat(AT_FDCWD, "/aa-sock", O_RDONLY) = 8` + "\n" + `11 openat(AT_FDCWD, "/zz", O_RDONLY) = 9` + "\n"
	s.d.files["/aa-sock"] = entry{socket: true}
	s.d.files["/zz"] = entry{link: "/aa-sock"}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range res.Warnings {
		if w == "not carried /zz: it leads to /aa-sock, which was not carried" {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %q", res.Warnings)
	}
}

// startFake is the part of Docker that Start uses; the other methods panic.
type startFake struct {
	Docker
	cfg     *docker.ImageConfig
	have    bool
	ip      string
	logs    string
	creates []docker.CreateOptions
	started []string
	stopped []string
	removed []string
	pulled  []string
	// exitAt, when not zero, is the time from which Running answers false.
	exitAt time.Time
	clock  *fakeClock
	// startErr fails Start of the container.
	startErr error
	// stopCtxErr and removeCtxErr are the errors of the contexts Stop and Remove were given.
	stopCtxErr   []error
	removeCtxErr []error
}

func (f *startFake) HasImage(ctx context.Context, image string) (bool, error) { return f.have, nil }

func (f *startFake) Pull(ctx context.Context, image string) error {
	f.pulled = append(f.pulled, image)
	return nil
}

func (f *startFake) Inspect(ctx context.Context, image string) (*docker.ImageConfig, error) {
	return f.cfg, nil
}

func (f *startFake) Create(ctx context.Context, o docker.CreateOptions) (string, error) {
	f.creates = append(f.creates, o)
	return "dep-1", nil
}

func (f *startFake) Start(ctx context.Context, id string) error {
	f.started = append(f.started, id)
	return f.startErr
}

func (f *startFake) Stop(ctx context.Context, id string, seconds int) error {
	f.stopped = append(f.stopped, id)
	f.stopCtxErr = append(f.stopCtxErr, ctx.Err())
	return errors.New("stop failed")
}

func (f *startFake) Remove(ctx context.Context, id string, force bool) error {
	f.removed = append(f.removed, id)
	f.removeCtxErr = append(f.removeCtxErr, ctx.Err())
	return nil
}

func (f *startFake) Running(ctx context.Context, id string) (bool, error) {
	return f.exitAt.IsZero() || f.clock.now().Before(f.exitAt), nil
}

func (f *startFake) LogsTail(ctx context.Context, id string, lines int) ([]byte, error) {
	return []byte(f.logs), nil
}

func (f *startFake) IPAddress(ctx context.Context, id string) (string, error) { return f.ip, nil }

type startScene struct {
	d     *startFake
	clock *fakeClock
	dials []string
	// open reports whether a dial succeeds
	open func(address string) bool
	o    StartOptions
}

func newStartScene() *startScene {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := &startScene{clock: clock}
	s.d = &startFake{clock: clock, have: true, ip: "172.18.0.2", cfg: &docker.ImageConfig{OS: "linux", ExposedPorts: []string{"3306/tcp", "33060/tcp", "53/udp"}}}
	s.open = func(string) bool { return true }
	s.o = StartOptions{Image: "db:1", now: clock.now, sleep: clock.sleep, dial: func(ctx context.Context, address string) error {
		s.dials = append(s.dials, address)
		if !s.open(address) {
			return errors.New("connection refused")
		}
		return nil
	}}
	return s
}

func TestStartCreateOptions(t *testing.T) {
	s := newStartScene()
	ep := []string{"/entry.sh"}
	s.o.Entrypoint = &ep
	s.o.Cmd = []string{"mysqld"}
	s.o.Env = []string{"A=1"}
	s.o.WorkingDir = "/srv"
	s.o.User = "999"
	s.o.Binds = []string{"/h:/c:ro"}
	s.o.Network = "dock2nano-0a0a0a0a"
	s.o.NetworkAliases = []string{"db"}
	if _, err := Start(context.Background(), s.d, s.o); err != nil {
		t.Fatal(err)
	}
	if len(s.d.creates) != 1 {
		t.Fatalf("%d creates", len(s.d.creates))
	}
	got := s.d.creates[0]
	if !containerName.MatchString(got.Name) {
		t.Errorf("name %q", got.Name)
	}
	got.Name = ""
	want := docker.CreateOptions{
		Image: "db:1", Entrypoint: &ep, Cmd: []string{"mysqld"}, Env: []string{"A=1"}, WorkingDir: "/srv", User: "999",
		Labels: []string{"dock2nano=1"}, Binds: []string{"/h:/c:ro"}, Network: "dock2nano-0a0a0a0a", NetworkAliases: []string{"db"},
	}
	equal(t, "create options", got, want)
	equal(t, "started", s.d.started, []string{"dep-1"})
}

func TestStartReadyWhenEveryPortAccepts(t *testing.T) {
	s := newStartScene()
	// 3306 opens at once, 33060 only after 3 seconds
	s.open = func(address string) bool {
		return address == "172.18.0.2:3306" || s.clock.now().Sub(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) >= 3*time.Second
	}
	r, err := Start(context.Background(), s.d, s.o)
	if err != nil {
		t.Fatal(err)
	}
	if s.clock.sleeps != 3 {
		t.Errorf("slept %d times, want 3", s.clock.sleeps)
	}
	// the udp port is not asked for
	for _, a := range s.dials {
		if strings.HasSuffix(a, ":53") {
			t.Errorf("dialed %s", a)
		}
	}
	if len(s.dials) == 0 || s.dials[len(s.dials)-1] != "172.18.0.2:33060" {
		t.Errorf("dials = %v", s.dials)
	}
	equal(t, "removed before Stop", s.d.removed, []string(nil))
	_ = r.Stop(context.Background())
	equal(t, "removed after Stop", s.d.removed, []string{"dep-1"})
}

func TestStartNotReadyWithinWait(t *testing.T) {
	s := newStartScene()
	s.open = func(string) bool { return false }
	s.d.logs = "booting\n"
	s.o.Wait = 5 * time.Second
	r, err := Start(context.Background(), s.d, s.o)
	if err == nil || r != nil {
		t.Fatalf("r = %v, err = %v", r, err)
	}
	if !strings.Contains(err.Error(), "3306, 33060") || !strings.Contains(err.Error(), "booting") {
		t.Errorf("err = %v", err)
	}
	if s.clock.sleeps != 5 {
		t.Errorf("slept %d times, want 5", s.clock.sleeps)
	}
	equal(t, "removed", s.d.removed, []string{"dep-1"})
}

func TestStartExitsBeforeReady(t *testing.T) {
	s := newStartScene()
	s.open = func(string) bool { return false }
	s.d.exitAt = s.clock.now().Add(2 * time.Second)
	s.o.Env = []string{"PASSWORD=s3cret-pw"}
	s.d.logs = strings.Repeat("old line\n", 200) + "last s3cret-pw\n"
	r, err := Start(context.Background(), s.d, s.o)
	if err == nil || r != nil {
		t.Fatalf("r = %v, err = %v", r, err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "stopped before it was ready") || !strings.Contains(msg, "last ***") || strings.Contains(msg, "s3cret-pw") {
		t.Errorf("err = %q", msg)
	}
	_, tail, _ := strings.Cut(msg, "ready: ")
	// the error of stopping the container is joined after the log
	tail, _, _ = strings.Cut(tail, "\nstopping container")
	if len(tail) > 1024 {
		t.Errorf("log in the error has %d bytes, it starts %q and ends %q", len(tail), tail[:60], tail[len(tail)-60:])
	}
	equal(t, "removed", s.d.removed, []string{"dep-1"})
	equal(t, "stopped", s.d.stopped, []string{"dep-1"})
}

func TestStartImageWithoutPortsStaysUpThreeSeconds(t *testing.T) {
	s := newStartScene()
	s.d.cfg.ExposedPorts = nil
	if _, err := Start(context.Background(), s.d, s.o); err != nil {
		t.Fatal(err)
	}
	if got := s.clock.now().Sub(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); got != 3*time.Second {
		t.Errorf("waited %s, want 3s", got)
	}
	if len(s.dials) != 0 {
		t.Errorf("dials = %v", s.dials)
	}

	s = newStartScene()
	s.d.cfg.ExposedPorts = []string{"53/udp"}
	s.d.exitAt = s.clock.now().Add(2 * time.Second)
	if r, err := Start(context.Background(), s.d, s.o); err == nil || r != nil {
		t.Fatalf("a container that stops at 2s was ready: %v", err)
	}
	equal(t, "removed", s.d.removed, []string{"dep-1"})
}

func TestStartCancelRemovesTheContainer(t *testing.T) {
	s := newStartScene()
	s.open = func(string) bool { return false }
	ctx, cancel := context.WithCancel(context.Background())
	s.o.sleep = func(ctx context.Context, d time.Duration) error {
		cancel()
		return ctx.Err()
	}
	r, err := Start(ctx, s.d, s.o)
	if r != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("r = %v, err = %v", r, err)
	}
	equal(t, "removed", s.d.removed, []string{"dep-1"})
}

func TestRunningStopRemovesEvenWhenStopFails(t *testing.T) {
	s := newStartScene()
	r, err := Start(context.Background(), s.d, s.o)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Stop(ctx); err == nil || !strings.Contains(err.Error(), "stop failed") {
		t.Errorf("err = %v", err)
	}
	equal(t, "removed", s.d.removed, []string{"dep-1"})
	// the context given to Stop was cancelled, the ones used for the calls were not
	equal(t, "context errors at stop", s.d.stopCtxErr, []error{nil})
	equal(t, "context errors at removal", s.d.removeCtxErr, []error{nil})
	if err := r.Stop(context.Background()); err != nil {
		t.Errorf("second Stop: %v", err)
	}
	equal(t, "removed once", s.d.removed, []string{"dep-1"})
}

func TestStartImageMissingWithoutPull(t *testing.T) {
	s := newStartScene()
	s.d.have = false
	if _, err := Start(context.Background(), s.d, s.o); err == nil {
		t.Fatal("no error")
	}
	s.o.Pull = true
	if _, err := Start(context.Background(), s.d, s.o); err != nil {
		t.Fatal(err)
	}
	equal(t, "pulled", s.d.pulled, []string{"db:1"})
}

func TestObservePassesTheNetworkToTheTracedContainer(t *testing.T) {
	s := newScene(t)
	s.o.Network = "dock2nano-0a0a0a0a"
	s.o.NetworkAliases = []string{"app"}
	if _, err := s.observe(t, context.Background()); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range s.d.creates {
		if c.User == "0" {
			n++
			equal(t, "network", c.Network, "dock2nano-0a0a0a0a")
			equal(t, "aliases", c.NetworkAliases, []string{"app"})
		} else if c.Network != "" || c.NetworkAliases != nil {
			t.Errorf("a helper container got the network: %+v", c)
		}
	}
	if n != 1 {
		t.Errorf("%d traced containers", n)
	}
}

func TestObserveRedactOnlyValuesOfSecretNames(t *testing.T) {
	s := newScene(t)
	s.o.Env = []string{"DB_NAME=petclinic", "DB_PASSWORD=hunter22", "API_KEY=abcdef123", "PETCLINIC=zzzzzzzz"}
	r := &run{o: s.o}
	got := r.redact("/opt/applications/petclinic/WEB-INF hunter22 abcdef123 zzzzzzzz")
	want := "/opt/applications/petclinic/WEB-INF *** *** zzzzzzzz"
	if got != want {
		t.Errorf("redact = %q, want %q", got, want)
	}
}

func TestObserveRedactHidesValuesFromSixBytes(t *testing.T) {
	r := &run{o: Options{Env: []string{"API_KEY=abcde", "DB_PASSWORD=fghijk"}}}
	equal(t, "redacted", r.redact("abcde fghijk"), "abcde ***")
}

func TestPasswdNameSkipsCommentLines(t *testing.T) {
	data := []byte("#ghost:x:472:0:comment:/h:/bin/false\nreal:x:472:0:r:/h:/bin/sh\n")
	equal(t, "name", passwdName(data, 472), "real")
	equal(t, "only a comment", passwdName([]byte("#ghost:x:472:0\n"), 472), "")
}

func TestResolveUserWithTooManyDigitsIsObservedAsRoot(t *testing.T) {
	r := &run{once: map[string]bool{}}
	for _, who := range []string{"99999999999", "4294967296:5"} {
		got, err := r.resolveUser(context.Background(), "img:1", who)
		if err != nil || got != "" {
			t.Errorf("user %q: got %q, %v, want root", who, got, err)
		}
	}
	equal(t, "warnings", r.warn, []string{
		`the image runs as "99999999999", which is not a uid; it was observed as root`,
		`the image runs as "4294967296", which is not a uid; it was observed as root`,
	})

	// a name that holds digits is a name
	r = &run{once: map[string]bool{}}
	got, err := r.resolveUser(context.Background(), "img:1", "12ab")
	if err != nil || got != "12ab" || len(r.warn) != 0 {
		t.Errorf("got %q, %v, warnings %q", got, err, r.warn)
	}
}

// noIDDocker makes a container without giving its id.
type noIDDocker struct {
	Docker
	removed []string
}

func (d *noIDDocker) Create(ctx context.Context, o docker.CreateOptions) (string, error) {
	return "", nil
}

func (d *noIDDocker) Remove(ctx context.Context, id string, force bool) error {
	d.removed = append(d.removed, id)
	return nil
}

func TestCreateWithoutAnIDIsAnErrorAndIsRemovedByName(t *testing.T) {
	d := &noIDDocker{}
	r := &run{d: d}
	id, err := r.create(context.Background(), docker.CreateOptions{Image: "img:1"})
	if id != "" || err == nil || err.Error() != "Docker gave no container id" {
		t.Fatalf("id = %q, err = %v", id, err)
	}
	if len(d.removed) != 1 || !containerName.MatchString(d.removed[0]) {
		t.Errorf("removed = %q, want the name of the container", d.removed)
	}
}

func TestObserveStoppedLogIsCutAtAWholeLine(t *testing.T) {
	s := newScene(t)
	s.d.notRunning = true
	var lines, want []string
	for i := 0; i < 30; i++ {
		line := fmt.Sprintf("L%02d", i) + strings.Repeat("x", 96)
		lines = append(lines, line)
		if i >= 20 {
			want = append(want, line)
		}
	}
	// 2999 bytes once trimmed: the last 1024 start inside line 19, which is dropped
	s.d.logs = strings.Join(lines, "\n") + "\n"
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "stopped log", res.StoppedLog, strings.Join(want, "\n"))
}

func TestObserveStoppedLogDropsABrokenCharacter(t *testing.T) {
	s := newScene(t)
	s.d.notRunning = true
	// 3001 bytes: the last 1024 start in the middle of a two-byte character
	s.d.logs = strings.Repeat("\u00e9", 1500) + "b"
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "stopped log", res.StoppedLog, strings.Repeat("\u00e9", 511)+"b")
}

func TestStartLogTailDropsABrokenCharacter(t *testing.T) {
	s := newStartScene()
	s.open = func(string) bool { return false }
	s.d.exitAt = s.clock.now().Add(2 * time.Second)
	s.d.logs = strings.Repeat("\u00e9", 1500) + "b"
	r, err := Start(context.Background(), s.d, s.o)
	if err == nil || r != nil {
		t.Fatalf("r = %v, err = %v", r, err)
	}
	want := "stopped before it was ready: " + strings.Repeat("\u00e9", 511) + "b\n"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want it to hold the end of the log without the broken character", err)
	}
}

// flakyProber fails its first request and answers the others.
type flakyProber struct {
	urls []string
}

func (p *flakyProber) Get(ctx context.Context, url string) error {
	p.urls = append(p.urls, url)
	if len(p.urls) == 1 {
		return errors.New("connection refused")
	}
	return nil
}

func TestObserveAPortThatFailedAndWasAskedLaterDoesNotWarn(t *testing.T) {
	s := newScene(t)
	p := &flakyProber{}
	s.o.Probe = p
	s.o.Seconds = 6
	s.d.cfg.ExposedPorts = []string{"8080/tcp"}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "urls", p.urls, []string{"http://172.17.0.2:8080/", "http://172.17.0.2:8080/"})
	equal(t, "warnings", res.Warnings, []string(nil))
}

func TestObserveARepeatedWarningIsGivenOnce(t *testing.T) {
	s := newScene(t)
	s.o.Seconds = 6
	// the table is asked for at every poll, and every poll fails
	delete(s.d.exec, "cat /proc/net/tcp")
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "polls", s.clock.sleeps, 3)
	equal(t, "warnings", res.Warnings, []string{"could not read /proc/net/tcp: fake exec: no answer for cat /proc/net/tcp"})
}

func TestStartFailureOfTheContainerRemovesIt(t *testing.T) {
	s := newStartScene()
	s.d.startErr = errors.New("cannot start")
	r, err := Start(context.Background(), s.d, s.o)
	if r != nil || err == nil || !strings.Contains(err.Error(), "observe: start: starting the container: cannot start") {
		t.Fatalf("r = %v, err = %v", r, err)
	}
	equal(t, "stopped", s.d.stopped, []string{"dep-1"})
	equal(t, "removed", s.d.removed, []string{"dep-1"})
}

// noAddress is a Docker that cannot give the address of a container.
type noAddress struct {
	Docker
}

func TestStartWithoutAnAddresserIsNeverReady(t *testing.T) {
	s := newStartScene()
	s.o.Wait = 3 * time.Second
	r, err := Start(context.Background(), noAddress{s.d}, s.o)
	if r != nil || err == nil || !strings.Contains(err.Error(), "did not accept connections on 3306, 33060 within 3s") {
		t.Fatalf("r = %v, err = %v", r, err)
	}
	equal(t, "dials", s.dials, []string(nil))
	equal(t, "removed", s.d.removed, []string{"dep-1"})
}

func TestStartWithAnEmptyAddressIsNeverReady(t *testing.T) {
	s := newStartScene()
	s.d.ip = ""
	s.o.Wait = 3 * time.Second
	r, err := Start(context.Background(), s.d, s.o)
	if r != nil || err == nil || !strings.Contains(err.Error(), "did not accept connections on 3306, 33060 within 3s") {
		t.Fatalf("r = %v, err = %v", r, err)
	}
	equal(t, "dials", s.dials, []string(nil))
	equal(t, "removed", s.d.removed, []string{"dep-1"})
}

func TestStartWithoutAnImageIsAnError(t *testing.T) {
	s := newStartScene()
	s.o.Image = ""
	r, err := Start(context.Background(), s.d, s.o)
	if r != nil || err == nil || err.Error() != "observe: start: no image given" {
		t.Fatalf("r = %v, err = %v", r, err)
	}
	equal(t, "created", len(s.d.creates), 0)
}

func TestObserveForksBeforeTheProgramStartedAreNotChildren(t *testing.T) {
	s := newScene(t)
	// the process that is the program forks first and executes the program afterwards
	s.d.traceTxt = strings.Replace(sceneTrace, "11 execve(", "11 fork() = 20\n11 execve(", 1)
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "children", res.Children, 0)
	equal(t, "child calls", res.ChildCalls, []string(nil))
}

func TestObserveAliasOfACarriedPathIsACopy(t *testing.T) {
	s := newScene(t)
	// the paths are asked for in order: the file first, then the link that leads to it
	s.d.traceTxt += `11 openat(AT_FDCWD, "/aa-file", O_RDONLY) = 8` + "\n" + `11 openat(AT_FDCWD, "/zz-link", O_RDONLY) = 9` + "\n"
	s.d.files["/aa-file"] = entry{data: "target"}
	s.d.files["/zz-link"] = entry{link: "/aa-file"}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(res.Sysroot, "zz-link"))
	if err != nil || string(got) != "target" {
		t.Errorf("zz-link = %q, %v", got, err)
	}
	equal(t, "warnings", res.Warnings, []string(nil))
}

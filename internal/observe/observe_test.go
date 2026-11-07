package observe

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/trace"
)

const (
	tracerID = "tracer-1"
	appID    = "app-1"
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
}

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
	if o.Image == trace.StraceImage {
		return tracerID, nil
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
	return true, nil
}

func (f *fakeDocker) Wait(ctx context.Context, id string) (int, error) {
	return 0, nil
}

func (f *fakeDocker) Exec(ctx context.Context, id string, cmd []string) ([]byte, error) {
	if v, ok := f.exec[strings.Join(cmd, " ")]; ok {
		return []byte(v), nil
	}
	return nil, errors.New("fake exec: no answer for " + strings.Join(cmd, " "))
}

func (f *fakeDocker) CopyFrom(ctx context.Context, id, containerPath, hostDir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if id == tracerID {
		out := filepath.Join(hostDir, "out")
		if err := os.MkdirAll(out, 0755); err != nil {
			return err
		}
		for _, name := range []string{"strace", loader} {
			if err := os.WriteFile(filepath.Join(out, name), []byte("x"), 0755); err != nil {
				return err
			}
		}
		return nil
	}
	f.copied = append(f.copied, containerPath)
	e, ok := f.files[containerPath]
	if !ok {
		return errors.New("no such file or directory: " + containerPath)
	}
	dest := filepath.Join(hostDir, path.Base(containerPath))
	switch {
	case e.link != "":
		return os.Symlink(e.link, dest)
	case e.dir:
		return os.Mkdir(dest, 0755)
	}
	mode := e.mode
	if mode == 0 {
		mode = 0644
	}
	return os.WriteFile(dest, []byte(e.data), mode)
}

func (f *fakeDocker) Logs(ctx context.Context, id string) ([]byte, error) {
	return []byte(f.logs), nil
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
	d.files["/srv/rel.txt"] = entry{data: "relative"}
	d.files["/usr/bin/app"] = entry{data: "#!/bin/sh\necho hi\n", mode: 0755}
	d.files["/var/log/app.log"] = entry{link: "/dev/stderr"}
	d.files["/bin/sh"] = entry{data: "shell", mode: 0755}
	d.files["/only/the/script"] = entry{data: "must not be copied"}
	d.exec = map[string]string{}
	d.exec["cat /proc/net/tcp"] = tcpHeader
	d.exec["cat /proc/net/tcp6"] = tcpHeader
	d.exec["cat /proc/11/cmdline"] = "/usr/bin/app\x00--serve\x00"
	d.exec["cat /proc/11/environ"] = "A=1\x00HOSTNAME=abc\x00PATH=/usr/bin\x00_=/x\x00"
	d.exec["readlink /proc/11/cwd"] = "/srv\n"
	d.exec["readlink /proc/11/exe"] = "/usr/bin/app\n"
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
		"-f", "-q", "-s", "4096", "-e", "trace=file", "-e", "signal=none",
		"-o", "/.ops-trace/files", "--",
		"/entry.sh", "--serve",
	}
	want := docker.CreateOptions{
		Image:       "img:1",
		Entrypoint:  &wrapped,
		Binds:       []string{s.d.bindDir + ":/.ops-trace"},
		CapAdd:      []string{"SYS_PTRACE"},
		SecurityOpt: []string{"seccomp=unconfined"},
	}
	equal(t, "traced container options", s.d.creates[1], want)
	if s.d.bindDir == "" {
		t.Fatal("the traced container was never started")
	}

	// the tracer directory as the program sees it
	equal(t, "tracer dir mode", s.d.dirMode, fs.FileMode(0777))
	equal(t, "trace file mode", s.d.listMode, fs.FileMode(0666))
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
		"/etc/app.conf", "/lib/libc.so", "/srv/rel.txt", "/usr/bin/app", "/var/log/app.log",
		"/lib/libc.so.6", "/bin/sh",
	})
	equal(t, "sysroot files", tree(t, res.Sysroot), []string{
		"bin/sh", "etc/app.conf", "lib/libc.so", "lib/libc.so.6", "srv/rel.txt", "usr/bin/app", "var/log/app.log",
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
	equal(t, "relative path read against the cwd of /proc", read("srv/rel.txt"), "relative")
	if st, err := os.Lstat(filepath.Join(res.Sysroot, "lib/libc.so")); err != nil || !st.Mode().IsRegular() {
		t.Errorf("lib/libc.so is not a regular file: %v %v", st, err)
	}

	// the program and its command line come from /proc
	equal(t, "program", res.Program, "/usr/bin/app")
	equal(t, "argv", res.Argv, []string{"/usr/bin/app", "--serve"})
	equal(t, "env", res.Env, []string{"A=1", "PATH=/usr/bin"})
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

func TestObserveProgramFromProc(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt += `11 openat(AT_FDCWD, "/usr/bin/app-real", O_RDONLY) = 8` + "\n"
	s.d.files["/usr/bin/app-real"] = entry{data: "real", mode: 0755}
	s.d.exec["readlink /proc/11/exe"] = "/usr/bin/app-real\n"
	s.d.exec["cat /proc/11/cmdline"] = "app\x00-v\x00"
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "program", res.Program, "/usr/bin/app-real")
	equal(t, "argv", res.Argv, []string{"/usr/bin/app-real", "-v"})
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
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "no address") {
		t.Errorf("warnings = %q", res.Warnings)
	}
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
	delete(s.d.files, "/srv/rel.txt")
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], "not carried /srv/rel.txt") {
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

package trace

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// put writes the values little endian, each with the size of its type.
func put(b *bytes.Buffer, values ...any) {
	for _, v := range values {
		if err := binary.Write(b, binary.LittleEndian, v); err != nil {
			panic(err)
		}
	}
}

// buildELF makes a minimal 64-bit ELF file: the header, a PT_INTERP program header and the .interp
// section naming interp, and a section header table. With interp "" there is no loader at all (a
// static binary).
func buildELF(interp string) []byte {
	var phnum, shnum uint16
	interpBytes := []byte{}
	shstr := []byte("\x00.shstrtab\x00")
	shstrName := uint32(1)
	if interp != "" {
		phnum, shnum = 1, 3
		interpBytes = []byte(interp + "\x00")
		shstr = []byte("\x00.interp\x00.shstrtab\x00")
		shstrName = 9
	} else {
		shnum = 2
	}
	interpOff := uint64(64 + 56*int(phnum))
	strOff := interpOff + uint64(len(interpBytes))
	shOff := (strOff + uint64(len(shstr)) + 7) &^ 7

	var b bytes.Buffer
	b.Write([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	put(&b, uint16(2), uint16(62), uint32(1), uint64(0), uint64(64), shOff, uint32(0))
	put(&b, uint16(64), uint16(56), phnum, uint16(64), shnum, shnum-1)
	if phnum == 1 {
		size := uint64(len(interpBytes))
		put(&b, uint32(3), uint32(4), interpOff, interpOff, interpOff, size, size, uint64(1))
		b.Write(interpBytes)
	}
	b.Write(shstr)
	for uint64(b.Len()) < shOff {
		b.WriteByte(0)
	}
	b.Write(make([]byte, 64))
	if phnum == 1 {
		size := uint64(len(interpBytes))
		put(&b, uint32(1), uint32(1), uint64(2), uint64(0), interpOff, size, uint32(0), uint32(0), uint64(1), uint64(0))
	}
	put(&b, shstrName, uint32(3), uint64(0), uint64(0), strOff, uint64(len(shstr)), uint32(0), uint32(0), uint64(1), uint64(0))

	return b.Bytes()
}

// buildELFNoSections makes a 64-bit ELF file with a PT_INTERP program header naming interp and no
// section headers at all, as sstrip leaves it.
func buildELFNoSections(interp string) []byte {
	data := []byte(interp + "\x00")
	off := uint64(64 + 56)
	size := uint64(len(data))

	var b bytes.Buffer
	b.Write([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	put(&b, uint16(2), uint16(62), uint32(1), uint64(0), uint64(64), uint64(0), uint32(0))
	put(&b, uint16(64), uint16(56), uint16(1), uint16(64), uint16(0), uint16(0))
	put(&b, uint32(3), uint32(4), off, off, off, size, size, uint64(1))
	b.Write(data)

	return b.Bytes()
}

func writeFile(t *testing.T, root string, name string, data []byte) string {
	t.Helper()
	full := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0755); err != nil {
		t.Fatal(err)
	}
	return full
}

func TestWrapCommand(t *testing.T) {
	argv := make([]string, 2, 10)
	argv[0], argv[1] = "/usr/sbin/nginx", "-g"

	got := WrapCommand(TraceMount, "ld-musl-x86_64.so.1", argv)
	want := []string{
		"/.ops-trace/out/ld-musl-x86_64.so.1", "--library-path", "/.ops-trace/out", "/.ops-trace/out/strace",
		"-f", "-q", "-s", "4096", "-e", "trace=file", "-e", "signal=none",
		"-o", "/.ops-trace/files", "--",
		"/usr/sbin/nginx", "-g",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WrapCommand = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(argv, []string{"/usr/sbin/nginx", "-g"}) {
		t.Errorf("WrapCommand changed argv: %q", argv)
	}

	other := WrapCommand("/x", "ld", nil)
	if other[0] != "/x/out/ld" || other[len(other)-1] != "--" || len(other) != 15 {
		t.Errorf("WrapCommand with another mount = %q", other)
	}
	if StraceImage != "alpine:3.20" {
		t.Errorf("StraceImage = %q", StraceImage)
	}
}

func TestFirstQuoted(t *testing.T) {
	tests := []struct {
		name      string
		rest      string
		wantName  string
		wantAfter string
		wantRel   bool
		wantOK    bool
	}{
		{"plain", `"/etc/passwd", O_RDONLY) = 3`, "/etc/passwd", `, O_RDONLY) = 3`, true, true},
		{"at fdcwd", `AT_FDCWD, "rel/x", O_RDONLY) = 3`, "rel/x", `, O_RDONLY) = 3`, true, true},
		{"descriptor", `5, "rel/x", O_RDONLY) = 3`, "rel/x", `, O_RDONLY) = 3`, false, true},
		{"hex descriptor", `0x7ffd, "/x") = 0`, "/x", `) = 0`, false, true},
		{"escaped quote", `AT_FDCWD, "/a\"b", 0) = 0`, `/a"b`, `, 0) = 0`, true, true},
		{"octal escape", `"/caf\303\251", 0) = 0`, "/café", `, 0) = 0`, true, true},
		{"invalid escape stays raw", `"/bad\qname", O_RDONLY) = 3`, `/bad\qname`, `, O_RDONLY) = 3`, true, true},
		{"unterminated", `"/abc`, "", "", false, false},
		{"ends in backslash", `"/x\`, "", "", false, false},
		{"no path", `NULL, 0) = -1`, "", "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, after, rel, ok := FirstQuoted(tt.rest)
			if name != tt.wantName || after != tt.wantAfter || rel != tt.wantRel || ok != tt.wantOK {
				t.Errorf("FirstQuoted(%q) = %q, %q, %v, %v; want %q, %q, %v, %v", tt.rest, name, after, rel, ok, tt.wantName, tt.wantAfter, tt.wantRel, tt.wantOK)
			}
		})
	}
}

func TestResultOf(t *testing.T) {
	tests := []struct {
		after string
		want  string
		found bool
	}{
		{`, O_RDONLY) = 3`, "3", true},
		{`) = -1 ENOENT (No such file or directory)`, "-1", true},
		{`, O_RDONLY <unfinished ...>`, "", false},
		{`) = 0 `, "0", true},
		{`) = ?`, "?", true},
		{`, "x) = y", 1) = 7`, "7", true},
	}
	for _, tt := range tests {
		got, found := ResultOf(tt.after)
		if got != tt.want || found != tt.found {
			t.Errorf("ResultOf(%q) = %q, %v; want %q, %v", tt.after, got, found, tt.want, tt.found)
		}
	}
}

func TestTouchedPaths(t *testing.T) {
	tests := []struct {
		name     string
		trace    string
		dir      string
		from     int
		paths    []string
		executed []string
	}{
		{"both prefixes, errors skipped", `100  execve("/entry.sh", ["/entry.sh"], 0x7ffd /* 4 vars */) = 0
100  openat(AT_FDCWD, "/etc/ld.so.cache", O_RDONLY|O_CLOEXEC) = 3
[pid  101] openat(AT_FDCWD, "/lib/libc.so.6", O_RDONLY) = 4
[pid  101] openat(AT_FDCWD, "/etc/missing.conf", O_RDONLY) = -1 ENOENT (No such file or directory)
[pid  101] stat("/etc/passwd", {st_mode=S_IFREG|0644, st_size=1200, ...}) = 0
[pid  101] access("/etc/nope", F_OK) = -1 ENOENT (No such file or directory)
100  execve("/bin/nope", ["nope"], 0x7ffd /* 4 vars */) = -1 ENOENT (No such file or directory)
`, "", 0, []string{"/entry.sh", "/etc/ld.so.cache", "/etc/passwd", "/lib/libc.so.6"}, []string{"/entry.sh"}},
		{"relative paths against the working directory", `7  openat(AT_FDCWD, "conf/app.yaml", O_RDONLY) = 3
7  openat(AT_FDCWD, "../etc/extra.conf", O_RDONLY) = 4
7  openat(AT_FDCWD, "", O_RDONLY) = -1 ENOENT (No such file or directory)
7  newfstatat(AT_FDCWD, "", {st_mode=S_IFDIR|0755, ...}, AT_EMPTY_PATH) = 0
7  openat(5, "rel/path", O_RDONLY) = 6
7  openat(5, "/abs/from/fd", O_RDONLY) = 6
`, "/app", 0, []string{"/abs/from/fd", "/app/conf/app.yaml", "/etc/extra.conf"}, nil},
		{"relative paths without a working directory", `7  openat(AT_FDCWD, "conf/app.yaml", O_RDONLY) = 3
7  openat(5, "rel/path", O_RDONLY) = 6
7  openat(5, "/abs/from/fd", O_RDONLY) = 6
`, "", 0, []string{"/abs/from/fd"}, nil},
		{"unfinished is taken, resumed is ignored", `200  clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 201
[pid  201] openat(AT_FDCWD, "/etc/slow.conf", O_RDONLY <unfinished ...>
[pid  200] openat(AT_FDCWD, "/etc/fast.conf", O_RDONLY) = 3
[pid  201] <... openat resumed>) = -1 ENOENT (No such file or directory)
`, "", 0, []string{"/etc/fast.conf", "/etc/slow.conf"}, nil},
		{"escaped characters", `9  openat(AT_FDCWD, "/tmp/a\"b c", O_RDONLY) = 3
9  openat(AT_FDCWD, "/data/caf\303\251.txt", O_RDONLY) = 4
9  openat(AT_FDCWD, "/data/line\nbreak", O_RDONLY) = 5
9  openat(AT_FDCWD, "/data/tab\tx", O_RDONLY) = 6
`, "", 0, []string{"/data/café.txt", "/data/line\nbreak", "/data/tab\tx", "/tmp/a\"b c"}, nil},
		{"lines before from are not read", `1  openat(AT_FDCWD, "/early/one", O_RDONLY) = 3
1  execve("/early/tool", ["tool"], 0x1 /* 1 var */) = 0
1  openat(AT_FDCWD, "/late/one", O_RDONLY) = 3
1  execve("/late/server", ["server"], 0x1 /* 1 var */) = 0
`, "", 2, []string{"/late/one", "/late/server"}, []string{"/late/server"}},
		{"from beyond the trace", `1  openat(AT_FDCWD, "/early/one", O_RDONLY) = 3
`, "", 10, nil, nil},
		{"cleaned, deduplicated, noise ignored", `5  openat(AT_FDCWD, "/a//b/../c", O_RDONLY) = 3
5  openat(AT_FDCWD, "/a/c", O_RDONLY) = 4
5  stat("/etc/", {st_mode=S_IFDIR|0755, ...}) = 0
5  --- SIGCHLD {si_signo=SIGCHLD} ---
openat(AT_FDCWD, "/no/prefix", O_RDONLY) = 3
5  +++ exited with 0 +++
`, "", 0, []string{"/a/c", "/etc"}, nil},
		{"execve from both prefixes, sorted, once", `3  execve("/usr/bin/b", ["b"], 0x1 /* 1 var */) = 0
[pid  3] execve("/usr/bin/a", ["a"], 0x1 /* 1 var */) = 0
[pid  3] execve("/usr/bin/a", ["a"], 0x1 /* 1 var */) = 0
[pid 4] execve("/usr/bin/c", ["c"], 0x1 /* 1 var */ <unfinished ...>
`, "", 0, []string{"/usr/bin/a", "/usr/bin/b", "/usr/bin/c"}, []string{"/usr/bin/a", "/usr/bin/b", "/usr/bin/c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths, executed := TouchedPaths([]byte(tt.trace), tt.dir, tt.from)
			if !reflect.DeepEqual(paths, tt.paths) {
				t.Errorf("paths = %q, want %q", paths, tt.paths)
			}
			if !reflect.DeepEqual(executed, tt.executed) {
				t.Errorf("executed = %q, want %q", executed, tt.executed)
			}
		})
	}
}

func TestTouchedPathsChdir(t *testing.T) {
	tests := []struct {
		name     string
		trace    string
		dir      string
		paths    []string
		executed []string
	}{
		{"chdir to a path", `7  chdir("/srv") = 0
7  openat(AT_FDCWD, "app.conf", O_RDONLY) = 3
`, "/app", []string{"/srv", "/srv/app.conf"}, nil},
		{"relative chdir", `7  chdir("sub") = 0
7  openat(AT_FDCWD, "x", O_RDONLY) = 3
`, "/app", []string{"/app/sub", "/app/sub/x"}, nil},
		{"failed chdir changes nothing", `7  chdir("/nope") = -1 ENOENT (No such file or directory)
7  openat(AT_FDCWD, "a", O_RDONLY) = 3
`, "/app", []string{"/app/a"}, nil},
		{"chdir cut in two", `7  chdir("/srv" <unfinished ...>
7  <... chdir resumed>) = 0
7  openat(AT_FDCWD, "a", O_RDONLY) = 3
`, "/app", []string{"/srv", "/srv/a"}, nil},
		{"chdir cut in two that failed", `7  chdir("/srv" <unfinished ...>
7  <... chdir resumed>) = -1 ENOENT (No such file or directory)
7  openat(AT_FDCWD, "a", O_RDONLY) = 3
`, "/app", []string{"/app/a", "/srv"}, nil},
		{"fchdir makes the directory unknown", `7  fchdir(3) = 0
7  openat(AT_FDCWD, "x", O_RDONLY) = 3
7  openat(AT_FDCWD, "/abs/ok", O_RDONLY) = 4
`, "/app", []string{"/abs/ok"}, nil},
		{"per process, a child starts with the cwd of its parent at the clone", `7  clone(child_stack=NULL, flags=SIGCHLD) = 8
8  openat(AT_FDCWD, "c.conf", O_RDONLY) = 3
7  chdir("/srv") = 0
7  openat(AT_FDCWD, "a.conf", O_RDONLY) = 3
8  openat(AT_FDCWD, "d.conf", O_RDONLY) = 3
`, "/app", []string{"/app/c.conf", "/app/d.conf", "/srv", "/srv/a.conf"}, nil},
		{"a child cloned after the chdir has the new directory", `9  chdir("/srv") = 0
9  clone(child_stack=NULL, flags=SIGCHLD) = 10
10  openat(AT_FDCWD, "e.conf", O_RDONLY) = 3
9  chdir("/other") = 0
10  openat(AT_FDCWD, "f.conf", O_RDONLY) = 3
`, "/app", []string{"/other", "/srv", "/srv/e.conf", "/srv/f.conf"}, nil},
		{"clone cut in two", `9  chdir("/srv") = 0
[pid  9] clone(child_stack=NULL, flags=SIGCHLD <unfinished ...>
[pid  9] <... clone resumed>) = 10
[pid 10] openat(AT_FDCWD, "e.conf", O_RDONLY) = 3
`, "/app", []string{"/srv", "/srv/e.conf"}, nil},
		{"a process with no clone record takes the directory of the line before", `7  chdir("/srv") = 0
8  openat(AT_FDCWD, "g.conf", O_RDONLY) = 3
`, "/app", []string{"/srv", "/srv/g.conf"}, nil},
		{"execve cancelled by its resumed line", `[pid 4] execve("/usr/bin/c", ["c"], 0x1 /* 1 var */ <unfinished ...>
[pid 4] <... execve resumed>) = -1 ENOENT (No such file or directory)
[pid 5] execve("/usr/bin/d", ["d"], 0x1 /* 1 var */ <unfinished ...>
[pid 5] <... execve resumed>) = 0
`, "", []string{"/usr/bin/d"}, []string{"/usr/bin/d"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths, executed := TouchedPaths([]byte(tt.trace), tt.dir, 0)
			if !reflect.DeepEqual(paths, tt.paths) {
				t.Errorf("paths = %q, want %q", paths, tt.paths)
			}
			if !reflect.DeepEqual(executed, tt.executed) {
				t.Errorf("executed = %q, want %q", executed, tt.executed)
			}
		})
	}
}

func TestNotCarried(t *testing.T) {
	for _, p := range []string{"/proc", "/proc/self/fd/1", "/sys/kernel/mm", "/dev/null", "/dev/stderr", "/.ops-trace", "/.ops-trace/files"} {
		if !NotCarried(p) {
			t.Errorf("NotCarried(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"/", "/process", "/proc2/x", "/devices", "/etc/hosts", "/usr/sys/x", "/.ops-trace2"} {
		if NotCarried(p) {
			t.Errorf("NotCarried(%q) = true, want false", p)
		}
	}
}

func TestIsConsoleLink(t *testing.T) {
	for _, p := range []string{"/dev/stderr", "/dev/stdout", "/proc/self/fd/1", "/proc/self/fd/2", "/dev/./stderr"} {
		if !IsConsoleLink(p) {
			t.Errorf("IsConsoleLink(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"/var/log/nginx/error.log", "/dev/../etc/passwd", "/procedures/x"} {
		if IsConsoleLink(p) {
			t.Errorf("IsConsoleLink(%q) = true, want false", p)
		}
	}
}

func TestCarryConsoleLink(t *testing.T) {
	root := t.TempDir()
	if err := CarryConsoleLink(root, "/var/log/nginx/error.log"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "var/log/nginx/error.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Size() != 0 {
		t.Errorf("want an empty regular file, got mode %v size %d", info.Mode(), info.Size())
	}

	// a sysroot that is a file cannot hold a directory: the error is returned, with the path
	blocker := writeFile(t, root, "blocker", []byte("x"))
	err = CarryConsoleLink(blocker, "/var/log/error.log")
	if err == nil || !strings.Contains(err.Error(), "/var/log/error.log") {
		t.Errorf("CarryConsoleLink into a file = %v, want an error naming the path", err)
	}
}

func TestResolveLink(t *testing.T) {
	tests := []struct {
		p    string
		link string
		want string
	}{
		{"/usr/bin/x", "../lib/y", "/usr/lib/y"},
		{"/usr/bin/x", "y", "/usr/bin/y"},
		{"/usr/bin/x", "/opt/z/../w", "/opt/w"},
		{"/x", "../../y", "/y"},
	}
	for _, tt := range tests {
		if got := ResolveLink(tt.p, tt.link); got != tt.want {
			t.Errorf("ResolveLink(%q, %q) = %q, want %q", tt.p, tt.link, got, tt.want)
		}
	}
}

func TestCopyHostFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/src", []byte("content"))
	writeFile(t, root, "b/taken", []byte("old"))

	if err := CopyHostFile(root, "/a/src", "/b/c/dst"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "b/c/dst"))
	if err != nil || string(got) != "content" {
		t.Errorf("copy = %q, %v; want content", got, err)
	}

	if err := CopyHostFile(root, "/a/src", "/b/taken"); err != nil {
		t.Errorf("an existing destination is not an error, got %v", err)
	}
	got, _ = os.ReadFile(filepath.Join(root, "b/taken"))
	if string(got) != "old" {
		t.Errorf("an existing destination was overwritten: %q", got)
	}

	err = CopyHostFile(root, "/a/missing", "/x/y/dst")
	if err == nil || !strings.Contains(err.Error(), filepath.Join(root, "a/missing")) || !errors.Is(err, os.ErrNotExist) {
		t.Errorf("CopyHostFile with a missing source = %v, want an error naming the source and wrapping ErrNotExist", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "x")); err == nil {
		t.Errorf("a missing source created the destination directory")
	}
}

func TestInSysroot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "opt/app/real", []byte("x"))
	for link, target := range map[string]string{
		"usr/bin/abs":   "/opt/app/real",
		"usr/bin/rel":   "../../opt/app/real",
		"usr/bin/chain": "abs",
		"usr/bin/dead":  "/nowhere",
		"usr/bin/ping":  "pong",
		"usr/bin/pong":  "ping",
	} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(link)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		p    string
		want string
	}{
		{"/opt/app/real", "/opt/app/real"},
		{"/usr/bin/abs", "/opt/app/real"},
		{"/usr/bin/rel", "/opt/app/real"},
		{"/usr/bin/chain", "/opt/app/real"},
		{"/usr/bin/dead", "/nowhere"},
		{"/not/there", "/not/there"},
		{"/usr/bin/ping", "/usr/bin/ping"},
	}
	for _, tt := range tests {
		if got := InSysroot(root, tt.p); got != tt.want {
			t.Errorf("InSysroot(%q) = %q, want %q", tt.p, got, tt.want)
		}
	}
}

func TestInSysrootDirectoryLinks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "usr/lib/libx.so.1", []byte("x"))
	for _, l := range [][2]string{
		{"lib", "/usr/lib"},
		{"usr/lib/libx.so", "libx.so.1"},
		{"escape", "../../../../usr/lib"},
		{"a", "b"},
		{"b", "c/../usr"},
		{"deep/lib64", "/lib"},
	} {
		link, target := l[0], l[1]
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(link)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		p    string
		want string
	}{
		{"/lib/libx.so", "/usr/lib/libx.so.1"},
		{"/lib/libx.so.1", "/usr/lib/libx.so.1"},
		{"/escape/libx.so", "/usr/lib/libx.so.1"},
		{"/deep/lib64/libx.so", "/usr/lib/libx.so.1"},
		{"/a/lib/libx.so", "/usr/lib/libx.so.1"},
		{"/lib/../lib/libx.so", "/usr/lib/libx.so.1"},
		{"/../../lib/libx.so", "/usr/lib/libx.so.1"},
		{"/", "/"},
	}
	for _, tt := range tests {
		if got := InSysroot(root, tt.p); got != tt.want {
			t.Errorf("InSysroot(%q) = %q, want %q", tt.p, got, tt.want)
		}
	}
}

func TestInterpreterOfProgramHeader(t *testing.T) {
	root := t.TempDir()
	file := writeFile(t, root, "sstripped", buildELFNoSections("/lib/ld-musl-x86_64.so.1"))
	if got := InterpreterOf(file); got != "/lib/ld-musl-x86_64.so.1" {
		t.Errorf("InterpreterOf(ELF with PT_INTERP and no sections) = %q, want /lib/ld-musl-x86_64.so.1", got)
	}
}

func TestInterpreterOf(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		"elf":          buildELF("/lib/ld-musl-x86_64.so.1"),
		"elf-glibc":    buildELF("/lib64/ld-linux-x86-64.so.2"),
		"static":       buildELF(""),
		"sh":           []byte("#!/bin/sh\necho hi\n"),
		"env":          []byte("#!/usr/bin/env python3\nprint(1)\n"),
		"spaced":       []byte("#! /bin/bash -e\n"),
		"no-newline":   []byte("#!/bin/ash"),
		"empty-bang":   []byte("#!\nexit\n"),
		"text":         []byte("hello world\n"),
		"one-byte":     []byte("#"),
		"empty":        {},
		"late-bang":    []byte("\n#!/bin/sh\n"),
		"bang-in-text": []byte(" #!/bin/sh\n"),
	}
	for name, data := range files {
		writeFile(t, root, name, data)
	}

	tests := map[string]string{
		"elf":          "/lib/ld-musl-x86_64.so.1",
		"elf-glibc":    "/lib64/ld-linux-x86-64.so.2",
		"static":       "",
		"sh":           "/bin/sh",
		"env":          "/usr/bin/env",
		"spaced":       "/bin/bash",
		"no-newline":   "/bin/ash",
		"empty-bang":   "",
		"text":         "",
		"one-byte":     "",
		"empty":        "",
		"late-bang":    "",
		"bang-in-text": "",
		"missing":      "",
	}
	for name, want := range tests {
		if got := InterpreterOf(filepath.Join(root, name)); got != want {
			t.Errorf("InterpreterOf(%s) = %q, want %q", name, got, want)
		}
	}
}

func TestCarryInterpreters(t *testing.T) {
	root := t.TempDir()
	// the files are put in the sysroot only when carried, as the copy out of a container would
	contents := map[string][]byte{
		"/usr/local/bin/wrapper":   []byte("#!/bin/sh\n"),
		"/bin/sh":                  buildELF("/lib/ld-musl-x86_64.so.1"),
		"/lib/ld-musl-x86_64.so.1": buildELF(""),
	}
	writeFile(t, root, "app/run", []byte("#!/usr/local/bin/wrapper --flag\n"))

	var carried, notes []string
	carry := func(interp string) error {
		carried = append(carried, interp)
		writeFile(t, root, interp, contents[interp])
		return nil
	}
	note := func(format string, args ...any) {
		notes = append(notes, fmt.Sprintf(format, args...))
	}
	if err := CarryInterpreters(root, []string{"/app/run"}, carry, note); err != nil {
		t.Fatal(err)
	}

	wantCarried := []string{"/usr/local/bin/wrapper", "/bin/sh", "/lib/ld-musl-x86_64.so.1"}
	if !reflect.DeepEqual(carried, wantCarried) {
		t.Errorf("carried = %q, want %q", carried, wantCarried)
	}
	wantNotes := []string{
		"/app/run is run by /usr/local/bin/wrapper\n",
		"/usr/local/bin/wrapper is run by /bin/sh\n",
		"/bin/sh is run by /lib/ld-musl-x86_64.so.1\n",
	}
	if !reflect.DeepEqual(notes, wantNotes) {
		t.Errorf("notes = %q, want %q", notes, wantNotes)
	}
}

func TestCarryInterpretersLinksAndRepeats(t *testing.T) {
	root := t.TempDir()
	loader := "/lib/ld-musl-x86_64.so.1"
	writeFile(t, root, "opt/app/real", buildELF(loader))
	writeFile(t, root, "app/other", buildELF(loader))
	if err := os.MkdirAll(filepath.Join(root, "usr/bin"), 0755); err != nil {
		t.Fatal(err)
	}
	// an absolute link: read on this machine it would lead outside the sysroot
	if err := os.Symlink("/opt/app/real", filepath.Join(root, "usr/bin/app")); err != nil {
		t.Fatal(err)
	}

	var carried []string
	carry := func(interp string) error {
		carried = append(carried, interp)
		return nil
	}
	if err := CarryInterpreters(root, []string{"/usr/bin/app", "/opt/app/real", "/app/other", "/app/missing"}, carry, nil); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(carried, []string{loader}) {
		t.Errorf("carried = %q, want the loader once", carried)
	}

	carried = nil
	if err := CarryInterpreters(root, []string{"/usr/bin/app"}, carry, nil); err != nil {
		t.Fatal(err)
	}
	if len(carried) != 1 {
		t.Errorf("carried = %q, want one entry", carried)
	}
}

func TestCarryInterpretersError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "app/run", []byte("#!/usr/local/bin/wrapper\n"))
	writeFile(t, root, "usr/local/bin/wrapper", []byte("#!/bin/sh\n"))

	boom := errors.New("boom")
	var carried []string
	carry := func(interp string) error {
		carried = append(carried, interp)
		return boom
	}
	err := CarryInterpreters(root, []string{"/app/run"}, carry, nil)
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "/usr/local/bin/wrapper") {
		t.Errorf("CarryInterpreters = %v, want the error of carry naming /usr/local/bin/wrapper", err)
	}
	if !reflect.DeepEqual(carried, []string{"/usr/local/bin/wrapper"}) {
		t.Errorf("carried = %q, want the walk to stop at the first error", carried)
	}
}

func TestWhereIs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "usr/bin/server", []byte("x"))
	writeFile(t, root, "opt/bin/tool", []byte("x"))

	tests := []struct {
		name    string
		program string
		env     []string
		want    string
	}{
		{"absolute stays", "/srv/run", nil, "/srv/run"},
		{"default path", "server", nil, "/usr/bin/server"},
		{"path of the image", "tool", []string{"HOME=/root", "PATH=/opt/bin:/usr/bin"}, "/opt/bin/tool"},
		{"not on the default path", "tool", nil, "tool"},
		{"last PATH wins", "tool", []string{"PATH=/opt/bin", "PATH=/usr/bin"}, "tool"},
		{"nowhere", "ghost", []string{"PATH=/usr/bin"}, "ghost"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WhereIs(root, tt.program, tt.env); got != tt.want {
				t.Errorf("WhereIs = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestListeningPorts(t *testing.T) {
	tcp := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0
   1: 0100007F:0050 0100007F:C350 01 00000000:00000000 00:00000000 00000000     0        0 12346 1 0000000000000000 100 0 0 10 0
   2: 0100007F:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12347 1 0000000000000000 100 0 0 10 0
`
	if got := ListeningPorts(tcp); !reflect.DeepEqual(got, []int{8080, 22}) {
		t.Errorf("ListeningPorts(tcp) = %v, want [8080 22]", got)
	}
	tcp6 := `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:0050 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 22222 1 0000000000000000 100 0 0 10 0
`
	if got := ListeningPorts(tcp6); !reflect.DeepEqual(got, []int{80}) {
		t.Errorf("ListeningPorts(tcp6) = %v, want [80]", got)
	}
	if got := ListeningPorts(""); got != nil {
		t.Errorf("ListeningPorts(empty) = %v, want nil", got)
	}
}

func TestFinalProgram(t *testing.T) {
	tests := []struct {
		name    string
		trace   string
		pid     string
		program string
		line    int
		found   bool
	}{
		{"entrypoint script execs the server", `10  execve("/docker-entrypoint.sh", ["/docker-entrypoint.sh", "nginx", "-g", "daemon off;"], 0x7ff /* 8 vars */) = 0
10  openat(AT_FDCWD, "/etc/ld.so.cache", O_RDONLY) = 3
10  clone(child_stack=NULL, flags=CLONE_CHILD_CLEARTID|SIGCHLD) = 11
[pid  11] execve("/usr/bin/id", ["id", "-u"], 0x55 /* 8 vars */) = 0
[pid  11] +++ exited with 0 +++
10  --- SIGCHLD ---
10  execve("/usr/sbin/nginx", ["nginx", "-g", "daemon off;"], 0x55 /* 8 vars */) = 0
10  openat(AT_FDCWD, "/etc/nginx/nginx.conf", O_RDONLY) = 4
`, "10", "/usr/sbin/nginx", 6, true},
		{"a later tool that exited is not the program", `20  execve("/bin/sh", ["sh", "-c", "x"], 0x1 /* 1 var */) = 0
20  clone(child_stack=NULL, flags=SIGCHLD) = 21
[pid  21] execve("/usr/bin/server", ["server"], 0x1 /* 1 var */) = 0
20  clone(child_stack=NULL, flags=SIGCHLD) = 22
[pid  22] execve("/bin/true", ["true"], 0x1 /* 1 var */) = 0
[pid  22] +++ exited with 0 +++
`, "21", "/usr/bin/server", 2, true},
		{"nothing running: the last one started", `30  execve("/bin/a", ["a"], 0x1 /* 1 var */) = 0
30  +++ exited with 0 +++
31  execve("/bin/b", ["b"], 0x1 /* 1 var */) = 0
31  +++ killed by SIGKILL +++
`, "31", "/bin/b", 2, true},
		{"an exited pid that execs again is running", `50  execve("/bin/a", ["a"], 0x1 /* 1 var */) = 0
50  +++ exited with 0 +++
[pid  50] execve("/opt/my\040app/run", ["run"], 0x1 /* 1 var */) = 0
`, "50", "/opt/my app/run", 2, true},
		{"execve cut in two is the program", `10  execve("/bin/sh", ["sh"], 0x1 /* 1 var */) = 0
10  clone(child_stack=NULL, flags=SIGCHLD) = 11
[pid  11] execve("/usr/bin/server", ["server"], 0x1 /* 1 var */ <unfinished ...>
10  openat(AT_FDCWD, "/etc/x", O_RDONLY) = 3
[pid  11] <... execve resumed>) = 0
`, "11", "/usr/bin/server", 2, true},
		{"unfinished execve never resumed counts", `30  execve("/bin/a", ["a"], 0x1 /* 1 var */) = 0
30  execve("/bin/b", ["b"], 0x1 /* 1 var */ <unfinished ...>
`, "30", "/bin/b", 1, true},
		{"execve cut in two that failed is cancelled", `20  execve("/bin/a", ["a"], 0x1 /* 1 var */) = 0
20  execve("/bin/b", ["b"], 0x1 /* 1 var */ <unfinished ...>
20  <... execve resumed>) = -1 ENOENT (No such file or directory)
`, "20", "/bin/a", 0, true},
		{"cancelled execve of an exited process gives back the exit", `60  execve("/bin/a", ["a"], 0x1 /* 1 var */) = 0
60  +++ exited with 0 +++
60  execve("/bin/b", ["b"], 0x1 /* 1 var */ <unfinished ...>
60  <... execve resumed>) = -1 ENOENT (No such file or directory)
61  execve("/bin/c", ["c"], 0x1 /* 1 var */) = 0
`, "61", "/bin/c", 4, true},
		{"failed execve does not count", `40  execve("/bin/x", ["x"], 0x1 /* 1 var */) = -1 ENOENT (No such file or directory)
`, "", "", -1, false},
		{"empty trace", ``, "", "", -1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pid, program, line, found := FinalProgram([]byte(tt.trace))
			if pid != tt.pid || program != tt.program || line != tt.line || found != tt.found {
				t.Errorf("FinalProgram = %q, %q, %d, %v; want %q, %q, %d, %v", pid, program, line, found, tt.pid, tt.program, tt.line, tt.found)
			}
		})
	}
}

func TestSplitNul(t *testing.T) {
	tests := []struct {
		raw  string
		want []string
	}{
		{"a\x00b\x00\x00c\x00", []string{"a", "b", "c"}},
		{"/usr/sbin/nginx\x00-g\x00daemon off;\x00", []string{"/usr/sbin/nginx", "-g", "daemon off;"}},
		{"no-terminator", []string{"no-terminator"}},
		{"\x00", nil},
		{"", nil},
	}
	for _, tt := range tests {
		if got := SplitNul([]byte(tt.raw)); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SplitNul(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestBelongsToTheContainer(t *testing.T) {
	want := []string{"HOSTNAME", "SHLVL", "OLDPWD", "_"}
	if !reflect.DeepEqual(BelongsToTheContainer, want) {
		t.Errorf("BelongsToTheContainer = %q, want %q", BelongsToTheContainer, want)
	}
}

func TestConfigurationFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		"etc/nginx/nginx.conf", "etc/passwd", "etc/ssl/openssl.cnf",
		"etc/hosts", "etc/hostname", "etc/ssl/certs/ca.pem", "etc/ld.so.cache", "etc/ld.so.conf.d/x.conf",
		"app/config.yaml", "app/app.yml", "app/a.toml", "app/b.properties", "app/c.ini", "app/d.cnf", "app/e.conf",
		"app/data.json", "app/UPPER.CONF", "lib/libc.so",
	} {
		writeFile(t, root, name, []byte("x"))
	}
	if err := os.MkdirAll(filepath.Join(root, "etc/dir.conf"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("passwd", filepath.Join(root, "etc/link")); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"/app/a.toml", "/app/app.yml", "/app/b.properties", "/app/c.ini", "/app/config.yaml", "/app/d.cnf", "/app/e.conf",
		"/etc/nginx/nginx.conf", "/etc/passwd", "/etc/ssl/openssl.cnf",
	}
	if got := ConfigurationFiles(root); !reflect.DeepEqual(got, want) {
		t.Errorf("ConfigurationFiles = %q, want %q", got, want)
	}

	if got := ConfigurationFiles(root + "/"); !reflect.DeepEqual(got, want) {
		t.Errorf("ConfigurationFiles with a trailing slash = %q, want %q", got, want)
	}

	if got := ConfigurationFiles(t.TempDir()); got != nil {
		t.Errorf("ConfigurationFiles(empty) = %q, want nil", got)
	}
	if got := ConfigurationFiles(filepath.Join(root, "does-not-exist")); got != nil {
		t.Errorf("ConfigurationFiles(missing) = %q, want nil", got)
	}
}

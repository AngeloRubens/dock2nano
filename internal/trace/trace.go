// Package trace reads the log that strace writes while an image runs, and decides which files the
// program reached. It has no dependency on Docker: it works on bytes and on a directory (the
// sysroot) that the caller filled.
//
// It is a port of the pure parts of nanovms/ops#1802 (cmd/extract_from_docker_trace.go). What
// calls the Docker client (getting strace, running the container, copying files out of it, asking
// it for /proc) stays with the caller.
//
// The rule: the package carries the files the program reaches.
//
// A program reaches a file in two ways. It asks for it, with a call that names it: open, stat,
// readlink, execve. An image run under strace gives that list, from the first instruction on: the
// libraries the program is linked against and the ones it loads by name, the configuration it
// reads, the data it opens. And the kernel asks for a file on its behalf when it executes a
// program: the interpreter that program names, which is the dynamic loader of an ELF file or the
// program after #! in a script. No call of the program names that file, so it is added for every
// file the program executed, and for the interpreter of that interpreter, and so on.
//
// Nothing else of the image is carried.
package trace

import (
	"debug/elf"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// TraceMount is where the directory holding strace, and the list it writes, is seen from inside
// the container.
const TraceMount = "/.ops-trace"

// StraceImage is the image strace is taken from. Alpine's is built against musl alone, which
// makes it a program that can be carried into an image of any other distribution along with the
// musl loader.
const StraceImage = "alpine:3.20"

// WrapCommand puts strace in front of what the image starts. It follows every process and thread
// the program makes and writes down the calls that name a file. mount is where the tracer
// directory is seen from inside the container (TraceMount), and loader is the file name of the
// musl loader that sits in mount/out. argv is not modified.
func WrapCommand(mount, loader string, argv []string) []string {
	wrapped := []string{
		mount + "/out/" + loader, "--library-path", mount + "/out", mount + "/out/strace",
		"-f", "-q", "-s", "4096", "-e", "trace=file", "-e", "signal=none",
		"-o", mount + "/files", "--",
	}
	return append(wrapped, argv...)
}

// TraceCall matches a line of a strace -f log that starts a call, with either form of the process
// prefix, and captures the name of the call. Lines that resume a call ("<... openat resumed>"),
// signals and exits do not match.
var TraceCall = regexp.MustCompile(`^(?:\[pid +\d+\] +|\d+ +)([a-z_0-9]+)\(`)

// FirstQuoted reads the path a call names: its first argument, or the one after a descriptor.
// rest is the line after the name of the call and its opening parenthesis. It gives the path
// unquoted when strace's escapes are valid for Go, what follows the closing quote, and whether a
// path that is not absolute is relative to the working directory (AT_FDCWD or no descriptor at
// all) rather than to a descriptor the reader does not know.
func FirstQuoted(rest string) (name string, after string, relativeToCwd bool, ok bool) {
	relativeToCwd = true
	if strings.HasPrefix(rest, "AT_FDCWD, ") {
		rest = rest[len("AT_FDCWD, "):]
	} else if !strings.HasPrefix(rest, "\"") {
		// the first argument is a descriptor: a path that is not absolute has no telling
		relativeToCwd = false
		if comma := strings.Index(rest, ", "); comma >= 0 {
			rest = rest[comma+2:]
		}
	}
	if !strings.HasPrefix(rest, "\"") {
		return "", "", false, false
	}

	end := 1
	for end < len(rest) && rest[end] != '"' {
		if rest[end] == '\\' {
			end++
		}
		end++
	}
	if end >= len(rest) {
		return "", "", false, false
	}
	name = rest[1:end]
	if unquoted, err := strconv.Unquote("\"" + name + "\""); err == nil {
		name = unquoted
	}

	return name, rest[end+1:], relativeToCwd, true
}

// ResultOf is what a call came back with, and whether the trace has it. after is what FirstQuoted
// returned as the rest of the line.
func ResultOf(after string) (string, bool) {
	at := strings.LastIndex(after, ") = ")
	if at < 0 {
		return "", false
	}
	result := after[at+len(") = "):]
	if space := strings.IndexAny(result, " \t"); space >= 0 {
		result = result[:space]
	}
	return result, true
}

// TouchedPaths reads what strace wrote and returns the files named by the calls that found them.
// A call that came back with an error found nothing; one the trace cut in two - unfinished, with
// its end written on another line - is taken, since a file looked for is a file that may be needed.
// A path that is not absolute is read against workingDir when it was relative to the working
// directory, and left out when it was relative to a file descriptor that is not known here.
// What happened before line from is not read: a launcher script runs tools before it hands over to
// the program, and a unikernel runs no script, so what they opened is not what the program needs.
// Both results are cleaned, without duplicates and sorted; executed holds the paths of execve.
//
// The working directory is followed per process (the pid that opens each line of strace -f). Every
// process starts in workingDir, except one that a clone, fork or vfork line names as its child: it
// starts with the directory its parent has at that line. strace -e trace=file does not print
// clone, so a process whose first line is not preceded by such a record starts with the directory
// of the process that wrote the line before its first one (the likeliest parent), or workingDir
// when it is the very first line. A successful chdir sets the directory (a relative one is read
// against the current directory; one cut in two is applied when its resumed line says 0). A
// successful fchdir sets it to unknown, since the path of a descriptor is not in the trace; a
// relative path is left out while the directory is unknown, as with an empty workingDir.
//
// An execve counts when it succeeded on one line, or when it is unfinished and its resumed line
// has not said otherwise: a resumed line with a result other than 0 cancels it, from both results.
// FinalProgram follows the same rule.
func TouchedPaths(trace []byte, workingDir string, from int) (paths []string, executed []string) {
	seen := map[string]bool{}
	ran := map[string]int{}
	cwd := map[string]string{}
	waitingExec := map[string]string{}
	waitingChdir := map[string]string{}
	active := ""

	for i, line := range strings.Split(string(trace), "\n") {
		if i < from {
			continue
		}
		prefix := pidPrefix.FindStringSubmatch(line)
		if prefix == nil {
			continue
		}
		pid := prefix[1] + prefix[2]
		if _, known := cwd[pid]; !known {
			if active != "" {
				cwd[pid] = cwd[active]
			} else {
				cwd[pid] = workingDir
			}
		}
		active = pid
		rest := line[len(prefix[0]):]

		if child := forkLine.FindStringSubmatch(line); child != nil {
			if _, known := cwd[child[3]]; !known {
				cwd[child[3]] = cwd[pid]
			}
			continue
		}

		if strings.HasPrefix(rest, "<... ") {
			result, found := ResultOf(rest)
			switch {
			case strings.HasPrefix(rest, "<... execve resumed>"):
				if waiting, ok := waitingExec[pid]; ok {
					delete(waitingExec, pid)
					if found && result != "0" {
						ran[waiting]--
					}
				}
			case strings.HasPrefix(rest, "<... chdir resumed>"), strings.HasPrefix(rest, "<... fchdir resumed>"):
				if dir, ok := waitingChdir[pid]; ok {
					delete(waitingChdir, pid)
					if found && result == "0" {
						cwd[pid] = dir
					}
				}
			}
			continue
		}

		call := TraceCall.FindStringSubmatch(line)
		if call == nil {
			continue
		}
		args := line[len(call[0]):]

		if call[1] == "fchdir" {
			if strings.Contains(args, "<unfinished ...>") {
				waitingChdir[pid] = ""
			} else if result, found := ResultOf(args); found && result == "0" {
				cwd[pid] = ""
			}
			continue
		}

		name, after, relativeToCwd, ok := FirstQuoted(args)
		if !ok {
			continue
		}
		unfinished := strings.Contains(after, "<unfinished ...>")
		if !unfinished {
			result, found := ResultOf(after)
			if !found || strings.HasPrefix(result, "-") {
				continue
			}
			if call[1] == "execve" && result != "0" {
				continue
			}
		}

		if !strings.HasPrefix(name, "/") {
			dir := cwd[pid]
			if !relativeToCwd || dir == "" || name == "" {
				continue
			}
			name = path.Join(dir, name)
		}
		name = path.Clean(name)

		switch call[1] {
		case "execve":
			ran[name]++
			if unfinished {
				waitingExec[pid] = name
			}
		case "chdir":
			seen[name] = true
			if unfinished {
				waitingChdir[pid] = name
			} else {
				cwd[pid] = name
			}
		default:
			seen[name] = true
		}
	}

	for p, n := range ran {
		if n > 0 {
			seen[p] = true
			executed = append(executed, p)
		}
	}
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	sort.Strings(executed)

	return paths, executed
}

// pidPrefix matches the process prefix of a line of strace -f, in either form, and captures the pid
// in the first or the second group.
var pidPrefix = regexp.MustCompile(`^(?:\[pid +(\d+)\] +|(\d+) +)`)

// forkLine matches a clone, clone3, fork or vfork that came back with the pid of the child, on one
// line or on the resumed line, and captures the pid of the caller (groups 1 or 2) and of the child
// (group 3).
var forkLine = regexp.MustCompile(`^(?:\[pid +(\d+)\] +|(\d+) +)(?:(?:clone3?|fork|vfork)\(|<\.\.\. (?:clone3?|fork|vfork) resumed>).*\) = ([0-9]+)`)

// NotCarried says whether a path is one the kernel of the machine makes or the container runtime
// puts in, which no package has to bring.
func NotCarried(p string) bool {
	for _, prefix := range []string{"/proc", "/sys", "/dev", TraceMount} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// IsConsoleLink says whether the target of a symbolic link is the console of the container
// (/dev/stderr, /dev/stdout, /proc/self/fd/1, ...). In the original this is the test
// NotCarried(link), so every target under /proc, /sys, /dev and the trace mount counts. A link to
// the console, like the error.log of the nginx image, is carried as an empty file, since the
// machine has no /dev/stderr; see CarryConsoleLink. The target is cleaned first.
func IsConsoleLink(p string) bool {
	return NotCarried(path.Clean(p))
}

// CarryConsoleLink puts an empty file in the sysroot under name, creating its directories, so that
// opening a log that was a link to the console works. A failure is returned, with the path it
// happened on.
func CarryConsoleLink(sysroot string, name string) error {
	if err := os.MkdirAll(filepath.Join(sysroot, path.Dir(name)), 0755); err != nil {
		return fmt.Errorf("carry console link %s: %w", name, err)
	}
	if err := os.WriteFile(filepath.Join(sysroot, name), nil, 0644); err != nil {
		return fmt.Errorf("carry console link %s: %w", name, err)
	}
	return nil
}

// ResolveLink gives the path a symbolic link found at p leads to: a link that is not absolute is
// read from the directory of p, and the result is cleaned. A package does not carry a symbolic
// link: an absolute one names a path of the machine that builds the image, not of the image. What
// a link leads to is carried instead, under the name of the link and under its own.
func ResolveLink(p string, link string) string {
	if !strings.HasPrefix(link, "/") {
		link = path.Join(path.Dir(p), link)
	}
	return path.Clean(link)
}

// CopyHostFile puts a copy of a file that is in the sysroot under another name. It does nothing,
// and returns nil, if the destination exists (even as a link). Any other failure (the source
// cannot be read, the destination cannot be written) is returned, wrapped with the path it
// happened on; a destination left half written is removed.
func CopyHostFile(sysroot string, from string, to string) error {
	source := filepath.Join(sysroot, from)
	destination := filepath.Join(sysroot, to)
	if _, err := os.Lstat(destination); err == nil {
		return nil
	}
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("copy %s: %w", source, err)
	}
	if err := os.MkdirAll(path.Dir(destination), 0755); err != nil {
		return fmt.Errorf("copy to %s: %w", destination, err)
	}
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("copy %s: %w", source, err)
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY, info.Mode()|0600)
	if err != nil {
		return fmt.Errorf("copy to %s: %w", destination, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(destination)
		return fmt.Errorf("copy %s to %s: %w", source, destination, err)
	}
	if err := out.Close(); err != nil {
		os.Remove(destination)
		return fmt.Errorf("copy to %s: %w", destination, err)
	}
	return nil
}

// InSysroot follows the symbolic links of a path that was carried, inside the sysroot rather than
// on this machine, and gives the path of the file they lead to. Every component is resolved, the
// directories on the way as well as the last one, from the root of the sysroot: an absolute link
// restarts there, and ".." at the root stays at the root, so the walk never leaves the sysroot.
// It follows at most 40 links and then gives the path it has reached.
func InSysroot(sysroot string, p string) string {
	parts := strings.Split(p, "/")
	var resolved []string
	links := 0

	for len(parts) > 0 {
		part := parts[0]
		parts = parts[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			if len(resolved) > 0 {
				resolved = resolved[:len(resolved)-1]
			}
			continue
		}

		candidate := append(append([]string{}, resolved...), part)
		host := filepath.Join(sysroot, filepath.FromSlash("/"+strings.Join(candidate, "/")))
		info, err := os.Lstat(host)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			resolved = candidate
			continue
		}
		link, err := os.Readlink(host)
		if err != nil {
			resolved = candidate
			continue
		}

		links++
		if links > 40 {
			return "/" + strings.Join(append(candidate, parts...), "/")
		}
		if strings.HasPrefix(link, "/") {
			resolved = nil
		}
		parts = append(strings.Split(link, "/"), parts...)
	}

	return "/" + strings.Join(resolved, "/")
}

// InterpreterOf is the file the kernel loads to run a program: the dynamic loader named by an ELF
// file, or the program a script names after #!. A file that names none, a static binary or a
// library, gives "". file is a path on this machine. The loader of an ELF file is read from its
// PT_INTERP program header, as the kernel does, so a file with no section headers has one too.
func InterpreterOf(file string) string {
	if f, err := elf.Open(file); err == nil {
		defer f.Close()
		for _, prog := range f.Progs {
			if prog.Type != elf.PT_INTERP {
				continue
			}
			raw, err := io.ReadAll(io.LimitReader(prog.Open(), 4096))
			if err != nil {
				return ""
			}
			interp, _, _ := strings.Cut(string(raw), "\x00")
			return interp
		}
		return ""
	}

	in, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer in.Close()
	head := make([]byte, 256)
	n, _ := in.Read(head)
	if n < 2 || head[0] != '#' || head[1] != '!' {
		return ""
	}
	line := strings.SplitN(string(head[2:n]), "\n", 2)[0]
	if fields := strings.Fields(line); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

// CarryInterpreters finds, for each file that was executed, the interpreter the kernel loads for
// it, then the interpreter of that one if it has one. It is the second half of the rule. The
// files are read from the sysroot (through InSysroot), so carry must put the interpreter in the
// sysroot before it returns: the original calls copyTouched with the single path, which copies it
// out of the container. note, when not nil, is told about each interpreter found, in the manner
// of fmt.Printf; the original prints "%s is run by %s\n" when verbose.
//
// If carry fails, the walk stops and the first error is returned, wrapped with the interpreter
// it was called for. Nothing is carried after it.
func CarryInterpreters(sysroot string, executed []string, carry func(interpreter string) error, note func(format string, args ...any)) error {
	done := map[string]bool{}
	queue := append([]string{}, executed...)

	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]

		interp := InterpreterOf(filepath.Join(sysroot, InSysroot(sysroot, p)))
		if interp == "" || done[interp] {
			continue
		}
		done[interp] = true

		if note != nil {
			note("%s is run by %s\n", p, interp)
		}
		if err := carry(interp); err != nil {
			return fmt.Errorf("carry %s: %w", interp, err)
		}
		queue = append(queue, interp)
	}

	return nil
}

// WhereIs finds a program the way a shell would, among the files already carried. A program with
// a path is given back as it is, and so is one that is nowhere on the path. The PATH is the last
// PATH= entry of env, or a default one.
func WhereIs(sysroot string, program string, env []string) string {
	if strings.HasPrefix(program, "/") {
		return program
	}
	searched := "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			searched = e[len("PATH="):]
		}
	}
	for _, dir := range strings.Split(searched, ":") {
		candidate := path.Join(dir, program)
		if _, err := os.Stat(filepath.Join(sysroot, candidate)); err == nil {
			return candidate
		}
	}
	return program
}

// ListeningPorts reads the text of /proc/net/tcp or /proc/net/tcp6 and gives the ports in the
// listening state, in the order of the table.
func ListeningPorts(table string) []int {
	var ports []int
	for _, line := range strings.Split(table, "\n") {
		fields := strings.Fields(line)
		// the fourth column is the state, and 0A is listening
		if len(fields) < 4 || fields[3] != "0A" {
			continue
		}
		if _, hex, found := strings.Cut(fields[1], ":"); found {
			if n, err := strconv.ParseUint(hex, 16, 32); err == nil {
				ports = append(ports, int(n))
			}
		}
	}
	return ports
}

var execveLine = regexp.MustCompile(`^(?:\[pid +(\d+)\] +|(\d+) +)execve\("((?:[^"\\]|\\.)*)", .*(\) = 0 *|<unfinished \.\.\.>)$`)
var exitedLine = regexp.MustCompile(`^(?:\[pid +(\d+)\] +|(\d+) +)\+\+\+ (?:exited|killed)`)

// FinalProgram is the program the image ends up running: the last one started that is still running
// when the trace ends, or the last one started when none is. An entrypoint script starts tools, and
// then the program, and the program is what a unikernel runs. It gives the process, the path the
// program was started by and the line of the trace (counted from 0) where it was. Only an execve
// that succeeded counts: on a single line, or cut in two by strace ("execve(... <unfinished ...>"
// with its result on a "<... execve resumed>" line), where it counts at the line of the first part
// unless the resumed line says it failed, which cancels it and gives back the state of the process
// before it. An unfinished one that is never resumed counts. TouchedPaths follows the same rule.
// When nothing was started, found is false and line is -1.
//
// The command line and the environment of the program are not in this trace (it is written with
// -e trace=file, and strace prints them only in part): the original reads them from /proc/<pid>/cmdline
// and /proc/<pid>/environ of the running container, which is for the caller to do.
func FinalProgram(trace []byte) (pid string, program string, line int, found bool) {
	type started struct {
		program string
		line    int
	}
	type before struct {
		previous    started
		hadPrevious bool
		wasGone     bool
	}
	last := map[string]started{}
	gone := map[string]bool{}
	waiting := map[string]before{}

	for i, text := range strings.Split(string(trace), "\n") {
		if m := execveLine.FindStringSubmatch(text); m != nil {
			process := m[1] + m[2]
			name := m[3]
			if unquoted, err := strconv.Unquote("\"" + name + "\""); err == nil {
				name = unquoted
			}
			if strings.HasPrefix(m[4], "<unfinished") {
				previous, had := last[process]
				waiting[process] = before{previous: previous, hadPrevious: had, wasGone: gone[process]}
			} else {
				delete(waiting, process)
			}
			last[process] = started{program: name, line: i}
			delete(gone, process)
		} else if prefix := pidPrefix.FindStringSubmatch(text); prefix != nil && strings.HasPrefix(text[len(prefix[0]):], "<... execve resumed>") {
			process := prefix[1] + prefix[2]
			state, ok := waiting[process]
			if !ok {
				continue
			}
			delete(waiting, process)
			if result, has := ResultOf(text); has && result != "0" {
				if state.hadPrevious {
					last[process] = state.previous
				} else {
					delete(last, process)
				}
				if state.wasGone {
					gone[process] = true
				}
			}
		} else if m := exitedLine.FindStringSubmatch(text); m != nil {
			gone[m[1]+m[2]] = true
		}
	}

	best, alive := -1, false
	for process, s := range last {
		running := !gone[process]
		if (running && !alive) || (running == alive && s.line > best) {
			pid, program, best, alive = process, s.program, s.line, running
		}
	}

	return pid, program, best, best >= 0
}

// SplitNul reads what /proc keeps as strings ended by a zero byte. Empty strings are dropped.
func SplitNul(raw []byte) []string {
	var parts []string
	for _, part := range strings.Split(string(raw), "\x00") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

// BelongsToTheContainer are the variables a container is given by the runtime or its shell, which
// the program of an image does not ask for.
var BelongsToTheContainer = []string{"HOSTNAME", "SHLVL", "OLDPWD", "_"}

// ConfigurationFiles says which of the files the package carries are the ones a user changes: what
// the program read from /etc, and the files named as configuration. A file that is put in the
// image with the same name in the configuration of the user replaces the one in the package.
// sysroot is a directory on this machine; the names are given as paths inside it, sorted. Only
// regular files count. The sysroot is cleaned first, so a trailing slash makes no difference.
func ConfigurationFiles(sysroot string) []string {
	var found []string

	sysroot = filepath.Clean(sysroot)
	filepath.Walk(sysroot, func(file string, info os.FileInfo, err error) error {
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(sysroot, file)
		if err != nil {
			return nil
		}
		name := "/" + filepath.ToSlash(rel)
		switch {
		case strings.HasPrefix(name, "/etc/ssl/certs/"), strings.HasPrefix(name, "/etc/ld.so"), name == "/etc/hosts", name == "/etc/hostname":
		case strings.HasPrefix(name, "/etc/"):
			found = append(found, name)
		case slices.Contains([]string{".conf", ".cnf", ".ini", ".yaml", ".yml", ".toml", ".properties"}, path.Ext(name)):
			found = append(found, name)
		}
		return nil
	})

	sort.Strings(found)

	return found
}

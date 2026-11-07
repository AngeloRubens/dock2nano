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

// TraceSet is what strace is asked to write down (-e trace=): the calls that name a file, and the
// ones that keep the working directory of each process (fchdir) and the parentage between processes
// (clone, clone3, fork, vfork), which TouchedPaths and FinalExec read.
const TraceSet = "%file,fchdir,clone,clone3,fork,vfork"

// WrapCommand puts strace in front of what the image starts. It follows every process and thread
// the program makes and writes down the calls of TraceSet. mount is where the tracer directory is
// seen from inside the container (TraceMount), and loader is the file name of the musl loader that
// sits in mount/out. argv is not modified.
func WrapCommand(mount, loader string, argv []string) []string {
	return WrapCommandAs(mount, loader, "", argv)
}

// WrapCommandAs is WrapCommand with strace's -u user (run the program as that user, which strace
// does after it has started as root) before the "--", when user is not empty.
func WrapCommandAs(mount, loader, user string, argv []string) []string {
	wrapped := []string{
		mount + "/out/" + loader, "--library-path", mount + "/out", mount + "/out/strace",
		"-f", "-q", "-s", "4096", "-e", "trace=" + TraceSet, "-e", "signal=none",
		"-o", mount + "/files",
	}
	if user != "" {
		wrapped = append(wrapped, "-u", user)
	}
	wrapped = append(wrapped, "--")
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

	name, after, ok = scanQuoted(rest)
	if !ok {
		return "", "", false, false
	}
	return name, after, relativeToCwd, true
}

// scanQuoted reads the string that rest starts with (rest[0] is the opening quote), unescaped, and
// gives what follows its closing quote.
func scanQuoted(rest string) (value string, after string, ok bool) {
	if !strings.HasPrefix(rest, "\"") {
		return "", "", false
	}
	end := 1
	for end < len(rest) && rest[end] != '"' {
		if rest[end] == '\\' {
			end++
		}
		end++
	}
	if end >= len(rest) {
		return "", "", false
	}
	return unescape(rest[1:end]), rest[end+1:], true
}

// unescape reads the escapes strace writes in a string: \\ \" \' \a \b \f \n \r \t \v, an octal
// escape of one to three digits (strace writes as few digits as it can: \33, \1, \0), and \x
// with one or two hex digits. An escape it does not know is left as it is, backslash included.
func unescape(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			out = append(out, c)
			continue
		}
		i++
		switch e := s[i]; {
		case e >= '0' && e <= '7':
			n, digits := 0, 0
			for digits < 3 && i < len(s) && s[i] >= '0' && s[i] <= '7' {
				n = n*8 + int(s[i]-'0')
				i++
				digits++
			}
			i--
			out = append(out, byte(n))
		case e == 'x':
			n, digits := 0, 0
			for digits < 2 && i+1 < len(s) && isHex(s[i+1]) {
				v, _ := strconv.ParseUint(s[i+1:i+2], 16, 8)
				n = n*16 + int(v)
				i++
				digits++
			}
			if digits == 0 {
				out = append(out, '\\', 'x')
			} else {
				out = append(out, byte(n))
			}
		default:
			switch e {
			case 'a':
				out = append(out, '\a')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'v':
				out = append(out, '\v')
			case '\\', '"', '\'':
				out = append(out, e)
			default:
				out = append(out, '\\', e)
			}
		}
	}
	return string(out)
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// ResultOf is what a call came back with, and whether the trace has it. after is what FirstQuoted
// returned as the rest of the line.
func ResultOf(after string) (string, bool) {
	at := resultMark.FindAllStringIndex(after, -1)
	if len(at) == 0 {
		return "", false
	}
	result := after[at[len(at)-1][1]:]
	if space := strings.IndexAny(result, " \t"); space >= 0 {
		result = result[:space]
	}
	return result, true
}

// resultMark is the ") = " before the result of a call. strace pads the spaces before the "=" so
// that results line up ("fchdir(5)                    = 0"), so any number of them is taken.
var resultMark = regexp.MustCompile(`\) +=[ \t]`)

// TouchedPaths reads what strace wrote and returns the files named by the calls that found them.
// A call that came back with an error found nothing; one the trace cut in two - unfinished, with
// its end written on another line - is taken, since a file looked for is a file that may be needed.
// A path that is not absolute is read against the working directory of the process that made the
// call when it was relative to the working directory, and left out when it was relative to a file
// descriptor that is not known here. The root "/" itself is never returned.
// Both results are cleaned, without duplicates and sorted; executed holds the paths of execve.
//
// The whole trace is read, from line 0, to follow the working directory, but only what is on line
// from or after it is collected: a launcher script runs tools before it hands over to the program,
// and a unikernel runs no script, so what they opened is not what the program needs. A relative
// program ("./srv") is read against the directory the process has at its execve line, so a chdir
// before from counts.
//
// The working directory is followed per process (the pid that opens each line of strace -f). Every
// process starts in workingDir (the working directory of the image), except one that a clone,
// clone3, fork or vfork line names as its child: it starts with the directory its parent has at
// that line. A process whose first line is not preceded by such a record starts with the directory
// of the process that wrote the line before its first one (the likeliest parent), or workingDir
// when it is the very first line. A successful chdir sets the directory (a relative one is read
// against the current directory; one cut in two is applied when its resumed line says 0). A
// successful fchdir sets it to unknown, since the path of a descriptor is not in the trace, until
// the next chdir; a relative path is left out while the directory is unknown, as with an empty
// workingDir. A "+++ superseded by execve in pid N +++" line hands the state of that thread to N.
//
// An execve counts when it succeeded on one line, or when it is unfinished and its resumed line
// has not said otherwise: a resumed line with a result other than 0 cancels it, from both results.
// FinalProgram follows the same rule.
func TouchedPaths(trace []byte, workingDir string, from int) (paths []string, executed []string) {
	r := scan(trace, workingDir, from)
	return r.paths, r.executed
}

// CreatedPaths returns, sorted, cleaned and without duplicates, the files the calls from line from
// on opened with O_CREAT, or with O_TRUNC and a write flag (O_WRONLY, O_RDWR), and creat: the pid
// files and logs the program makes when it runs. They are read in the same scan as TouchedPaths
// (same working directory rules). A file opened with O_CREAT that already existed is in the list
// too: the trace does not say which it was.
func CreatedPaths(trace []byte, workingDir string, from int) []string {
	var out []string
	for _, p := range scan(trace, workingDir, from).created {
		// what is never carried (the console, /proc) is not worth a warning either
		if !NotCarried(p) {
			out = append(out, p)
		}
	}
	return out
}

type scanned struct {
	paths, executed, created []string
}

// openFlags gives the flags of an open, openat or openat2 call, from the text after the path.
func openFlags(call, after string) []string {
	s := strings.TrimPrefix(after, ", ")
	if call == "openat2" {
		at := strings.Index(s, "flags=")
		if at < 0 {
			return nil
		}
		s = s[at+len("flags="):]
	}
	if end := strings.IndexAny(s, ", )}"); end >= 0 {
		s = s[:end]
	}
	return strings.Split(s, "|")
}

func scan(trace []byte, workingDir string, from int) scanned {
	seen := map[string]bool{}
	made := map[string]bool{}
	ran := map[string]int{}
	cwd := map[string]string{}
	waitingExec := map[string]string{}
	waitingChdir := map[string]string{}
	active := ""

	for i, line := range strings.Split(string(trace), "\n") {
		collect := i >= from
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

		if sup := supersededLine.FindStringSubmatch(line); sup != nil {
			if to := sup[3]; to != pid {
				cwd[to] = cwd[pid]
				delete(cwd, pid)
				if w, ok := waitingExec[pid]; ok {
					waitingExec[to] = w
					delete(waitingExec, pid)
				}
				if w, ok := waitingChdir[pid]; ok {
					waitingChdir[to] = w
					delete(waitingChdir, pid)
				}
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

		note := func(set map[string]bool) {
			if collect && name != "/" {
				set[name] = true
			}
		}

		switch call[1] {
		case "execve":
			if collect {
				ran[name]++
				if unfinished {
					waitingExec[pid] = name
				}
			}
		case "chdir":
			note(seen)
			if unfinished {
				waitingChdir[pid] = name
			} else {
				cwd[pid] = name
			}
		case "creat":
			note(seen)
			note(made)
		case "open", "openat", "openat2":
			note(seen)
			flags := openFlags(call[1], after)
			if slices.Contains(flags, "O_CREAT") || (slices.Contains(flags, "O_TRUNC") && (slices.Contains(flags, "O_WRONLY") || slices.Contains(flags, "O_RDWR"))) {
				note(made)
			}
		default:
			note(seen)
		}
	}

	var r scanned
	for p, n := range ran {
		if n > 0 {
			seen[p] = true
			r.executed = append(r.executed, p)
		}
	}
	for p := range seen {
		r.paths = append(r.paths, p)
	}
	for p := range made {
		r.created = append(r.created, p)
	}
	sort.Strings(r.paths)
	sort.Strings(r.executed)
	sort.Strings(r.created)

	return r
}

// pidPrefix matches the process prefix of a line of strace -f, in either form, and captures the pid
// in the first or the second group.
var pidPrefix = regexp.MustCompile(`^(?:\[pid +(\d+)\] +|(\d+) +)`)

// supersededLine matches the line strace writes for a thread that another thread of its process
// replaced by an execve, and captures the pid of the line (groups 1 or 2) and the pid it went to
// (group 3).
var supersededLine = regexp.MustCompile(`^(?:\[pid +(\d+)\] +|(\d+) +)\+\+\+ superseded by execve in pid (\d+) \+\+\+`)

// forkLine matches a clone, clone3, fork or vfork that came back with the pid of the child, on one
// line or on the resumed line, and captures the pid of the caller (groups 1 or 2) and of the child
// (group 3).
var forkLine = regexp.MustCompile(`^(?:\[pid +(\d+)\] +|(\d+) +)(?:(?:clone3?|fork|vfork)\(|<\.\.\. (?:clone3?|fork|vfork) resumed>).*\) += ([0-9]+)`)

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
// cannot be read, the destination cannot be written) is returned, except that a directory as the
// source makes the destination an empty directory, wrapped with the path it
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
	if info.IsDir() {
		// an alias of a directory (/var/run for /run) is an empty directory: the observer fills
		// it when the directory itself is carried
		if err := os.MkdirAll(destination, 0755); err != nil {
			return fmt.Errorf("copy to %s: %w", destination, err)
		}
		return nil
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
// It follows at most 40 links and then gives the path it has reached, cleaned, which is inside the
// sysroot like any other result.
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
			return path.Clean("/" + strings.Join(append(candidate, parts...), "/"))
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
	// only spaces and tabs separate the words, as for the kernel: a carriage return stays in the
	// name, so the interpreter of a script with CRLF line ends is a file that does not exist, which
	// is how the kernel fails to run it too
	if fields := strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == '\t' }); len(fields) > 0 {
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

// WhereIs finds a program the way a shell would, among the files already carried. A name with a
// slash in it ("/srv/run", "./srv", "bin/x") is not searched for on the PATH; it is given back as it
// is, and so is one that is nowhere on the path. The PATH is the last PATH= entry of env, or a
// default one. An entry with a ".." element is skipped.
func WhereIs(sysroot string, program string, env []string) string {
	if strings.Contains(program, "/") {
		return program
	}
	searched := "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			searched = e[len("PATH="):]
		}
	}
	for _, dir := range strings.Split(searched, ":") {
		// a relative entry that climbs out (PATH=../..) would look outside the sysroot
		if slices.Contains(strings.Split(dir, "/"), "..") {
			continue
		}
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

var execveLine = regexp.MustCompile(`^(?:\[pid +(\d+)\] +|(\d+) +)execve\("((?:[^"\\]|\\.)*)", .*(\) += 0 *|<unfinished \.\.\.>)$`)
var exitedLine = regexp.MustCompile(`^(?:\[pid +(\d+)\] +|(\d+) +)\+\+\+ (?:exited|killed)`)

// Exec is a program the trace saw started: the pid, the file that was executed, the argv as the
// execve call wrote it (argv[0] as the caller gave it, which is not always the name of the file),
// and the line of the trace (counted from 0).
type Exec struct {
	Pid  string
	Path string
	Argv []string
	Line int
}

// FinalProgram is FinalExec without the arguments: the process, the path the program was started
// by and the line of the trace. When nothing was started, found is false and line is -1.
func FinalProgram(trace []byte) (pid string, program string, line int, found bool) {
	exec, found := FinalExec(trace)
	if !found {
		return "", "", -1, false
	}
	return exec.Pid, exec.Path, exec.Line, true
}

// parseArgv reads the argv of an execve call from the text after its path (", [\"a\", \"b\"], 0x7ff
// /* 5 vars */) = 0"). The strings are cut by strace at the -s limit and may be followed by "...":
// what follows a string up to the next quote is skipped. The environment part is not read. A call
// that does not print an array (an unreadable pointer) gives nil.
func parseArgv(after string) []string {
	s, ok := strings.CutPrefix(after, ", [")
	if !ok {
		return nil
	}
	argv := []string{}
	for len(s) > 0 {
		switch s[0] {
		case ']':
			return argv
		case '"':
			value, rest, ok := scanQuoted(s)
			if !ok {
				return argv
			}
			argv = append(argv, value)
			s = rest
		default:
			s = s[1:]
		}
	}
	return argv
}

// FinalExec is the program the image ends up running, chosen among the processes still running when
// the trace ends (all of them, if none is). Only an execve that succeeded counts: on a single line,
// or cut in two by strace ("execve(... <unfinished ...>" with its result on a "<... execve
// resumed>" line), where it counts at the line of the first part, with the argv of the first part,
// unless the resumed line says it failed, which cancels it and gives back the state of the process
// before it. An unfinished one that is never resumed counts. TouchedPaths follows the same rule.
// A "+++ superseded by execve in pid N +++" line hands the program (and an execve waiting for its
// result) of that thread to N.
//
// Which running process: an entrypoint script starts tools and then the program, but a program also
// starts helpers that outlive the line that started them (the controller of Elasticsearch, a sleep
// loop), and the most recent one is not the program. So the process that no other running traced
// program started wins: the parentage comes from the clone, clone3, fork and vfork lines. Of
// several such processes the one started last wins. If every running process is a child of another
// running one (pid reuse can make that circular), the oldest is taken. When none is running, the
// one started last is taken. When nothing was started, found is false.
//
// The command line and the environment of the running program are better read from
// /proc/<pid>/cmdline and /proc/<pid>/environ by the caller, except that a program may rewrite its
// command line (nginx does): Argv here is what execve was called with. The environment is not kept.
func FinalExec(trace []byte) (Exec, bool) {
	type before struct {
		previous    Exec
		hadPrevious bool
		wasGone     bool
	}
	last := map[string]Exec{}
	gone := map[string]bool{}
	waiting := map[string]before{}
	parent := map[string]string{}

	for i, text := range strings.Split(string(trace), "\n") {
		if m := forkLine.FindStringSubmatch(text); m != nil {
			parent[m[3]] = m[1] + m[2]
		} else if m := supersededLine.FindStringSubmatch(text); m != nil {
			from, to := m[1]+m[2], m[3]
			if from == to {
				continue
			}
			if moved, ok := last[from]; ok {
				if current, has := last[to]; !has || current.Line <= moved.Line {
					moved.Pid = to
					last[to] = moved
					delete(gone, to)
				}
				delete(last, from)
			}
			if w, ok := waiting[from]; ok {
				waiting[to] = w
				delete(waiting, from)
			}
			delete(gone, from)
		} else if m := execveLine.FindStringSubmatch(text); m != nil {
			process := m[1] + m[2]
			var argv []string
			if prefix := pidPrefix.FindStringSubmatch(text); prefix != nil {
				if _, after, _, ok := FirstQuoted(text[len(prefix[0])+len("execve("):]); ok {
					argv = parseArgv(after)
				}
			}
			if strings.HasPrefix(m[4], "<unfinished") {
				previous, had := last[process]
				waiting[process] = before{previous: previous, hadPrevious: had, wasGone: gone[process]}
			} else {
				delete(waiting, process)
			}
			last[process] = Exec{Pid: process, Path: unescape(m[3]), Argv: argv, Line: i}
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

	var running, all []string
	for process := range last {
		all = append(all, process)
		if !gone[process] {
			running = append(running, process)
		}
	}
	if len(all) == 0 {
		return Exec{Line: -1}, false
	}
	newest := func(pool []string) string {
		best := pool[0]
		for _, process := range pool[1:] {
			if last[process].Line > last[best].Line {
				best = process
			}
		}
		return best
	}
	if len(running) == 0 {
		return last[newest(all)], true
	}

	isRunning := map[string]bool{}
	for _, process := range running {
		isRunning[process] = true
	}
	var roots []string
	for _, process := range running {
		if up, ok := parent[process]; !ok || up == process || !isRunning[up] {
			roots = append(roots, process)
		}
	}
	if len(roots) == 0 {
		oldest := running[0]
		for _, process := range running[1:] {
			if last[process].Line < last[oldest].Line {
				oldest = process
			}
		}
		return last[oldest], true
	}
	return last[newest(roots)], true
}

// SplitNul reads what /proc keeps as strings ended by a zero byte. Only the last zero byte is a
// terminator and is dropped; an empty string between two zero bytes is an empty argument and stays.
func SplitNul(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
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

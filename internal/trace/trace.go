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
	"errors"
	"fmt"
	"io"
	"io/fs"
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
// workingDir. A thread that calls execve ("T execve(... <pid changed to N ...>", then "N +++ superseded
// by execve in pid T +++") hands its state to N.
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

// cwdSet is the working directory of each process. Processes that share their directory (a thread,
// or a clone with CLONE_FS) share one cell, so that a chdir of one is the chdir of the others, as in
// the kernel.
type cwdSet map[string]*string

func (c cwdSet) has(pid string) bool { _, ok := c[pid]; return ok }

func (c cwdSet) get(pid string) string {
	if p := c[pid]; p != nil {
		return *p
	}
	return ""
}

// set changes the directory of pid and of everything that shares it.
func (c cwdSet) set(pid, dir string) {
	if p := c[pid]; p != nil {
		*p = dir
		return
	}
	c[pid] = &dir
}

// copyOf gives child its own copy of the directory of parent (or dir when parent is not known).
func (c cwdSet) copyOf(child, parent string) {
	v := c.get(parent)
	c[child] = &v
}

// share makes child use the same directory as parent.
func (c cwdSet) share(child, parent string) {
	if c[parent] == nil {
		v := ""
		c[parent] = &v
	}
	c[child] = c[parent]
}

// sharesDirectory tells whether a clone call shares the working directory with its parent: only a
// clone with CLONE_FS does (a thread made without it has a copy, as in the kernel). Not modelled:
// unshare(CLONE_FS), which ends the sharing later, and chroot and setns, which change what a path
// is read against.
var sharesDirectory = regexp.MustCompile(`\bCLONE_FS\b`)

// resumedClone matches the resumed line of a clone, whatever the result.
var resumedClone = regexp.MustCompile(`^(?:\[pid +(\d+)\] +|(\d+) +)<\.\.\. (?:clone3?|fork|vfork) resumed>`)

// cloneStart matches a clone call that strace cut in two, on its first line.
var cloneStart = regexp.MustCompile(`^(?:\[pid +(\d+)\] +|(\d+) +)(?:clone3?|fork|vfork)\(.*<unfinished \.\.\.>`)

// pidChanged is what strace writes at the end of the execve of a thread that is not the leader (the
// result comes on a "<... execve resumed>" line of the leader, after the "superseded" line, which
// hands the state of the thread to the leader).
var pidChanged = regexp.MustCompile(`<pid changed to \d+ \.\.\.>`)

type scanned struct {
	paths, executed, created []string
	// execCwd is the working directory of the process on each line of an execve, before it
	execCwd map[int]string
}

// CwdAtExec is the working directory the process had on the trace line of its execve (counted from
// 0), read the way TouchedPaths reads it, starting from workingDir. It is "" when that is not
// known (a fchdir came before) or when the line is not an execve. A program that changes its
// directory while it runs ends in another one: this is where it started.
func CwdAtExec(trace []byte, workingDir string, line int) string {
	return scan(trace, workingDir, 0).execCwd[line]
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
	cwd := cwdSet{}
	pendingShare := map[string]bool{}
	// the processes whose clone call was cut in two and is not finished: strace often writes the first
	// line of the child before the resumed line of the parent
	var cloning []string
	chdirDone := map[string]bool{}
	waitingExec := map[string]string{}
	waitingChdir := map[string]string{}
	active := ""
	execCwd := map[int]string{}

	for i, line := range strings.Split(string(trace), "\n") {
		collect := i >= from
		prefix := pidPrefix.FindStringSubmatch(line)
		if prefix == nil {
			continue
		}
		pid := prefix[1] + prefix[2]
		if exitedLine.MatchString(line) {
			// the pid may be given to another process later: it starts again from what its parent has
			delete(cwd, pid)
			delete(chdirDone, pid)
			continue
		}
		if !cwd.has(pid) {
			parent := active
			for k := len(cloning) - 1; k >= 0; k-- {
				if cloning[k] != pid {
					// a process seen for the first time while a clone is open is its child
					parent = cloning[k]
					break
				}
			}
			if parent != "" {
				cwd.copyOf(pid, parent)
			} else {
				cwd.set(pid, workingDir)
			}
		}
		active = pid
		rest := line[len(prefix[0]):]

		if cs := cloneStart.FindStringSubmatch(line); cs != nil {
			pendingShare[pid] = sharesDirectory.MatchString(line)
			cloning = append(cloning, pid)
		}
		resumed := resumedClone.MatchString(line)
		child := forkLine.FindStringSubmatch(line)
		if resumed {
			// the clone is finished, whatever it gave back (a number, -1, ?)
			for k := len(cloning) - 1; k >= 0; k-- {
				if cloning[k] == pid {
					cloning = append(cloning[:k], cloning[k+1:]...)
					break
				}
			}
		}
		if child != nil {
			shared := sharesDirectory.MatchString(line)
			if resumed {
				shared = pendingShare[pid]
			}
			delete(pendingShare, pid)
			if shared {
				if cwd.has(child[3]) && chdirDone[child[3]] {
					// the child changed directory before the line that says it was made: it is the
					// directory of both
					cwd.set(pid, cwd.get(child[3]))
				}
				cwd.share(child[3], pid)
			} else if !cwd.has(child[3]) || !chdirDone[child[3]] {
				// a child that did not change directory has what its real parent has: it may have been
				// seen before, when another process had a clone open too, and been given the wrong one
				cwd.copyOf(child[3], pid)
			}
			continue
		}
		if resumed {
			delete(pendingShare, pid)
			continue
		}

		// "N +++ superseded by execve in pid T +++": the thread T called execve and the process goes on
		// as N (the line before it ends in "<pid changed to N ...>"), with what T had.
		if sup := supersededLine.FindStringSubmatch(line); sup != nil {
			if thread := sup[3]; thread != pid {
				if cwd.has(thread) {
					cwd.share(pid, thread)
				}
				delete(cwd, thread)
				if w, ok := waitingExec[thread]; ok {
					waitingExec[pid] = w
					delete(waitingExec, thread)
				}
				if w, ok := waitingChdir[thread]; ok {
					waitingChdir[pid] = w
					delete(waitingChdir, thread)
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
						cwd.set(pid, dir)
						chdirDone[pid] = true
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
				cwd.set(pid, "")
				chdirDone[pid] = true
			}
			continue
		}

		name, after, relativeToCwd, ok := FirstQuoted(args)
		if !ok {
			continue
		}
		unfinished := strings.Contains(after, "<unfinished ...>") || pidChanged.MatchString(after)
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
			dir := cwd.get(pid)
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
			execCwd[i] = cwd.get(pid)
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
				cwd.set(pid, name)
				chdirDone[pid] = true
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

	r := scanned{execCwd: execCwd}
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
// puts in, which no package has to bring. The files the runtime writes for the network of the
// container (/etc/resolv.conf, /etc/hosts, /etc/hostname) count too: ops writes its own, and the
// resolv.conf of the container holds the address of the resolver of the docker network.
func NotCarried(p string) bool {
	return kernelPath(p) || slices.Contains(runtimeFiles, p)
}

// runtimeFiles are the files the container runtime writes into every container.
var runtimeFiles = []string{"/etc/resolv.conf", "/etc/hosts", "/etc/hostname"}

// kernelPath says whether a path is under /proc, /sys, /dev or the trace mount.
func kernelPath(p string) bool {
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
	return kernelPath(path.Clean(p))
}

// CarryConsoleLink puts an empty file in the sysroot under name, creating its directories, so that
// opening a log that was a link to the console works. A failure is returned, with the path it
// happened on.
func CarryConsoleLink(sysroot string, name string) error {
	if !InsideSysroot(sysroot, filepath.Join(sysroot, name)) {
		return &LinkSkipped{name, "it is, through a link of the image, outside the package"}
	}
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
	if !InsideSysroot(sysroot, source) {
		return &LinkSkipped{to, "what it copies (" + from + ") is, through a link of the image, outside the package"}
	}
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("copy %s: %w", scrub(sysroot, source), err)
	}
	if !parentInside(sysroot, filepath.Dir(destination)) {
		return &LinkSkipped{to, "its directory is, through a link of the image, outside the package"}
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

var execveLine = regexp.MustCompile(`^(?:\[pid +(\d+)\] +|(\d+) +)execve\("((?:[^"\\]|\\.)*)", .*(\) += 0 *|<unfinished \.\.\.>|<pid changed to \d+ \.\.\.>)$`)
var exitedLine = regexp.MustCompile(`^(?:\[pid +(\d+)\] +|(\d+) +)\+\+\+ (?:exited|killed)`)

// Exec is a program the trace saw started: the pid, the file that was executed, the argv as the
// execve call wrote it (argv[0] as the caller gave it, which is not always the name of the file),
// and the line of the trace (counted from 0).
type Exec struct {
	Pid  string
	Path string
	Argv []string
	Line int

	// PassedOver are the package manager launchers (see Launcher) that FinalExec passed over to
	// reach this program, the outermost first, with the shells it passed over before the first of
	// them and after it (the shell that runs the script of npm start, when it stayed a process of its
	// own): what ran around the program, which is not what the package runs. A process that executed
	// several programs (a shell that forks for its first command and executes its last) gives one
	// entry for each, in order; so does the process of the program itself, for what it executed
	// before the program (a shell that executed the program in its own process). It is empty when no
	// launcher was passed over.
	PassedOver []Exec

	// NotRun are the labels (see Label) of the commands that ran before the program or beside it
	// and are gone: children of the processes that were passed over, other than the next one on the
	// chain, that executed a program and ended ("npm run migrate" of "npm run migrate && npm start"),
	// and the children of the process of the program that ended before the program was executed. The
	// package does not run them. It is empty when no launcher was passed over. A pid that the
	// system reused during the observation keeps one history for both processes: its label is the
	// last program it executed. That is not handled.
	NotRun []string

	// Beside are the labels (see Label) of the programs that ran under the shell that was passed over
	// for the program, were not chosen and are not below it: "celery worker" of
	// "celery worker & gunicorn app". The package does not run them.
	Beside []string

	// ChildLaunchers are the labels of the launchers that the program started as children: a script
	// of the image that starts npm (a script is not told from a program by its name, so it stays the
	// program).
	ChildLaunchers []string
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

// inits are the tiny init programs that images put in front of the program (ENTRYPOINT ["tini",
// "--"], ["dumb-init", ...]): they start the program as their only child, reap zombies and pass
// signals on. On nanos there is no fork, so the init cannot run, and the program it starts can.
// initName is the file name of an init: tini, tini-static, dumb-init, catatonit, docker-init, with
// the architecture of a release (tini-amd64, tini-static-arm64) or its version and architecture
// (dumb-init_1.2.5_x86_64).
var initName = regexp.MustCompile(`^(?:tini|tini-static|dumb-init|catatonit|docker-init)(?:_[0-9][0-9.]*)?(?:[-_](?:amd64|arm64|armhf|armel|i386|ppc64le|ppc64el|s390x|x86_64|aarch64|mips|mipsel|mips64el|riscv64))?$`)

// IsInit tells whether the program at p is one of the init wrappers (tini, dumb-init, ...).
func IsInit(p string) bool { return initName.MatchString(path.Base(p)) }

// FinalExec is the program the image ends up running, chosen among the processes still running when
// the trace ends (all of them, if none is). Only an execve that succeeded counts: on a single line,
// or cut in two by strace ("execve(... <unfinished ...>" with its result on a "<... execve
// resumed>" line), where it counts at the line of the first part, with the argv of the first part,
// unless the resumed line says it failed, which cancels it and gives back the state of the process
// before it. An unfinished one that is never resumed counts. TouchedPaths follows the same rule.
// A thread that is not the leader and calls execve is written by strace as "T execve(... <pid
// changed to N ...>", "N +++ superseded by execve in pid T +++" and "N <... execve resumed>) = 0":
// the program is the one of N, the process, and what T had (its program, an execve waiting for its
// result) is N's.
//
// Which running process: an entrypoint script starts tools and then the program, but a program also
// starts helpers that outlive the line that started them (the controller of Elasticsearch, a sleep
// loop), and the most recent one is not the program. So the process that no other running traced
// program started wins: the parentage comes from the clone, clone3, fork and vfork lines. Of
// several such processes the one started last wins. A shell that has one running child that
// started a program is passed over for that child (the shell form of CMD); so is a package manager
// launcher (npm, npx, yarn, pnpm: see Launcher) that has exactly one running child, the script it
// runs, and the launchers passed over are in Exec.PassedOver. If every running process is a child
// of another running one (pid reuse can make that circular), the oldest is taken. When none is
// running, the one started last is taken. When nothing was started, found is false.
//
// A shell that has several running children (a server and what was started beside it:
// "(while :; do sleep 60; done) & server", "worker & server") is passed over for the nearest of
// them, by the rule explained at nearestProgram below: the nearest to the shell that is not a
// shell, a supervisor or a command that only waits or copies, and of those at the same distance the
// one in the branch the shell forked last. The others are in Exec.Beside.
//
// The command line and the environment of the running program are better read from
// /proc/<pid>/cmdline and /proc/<pid>/environ by the caller, except that a program may rewrite its
// command line (nginx does): Argv here is what execve was called with. The environment is not kept.
func FinalExec(trace []byte) (Exec, bool) {
	type before struct {
		previous    Exec
		hadPrevious bool
		wasGone     bool
		historyLen  int
	}
	last := map[string]Exec{}
	history := map[string][]Exec{}
	gone := map[string]bool{}
	waiting := map[string]before{}
	parent := map[string]string{}
	// forkedAt is the line of the call that made each process (for a clone that strace cut in two,
	// the resumed line: a process makes its calls one after the other, so the order of the forks of
	// one parent is the order of these lines)
	forkedAt := map[string]int{}
	var beside []string

	for i, text := range strings.Split(string(trace), "\n") {
		if m := forkLine.FindStringSubmatch(text); m != nil {
			parent[m[3]] = m[1] + m[2]
			forkedAt[m[3]] = i
		} else if m := supersededLine.FindStringSubmatch(text); m != nil {
			// "N +++ superseded by execve in pid T +++": the thread T called execve and the process goes
			// on as N, so what T had is N's now
			from, to := m[3], m[1]+m[2]
			if from == to {
				continue
			}
			if moved, ok := last[from]; ok {
				if current, has := last[to]; !has || current.Line <= moved.Line {
					moved.Pid = to
					last[to] = moved
					history[to] = append(history[to], history[from]...)
					delete(gone, to)
				}
				delete(last, from)
				delete(history, from)
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
			if strings.HasPrefix(m[4], "<unfinished") || strings.HasPrefix(m[4], "<pid changed") {
				previous, had := last[process]
				waiting[process] = before{previous: previous, hadPrevious: had, wasGone: gone[process], historyLen: len(history[process])}
			} else {
				delete(waiting, process)
			}
			e := Exec{Pid: process, Path: unescape(m[3]), Argv: argv, Line: i}
			last[process] = e
			history[process] = append(history[process], e)
			delete(gone, process)
		} else if prefix := pidPrefix.FindStringSubmatch(text); prefix != nil && strings.HasPrefix(text[len(prefix[0]):], "<... execve resumed>") {
			process := prefix[1] + prefix[2]
			state, ok := waiting[process]
			if !ok {
				continue
			}
			delete(waiting, process)
			if result, has := ResultOf(text); has && result != "0" {
				if state.historyLen <= len(history[process]) {
					history[process] = history[process][:state.historyLen]
				}
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
	// The process that started another is the nearest one that executed a program: a thread that
	// started it (a Go program starts its children from any thread, which has no execve of its own)
	// or a process that only forked is passed over, so that the children of the threads of rr are
	// the children of rr.
	starter := func(process string) string {
		seen := map[string]bool{process: true}
		for up, ok := parent[process]; ok && !seen[up]; up, ok = parent[up] {
			if _, executed := last[up]; executed {
				return up
			}
			seen[up] = true
		}
		return ""
	}
	var roots []string
	for _, process := range running {
		if up := starter(process); up == "" || !isRunning[up] {
			roots = append(roots, process)
		}
	}
	// A shell that runs the program as a child (the shell form of CMD, "sh -c java -jar x") is not
	// the program: when it has exactly one running child that started a program, that child is.
	// commands that only wait, and commands that only copy what they are given (the reader of a
	// pipeline, a log writer): never the program of an image
	waiters := map[string]bool{"sleep": true, "tail": true, "watch": true}
	copiers := map[string]bool{"tee": true, "cat": true, "logger": true, "rotatelogs": true, "cronolog": true, "svlogd": true, "multilog": true}
	shells := map[string]bool{"sh": true, "bash": true, "dash": true, "ash": true, "zsh": true, "ksh": true, "mksh": true}
	var recorded, pending []string
	// nearestProgram picks, among the processes that run under the shell, the one nearest to it that
	// is not itself a shell, a supervisor, ps, busybox, or a command that only waits (sleep, tail,
	// watch) or only copies what it is given (tee, cat, logger, rotatelogs, cronolog, svlogd,
	// multilog: the reader of a pipeline "server | tee log"), and gives the others that run under
	// the shell and are not below the one it picked (they are not run by the package, and the report
	// says so). What is below the one picked is its own (a name resolver of a runtime, plugins it
	// installs). What is below a supervisor is the supervisor's own too: when the supervisor is all
	// there is (a supervisor beside a tail), the supervisor is the program, as it is for a shell
	// that has only it.
	//
	// The distance counts the processes that executed a program between the shell and the process,
	// so a subshell that only forked (the loop of "(while :; do sleep 60; done) & server") is the
	// shell. At the same distance the process whose fork is the later one wins, comparing the lines
	// of the forks on the way down from the shell, so a setup step beside the server, which comes
	// first on the command line, loses. Which of two processes calls execve first is a race of the
	// start and decides nothing; only two processes with the very same fork (none) fall back to
	// the line of their execve. This is the rule of mainProgram of the survey fork of ops, which
	// reads a snapshot of "docker top"; here it reads the trace. A pipeline cannot be told from "a &
	// b" by the calls that are traced (the pipe and dup2 calls are not), so the readers of a
	// pipeline are kept out by name, and the others are not.
	nearestProgram := func(shell string) (string, []string) {
		type candidate struct {
			pid        string
			depth      int
			path       []int
			chain      []string
			isShell    bool
			notProgram bool
			supervisor bool
			under      bool
		}
		var found []candidate
		for _, pid := range running {
			if pid == shell {
				continue
			}
			c := candidate{pid: pid}
			below, x := false, pid
			for hops := 0; hops < 64 && !below; hops++ {
				up := starter(x)
				if up == "" {
					break
				}
				c.depth++
				below = up == shell
				c.chain = append(c.chain, up)
				x = up
			}
			if !below {
				continue
			}
			// the lines of the forks on the way down from the shell, processes that only forked included
			for x, hops := pid, 0; x != "" && x != shell && hops < 64; x, hops = parent[x], hops+1 {
				c.path = append([]int{forkedAt[x]}, c.path...)
			}
			e := last[pid]
			name := path.Base(e.Path)
			interp, _ := interpreterOf(e.Path, e.Argv)
			c.isShell = shells[interp]
			c.supervisor = Supervisor(e.Path, e.Argv) != ""
			c.notProgram = c.isShell || waiters[name] || copiers[name] || name == "busybox" || name == "ps"
			for _, up := range c.chain {
				if up != shell && Supervisor(last[up].Path, last[up].Argv) != "" {
					c.under = true
				}
			}
			found = append(found, c)
		}
		// a is later than b: where the forks differ, the later fork; the same forks, the later execve
		later := func(a, b candidate) bool {
			for k := 0; k < len(a.path) && k < len(b.path); k++ {
				if a.path[k] != b.path[k] {
					return a.path[k] > b.path[k]
				}
			}
			return last[a.pid].Line > last[b.pid].Line
		}
		best := -1
		for tier := 0; tier < 2 && best < 0; tier++ {
			for k, c := range found {
				if c.under {
					continue
				}
				if tier == 0 && (c.notProgram || c.supervisor) || tier == 1 && (!c.supervisor || c.isShell) {
					continue
				}
				if best < 0 || c.depth < found[best].depth || (c.depth == found[best].depth && later(c, found[best])) {
					best = k
				}
			}
		}
		if best < 0 {
			return "", nil
		}
		chosen := found[best].pid
		var others []string
		for k, c := range found {
			if k == best || c.isShell || c.under || slices.Contains(c.chain, chosen) {
				continue
			}
			others = append(others, c.pid)
		}
		sort.Slice(others, func(i, j int) bool { return last[others[i]].Line < last[others[j]].Line })
		return chosen, others
	}
	descend := func(process string) string {
		for step := 0; step < 8; step++ {
			_, _, launcher := Launcher(last[process].Path, last[process].Argv)
			isInit := IsInit(last[process].Path)
			if !launcher && !isInit && !shells[path.Base(last[process].Path)] {
				break
			}
			var children []string
			for _, other := range running {
				if other != process && starter(other) == process {
					children = append(children, other)
				}
			}
			if len(children) > 1 && shells[path.Base(last[process].Path)] && !launcher && !isInit {
				// Several things run under a shell: a server and what was started beside it (a loop
				// that sleeps, a second server). The program is the one nearest the shell.
				if program, others := nearestProgram(process); program != "" {
					children = []string{program}
					for _, o := range others {
						beside = append(beside, besideLabel(last[o].Path, last[o].Argv))
					}
				}
			}
			if len(children) != 1 {
				break
			}
			// A launcher is recorded, and with it the shells passed over before it (the shell form
			// of CMD that ran "npm run migrate && npm start", an entrypoint script) and everything
			// passed over after it, the shell that runs its script included: what they ran besides
			// the program is not run. Shells passed over with no launcher after them are not.
			switch {
			case launcher || isInit:
				// an init (tini, dumb-init) only starts the program as its child and waits for it:
				// it is passed over as a launcher is, and named in the report
				recorded = append(append(recorded, pending...), process)
				pending = nil
			case len(recorded) > 0:
				recorded = append(recorded, process)
			default:
				pending = append(pending, process)
			}
			process = children[0]
		}
		return process
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
	finalPid := descend(newest(roots))
	final := last[finalPid]
	final.Beside = beside
	onChain := map[string]bool{finalPid: true}
	for _, process := range recorded {
		onChain[process] = true
		// every program the process executed, not only the one it ended as
		final.PassedOver = append(final.PassedOver, history[process]...)
	}
	if h := history[finalPid]; len(recorded) > 0 && len(h) > 1 {
		// what the process of the program executed before the program
		final.PassedOver = append(final.PassedOver, h[:len(h)-1]...)
	}

	// What ran besides the program: the children of the processes passed over, other than the ones
	// on the chain, that executed a program and ended, and the children of the process of the
	// program that ended before it was executed (what runs after is the program's own). A child that
	// only forked (a $(...) of a shell) did not execute a program and is not a command.
	if len(recorded) > 0 {
		var ran []Exec
		for child := range parent {
			up := starter(child)
			if !onChain[up] || !gone[child] || len(history[child]) == 0 {
				continue
			}
			if up == finalPid && history[child][0].Line > final.Line {
				continue
			}
			ran = append(ran, history[child][0])
		}
		sort.Slice(ran, func(i, j int) bool { return ran[i].Line < ran[j].Line })
		for _, e := range ran {
			final.NotRun = append(final.NotRun, Label(last[e.Pid].Path, last[e.Pid].Argv))
		}
	}

	// A program that started a launcher: a script of the image that runs npm (the kernel runs the
	// interpreter of a script within the same execve, so a script is not known to be one and stays
	// the program), or a program that runs a launcher itself.
	for child := range parent {
		if starter(child) != finalPid || child == finalPid {
			continue
		}
		for _, e := range history[child] {
			if name, rest, ok := Launcher(e.Path, e.Argv); ok {
				// the launcher by its name, also when a shell ran it ("sh -c npm run build")
				label := launcherLabel(name, rest)
				if !slices.Contains(final.ChildLaunchers, label) {
					final.ChildLaunchers = append(final.ChildLaunchers, label)
				}
			}
		}
	}
	sort.Strings(final.ChildLaunchers)
	return final, true
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
		case strings.HasPrefix(name, "/etc/ssl/certs/"), strings.HasPrefix(name, "/etc/ld.so"), slices.Contains(runtimeFiles, name):
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

// LinkSkipped is returned by LinkHostFile and CopyHostFile when they leave what is in the sysroot as
// it is on purpose: the link or the copy would lead out of the sysroot, or into itself, or the
// directory that files opened through the link left cannot be merged into the one the link leads to
// (a file where the other has a directory). The caller reports it; nothing is lost.
type LinkSkipped struct {
	Name, Why string
}

func (e *LinkSkipped) Error() string { return e.Name + " was left as it is: " + e.Why }

// within says whether p is below root (not root itself). Both are clean, real paths.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveNearest resolves the links of p as far as p exists: the nearest directory of it that exists
// is resolved, and the part that does not exist yet is added to it.
func resolveNearest(p string) (string, error) {
	rest := ""
	for d := p; ; d = filepath.Dir(d) {
		if _, err := os.Lstat(d); err == nil {
			resolved, err := filepath.EvalSymlinks(d)
			if err != nil {
				return "", err
			}
			return filepath.Join(resolved, rest), nil
		}
		rest = filepath.Join(filepath.Base(d), rest)
		if filepath.Dir(d) == d {
			return "", fmt.Errorf("no part of %s exists", p)
		}
	}
}

// InsideSysroot says whether p, with the links already in the sysroot followed as far as p exists,
// is the sysroot or below it. An image may have a link that goes out through /.., which on this
// machine is not the root of the image: a file written through it would land on this machine.
func InsideSysroot(sysroot, p string) bool {
	root, err := filepath.EvalSymlinks(sysroot)
	if err != nil {
		return false
	}
	resolved, err := resolveNearest(p)
	return err == nil && (resolved == root || within(root, resolved))
}

func parentInside(sysroot, dir string) bool { return InsideSysroot(sysroot, dir) }

func mustResolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// scrub takes the path of the sysroot out of the text of an error: the report names paths of the
// image, not of this machine.
func scrub(sysroot, text string) string {
	for _, root := range []string{mustResolve(sysroot), sysroot} {
		text = strings.ReplaceAll(text, root+string(filepath.Separator), "/")
		// the folder itself, when no name follows it ("/s" is not the start of "/s.old")
		text = regexp.MustCompile(regexp.QuoteMeta(root)+`($|[^A-Za-z0-9._-])`).ReplaceAllString(text, "/$1")
	}
	return text
}

// scrubErr is the text of an error for the report: a path that does not exist or cannot be read is
// said without the path (it may be a path of this machine), and the sysroot is taken out of the rest.
func scrubErr(sysroot string, err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "it does not exist"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	}
	return scrub(sysroot, err.Error())
}

// LinkHostFile makes a symbolic link with the text it had at name in sysroot, unless something is
// there already. The text is relative, so it leads to the same file from the new place as it did in
// the image, provided that file was carried.
//
// A directory that is there already, with the directory the link leads to carried, is what files
// opened through the link left: docker cp of /pkg/node_modules/x/dist/a.js, where x is a link to
// ../.pnpm/x/node_modules/x, makes a directory x with a.js in it, and the program then finds its
// other files (dist/b.js) beside a.js only through the link. What the directory holds is moved
// into the directory the link leads to (where it is not there already) and the link takes its place.
// Links that are in the sysroot already are followed to where they really lead, and nothing is
// touched unless both directories are really below sysroot, and neither holds the other. In all the
// cases where the directory is kept the error is a *LinkSkipped, and nothing has been moved.
func LinkHostFile(sysroot string, text string, name string) error {
	destination := filepath.Join(sysroot, name)
	if info, err := os.Lstat(destination); err == nil {
		if !info.IsDir() {
			return nil
		}
		if why := linkTextShape(text); why != "" {
			return &LinkSkipped{name, why + " (" + text + ")"}
		}
		root, err := filepath.EvalSymlinks(sysroot)
		if err != nil {
			return &LinkSkipped{name, "the sysroot cannot be resolved: " + scrubErr(sysroot, err)}
		}
		realDir, err := filepath.EvalSymlinks(destination)
		if err != nil {
			return &LinkSkipped{name, "the directory cannot be resolved: " + scrubErr(sysroot, err)}
		}
		if !within(root, realDir) {
			return &LinkSkipped{name, "the directory is, through a link of the image, outside the package"}
		}
		target, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(realDir), text))
		if err != nil {
			return &LinkSkipped{name, "what the link leads to (" + text + ") is not in the package: " + scrubErr(sysroot, err)}
		}
		if !within(root, target) {
			return &LinkSkipped{name, "the link leads outside the package (" + text + ")"}
		}
		if target == realDir || within(realDir, target) || within(target, realDir) {
			return &LinkSkipped{name, "the link leads into itself or to a directory that holds it (" + text + ")"}
		}
		if ti, err := os.Stat(target); err != nil || !ti.IsDir() {
			return &LinkSkipped{name, "what the link leads to (" + text + ") is not a directory"}
		}
		if blocked := mergeBlocked(realDir); blocked != "" {
			return &LinkSkipped{name, "the link " + blocked + " in the directory is absolute or goes up with .., and would end at another depth if it was moved; the directory was kept and the link was not made"}
		}
		if clash := mergeClash(realDir, target); clash != "" {
			return &LinkSkipped{name, "the directory and the one its link leads to disagree on what " + clash + " is (a file or a directory); the directory was kept and the link was not made"}
		}
		if err := mergeInto(realDir, target); err != nil {
			why := "the files could not be moved to the directory the link leads to: " + scrubErr(sysroot, err)
			var me *mergeError
			if errors.As(err, &me) && len(me.unrestored) > 0 {
				why += "; these files could not be put back and are in the directory the link leads to: " + scrub(sysroot, strings.Join(me.unrestored, ", ")) + "; the directories that were made there were left at mode 0700"
			} else {
				why += "; what was moved was put back"
			}
			return &LinkSkipped{name, why}
		}
		if err := os.RemoveAll(realDir); err != nil {
			return fmt.Errorf("link %s: %w", destination, err)
		}
		destination = realDir
	} else {
		if why := linkTextShape(text); why != "" {
			return &LinkSkipped{name, why + " (" + text + "); the link was not made"}
		}
		if !parentInside(sysroot, filepath.Dir(destination)) {
			return &LinkSkipped{name, "its directory is, through a link of the image, outside the package"}
		}
		// The link must not lead out of the sysroot either: a writer that follows it would write on this
		// machine. Its text has only ".." at the start (linkTextShape), which is read from the directory
		// as it really is (dir is resolved), so the join below is where a path through the link ends.
		if dir, err := resolveNearest(filepath.Dir(destination)); err != nil || !InsideSysroot(sysroot, filepath.Join(dir, text)) {
			return &LinkSkipped{name, "the link would lead out of the package (" + text + "), which is a path of this machine there; the link was not made"}
		}
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return fmt.Errorf("link %s: %w", destination, err)
	}
	if err := os.Symlink(text, destination); err != nil {
		return fmt.Errorf("link %s: %w", destination, err)
	}
	return nil
}

// linkTextShape refuses the texts of links that are not checked on disk: an absolute one, and one
// that goes up again after a name (l/../x: where it ends depends on where l leads, which is not
// known when the text is checked, and changes when l is made later). What is left, ".." at the start
// and names, is checked against the directory the link is in, as it really is.
func linkTextShape(text string) string {
	if strings.HasPrefix(text, "/") {
		return "the link text is absolute"
	}
	named := false
	for _, part := range strings.Split(text, "/") {
		switch part {
		case "", ".":
		case "..":
			if named {
				return "the link text goes up (..) again after a name"
			}
		default:
			named = true
		}
	}
	return ""
}

// mergeBlocked gives the first link under from (relative to it) whose text is absolute or has "..":
// moved to a directory at another depth it would lead somewhere else, out of the sysroot at worst.
func mergeBlocked(from string) string {
	blocked := ""
	filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil || blocked != "" || d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		text, err := os.Readlink(p)
		if err != nil || strings.HasPrefix(text, "/") || strings.Contains("/"+text+"/", "/../") {
			rel, _ := filepath.Rel(from, p)
			blocked = filepath.ToSlash(rel)
		}
		return nil
	})
	return blocked
}

// mergeClash gives the first path (relative to from) where from holds a directory and into holds
// something that is not one, or the other way round, or "" when there is none.
func mergeClash(from, into string) string {
	clash := ""
	filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil || clash != "" {
			return nil
		}
		rel, err := filepath.Rel(from, p)
		if err != nil || rel == "." {
			return nil
		}
		other, err := os.Lstat(filepath.Join(into, rel))
		if err != nil {
			return nil
		}
		if d.IsDir() != other.IsDir() {
			clash = filepath.ToSlash(rel)
			return filepath.SkipDir
		}
		return nil
	})
	return clash
}

// mergeError is the error of mergeInto when something failed. unrestored are the files that were moved
// and could not be put back (their names, relative to the directory they came from).
type mergeError struct {
	err        error
	unrestored []string
}

func (e *mergeError) Error() string { return e.err.Error() }
func (e *mergeError) Unwrap() error { return e.err }

// mergeInto moves what is under the directory from into the directory into, keeping what is in
// into already. A directory made keeps the mode it had; it is made writable while the files go in
// and its mode is set at the end, and the directories of from are made writable too (a directory
// of mode 0555 cannot be emptied by its owner). If something fails the files are put back, the
// directories made are removed and the modes are restored; a *mergeError names what could not be
// put back. mergeClash has been asked first.
func mergeInto(from, into string) (err error) {
	type dirMode struct {
		path string
		mode os.FileMode
	}
	var sources, made []dirMode
	var moved [][2]string
	defer func() {
		if err == nil {
			// the modes of the directories made, deepest first
			for i := len(made) - 1; i >= 0; i-- {
				os.Chmod(made[i].path, made[i].mode)
			}
			return
		}
		me := &mergeError{err: err}
		for i := len(moved) - 1; i >= 0; i-- {
			if rerr := os.Rename(moved[i][1], moved[i][0]); rerr != nil {
				rel, _ := filepath.Rel(from, moved[i][0])
				me.unrestored = append(me.unrestored, filepath.ToSlash(rel))
			}
		}
		for i := len(made) - 1; i >= 0; i-- {
			os.Remove(made[i].path)
		}
		for i := len(sources) - 1; i >= 0; i-- {
			os.Chmod(sources[i].path, sources[i].mode)
		}
		err = me
	}()
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, ierr := d.Info()
		if d.IsDir() && ierr == nil {
			sources = append(sources, dirMode{p, info.Mode().Perm()})
			if err := os.Chmod(p, info.Mode().Perm()|0700); err != nil {
				return err
			}
		}
		rel, err := filepath.Rel(from, p)
		if err != nil || rel == "." {
			return err
		}
		dst := filepath.Join(into, rel)
		if d.IsDir() {
			if _, err := os.Lstat(dst); err == nil {
				return nil
			}
			mode := os.FileMode(0755)
			if ierr == nil {
				mode = info.Mode().Perm()
			}
			if err := os.Mkdir(dst, 0700); err != nil {
				return err
			}
			made = append(made, dirMode{dst, mode})
			return nil
		}
		if _, err := os.Lstat(dst); err == nil {
			return nil
		}
		if err := os.Rename(p, dst); err != nil {
			return err
		}
		moved = append(moved, [2]string{p, dst})
		return nil
	})
}

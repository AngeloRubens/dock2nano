package trace

import (
	"regexp"
	"strconv"
	"strings"
)

// Child is a process the program started: the pid of the child, the pid of the process that
// started it, the call (clone, clone3, fork or vfork) and the line of the trace (counted from 0)
// where the call came back with the pid. For a call that strace cut in two, that is the resumed
// line.
type Child struct {
	Pid    string
	Parent string
	Call   string
	Line   int
}

// callLine matches the start of a clone, clone3, fork or vfork call, or the resumed part of one,
// and captures the pid of the line (groups 1 or 2) and the name of the call (group 3 for the
// start, group 4 for the resumed part).
var callLine = regexp.MustCompile(`^(?:\[pid +(\d+)\] +|(\d+) +)(?:(clone3?|fork|vfork)\(|<\.\.\. (clone3?|fork|vfork) resumed>)`)

// threadFlag is the flag that makes a clone a thread of the caller instead of a process.
var threadFlag = regexp.MustCompile(`\bCLONE_THREAD\b`)

// ChildProcesses lists the processes the program started: every clone, clone3, fork and vfork
// that succeeded (the result is a pid above 0; one cut in two counts at its resumed line) on a
// line from from onwards, and that made a process. A fork and a vfork always make one; a clone or
// clone3 makes one unless its flags hold CLONE_THREAD, which makes a thread. Threads are not
// children, but a process started by a thread counts like one started by the program.
//
// Only descendants of the final program (FinalExec) count: the caller of the call has to be that
// process or have it among its ancestors, read from the successful calls on every line of the
// trace, from line 0. A helper started by the entrypoint script, or by something that is not the
// program, is not counted. When the trace holds no program, every process counts. The result is in
// the order of the trace.
func ChildProcesses(trace []byte, from int) []Child {
	final, found := FinalExec(trace)
	type made struct {
		child   Child
		process bool
	}
	var all []made
	parent := map[string]string{}
	pending := map[string]string{}

	for i, text := range strings.Split(string(trace), "\n") {
		m := callLine.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		caller := m[1] + m[2]
		call, resumed := m[3], false
		if call == "" {
			call, resumed = m[4], true
		}
		callText := text
		if resumed {
			callText = pending[caller] + text
			delete(pending, caller)
		} else if strings.HasSuffix(strings.TrimSpace(text), "<unfinished ...>") {
			pending[caller] = text
			continue
		}
		result, ok := ResultOf(callText)
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(result)
		if err != nil || pid <= 0 {
			continue
		}
		child := strconv.Itoa(pid)
		parent[child] = caller
		isProcess := call == "fork" || call == "vfork" || !threadFlag.MatchString(callText)
		all = append(all, made{Child{Pid: child, Parent: caller, Call: call, Line: i}, isProcess})
	}

	var out []Child
	for _, c := range all {
		if !c.process || c.child.Line < from {
			continue
		}
		if found && !reaches(parent, c.child.Parent, final.Pid) {
			continue
		}
		out = append(out, c.child)
	}
	return out
}

// reaches says whether pid is target or has it among its ancestors in parent. A loop (pid reuse)
// ends the walk.
func reaches(parent map[string]string, pid, target string) bool {
	seen := map[string]bool{}
	for !seen[pid] {
		if pid == target {
			return true
		}
		seen[pid] = true
		up, ok := parent[pid]
		if !ok {
			return false
		}
		pid = up
	}
	return false
}

package trace

import (
	"reflect"
	"strings"
	"testing"
)

const childHead = `10 execve("/app", ["app"], 0x7ff /* 1 vars */) = 0` + "\n"

func lines(l ...string) []byte { return []byte(strings.Join(l, "\n") + "\n") }

func TestChildProcessesRealNginx(t *testing.T) {
	raw := readFixture(t, "nginx.strace")
	final, ok := FinalExec(raw)
	if !ok {
		t.Fatal("no final program")
	}
	got := ChildProcesses(raw, final.Line)
	want := []Child{
		{Pid: "39", Parent: "9", Call: "fork", Line: 247},
		{Pid: "40", Parent: "9", Call: "fork", Line: 250},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("children %+v, want %+v", got, want)
	}
	// read from the start, the forks of the entrypoint scripts are children too
	if all := ChildProcesses(raw, 0); len(all) <= len(want) {
		t.Errorf("from 0: %d children, want more than %d", len(all), len(want))
	}
}

func TestChildProcessesRealEntrypointScript(t *testing.T) {
	raw := readFixture(t, "entrypoint-script.strace")
	final, _ := FinalExec(raw)
	got := ChildProcesses(raw, final.Line)
	want := []Child{
		{Pid: "23", Parent: "9", Call: "fork", Line: 10},
		{Pid: "24", Parent: "9", Call: "fork", Line: 11},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("children %+v, want %+v", got, want)
	}
}

func TestChildProcessesCalls(t *testing.T) {
	raw := lines(
		strings.TrimSuffix(childHead, "\n"),
		`10 clone(child_stack=NULL, flags=CLONE_CHILD_CLEARTID|CLONE_CHILD_SETTID|SIGCHLD, child_tidptr=0x7f1) = 13`,
		`10 clone(child_stack=0x7f2, flags=CLONE_VM|CLONE_FS|CLONE_FILES|CLONE_SIGHAND|CLONE_THREAD|CLONE_SYSVSEM|CLONE_SETTLS) = 14`,
		`10 clone3({flags=CLONE_VM|CLONE_FS|CLONE_FILES|CLONE_SIGHAND|CLONE_THREAD|CLONE_SYSVSEM, exit_signal=0}, 88) = 15`,
		`10 clone3({flags=CLONE_VM|CLONE_VFORK, exit_signal=SIGCHLD}, 88) = 16`,
		`10 vfork() = 17`,
		`10 fork() = -1 EAGAIN (Resource temporarily unavailable)`,
		`10 clone(child_stack=NULL, flags=SIGCHLD) = -1 ENOMEM (Cannot allocate memory)`,
		`[pid    10] fork() = 18`,
	)
	got := ChildProcesses(raw, 0)
	want := []Child{
		{Pid: "13", Parent: "10", Call: "clone", Line: 1},
		{Pid: "16", Parent: "10", Call: "clone3", Line: 4},
		{Pid: "17", Parent: "10", Call: "vfork", Line: 5},
		{Pid: "18", Parent: "10", Call: "fork", Line: 8},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("children %+v, want %+v", got, want)
	}
}

func TestChildProcessesUnfinished(t *testing.T) {
	raw := lines(
		strings.TrimSuffix(childHead, "\n"),
		`10 fork( <unfinished ...>`,
		`11 getpid() = 11`,
		`10 <... fork resumed>) = 11`,
		`10 clone(child_stack=0x7f2, flags=CLONE_VM|CLONE_FS|CLONE_THREAD <unfinished ...>`,
		`10 <... clone resumed>, parent_tid=[12], tls=0x7f3) = 12`,
		`10 clone(child_stack=NULL, flags=CLONE_CHILD_CLEARTID|SIGCHLD <unfinished ...>`,
		`10 <... clone resumed>, child_tidptr=0x7f1) = 13`,
		`10 fork( <unfinished ...>`,
		`10 <... fork resumed>) = -1 EAGAIN (Resource temporarily unavailable)`,
		`10 fork( <unfinished ...>`,
	)
	got := ChildProcesses(raw, 0)
	want := []Child{
		{Pid: "11", Parent: "10", Call: "fork", Line: 3},
		{Pid: "13", Parent: "10", Call: "clone", Line: 7},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("children %+v, want %+v", got, want)
	}

	// the thread flag may be on the resumed part
	raw = lines(
		strings.TrimSuffix(childHead, "\n"),
		`10 clone(child_stack=0x7f2 <unfinished ...>`,
		`10 <... clone resumed>, flags=CLONE_VM|CLONE_THREAD, tls=0x7f3) = 12`,
	)
	if got := ChildProcesses(raw, 0); len(got) != 0 {
		t.Errorf("a thread was counted: %+v", got)
	}
}

func TestChildProcessesFromAndAncestry(t *testing.T) {
	raw := lines(
		`9 execve("/entry.sh", ["entry.sh"], 0x7ff /* 1 vars */) = 0`,
		`9 fork() = 10`,
		`10 fork() = 15`,
		`9 +++ exited with 0 +++`,
		`10 execve("/app", ["app"], 0x7ff /* 1 vars */) = 0`,
		`10 fork() = 11`,
		`10 clone(child_stack=0x7f2, flags=CLONE_VM|CLONE_THREAD) = 12`,
		`12 fork() = 13`,
		`11 fork() = 14`,
		`77 fork() = 78`,
		`78 fork() = 79`,
	)
	pidsFrom := func(from int) []string {
		var pids []string
		for _, c := range ChildProcesses(raw, from) {
			pids = append(pids, c.Pid)
		}
		return pids
	}
	// 13 comes from a thread of the program, 14 from a child of it; 10 is a child of the script,
	// 78 and 79 are from a stranger
	if got, want := pidsFrom(4), []string{"11", "13", "14"}; !reflect.DeepEqual(got, want) {
		t.Errorf("from 4: %q, want %q", got, want)
	}
	// a fork before from is not counted, one at from is
	if got, want := pidsFrom(0), []string{"15", "11", "13", "14"}; !reflect.DeepEqual(got, want) {
		t.Errorf("from 0: %q, want %q", got, want)
	}
	if got, want := pidsFrom(5), []string{"11", "13", "14"}; !reflect.DeepEqual(got, want) {
		t.Errorf("from 5: %q, want %q", got, want)
	}
	if got, want := pidsFrom(6), []string{"13", "14"}; !reflect.DeepEqual(got, want) {
		t.Errorf("from 6: %q, want %q", got, want)
	}
}

func TestChildProcessesNone(t *testing.T) {
	if got := ChildProcesses([]byte(""), 0); len(got) != 0 {
		t.Errorf("empty trace: %+v", got)
	}
	raw := lines(
		strings.TrimSuffix(childHead, "\n"),
		`10 openat(AT_FDCWD, "/etc/fork() = 5", O_RDONLY) = 3`,
	)
	if got := ChildProcesses(raw, 0); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

package trace

import (
	"strings"
	"testing"
)

// execLine returns the number of the first line of the trace that holds text.
func lineOf(t *testing.T, trace, text string) int {
	t.Helper()
	for i, l := range strings.Split(trace, "\n") {
		if strings.Contains(l, text) {
			return i
		}
	}
	t.Fatalf("no line with %q", text)
	return -1
}

func TestCwdAtExec(t *testing.T) {
	tests := []struct {
		name  string
		trace string
		find  string
		start string
		want  string
	}{
		{"the directory of the image", `1     execve("/bin/a", ["a"], 0x1 /* 1 var */) = 0
`, "/bin/a", "/app", "/app"},
		{"a chdir before the execve, and one after it", `1     execve("/entry.sh", ["entry.sh"], 0x1 /* 1 var */) = 0
1     chdir("/etc/shlink") = 0
1     execve("./bin/rr", ["rr"], 0x1 /* 1 var */) = 0
1     chdir("/etc/shlink/config") = 0
`, "./bin/rr", "/", "/etc/shlink"},
		{"a relative chdir", `1     chdir("sub") = 0
1     execve("/bin/a", ["a"], 0x1 /* 1 var */) = 0
`, "/bin/a", "/srv", "/srv/sub"},
		{"a child starts where its parent was", `1     chdir("/data") = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 7
7     execve("/bin/a", ["a"], 0x1 /* 1 var */) = 0
`, "/bin/a", "/", "/data"},
		{"a child that chdirs does not move its parent", `1     clone(child_stack=NULL, flags=SIGCHLD) = 7
7     chdir("/elsewhere") = 0
1     execve("/bin/a", ["a"], 0x1 /* 1 var */) = 0
`, "/bin/a", "/app", "/app"},
		{"a fchdir makes it unknown", `1     fchdir(3) = 0
1     execve("/bin/a", ["a"], 0x1 /* 1 var */) = 0
`, "/bin/a", "/app", ""},
		{"a chdir that failed changes nothing", `1     chdir("/nope") = -1 ENOENT (No such file or directory)
1     execve("/bin/a", ["a"], 0x1 /* 1 var */) = 0
`, "/bin/a", "/app", "/app"},
		{"an execve cut in two", `1     chdir("/w") = 0
1     execve("/bin/a", ["a"], 0x1 /* 1 var */ <unfinished ...>
1     <... execve resumed>) = 0
`, `execve("/bin/a"`, "/", "/w"},
		{"a line that is not an execve", `1     chdir("/w") = 0
1     openat(AT_FDCWD, "/etc/x", O_RDONLY) = 3
`, "openat", "/", ""},
		// modelled on strace 5.16 (the fixture is written by hand): a thread of the leader changes directory, a vfork child of another
		// thread executes: all of them share the directory
		{"a thread and its leader share the directory", `1     execve("/bin/sh", ["sh"], 0x1 /* 1 var */) = 0
1     clone3({flags=CLONE_VM|CLONE_FS|CLONE_FILES|CLONE_SIGHAND|CLONE_THREAD|CLONE_SYSVSEM|CLONE_SETTLS, child_tid=0x7f, tls=0x7f}, 88) = 5
1     chdir("/tmp") = 0
5     vfork( <unfinished ...>
6     execve("/bin/pwd", ["pwd"], 0x1 /* 1 var */ <unfinished ...>
5     <... vfork resumed>) = 6
6     <... execve resumed>) = 0
`, `execve("/bin/pwd"`, "/app", "/tmp"},
		{"a clone cut in two with CLONE_FS shares the directory too", `1     clone(child_stack=0x1, flags=CLONE_VM|CLONE_FS|SIGCHLD <unfinished ...>
1     <... clone resumed>, tls=0x1) = 5
1     chdir("/shared") = 0
5     execve("/bin/a", ["a"], 0x1 /* 1 var */) = 0
`, "/bin/a", "/app", "/shared"},
		{"a child seen before the resumed line of its clone with CLONE_FS keeps its chdir", `1     clone(child_stack=0x1, flags=CLONE_VM|CLONE_FS|SIGCHLD <unfinished ...>
7     chdir("/tmp") = 0
1     <... clone resumed>, tls=0x1) = 7
1     execve("/bin/a", ["a"], 0x1 /* 1 var */) = 0
`, "/bin/a", "/app", "/tmp"},
		{"a child seen first while another process was active copies its parent", `1     chdir("/data") = 0
9     chdir("/other") = 0
1     clone(child_stack=NULL, flags=SIGCHLD <unfinished ...>
9     chdir("/elsewhere") = 0
7     execve("/bin/a", ["a"], 0x1 /* 1 var */) = 0
1     <... clone resumed>) = 7
`, `execve("/bin/a"`, "/", "/data"},
		{"a thread made without CLONE_FS has its own copy of the directory", `1     clone(child_stack=0x1, flags=CLONE_VM|CLONE_SIGHAND|CLONE_THREAD|SIGCHLD) = 5
5     chdir("/threaddir") = 0
1     execve("/bin/a", ["a"], 0x1 /* 1 var */) = 0
`, "/bin/a", "/app", "/app"},
		{"a thread that execs under the pid of the leader", `7     chdir("/srv") = 0
7     clone(child_stack=0x7f00, flags=CLONE_VM|CLONE_FS|CLONE_THREAD, tls=0x7f1) = 8
8     chdir("/other") = 0
8     execve("/srv/go", ["go"], 0x7ffd /* 5 vars */ <pid changed to 7 ...>
7     +++ superseded by execve in pid 8 +++
7     <... execve resumed>) = 0
`, `execve("/srv/go"`, "/", "/other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := lineOf(t, tt.trace, tt.find)
			if got := CwdAtExec([]byte(tt.trace), tt.start, line); got != tt.want {
				t.Errorf("CwdAtExec = %q, want %q", got, tt.want)
			}
		})
	}
}

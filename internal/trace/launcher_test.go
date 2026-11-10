package trace

import (
	"reflect"
	"testing"
)

func TestLauncher(t *testing.T) {
	tests := []struct {
		name    string
		program string
		argv    []string
		want    string
		rest    []string
	}{
		{"npm run by node", "/usr/local/bin/node", []string{"node", "/usr/local/bin/npm", "run", "dev"}, "npm", []string{"run", "dev"}},
		{"node with an option first", "/usr/local/bin/node", []string{"node", "--no-warnings", "/usr/local/bin/npm", "start"}, "npm", []string{"start"}},
		{"npm cli file", "/usr/local/bin/node", []string{"node", "/usr/local/lib/node_modules/npm/bin/npm-cli.js", "start"}, "npm", []string{"start"}},
		{"npx cli file", "/usr/local/bin/node", []string{"node", "/usr/local/lib/node_modules/npm/bin/npx-cli.js", "serve"}, "npx", []string{"serve"}},
		{"yarn file", "/usr/local/bin/node", []string{"node", "/opt/yarn-v1.22.19/bin/yarn.js", "start"}, "yarn", []string{"start"}},
		{"pnpm file", "/usr/local/bin/node", []string{"node", "/usr/local/lib/node_modules/pnpm/bin/pnpm.cjs", "dev"}, "pnpm", []string{"dev"}},
		{"launcher itself", "/usr/local/bin/yarn", []string{"yarn", "start"}, "yarn", []string{"start"}},
		{"shell form", "/bin/sh", []string{"/bin/sh", "-c", "npm start"}, "npm", []string{"start"}},
		{"shell form with exec and a variable", "/bin/sh", []string{"sh", "-c", "exec NODE_ENV=production yarn run serve"}, "yarn", []string{"run", "serve"}},
		{"node running the program", "/usr/local/bin/node", []string{"node", "server.js"}, "", nil},
		{"shell running a program", "/bin/sh", []string{"sh", "-c", "java -jar app.jar"}, "", nil},
		{"shell with a script", "/bin/sh", []string{"sh", "/entry.sh"}, "", nil},
		{"a file that only ends in npm", "/usr/local/bin/node", []string{"node", "/app/mynpm.js"}, "", nil},
		{"no argv", "/usr/local/bin/node", nil, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, rest, ok := Launcher(tt.program, tt.argv)
			if got != tt.want || ok != (tt.want != "") || !reflect.DeepEqual(rest, tt.rest) {
				t.Errorf("Launcher = %q, %v, %v; want %q, %v", got, rest, ok, tt.want, tt.rest)
			}
		})
	}
}

func TestScriptOf(t *testing.T) {
	tests := []struct {
		launcher string
		rest     []string
		want     string
	}{
		{"npm", []string{"start"}, "start"},
		{"npm", []string{"run", "dev"}, "dev"},
		{"npm", []string{"run-script", "--silent", "dev"}, "dev"},
		{"npm", []string{"install"}, ""},
		{"npm", nil, ""},
		{"yarn", []string{"dev"}, "dev"},
		{"yarn", []string{"run", "dev"}, "dev"},
		{"pnpm", []string{"run", "dev"}, "dev"},
		{"npx", []string{"serve", "-s"}, ""},
	}
	for _, tt := range tests {
		if got := ScriptOf(tt.launcher, tt.rest); got != tt.want {
			t.Errorf("ScriptOf(%q, %v) = %q, want %q", tt.launcher, tt.rest, got, tt.want)
		}
	}
}

func TestFinalExecPassesOverALauncher(t *testing.T) {
	tests := []struct {
		name  string
		trace string
		want  Exec
	}{
		{"npm, its shell and the script", `1     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "run", "dev"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 7
7     execve("/bin/sh", ["sh", "-c", "react-scripts start"], 0x7ffd1 /* 5 vars */) = 0
7     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 8
8     execve("/usr/local/bin/node", ["node", "/app/node_modules/.bin/react-scripts", "start"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "8", Path: "/usr/local/bin/node", Argv: []string{"node", "/app/node_modules/.bin/react-scripts", "start"}, Line: 4}},
		{"yarn that starts the script directly", `1     execve("/usr/local/bin/node", ["node", "/opt/yarn-v1.22.19/bin/yarn.js", "start"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 9
9     execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "9", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 2}},
		{"a launcher with a finished helper and one running script", `1     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "start"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 6
6     execve("/usr/bin/git", ["git", "status"], 0x7ffd1 /* 5 vars */) = 0
6     +++ exited with 0 +++
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 9
9     execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "9", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 5}},
		{"a launcher with two running children stays", `1     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "start"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 7
7     execve("/usr/local/bin/node", ["node", "a.js"], 0x7ffd1 /* 1 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 8
8     execve("/usr/local/bin/node", ["node", "b.js"], 0x7ffd1 /* 1 vars */) = 0
`, Exec{Pid: "1", Path: "/usr/local/bin/node", Argv: []string{"node", "/usr/local/bin/npm", "start"}, Line: 0}},
		{"a launcher with no running child stays", `1     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "start"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "1", Path: "/usr/local/bin/node", Argv: []string{"node", "/usr/local/bin/npm", "start"}, Line: 0}},
		{"node that is not a launcher stays", `1     execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 7
7     execve("/usr/bin/worker", ["worker"], 0x7ffd1 /* 1 vars */) = 0
`, Exec{Pid: "1", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := FinalExec([]byte(tt.trace))
			if !found || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FinalExec = %+v, %v; want %+v", got, found, tt.want)
			}
		})
	}
}

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
		{"yarn berry release", "/usr/local/bin/node", []string{"node", "/app/.yarn/releases/yarn-4.1.0.cjs", "start"}, "yarn", []string{"start"}},
		{"corepack shim", "/usr/local/bin/node", []string{"node", "/usr/local/lib/node_modules/corepack/dist/pnpm.js", "dev"}, "pnpm", []string{"dev"}},
		{"bare name npm.js of the user", "/usr/local/bin/node", []string{"node", "/app/npm.js"}, "", nil},
		{"bare name yarn.js of the user", "/usr/local/bin/node", []string{"node", "/app/yarn.js", "start"}, "", nil},
		{"bare name pnpm of the user", "/usr/local/bin/node", []string{"node", "/app/bin/pnpm"}, "", nil},
		{"user program called yarn in its own directory", "/app/bin/yarn", []string{"yarn"}, "", nil},
		{"busybox as the shell", "/bin/busybox", []string{"sh", "-c", "npm start"}, "npm", []string{"start"}},
		{"busybox named by its first argument", "/bin/busybox", []string{"busybox", "sh", "-c", "yarn start"}, "yarn", []string{"start"}},
		{"busybox that is not a shell", "/bin/busybox", []string{"httpd", "-f"}, "", nil},
		{"compound and", "/bin/sh", []string{"sh", "-c", "npm run build && node server.js"}, "", nil},
		{"compound or", "/bin/sh", []string{"sh", "-c", "npm start || true"}, "", nil},
		{"compound semicolon", "/bin/sh", []string{"sh", "-c", "npm install; npm start"}, "", nil},
		{"compound pipe", "/bin/sh", []string{"sh", "-c", "npm start | tee log"}, "", nil},
		{"compound background", "/bin/sh", []string{"sh", "-c", "npm start &"}, "", nil},
		{"compound newline", "/bin/sh", []string{"sh", "-c", "npm install\nnpm start"}, "", nil},
		{"compound substitution", "/bin/sh", []string{"sh", "-c", "npm start $(cat x)"}, "", nil},
		{"compound backtick", "/bin/sh", []string{"sh", "-c", "npm start `cat x`"}, "", nil},
		{"compound quotes", "/bin/sh", []string{"sh", "-c", "npm run 'a b'"}, "", nil},
		{"env prefix", "/bin/sh", []string{"sh", "-c", "env NODE_ENV=production npm start"}, "npm", []string{"start"}},
		{"env -i prefix", "/bin/sh", []string{"sh", "-c", "env -i PATH=/bin npm start"}, "npm", []string{"start"}},
		{"node require option with a value", "/usr/local/bin/node", []string{"node", "-r", "dotenv/config", "/usr/local/bin/npm", "start"}, "npm", []string{"start"}},
		{"node import option with a value", "/usr/local/bin/node", []string{"node", "--import", "x", "/usr/local/bin/npm", "start"}, "npm", []string{"start"}},
		{"node eval", "/usr/local/bin/node", []string{"node", "-e", "require('/usr/local/bin/npm')"}, "", nil},
		{"node print", "/usr/local/bin/node", []string{"node", "-p", "/usr/local/bin/npm"}, "", nil},
		{"node stdin", "/usr/local/bin/node", []string{"node", "-", "/usr/local/bin/npm"}, "", nil},
		{"node value of an option is not the script", "/usr/local/bin/node", []string{"node", "--title", "/usr/local/bin/npm", "server.js"}, "", nil},
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
		launcher  string
		rest      []string
		want      string
		elsewhere bool
	}{
		{"npm", []string{"start"}, "start", false},
		{"npm", []string{"run", "dev"}, "dev", false},
		{"npm", []string{"run-script", "--silent", "dev"}, "dev", false},
		{"npm", []string{"rum", "dev"}, "dev", false},
		{"npm", []string{"urn", "dev"}, "dev", false},
		{"npm", []string{"t"}, "test", false},
		{"npm", []string{"tst"}, "test", false},
		{"npm", []string{"install"}, "", false},
		{"npm", []string{"exec", "serve"}, "", false},
		{"npm", nil, "", false},
		{"npm", []string{"--prefix", "/app", "run", "dev"}, "dev", true},
		{"npm", []string{"--prefix=/app", "start"}, "start", true},
		{"npm", []string{"-w", "web", "run", "dev"}, "dev", true},
		{"npm", []string{"run", "dev", "--", "--port", "80"}, "dev", false},
		{"yarn", []string{"dev"}, "dev", false},
		{"yarn", []string{"run", "dev"}, "dev", false},
		{"yarn", []string{"--cwd", "/app", "start"}, "start", true},
		{"yarn", []string{"workspace", "web", "start"}, "", false},
		{"yarn", []string{"node", "server.js"}, "", false},
		{"yarn", []string{"exec", "x"}, "", false},
		{"yarn", []string{"install"}, "", false},
		{"pnpm", []string{"run", "dev"}, "dev", false},
		{"pnpm", []string{"-C", "/app", "start"}, "start", true},
		{"pnpm", []string{"--filter", "web", "dev"}, "dev", true},
		{"pnpm", []string{"-w", "dev"}, "dev", true},
		{"pnpm", []string{"exec", "x"}, "", false},
		{"pnpm", []string{"dlx", "x"}, "", false},
		{"npx", []string{"serve", "-s"}, "", false},
		{"pnpx", []string{"serve"}, "", false},
	}
	for _, tt := range tests {
		got, elsewhere := ScriptOf(tt.launcher, tt.rest)
		if got != tt.want || elsewhere != tt.elsewhere {
			t.Errorf("ScriptOf(%q, %v) = %q, %v; want %q, %v", tt.launcher, tt.rest, got, elsewhere, tt.want, tt.elsewhere)
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
`, Exec{Pid: "8", Path: "/usr/local/bin/node", Argv: []string{"node", "/app/node_modules/.bin/react-scripts", "start"}, Line: 4,
			PassedOver: []Exec{{Pid: "1", Path: "/usr/local/bin/node", Argv: []string{"node", "/usr/local/bin/npm", "run", "dev"}, Line: 0}}}},
		{"yarn that starts the script directly", `1     execve("/usr/local/bin/node", ["node", "/opt/yarn-v1.22.19/bin/yarn.js", "start"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 9
9     execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "9", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 2,
			PassedOver: []Exec{{Pid: "1", Path: "/usr/local/bin/node", Argv: []string{"node", "/opt/yarn-v1.22.19/bin/yarn.js", "start"}, Line: 0}}}},
		{"a launcher with a finished helper and one running script", `1     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "start"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 6
6     execve("/usr/bin/git", ["git", "status"], 0x7ffd1 /* 5 vars */) = 0
6     +++ exited with 0 +++
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 9
9     execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "9", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 5,
			PassedOver: []Exec{{Pid: "1", Path: "/usr/local/bin/node", Argv: []string{"node", "/usr/local/bin/npm", "start"}, Line: 0}}}},
		{"yarn 1 that starts yarn berry that starts the program", `1     execve("/usr/local/bin/node", ["node", "/opt/yarn-v1.22.19/bin/yarn.js", "start"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 7
7     execve("/usr/local/bin/node", ["node", "/app/.yarn/releases/yarn-4.1.0.cjs", "start"], 0x7ffd1 /* 5 vars */) = 0
7     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 8
8     execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "8", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 4,
			PassedOver: []Exec{
				{Pid: "1", Path: "/usr/local/bin/node", Argv: []string{"node", "/opt/yarn-v1.22.19/bin/yarn.js", "start"}, Line: 0},
				{Pid: "7", Path: "/usr/local/bin/node", Argv: []string{"node", "/app/.yarn/releases/yarn-4.1.0.cjs", "start"}, Line: 2},
			}}},
		{"a program of the user named npm.js with one child stays", `1     execve("/usr/local/bin/node", ["node", "/app/npm.js"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 7
7     execve("/usr/bin/worker", ["worker"], 0x7ffd1 /* 1 vars */) = 0
`, Exec{Pid: "1", Path: "/usr/local/bin/node", Argv: []string{"node", "/app/npm.js"}, Line: 0}},
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

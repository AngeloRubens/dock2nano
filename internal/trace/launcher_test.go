package trace

import (
	"reflect"
	"strings"
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
		{"node eval", "/usr/local/bin/node", []string{"node", "-e", "/usr/local/bin/npm"}, "", nil},
		{"node print", "/usr/local/bin/node", []string{"node", "-p", "/usr/local/bin/npm"}, "", nil},
		{"node stdin", "/usr/local/bin/node", []string{"node", "-", "/usr/local/bin/npm"}, "", nil},
		{"nvm layout", "/root/.nvm/versions/node/v20.15.0/bin/node", []string{"node", "/root/.nvm/versions/node/v20.15.0/bin/npm", "start"}, "npm", []string{"start"}},
		{"nvm link as the program", "/home/app/.nvm/versions/node/v18.0.0/bin/yarn", []string{"yarn", "dev"}, "yarn", []string{"dev"}},
		{"npm-global layout", "/usr/local/bin/node", []string{"node", "/home/app/.npm-global/bin/pnpm", "dev"}, "pnpm", []string{"dev"}},
		{"volta layout", "/usr/local/bin/node", []string{"node", "/root/.volta/bin/yarn", "start"}, "yarn", []string{"start"}},
		{"yarn from npm", "/usr/local/bin/node", []string{"node", "/usr/local/lib/node_modules/yarn/bin/yarn.js", "start"}, "yarn", []string{"start"}},
		{"corepack runs a package manager", "/usr/local/bin/node", []string{"node", "/usr/local/lib/node_modules/corepack/dist/corepack.js", "yarn", "start"}, "yarn", []string{"start"}},
		{"corepack link as the program", "/usr/local/bin/corepack", []string{"corepack", "pnpm", "run", "dev"}, "pnpm", []string{"run", "dev"}},
		{"corepack in a shell line", "/bin/sh", []string{"sh", "-c", "corepack yarn start"}, "yarn", []string{"start"}},
		{"corepack alone", "/usr/local/bin/corepack", []string{"corepack", "enable"}, "", nil},
		{"corepack store", "/usr/local/bin/node", []string{"node", "/root/.cache/node/corepack/v1/yarn/1.22.19/bin/yarn.js", "start"}, "yarn", []string{"start"}},
		{"a directory of the user called corepack", "/usr/local/bin/node", []string{"node", "/app/corepack/yarn.js", "start"}, "", nil},
		{"shell option cluster -ec", "/bin/sh", []string{"sh", "-ec", "npm start"}, "npm", []string{"start"}},
		{"shell option cluster -lc", "/bin/bash", []string{"bash", "-lc", "yarn start"}, "yarn", []string{"start"}},
		{"shell -x is not a command option", "/bin/sh", []string{"sh", "-x", "npm start"}, "", nil},
		{"env -u", "/bin/sh", []string{"sh", "-c", "env -u FOO npm start"}, "npm", []string{"start"}},
		{"script operand stops the search for -c", "/bin/sh", []string{"sh", "/entry.sh", "-c", "npm start"}, "", nil},
		{"-o with a value before -c", "/bin/sh", []string{"sh", "-o", "errexit", "-c", "npm start"}, "npm", []string{"start"}},
		{"cluster with c not last", "/bin/sh", []string{"sh", "-ce", "npm start"}, "npm", []string{"start"}},
		{"relative npm-cli path", "/usr/local/bin/node", []string{"node", "node_modules/npm/bin/npm-cli.js", "start"}, "npm", []string{"start"}},
		{"corepack with a version", "/usr/local/bin/corepack", []string{"corepack", "yarn@4.1.0", "start"}, "yarn", []string{"start"}},
		{"a directory called vendor is not nvm", "/usr/local/bin/node", []string{"node", "/opt/node/vendor/bin/npm", "start"}, "", nil},
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
		{"yarn", []string{"global", "add", "x"}, "", false},
		{"yarn", []string{"audit"}, "", false},
		{"yarn", []string{"help"}, "", false},
		{"pnpm", []string{"outdated"}, "", false},
		{"pnpm", []string{"store", "status"}, "", false},
		{"yarn", []string{"explain", "x"}, "", false},
		{"pnpm", []string{"run", "dev"}, "dev", false},
		{"pnpm", []string{"-C", "/app", "start"}, "start", true},
		{"pnpm", []string{"--filter", "web", "dev"}, "dev", true},
		{"pnpm", []string{"-w", "dev"}, "dev", true},
		{"pnpm", []string{"exec", "x"}, "", false},
		{"pnpm", []string{"dlx", "x"}, "", false},
		{"yarn", []string{"--network-timeout", "100000", "start"}, "start", false},
		{"pnpm", []string{"--reporter", "silent", "start"}, "start", false},
		{"npm", []string{"--loglevel", "silent", "start"}, "start", false},
		{"npm", []string{"--registry", "http://r", "run", "dev"}, "dev", false},
		{"yarnpkg", []string{"--mutex", "network", "dev"}, "dev", false},
		{"npm", []string{"--workspaces", "run", "dev"}, "dev", true},
		{"npm", []string{"--ws", "run", "dev"}, "dev", true},
		{"pnpm", []string{"-r", "dev"}, "dev", true},
		{"pnpm", []string{"--recursive", "run", "dev"}, "dev", true},
		{"pnpm", []string{"--workspace-root", "dev"}, "dev", true},
		{"npx", []string{"serve", "-s"}, "", false},
		{"pnpx", []string{"serve"}, "", false},
	}
	for _, tt := range tests {
		got, elsewhere, _ := ScriptRun(tt.launcher, tt.rest)
		if got != tt.want || elsewhere != tt.elsewhere {
			t.Errorf("ScriptRun(%q, %v) = %q, %v; want %q, %v", tt.launcher, tt.rest, got, elsewhere, tt.want, tt.elsewhere)
		}
	}
}

func TestScriptRun(t *testing.T) {
	tests := []struct {
		launcher string
		rest     []string
		script   string
		viaRun   bool
	}{
		{"pnpm", []string{"run", "foo"}, "foo", true},
		{"pnpm", []string{"foo"}, "foo", false},
		{"npm", []string{"run", "dev"}, "dev", true},
		{"npm", []string{"start"}, "start", false},
		{"yarn", []string{"run", "dev"}, "dev", true},
	}
	for _, tt := range tests {
		script, _, viaRun := ScriptRun(tt.launcher, tt.rest)
		if script != tt.script || viaRun != tt.viaRun {
			t.Errorf("ScriptRun(%q, %v) = %q, %v; want %q, %v", tt.launcher, tt.rest, script, viaRun, tt.script, tt.viaRun)
		}
	}
}

func TestLabel(t *testing.T) {
	tests := []struct {
		name    string
		program string
		argv    []string
		want    string
	}{
		{"npm with a script", "/usr/local/bin/node", []string{"node", "/usr/local/bin/npm", "run", "dev", "--token", "abc123"}, `npm (script "dev")`},
		{"npm start", "/usr/local/bin/node", []string{"node", "/usr/local/bin/npm", "start"}, `npm (script "start")`},
		{"npx has no script", "/usr/local/bin/node", []string{"node", "/usr/local/lib/node_modules/npm/bin/npx-cli.js", "serve"}, "npx"},
		{"shell with a command", "/bin/sh", []string{"sh", "-c", "node server.js --api-key abc123"}, "sh -c"},
		{"shell with a script file", "/bin/sh", []string{"sh", "/entrypoint.sh", "--token", "abc123"}, "sh (script file)"},
		{"busybox shell", "/bin/busybox", []string{"sh", "-c", "x"}, "sh -c"},
		{"bash", "/bin/bash", []string{"bash", "-lc", "x"}, "bash -c"},
		{"a program is its name only", "/usr/local/bin/node", []string{"node", "migrate.js", "--password", "abc123"}, "node"},
		{"a program without argv", "/usr/bin/git", nil, "git"},
		{"argv[0] with a control character", "/usr/local/bin/node", []string{"node\x1b[31m"}, "node"},
		{"argv[0] with a space", "/usr/bin/git", []string{"git status"}, "git"},
	}
	for _, tt := range tests {
		got := Label(tt.program, tt.argv)
		if got != tt.want || strings.Contains(got, "abc123") {
			t.Errorf("%s: Label = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestSupervisor(t *testing.T) {
	tests := []struct {
		name    string
		program string
		argv    []string
		want    string
	}{
		{"pm2-runtime in a bin directory", "/usr/local/bin/pm2-runtime", []string{"pm2-runtime", "app.js"}, "pm2-runtime"},
		{"pm2-runtime in an nvm bin directory", "/root/.nvm/versions/node/v20.1.0/bin/pm2-runtime", []string{"pm2-runtime", "app.js"}, "pm2-runtime"},
		{"a program of the user called nodemon in a bin directory of the user", "/app/bin/nodemon", []string{"nodemon"}, ""},
		{"nodemon bin", "/usr/local/bin/node", []string{"node", "/usr/src/app/node_modules/.bin/nodemon", "server.js"}, "nodemon"},
		{"nodemon package", "/usr/local/bin/node", []string{"node", "/app/node_modules/nodemon/bin/nodemon.js", "server.js"}, "nodemon"},
		{"node with an option first", "/usr/local/bin/node", []string{"node", "-r", "x", "/app/node_modules/.bin/ts-node-dev", "a.ts"}, "ts-node-dev"},
		{"pm2-runtime", "/usr/local/bin/node", []string{"node", "/usr/local/lib/node_modules/pm2/bin/pm2-runtime", "app.js"}, "pm2"},
		{"concurrently", "/usr/local/bin/node", []string{"node", "/app/node_modules/.bin/concurrently", "a", "b"}, "concurrently"},
		{"the program itself", "/app/node_modules/.bin/nodemon", []string{"nodemon"}, "nodemon"},
		{"a program of the user", "/usr/local/bin/node", []string{"node", "/app/server.js"}, ""},
		{"a file called nodemon outside node_modules", "/usr/local/bin/node", []string{"node", "/app/nodemon.js"}, ""},
		{"node -e", "/usr/local/bin/node", []string{"node", "-e", "/app/node_modules/.bin/nodemon"}, ""},
	}
	for _, tt := range tests {
		if got := Supervisor(tt.program, tt.argv); got != tt.want {
			t.Errorf("%s: Supervisor = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestShellCommand(t *testing.T) {
	tests := []struct {
		name    string
		program string
		argv    []string
		want    string
		ok      bool
	}{
		{"sh -c", "/bin/sh", []string{"sh", "-c", "a && b"}, "a && b", true},
		{"busybox sh -c", "/bin/busybox", []string{"sh", "-c", "a; b"}, "a; b", true},
		{"script operand first", "/bin/sh", []string{"sh", "/e.sh", "-c", "a"}, "", false},
		{"rcfile with a value", "/bin/bash", []string{"bash", "--rcfile", "/f", "-c", "a"}, "a", true},
		{"node is not a shell", "/usr/local/bin/node", []string{"node", "-c", "a"}, "", false},
	}
	for _, tt := range tests {
		got, ok := ShellCommand(tt.program, tt.argv)
		if got != tt.want || ok != tt.ok {
			t.Errorf("%s: ShellCommand = %q, %v; want %q, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
	if !IsCompound("a && b") || IsCompound("npm start") {
		t.Errorf("IsCompound is wrong")
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
			PassedOver: []Exec{
				{Pid: "1", Path: "/usr/local/bin/node", Argv: []string{"node", "/usr/local/bin/npm", "run", "dev"}, Line: 0},
				{Pid: "7", Path: "/bin/sh", Argv: []string{"sh", "-c", "react-scripts start"}, Line: 2},
			}}},
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
			NotRun:     []string{"git"},
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
		{"a shell that is itself a launcher is passed over", `1     execve("/bin/sh", ["sh", "-c", "npm start"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 7
7     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "start"], 0x7ffd1 /* 5 vars */) = 0
7     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 8
8     execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "8", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 4,
			PassedOver: []Exec{
				{Pid: "1", Path: "/bin/sh", Argv: []string{"sh", "-c", "npm start"}, Line: 0},
				{Pid: "7", Path: "/usr/local/bin/node", Argv: []string{"node", "/usr/local/bin/npm", "start"}, Line: 2},
			}}},
		{"the shell of a compound script is recorded", `1     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "start"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 7
7     execve("/bin/sh", ["sh", "-c", "node migrate.js && node server.js"], 0x7ffd1 /* 5 vars */) = 0
7     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 8
8     execve("/usr/local/bin/node", ["node", "migrate.js"], 0x7ffd1 /* 5 vars */) = 0
8     +++ exited with 0 +++
7     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 9
9     execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "9", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 7,
			NotRun: []string{"node"},
			PassedOver: []Exec{
				{Pid: "1", Path: "/usr/local/bin/node", Argv: []string{"node", "/usr/local/bin/npm", "start"}, Line: 0},
				{Pid: "7", Path: "/bin/sh", Argv: []string{"sh", "-c", "node migrate.js && node server.js"}, Line: 2},
			}}},
		{"a shell of the entrypoint before the launcher is recorded", `1     execve("/bin/sh", ["sh", "/entrypoint.sh"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 7
7     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "start"], 0x7ffd1 /* 5 vars */) = 0
7     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 8
8     execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "8", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 4,
			PassedOver: []Exec{
				{Pid: "1", Path: "/bin/sh", Argv: []string{"sh", "/entrypoint.sh"}, Line: 0},
				{Pid: "7", Path: "/usr/local/bin/node", Argv: []string{"node", "/usr/local/bin/npm", "start"}, Line: 2},
			}}},
		{"the shell form of CMD with a migration before npm start", `1     execve("/bin/sh", ["sh", "-c", "npm run migrate && npm start"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 7
7     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "run", "migrate"], 0x7ffd1 /* 5 vars */) = 0
7     +++ exited with 0 +++
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 8
8     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "start"], 0x7ffd1 /* 5 vars */) = 0
8     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 9
9     execve("/bin/sh", ["sh", "-c", "node server.js"], 0x7ffd1 /* 5 vars */) = 0
9     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 10
10    execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "10", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 9,
			NotRun: []string{`npm (script "migrate")`},
			PassedOver: []Exec{
				{Pid: "1", Path: "/bin/sh", Argv: []string{"sh", "-c", "npm run migrate && npm start"}, Line: 0},
				{Pid: "8", Path: "/usr/local/bin/node", Argv: []string{"node", "/usr/local/bin/npm", "start"}, Line: 5},
				{Pid: "9", Path: "/bin/sh", Argv: []string{"sh", "-c", "node server.js"}, Line: 7},
			}}},
		{"a shell that forks for its first command and executes its last", `11    execve("/bin/sh", ["sh", "-c", "npm run migrate && npm start"], 0x7ffd1 /* 5 vars */) = 0
11    clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 12
12    execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "run", "migrate"], 0x7ffd1 /* 5 vars */) = 0
12    +++ exited with 0 +++
11    execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "start"], 0x7ffd1 /* 5 vars */) = 0
11    clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 13
13    execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "13", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 6,
			NotRun: []string{`npm (script "migrate")`},
			PassedOver: []Exec{
				{Pid: "11", Path: "/bin/sh", Argv: []string{"sh", "-c", "npm run migrate && npm start"}, Line: 0},
				{Pid: "11", Path: "/usr/local/bin/node", Argv: []string{"node", "/usr/local/bin/npm", "start"}, Line: 4},
			}}},
		{"yarn berry that runs two commands itself", `1     execve("/usr/local/bin/node", ["node", "/app/.yarn/releases/yarn-4.1.0.cjs", "start"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 2
2     execve("/usr/local/bin/node", ["node", "migrate.js"], 0x7ffd1 /* 5 vars */) = 0
2     +++ exited with 0 +++
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 3
3     execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "3", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 5,
			NotRun:     []string{"node"},
			PassedOver: []Exec{{Pid: "1", Path: "/usr/local/bin/node", Argv: []string{"node", "/app/.yarn/releases/yarn-4.1.0.cjs", "start"}, Line: 0}}}},
		{"children that only forked are not commands", `1     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "start"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 7
7     +++ exited with 0 +++
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 8
8     execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "8", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 4,
			PassedOver: []Exec{{Pid: "1", Path: "/usr/local/bin/node", Argv: []string{"node", "/usr/local/bin/npm", "start"}, Line: 0}}}},
		{"a child whose execve failed is not a command", `1     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "start"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 7
[pid     7] execve("/usr/bin/x", ["x"], 0x7ffd1 /* 5 vars */ <unfinished ...>
[pid     7] <... execve resumed>) = -1 ENOENT (No such file or directory)
7     +++ exited with 1 +++
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 8
8     execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "8", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 6,
			PassedOver: []Exec{{Pid: "1", Path: "/usr/local/bin/node", Argv: []string{"node", "/usr/local/bin/npm", "start"}, Line: 0}}}},
		{"a helper of the program that ended is not a command", `1     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "start"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 7
7     execve("/bin/sh", ["sh", "-c", "node server.js"], 0x7ffd1 /* 5 vars */) = 0
7     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 8
8     execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
8     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 9
9     execve("/usr/bin/convert", ["convert"], 0x7ffd1 /* 5 vars */) = 0
9     +++ exited with 0 +++
`, Exec{Pid: "8", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 4,
			PassedOver: []Exec{
				{Pid: "1", Path: "/usr/local/bin/node", Argv: []string{"node", "/usr/local/bin/npm", "start"}, Line: 0},
				{Pid: "7", Path: "/bin/sh", Argv: []string{"sh", "-c", "node server.js"}, Line: 2},
			}}},
		{"a program that runs a launcher itself is named", `1     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "start"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 7
7     execve("/bin/sh", ["sh", "-c", "node server.js"], 0x7ffd1 /* 5 vars */) = 0
7     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 8
8     execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
8     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 9
9     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "run", "x"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "8", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 4,
			ChildLaunchers: []string{`npm (script "x")`},
			PassedOver: []Exec{
				{Pid: "1", Path: "/usr/local/bin/node", Argv: []string{"node", "/usr/local/bin/npm", "start"}, Line: 0},
				{Pid: "7", Path: "/bin/sh", Argv: []string{"sh", "-c", "node server.js"}, Line: 2},
			}}},
		{"a script that runs npm stays the program and names the launcher", `1     execve("/entrypoint.sh", ["/entrypoint.sh"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 7
7     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "start"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "1", Path: "/entrypoint.sh", Argv: []string{"/entrypoint.sh"}, Line: 0,
			ChildLaunchers: []string{`npm (script "start")`}}},
		{"a shell that executes the program in its own process", `1     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "start"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 7
7     execve("/bin/sh", ["sh", "-c", "node migrate.js && node server.js"], 0x7ffd1 /* 5 vars */) = 0
7     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 8
8     execve("/usr/local/bin/node", ["node", "migrate.js"], 0x7ffd1 /* 5 vars */) = 0
8     +++ exited with 0 +++
7     execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "7", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 6,
			NotRun: []string{"node"},
			PassedOver: []Exec{
				{Pid: "1", Path: "/usr/local/bin/node", Argv: []string{"node", "/usr/local/bin/npm", "start"}, Line: 0},
				{Pid: "7", Path: "/bin/sh", Argv: []string{"sh", "-c", "node migrate.js && node server.js"}, Line: 2},
			}}},
		{"the children of a command that ran are not commands of the chain", `11    execve("/bin/sh", ["sh", "-c", "npm run migrate && npm start"], 0x7ffd1 /* 5 vars */) = 0
11    clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 12
12    execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "run", "migrate"], 0x7ffd1 /* 5 vars */) = 0
12    clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 20
20    execve("/bin/sh", ["sh", "-c", "node migrate.js"], 0x7ffd1 /* 5 vars */) = 0
20    +++ exited with 0 +++
12    +++ exited with 0 +++
11    execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "start"], 0x7ffd1 /* 5 vars */) = 0
11    clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 13
13    execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "13", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 9,
			NotRun: []string{`npm (script "migrate")`},
			PassedOver: []Exec{
				{Pid: "11", Path: "/bin/sh", Argv: []string{"sh", "-c", "npm run migrate && npm start"}, Line: 0},
				{Pid: "11", Path: "/usr/local/bin/node", Argv: []string{"node", "/usr/local/bin/npm", "start"}, Line: 7},
			}}},
		{"a shell that ran a launcher for the program is named by the launcher once", `1     execve("/usr/local/bin/node", ["node", "server.js"], 0x7ffd1 /* 5 vars */) = 0
1     clone(child_stack=NULL, flags=CLONE_VM|SIGCHLD) = 9
9     execve("/bin/sh", ["sh", "-c", "npm run build"], 0x7ffd1 /* 5 vars */) = 0
9     execve("/usr/local/bin/node", ["node", "/usr/local/bin/npm", "run", "build"], 0x7ffd1 /* 5 vars */) = 0
`, Exec{Pid: "1", Path: "/usr/local/bin/node", Argv: []string{"node", "server.js"}, Line: 0,
			ChildLaunchers: []string{`npm (script "build")`}}},
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

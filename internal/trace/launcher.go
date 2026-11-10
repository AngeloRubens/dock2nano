package trace

import (
	"path"
	"regexp"
	"strings"
)

// A package manager launcher (npm, npx, yarn, pnpm) is a node program that reads package.json and
// starts the script it names as a child process. It is the parent of the program, not the program:
// nanos has no fork, so the launcher cannot run there, and the node command it starts can.
//
// A file is taken for a launcher by where it is, not only by its name: node /app/npm.js is the
// program of the user. The places are the ones the package managers install to.

// launcherSet are the commands that are launchers.
var launcherSet = map[string]bool{
	"npm": true, "npx": true, "yarn": true, "yarnpkg": true, "pnpm": true, "pnpx": true,
}

// binDirs are the directories the links to the launchers are in.
var binDirs = map[string]bool{"/usr/local/bin": true, "/usr/bin": true, "/bin": true}

// berryFile is the file yarn 2 and later starts from .yarn/releases of the project.
var berryFile = regexp.MustCompile(`^yarn-\d[^/]*\.c?js$`)

// yarnClassicDir is the directory of yarn 1 as the image of node installs it (/opt/yarn-v1.22.19/bin).
var yarnClassicDir = regexp.MustCompile(`/yarn-v[^/]+/bin$`)

// corepackStore is the directory the store of corepack keeps the package managers in.
var corepackStore = regexp.MustCompile(`/(node/corepack|corepack/v\d+)/`)

// nvmBin and npmGlobalBin are the bin directories that nvm and "npm config set prefix" make, and
// ~/.volta/bin holds Volta's shims: the links to the launchers are there. The files Volta keeps in
// ~/.volta/tools/image/<package manager>/<version> are not listed: that layout is not known well
// enough to name it here (npm and pnpm are found through their node_modules directories anyway).
var (
	nvmBin       = regexp.MustCompile(`/node/v\d[^/]*/bin$`)
	npmGlobalBin = regexp.MustCompile(`/\.npm-global/bin$`)
)

// nodeExtension is the extension of a file node runs.
func nodeExtension(b string) string {
	for _, ext := range []string{".js", ".cjs", ".mjs"} {
		if s, ok := strings.CutSuffix(b, ext); ok && s != "" {
			return s
		}
	}
	return b
}

// launcherFile tells whether the file at p is a launcher, from its place and its name, and gives
// the name of the launcher ("npm", "npx", "yarn", "yarnpkg", "pnpm" or "pnpx"), or "" when it is not.
func launcherFile(p string) string {
	if !path.IsAbs(p) {
		// node node_modules/npm/bin/npm-cli.js: the places are tested with a directory before them
		p = "/" + p
	}
	d, b := path.Dir(p), path.Base(p)
	switch {
	case binDirs[d] && launcherSet[b]:
		return b
	case (nvmBin.MatchString(d) || npmGlobalBin.MatchString(d) || (strings.Contains(d, "/.volta/") && strings.HasSuffix(d, "/bin"))) && launcherSet[b]:
		return b
	case (binDirs[d] || nvmBin.MatchString(d) || npmGlobalBin.MatchString(d)) && b == "corepack":
		return "corepack"
	case strings.HasSuffix(d, "/node_modules/corepack/dist") && nodeExtension(b) == "corepack":
		return "corepack"
	case strings.HasSuffix(d, "/node_modules/yarn/bin") && (b == "yarn.js" || b == "yarn"):
		return "yarn"
	case strings.HasSuffix(d, "/node_modules/npm/bin") && (b == "npm-cli.js" || b == "npx-cli.js"):
		return strings.TrimSuffix(b, "-cli.js")
	case (strings.HasSuffix(d, "/node_modules/pnpm/bin") || strings.HasSuffix(d, "/node_modules/pnpm/dist")) && (b == "pnpm.cjs" || b == "pnpm.js"):
		return "pnpm"
	case strings.HasSuffix(d, "/node_modules/corepack/dist") && launcherSet[nodeExtension(b)] && nodeExtension(b) != b:
		return nodeExtension(b)
	case corepackStore.MatchString(d+"/") && launcherSet[nodeExtension(b)] && nodeExtension(b) != b:
		return nodeExtension(b)
	case yarnClassicDir.MatchString(d) && (nodeExtension(b) == "yarn" || nodeExtension(b) == "yarnpkg"):
		return nodeExtension(b)
	case strings.HasSuffix(d, "/.yarn/releases") && berryFile.MatchString(b):
		return "yarn"
	}
	return ""
}

// interpreters are the programs that run a launcher: node and the shells. A shell may be the
// busybox of an image of alpine, which is told by argv[0].
var interpreters = map[string]bool{
	"node": true, "nodejs": true, "sh": true, "bash": true, "dash": true, "ash": true,
}

// nodeNoScript are the options of node that run code given on the command line, not a file.
var nodeNoScript = map[string]bool{
	"-": true, "-e": true, "--eval": true, "-p": true, "--print": true, "-pe": true,
	"-c": true, "--check": true, "-i": true, "--interactive": true,
}

// nodeWithValue are the options of node that take the next argument as their value.
var nodeWithValue = map[string]bool{
	"-r": true, "--require": true, "--import": true, "--loader": true, "--experimental-loader": true,
	"--title": true, "--inspect-port": true,
}

// compound are the parts of a command line that make it more than one command.
var compound = []string{"&", "|", ";", "\n", "`", "$(", `"`, "'"}

// Launcher says whether a program started by execve as program with argv is a package manager
// launcher, and which one ("npm", "npx", "yarn", "yarnpkg", "pnpm" or "pnpx"). The program is a
// launcher when it is one itself (a link in /usr/local/bin and the like), when it is node (or
// nodejs) running the file of one (the first argument that is not an option or the value of one),
// or when it is a shell whose command line (-c) is a launcher and nothing else, possibly after exec,
// env, env -i or variable assignments. A busybox that is a shell by argv[0] counts as a shell.
// rest is the arguments the launcher was given.
func Launcher(program string, argv []string) (name string, rest []string, ok bool) {
	name, rest, ok = launcherRaw(program, argv)
	if ok && name == "corepack" {
		// corepack <package manager> <arguments>
		if len(rest) > 0 {
			// corepack yarn@4.1.0 start
			if pm, _, _ := strings.Cut(rest[0], "@"); launcherSet[pm] {
				return pm, rest[1:], true
			}
		}
		return "", nil, false
	}
	return name, rest, ok
}

// interpreterOf tells which interpreter a program is (node, nodejs, or one of the shells), from its
// path and argv[0], and gives its arguments. A busybox is the applet named by argv[0] or by its first
// argument. interp is "" when it is none of them.
func interpreterOf(program string, argv []string) (interp string, args []string) {
	args = argv
	if len(args) > 0 {
		args = args[1:]
	}
	for _, c := range []string{path.Base(program), path.Base(firstOf(argv))} {
		if interpreters[c] {
			interp = c
			break
		}
		if c == "busybox" && interp == "" {
			interp = "busybox"
		}
	}
	if interp == "busybox" {
		// busybox sh -c "..."
		if len(args) == 0 || !interpreters[path.Base(args[0])] {
			return "", nil
		}
		interp, args = path.Base(args[0]), args[1:]
	}
	return interp, args
}

func launcherRaw(program string, argv []string) (name string, rest []string, ok bool) {
	if n := launcherFile(program); n != "" {
		if len(argv) > 0 {
			rest = argv[1:]
		}
		return n, rest, true
	}
	interp, args := interpreterOf(program, argv)
	switch interp {
	case "":
		return "", nil, false
	case "node", "nodejs":
		for i := 0; i < len(args); i++ {
			a := args[i]
			if nodeNoScript[a] {
				return "", nil, false
			}
			if nodeWithValue[a] {
				i++
				continue
			}
			if strings.HasPrefix(a, "-") {
				continue
			}
			if n := launcherFile(a); n != "" {
				return n, args[i+1:], true
			}
			return "", nil, false
		}
		return "", nil, false
	}
	// a shell: only the form "-c command"
	if text, found := commandText(args); found {
		if IsCompound(text) {
			return "", nil, false
		}
		words := strings.Fields(text)
	strip:
		for len(words) > 0 {
			switch {
			case words[0] == "-u" && len(words) > 1:
				// env -u NAME
				words = words[2:]
			case words[0] == "exec" || words[0] == "env" || words[0] == "-i" || (!strings.HasPrefix(words[0], "-") && strings.Contains(words[0], "=")):
				words = words[1:]
			default:
				break strip
			}
		}
		if len(words) > 0 {
			// the command is looked up in PATH: it is a launcher by its name
			if launcherSet[words[0]] || words[0] == "corepack" {
				return words[0], words[1:], true
			}
			if n := launcherFile(words[0]); n != "" {
				return n, words[1:], true
			}
		}
		return "", nil, false
	}
	return "", nil, false
}

// IsCompound tells whether the text of a shell command line is more than one command: it holds
// &, |, ;, a newline, a backtick, $( or a quote.
func IsCompound(text string) bool {
	for _, c := range compound {
		if strings.Contains(text, c) {
			return true
		}
	}
	return false
}

// SeveralCommands tells whether the text of a shell command line runs more than one command: it
// holds &&, ||, ;, a newline, a pipe, a & that puts a command in the background (not the & of a
// redirection such as 2>&1 or &>), $( or a backtick. A quote or a redirection alone does not count.
// It is a guess from the text: nothing is parsed.
func SeveralCommands(text string) bool {
	if strings.ContainsAny(text, ";\n|`") || strings.Contains(text, "$(") {
		return true
	}
	for i := 0; i < len(text); i++ {
		if text[i] != '&' {
			continue
		}
		prev, next := byte(' '), byte(' ')
		if i > 0 {
			prev = text[i-1]
		}
		if i+1 < len(text) {
			next = text[i+1]
		}
		if prev == '>' || prev == '<' || next == '>' {
			continue
		}
		return true
	}
	return false
}

// IsShell tells whether a program is a shell (or busybox as one), whatever it was asked to run.
func IsShell(program string, argv []string) bool {
	switch interp, _ := interpreterOf(program, argv); interp {
	case "sh", "bash", "dash", "ash":
		return true
	}
	return false
}

// supervisors are the programs, found by name under node_modules, that start the program of the
// user as a child process and keep it running: nanos has no fork, so they cannot run there.
var supervisors = map[string]bool{
	"nodemon": true, "ts-node-dev": true, "node-dev": true, "pm2-runtime": true, "pm2": true,
	"forever": true, "concurrently": true, "npm-run-all": true, "run-p": true, "run-s": true,
}

// Supervisor tells whether a program started as program with argv is one of the supervisors (by its
// place: node_modules/.bin/<name> or node_modules/<name>/..., run by node or itself), and gives its
// name, or "" when it is not. Nothing is passed over for it: it is only named.
func Supervisor(program string, argv []string) string {
	files := []string{program}
	if interp, args := interpreterOf(program, argv); interp == "node" || interp == "nodejs" {
		files = nil
		for i := 0; i < len(args); i++ {
			if nodeNoScript[args[i]] {
				break
			}
			if nodeWithValue[args[i]] {
				i++
				continue
			}
			if !strings.HasPrefix(args[i], "-") {
				files = append(files, args[i])
				break
			}
		}
	}
	for _, f := range files {
		if !path.IsAbs(f) {
			f = "/" + f
		}
		if d, b := path.Dir(f), path.Base(f); strings.HasSuffix(d, "/node_modules/.bin") && supervisors[nodeExtension(b)] {
			return nodeExtension(b)
		}
		parts := strings.Split(f, "/")
		for i := 0; i+1 < len(parts); i++ {
			if parts[i] == "node_modules" && supervisors[parts[i+1]] {
				return parts[i+1]
			}
		}
	}
	return ""
}

// commandText finds the command line a shell is given with -c (or a cluster of options that holds
// c, as -ec), among its arguments. The search stops at the first operand that is not an option: after
// it the arguments belong to the script ("sh /entry.sh -c x" runs /entry.sh). The value of -o and +o
// is skipped.
func commandText(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case shellCommandOption(a):
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", false
		case a == "-o" || a == "+o" || a == "--rcfile" || a == "--init-file":
			// the value of the option is not the script
			i++
		case a == "--" || a == "-" || a == "+" || !(strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+")):
			return "", false
		}
	}
	return "", false
}

// ShellCommand gives the command line of a program that is a shell (or busybox as one) started with
// -c, and whether it is one.
func ShellCommand(program string, argv []string) (string, bool) {
	interp, args := interpreterOf(program, argv)
	switch interp {
	case "sh", "bash", "dash", "ash":
		return commandText(args)
	}
	return "", false
}

// shellCommandOption tells whether an argument of a shell is -c, or a cluster of single letter
// options that holds c (-ec, -lc, -ce), which is followed by the command.
func shellCommandOption(a string) bool {
	if len(a) < 2 || a[0] != '-' || !strings.Contains(a, "c") {
		return false
	}
	for _, r := range a[1:] {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

func firstOf(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	return argv[0]
}

// dirOptions are the options that point the launcher at another package or directory: the
// package.json of the working directory is then not the one it reads.
var dirOptions = map[string]bool{
	"--cwd": true, "-C": true, "--dir": true, "--prefix": true, "--filter": true, "-F": true,
	"-w": true, "--workspace": true,
}

// valueOptions are the other options, per launcher, that take the next argument as their value, so
// that the value is not taken for the name of a script.
var valueOptions = map[string]map[string]bool{
	"npm":  {"--loglevel": true, "--userconfig": true, "--cache": true, "--registry": true, "--script-shell": true},
	"yarn": {"--network-timeout": true, "--mutex": true, "--modules-folder": true, "--cache-folder": true},
	"pnpm": {"--reporter": true, "--loglevel": true},
}

// builtins are the commands of yarn and pnpm that are not the name of a script.
var builtins = map[string]bool{
	"exec": true, "dlx": true, "node": true, "workspace": true, "workspaces": true, "add": true,
	"install": true, "i": true, "remove": true, "rm": true, "up": true, "update": true, "upgrade": true,
	"create": true, "init": true, "bin": true, "cache": true, "config": true, "link": true,
	"unlink": true, "pack": true, "publish": true, "why": true, "ls": true, "list": true,
	"set": true, "info": true, "dedupe": true, "rebuild": true, "patch": true, "x": true,
	"global": true, "outdated": true, "audit": true, "explain": true, "help": true, "version": true,
	"store": true, "plugin": true, "constraints": true,
}

// elsewhereFlags are the options, per launcher, with no value, that make the launcher run the script
// in other packages (every workspace, the root of the workspace).
var elsewhereFlags = map[string]map[string]bool{
	"npm":  {"--workspaces": true, "--ws": true},
	"pnpm": {"-r": true, "--recursive": true, "--workspace-root": true},
}

// ScriptRun names the package.json script a launcher was asked to run: "start" for "npm start",
// "dev" for "npm run dev", "yarn dev", "pnpm run dev". It is "" when the arguments do not name one
// (npx and pnpx run a package, yarn node and pnpm exec a command, npm install installs). Options
// are skipped, with the value of the ones that take one. elsewhere is true when an option points the
// launcher at another directory or package (--cwd, -C, --dir, --prefix, --filter, -F, -w,
// --workspace, --workspaces, -r): the script is not in the package.json of the working directory.
// viaRun says whether the script was named with run (or run-script): "pnpm run foo" fails when
// there is no script foo, where "pnpm foo" falls back to running a command.
func ScriptRun(launcher string, rest []string) (script string, elsewhere, viaRun bool) {
	var words []string
loop:
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--":
			break loop
		case strings.HasPrefix(a, "-"):
			name, _, hasValue := strings.Cut(a, "=")
			if elsewhereFlags[strings.TrimSuffix(launcher, "pkg")][name] {
				elsewhere = true
			} else if dirOptions[name] {
				elsewhere = true
				// -w of pnpm is the root of the workspace, with no value
				if !hasValue && !(name == "-w" && launcher == "pnpm") {
					i++
				}
			} else if !hasValue && valueOptions[strings.TrimSuffix(launcher, "pkg")][name] {
				i++
			}
		default:
			words = append(words, a)
		}
	}
	if len(words) == 0 {
		return "", elsewhere, false
	}
	switch launcher {
	case "npm":
		switch words[0] {
		case "run", "run-script", "rum", "urn":
			if len(words) > 1 {
				return words[1], elsewhere, true
			}
		case "start", "stop", "restart":
			return words[0], elsewhere, false
		case "test", "t", "tst":
			return "test", elsewhere, false
		}
	case "yarn", "yarnpkg", "pnpm":
		switch {
		case words[0] == "run" || words[0] == "run-script":
			if len(words) > 1 {
				return words[1], elsewhere, true
			}
		case !builtins[words[0]]:
			return words[0], elsewhere, false
		}
	}
	return "", elsewhere, false
}

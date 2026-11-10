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
	d, b := path.Dir(p), path.Base(p)
	switch {
	case binDirs[d] && launcherSet[b]:
		return b
	case strings.HasSuffix(d, "/node_modules/npm/bin") && (b == "npm-cli.js" || b == "npx-cli.js"):
		return strings.TrimSuffix(b, "-cli.js")
	case (strings.HasSuffix(d, "/node_modules/pnpm/bin") || strings.HasSuffix(d, "/node_modules/pnpm/dist")) && (b == "pnpm.cjs" || b == "pnpm.js"):
		return "pnpm"
	case strings.HasSuffix(d, "/node_modules/corepack/dist") && launcherSet[nodeExtension(b)] && nodeExtension(b) != b:
		return nodeExtension(b)
	case strings.Contains(d, "/corepack/") && launcherSet[nodeExtension(b)] && nodeExtension(b) != b:
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
	if n := launcherFile(program); n != "" {
		if len(argv) > 0 {
			rest = argv[1:]
		}
		return n, rest, true
	}
	var interp string
	args := argv
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
			return "", nil, false
		}
		interp, args = path.Base(args[0]), args[1:]
	}
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
	for i, a := range args {
		if a != "-c" || i+1 >= len(args) {
			continue
		}
		text := args[i+1]
		for _, c := range compound {
			if strings.Contains(text, c) {
				return "", nil, false
			}
		}
		words := strings.Fields(text)
		for len(words) > 0 && (words[0] == "exec" || words[0] == "env" || words[0] == "-i" || (!strings.HasPrefix(words[0], "-") && strings.Contains(words[0], "="))) {
			words = words[1:]
		}
		if len(words) > 0 {
			// the command is looked up in PATH: it is a launcher by its name
			if launcherSet[words[0]] {
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

// builtins are the commands of yarn and pnpm that are not the name of a script.
var builtins = map[string]bool{
	"exec": true, "dlx": true, "node": true, "workspace": true, "workspaces": true, "add": true,
	"install": true, "i": true, "remove": true, "rm": true, "up": true, "update": true, "upgrade": true,
	"create": true, "init": true, "bin": true, "cache": true, "config": true, "link": true,
	"unlink": true, "pack": true, "publish": true, "why": true, "ls": true, "list": true,
	"set": true, "info": true, "dedupe": true, "rebuild": true, "patch": true, "x": true,
}

// ScriptOf names the package.json script a launcher was asked to run: "start" for "npm start",
// "dev" for "npm run dev", "yarn dev", "pnpm run dev". It is "" when the arguments do not name one
// (npx and pnpx run a package, yarn node and pnpm exec a command, npm install installs). Options
// are skipped, with the value of the ones that take one. elsewhere is true when an option points the
// launcher at another directory or package (--cwd, -C, --dir, --prefix, --filter, -F, -w,
// --workspace): the script is not in the package.json of the working directory.
func ScriptOf(launcher string, rest []string) (script string, elsewhere bool) {
	var words []string
loop:
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--":
			break loop
		case strings.HasPrefix(a, "-"):
			name, _, hasValue := strings.Cut(a, "=")
			if dirOptions[name] {
				elsewhere = true
				// -w of pnpm is the root of the workspace, with no value
				if !hasValue && !(name == "-w" && launcher == "pnpm") {
					i++
				}
			}
		default:
			words = append(words, a)
		}
	}
	if len(words) == 0 {
		return "", elsewhere
	}
	switch launcher {
	case "npm":
		switch words[0] {
		case "run", "run-script", "rum", "urn":
			if len(words) > 1 {
				return words[1], elsewhere
			}
		case "start", "stop", "restart":
			return words[0], elsewhere
		case "test", "t", "tst":
			return "test", elsewhere
		}
	case "yarn", "yarnpkg", "pnpm":
		switch {
		case words[0] == "run" || words[0] == "run-script":
			if len(words) > 1 {
				return words[1], elsewhere
			}
		case !builtins[words[0]]:
			return words[0], elsewhere
		}
	}
	return "", elsewhere
}

package trace

import (
	"path"
	"strings"
)

// A package manager launcher (npm, npx, yarn, pnpm) is a node program that reads package.json and
// starts the script it names as a child process. It is the parent of the program, not the program:
// nanos has no fork, so the launcher cannot run there, and the node command it starts can.

// launcherNames are the names a launcher is started by (the command, or the file of its package,
// without .js, .cjs or .mjs), and the name each one goes by.
var launcherNames = map[string]string{
	"npm": "npm", "npx": "npx", "yarn": "yarn", "yarnpkg": "yarnpkg", "pnpm": "pnpm", "pnpx": "pnpx",
	"npm-cli": "npm", "npx-cli": "npx",
}

var interpreters = map[string]bool{
	"node": true, "nodejs": true, "sh": true, "bash": true, "dash": true, "ash": true,
}

// launcherName reduces a path to the name a launcher goes by. It is "" when that is not a launcher.
func launcherName(p string) string {
	b := path.Base(p)
	for _, ext := range []string{".js", ".cjs", ".mjs"} {
		if s, ok := strings.CutSuffix(b, ext); ok && s != "" {
			b = s
			break
		}
	}
	return launcherNames[b]
}

// Launcher says whether a program started by execve as program with argv is a package manager
// launcher, and which one ("npm", "npx", "yarn", "yarnpkg", "pnpm" or "pnpx"). The program is a
// launcher when it is one itself, when it is node (or nodejs) running the file of one (the first
// argument that is not an option), or when it is a shell whose command line (-c) starts with one,
// possibly after exec or variable assignments. rest is the arguments the launcher was given.
func Launcher(program string, argv []string) (name string, rest []string, ok bool) {
	if n := launcherName(program); n != "" {
		if len(argv) > 0 {
			rest = argv[1:]
		}
		return n, rest, true
	}
	base := path.Base(program)
	if !interpreters[base] || len(argv) < 2 {
		return "", nil, false
	}
	args := argv[1:]
	if base == "node" || base == "nodejs" {
		for i, a := range args {
			if strings.HasPrefix(a, "-") {
				continue
			}
			if n := launcherName(a); n != "" {
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
		words := strings.Fields(args[i+1])
		for len(words) > 0 && (words[0] == "exec" || strings.Contains(words[0], "=")) {
			words = words[1:]
		}
		if len(words) > 0 {
			if n := launcherName(words[0]); n != "" {
				return n, words[1:], true
			}
		}
		return "", nil, false
	}
	return "", nil, false
}

// ScriptOf names the package.json script a launcher was asked to run: "start" for "npm start",
// "dev" for "npm run dev", "yarn dev", "pnpm run dev". It is "" when the arguments do not name
// one (npx runs a package, not a script).
func ScriptOf(launcher string, rest []string) string {
	var words []string
	for _, a := range rest {
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
	}
	if len(words) == 0 || launcher == "npx" || launcher == "pnpx" {
		return ""
	}
	switch words[0] {
	case "run", "run-script":
		if len(words) > 1 {
			return words[1]
		}
		return ""
	case "start", "test", "stop", "restart":
		return words[0]
	}
	if launcher == "yarn" || launcher == "yarnpkg" || launcher == "pnpm" {
		return words[0]
	}
	return ""
}

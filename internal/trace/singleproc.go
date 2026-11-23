package trace

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Some servers start more than one process by default, and have an option, or a sibling program,
// that keeps one. SingleProcess knows three of them (the reloader of the development server of
// django, uvicorn with workers or the reloader, gunicorn) and says what to run instead. It reads
// the command line only: what it gives is a guess for the user to review, never applied by itself.

// SingleProcessAdvice is what is known about running a multi-process program in one process.
type SingleProcessAdvice struct {
	// Server is "django", "uvicorn" or "gunicorn".
	Server string

	// Command is the command that runs the program in one process, as words. It is empty when no
	// command could be built from the command line.
	Command []string

	// Reason says why the program starts more than one process, and what was changed.
	Reason string

	// Assumed says which values of the command were not found in the command line and were
	// assumed ("host and port"), or is "" when none was.
	Assumed string
}

var (
	pythonName = regexp.MustCompile(`^python(\d+(\.\d+)*)?$`)
	appSpec    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*:[A-Za-z_][A-Za-z0-9_]*$`)
	bindPort   = regexp.MustCompile(`^[0-9]{1,5}$`)
)

// serverInvocation reads a command line that starts one of the servers: the server, the words that
// start it (the interpreter, its options, the script) and the arguments of the server.
func serverInvocation(program string, argv []string) (server string, prefix, args []string, ok bool) {
	if len(argv) == 0 {
		return "", nil, nil, false
	}
	known := func(name string) string {
		switch name {
		case "uvicorn", "gunicorn":
			return name
		case "django-admin":
			return "django"
		}
		return ""
	}
	if s := known(path.Base(argv[0])); s != "" {
		return s, argv[:1], argv[1:], true
	}
	if !pythonName.MatchString(path.Base(argv[0])) && !pythonName.MatchString(path.Base(program)) {
		return "", nil, nil, false
	}
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "-m":
			if i+1 >= len(argv) {
				return "", nil, nil, false
			}
			mod := argv[i+1]
			if mod == "django" {
				return "django", argv[:i+2], argv[i+2:], true
			}
			if s := known(mod); s != "" && s != "django" {
				return s, argv[:i+2], argv[i+2:], true
			}
			return "", nil, nil, false
		case a == "-c" || a == "-" || a == "--":
			return "", nil, nil, false
		case a == "-W" || a == "-X":
			// the value is the next word
			i++
		case strings.HasPrefix(a, "-"):
		default:
			b := path.Base(a)
			if b == "manage.py" {
				return "django", argv[:i+1], argv[i+1:], true
			}
			if s := known(b); s == "uvicorn" || s == "gunicorn" {
				return s, argv[:i+1], argv[i+1:], true
			}
			return "", nil, nil, false
		}
	}
	return "", nil, nil, false
}

// SingleProcess tells, for a program started as program with argv, how to run it in one process
// when it is one of the servers that start more than one: the development server of django
// (manage.py runserver without --noreload), uvicorn with --workers above 1 or --reload, and
// gunicorn. ok is false for any other program, and for these when they already run as one process.
// For gunicorn with the worker class of uvicorn the command runs uvicorn; with another worker class
// there is no command: gunicorn forks its workers, and the server has to be chosen by the user.
func SingleProcess(program string, argv []string) (SingleProcessAdvice, bool) {
	server, prefix, args, ok := serverInvocation(program, argv)
	if !ok {
		return SingleProcessAdvice{}, false
	}
	switch server {
	case "django":
		return djangoAdvice(prefix, args)
	case "uvicorn":
		return uvicornAdvice(prefix, args)
	}
	return gunicornAdvice(args)
}

func withPrefix(prefix []string, rest ...string) []string {
	out := append([]string{}, prefix...)
	return append(out, rest...)
}

func djangoAdvice(prefix, args []string) (SingleProcessAdvice, bool) {
	if len(args) == 0 || args[0] != "runserver" {
		return SingleProcessAdvice{}, false
	}
	for _, a := range args {
		if a == "--noreload" {
			return SingleProcessAdvice{}, false
		}
		if a == "--" {
			return SingleProcessAdvice{}, false
		}
	}
	return SingleProcessAdvice{
		Server:  "django",
		Command: withPrefix(prefix, append(append([]string{}, args...), "--noreload")...),
		Reason:  "the development server of django starts a second process that reloads the code; --noreload keeps one",
	}, true
}

func uvicornAdvice(prefix, args []string) (SingleProcessAdvice, bool) {
	var kept []string
	changed := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--reload":
			changed = true
		case a == "--workers" && i+1 < len(args):
			if n, err := strconv.Atoi(args[i+1]); err == nil && n > 1 {
				changed = true
				i++
				continue
			}
			kept = append(kept, a, args[i+1])
			i++
		case strings.HasPrefix(a, "--workers="):
			if n, err := strconv.Atoi(strings.TrimPrefix(a, "--workers=")); err == nil && n > 1 {
				changed = true
				continue
			}
			kept = append(kept, a)
		default:
			kept = append(kept, a)
		}
	}
	if !changed {
		return SingleProcessAdvice{}, false
	}
	return SingleProcessAdvice{
		Server:  "uvicorn",
		Command: withPrefix(prefix, kept...),
		Reason:  "uvicorn starts one process per worker, and a reloader process with --reload; --workers (above 1) and --reload are left out",
	}, true
}

// gunicornFlags are the options of gunicorn that take no value; gunicornValues take the next
// argument. Any other option stops the reading: the application could be taken for a value.
var gunicornFlags = map[string]bool{
	"--preload": true, "--reload": true, "-D": true, "--daemon": true, "--capture-output": true,
	"--check-config": true, "--print-config": true, "-R": true, "--enable-stdio-inheritance": true,
	"--reuse-port": true, "--no-sendfile": true, "--sendfile": true,
}

var gunicornValues = map[string]bool{
	"-b": true, "--bind": true, "-w": true, "--workers": true, "-k": true, "--worker-class": true,
	"-c": true, "--config": true, "-t": true, "--timeout": true, "--threads": true, "-n": true,
	"--name": true, "--log-level": true, "--access-logfile": true, "--error-logfile": true,
	"--chdir": true, "-u": true, "--user": true, "-g": true, "--group": true, "-p": true,
	"--pid": true, "--worker-connections": true, "--max-requests": true,
	"--max-requests-jitter": true, "--keep-alive": true, "--graceful-timeout": true, "-e": true,
	"--env": true, "--forwarded-allow-ips": true, "--worker-tmp-dir": true, "--backlog": true,
	"--access-logformat": true, "--log-file": true,
}

const uvicornWorker = "uvicorn.workers.UvicornWorker"

func gunicornAdvice(args []string) (SingleProcessAdvice, bool) {
	var class, app, bind string
	parsed := true
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, value, hasValue := a, "", false
		if strings.HasPrefix(a, "--") {
			name, value, hasValue = strings.Cut(a, "=")
		} else if len(a) > 2 && a[0] == '-' && gunicornValues[a[:2]] {
			// -kuvicorn.workers.UvicornWorker
			name, value, hasValue = a[:2], a[2:], true
		}
		switch {
		case gunicornValues[name]:
			if !hasValue {
				if i+1 >= len(args) {
					parsed = false
					break
				}
				i++
				value = args[i]
			}
			switch name {
			case "-k", "--worker-class":
				class = value
			case "-b", "--bind":
				if bind == "" {
					bind = value
				}
			}
		case gunicornFlags[name] && !hasValue:
		case strings.HasPrefix(a, "-"):
			parsed = false
		case app == "":
			app = a
		}
		if !parsed {
			break
		}
	}
	generic := SingleProcessAdvice{
		Server: "gunicorn",
		Reason: "gunicorn forks its workers, so it always starts more than one process; a server that runs in one process (waitress, uvicorn, hypercorn) has to be chosen by you",
	}
	if class == uvicornWorker {
		return SingleProcessAdvice{}, false
	}
	if class != uvicornWorker {
		return generic, true
	}
	if !parsed || !appSpec.MatchString(app) {
		// a factory, a missing application or an option that is not known: nothing is invented
		return generic, true
	}
	host, port, assumed := "0.0.0.0", "8000", false
	if bind != "" {
		if h, p, ok := splitBind(bind); ok {
			host, port = h, p
		} else {
			assumed = true
		}
	} else {
		assumed = true
	}
	adv := SingleProcessAdvice{
		Server:  "gunicorn",
		Command: []string{"uvicorn", app, "--host", host, "--port", port},
		Reason:  "gunicorn forks its workers; with the worker class of uvicorn the same application runs under uvicorn in one process (the other options of gunicorn are not carried)",
	}
	if assumed {
		adv.Assumed = "host and port"
	}
	return adv, true
}

// splitBind reads the value of --bind of gunicorn as a host and a port: HOST:PORT, :PORT, HOST
// (port 8000) and [IPV6]:PORT. A socket, a file descriptor or anything else gives false.
func splitBind(b string) (host, port string, ok bool) {
	if strings.Contains(b, "/") {
		return "", "", false
	}
	port = "8000"
	switch {
	case strings.HasPrefix(b, "["):
		h, rest, found := strings.Cut(b[1:], "]")
		if !found || h == "" {
			return "", "", false
		}
		host = h
		if rest != "" {
			p, found := strings.CutPrefix(rest, ":")
			if !found {
				return "", "", false
			}
			port = p
		}
	case strings.Count(b, ":") == 1:
		h, p, _ := strings.Cut(b, ":")
		host, port = h, p
		if host == "" {
			host = "0.0.0.0"
		}
	case strings.Contains(b, ":"):
		return "", "", false
	default:
		host = b
	}
	if host == "" || !bindPort.MatchString(port) {
		return "", "", false
	}
	if n, _ := strconv.Atoi(port); n < 1 || n > 65535 {
		return "", "", false
	}
	return host, port, true
}

// safeWord is a word of a command that needs no quote.
var safeWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// QuoteWords writes words as the text of a command line that reads back as the same words, both in
// a shell and in the compose file, and that sits inside single quotes: a word with no special
// character is written as it is, any other is put in double quotes. It gives false for a word that
// cannot be written so (one with a quote, a backslash, a $, a backtick or a control character):
// no command is made of it.
func QuoteWords(words []string) (string, bool) {
	out := make([]string, len(words))
	for i, w := range words {
		switch {
		case safeWord.MatchString(w):
			out[i] = w
		case strings.IndexFunc(w, func(r rune) bool {
			return r < 0x20 || r == 0x7f || r == '\'' || r == '"' || r == '\\' || r == '$' || r == '`'
		}) >= 0:
			return "", false
		default:
			out[i] = `"` + w + `"`
		}
	}
	return strings.Join(out, " "), true
}

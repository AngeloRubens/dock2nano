package trace

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Some servers start more than one process by default, and have an option, or a sibling program,
// that keeps one. SingleProcess knows three of them (the reloader of the development server of
// django, uvicorn with workers or the reloader, gunicorn) and says what to run instead. It reads
// the command line only: what it gives is a guess for the user to review, never applied by itself.

// SingleProcessAdvice is what is known about running a multi-process program in one process.
type SingleProcessAdvice struct {
	// Server is "django", "uvicorn", "gunicorn" or "apache".
	Server string

	// Requires is the name of a program the service must start the observed program through for
	// Command to apply ("apache2-foreground"), or "".
	Requires string

	// Caveat says what was measured and what was not, for a suggestion that was run on few images.
	Caveat string

	// Command is the command that runs the program in one process, as words. It is empty when no
	// command could be built from the command line.
	Command []string

	// Reason says why the program starts more than one process, and what was changed.
	Reason string

	// Assumed says which values of the command were not found in the command line and were
	// assumed ("host and port"), or is "" when none was.
	Assumed string

	// Withheld is true when a command could have been built but a word of the command line is not
	// one the suggestion carries (an option that may hold a secret or is not known): nothing of the
	// command line is repeated then.
	Withheld bool
}

// WithheldText is what the report says when Withheld is true.
const WithheldText = "no command is suggested: an option of the command line may hold a secret or is not known"

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
	if adv, ok := apacheAdvice(argv); ok {
		return adv, true
	}
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

// apacheEnv is the environment that apache2-foreground of the Debian images sets, from
// /etc/apache2/envvars, before it runs apache2 -DFOREGROUND. It is a fixed list: nothing of the
// command line or of the environment of the user is copied into it.
var apacheEnv = []string{}

// ApacheCaveat says how far the suggestion for apache2 was measured.
const ApacheCaveat = "measured on one image only, php:7.2-apache, which answered 200 on nanos with this command; an image with an entrypoint of its own that checks the command (nextcloud) was tried once with the same command, apache started and exited again and again, and the cause is not known; the user, group and directories are the values of the Debian image, not read from yours: review them"

// apacheAdvice reads the command line of apache2 or httpd as apache2-foreground / httpd-foreground
// start it (-DFOREGROUND). apache starts several processes (fork, prefork); -DONE_PROCESS keeps
// one. For apache2 of the Debian images the command is the one measured, with the environment that
// apache2-foreground sets; for httpd no command is given, because the one run that was made
// (httpd -DONE_PROCESS on httpd:2.4-alpine) got no answer. The arguments are never repeated: only
// their number is told.
func apacheAdvice(argv []string) (SingleProcessAdvice, bool) {
	if len(argv) == 0 {
		return SingleProcessAdvice{}, false
	}
	name := path.Base(argv[0])
	if name != "apache2" && name != "httpd" {
		return SingleProcessAdvice{}, false
	}
	args := argv[1:]
	for _, a := range args {
		if a == "-X" || a == "-DONE_PROCESS" {
			return SingleProcessAdvice{}, false
		}
	}
	adv := SingleProcessAdvice{Server: "apache"}
	if name == "httpd" {
		adv.Reason = "httpd starts several processes (fork, prefork); the option -DONE_PROCESS keeps one, but the one run made with it (httpd -DONE_PROCESS on httpd:2.4-alpine) got no answer, so no command is suggested"
		return adv, true
	}
	adv.Reason = "apache2 starts several processes (fork, prefork); -DONE_PROCESS keeps one, and apache2-foreground sets an environment (from /etc/apache2/envvars) that the command has to write out"
	if len(args) != 1 || args[0] != "-DFOREGROUND" {
		adv.Reason += fmt.Sprintf("; apache2 was started with %d arguments that are not the ones of apache2-foreground", len(args))
		adv.Withheld = true
		return adv, true
	}
	adv.Requires = "apache2-foreground"
			adv.Command = append(append([]string{"env"}, apacheEnv...), "apache2", "-DONE_PROCESS", "-DFOREGROUND")
	return adv, true
}

func withPrefix(prefix []string, rest ...string) []string {
	out := append([]string{}, prefix...)
	return append(out, rest...)
}

// djangoFlags are the options of runserver that the suggestion carries (they take no value).
var djangoFlags = map[string]bool{
	"--insecure": true, "--nothreading": true, "--ipv6": true, "-6": true, "--nostatic": true,
	"--skip-checks": true, "--noreload": true,
}

// djangoAddrport is the optional argument of runserver: [ADDR:]PORT, with an IPv6 address in
// brackets or a name.
var djangoAddrport = regexp.MustCompile(`^[A-Za-z0-9._:\[\]-]+$`)

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
	adv := SingleProcessAdvice{
		Server: "django",
		Reason: "the development server of django starts a second process that reloads the code; --noreload keeps one",
	}
	addrport := false
	for _, a := range args[1:] {
		switch {
		case djangoFlags[a]:
		case !strings.HasPrefix(a, "-") && !addrport && djangoAddrport.MatchString(a):
			addrport = true
		default:
			adv.Withheld = true
			return adv, true
		}
	}
	adv.Command = withPrefix(prefix, append(append([]string{}, args...), "--noreload")...)
	return adv, true
}

// uvicornFlags are the options of uvicorn that the suggestion carries and that take no value;
// uvicornValues take one (the next word, or after =). Any other option makes the command withheld:
// it may hold a secret (a key password, a header) or not be known.
var uvicornFlags = map[string]bool{
	"--factory": true, "--proxy-headers": true, "--no-proxy-headers": true, "--no-access-log": true,
	"--access-log": true, "--no-server-header": true, "--no-date-header": true,
}

var uvicornValues = map[string]bool{
	"--host": true, "--port": true, "--uds": true, "--fd": true, "--app-dir": true,
	"--root-path": true, "--forwarded-allow-ips": true, "--log-level": true, "--loop": true,
	"--http": true, "--ws": true, "--lifespan": true, "--interface": true,
}

// uvicornNumbers are harmless options that take a number (the next word, or after =); the value is
// checked, and any other text withholds the command.
var uvicornNumbers = map[string]bool{
	"--timeout-keep-alive": true, "--limit-concurrency": true, "--backlog": true,
	"--limit-max-requests": true, "--timeout-graceful-shutdown": true,
	"--h11-max-incomplete-event-size": true,
}

var digits = regexp.MustCompile(`^[0-9]{1,18}$`)

// uvicornReloadValues are the options of the reloader; they are left out together with --reload.
// UVICORN_RELOAD and UVICORN_WORKERS of the environment are not looked at here.
var uvicornReloadValues = map[string]bool{
	"--reload-dir": true, "--reload-include": true, "--reload-exclude": true, "--reload-delay": true,
}

func uvicornAdvice(prefix, args []string) (SingleProcessAdvice, bool) {
	var kept []string
	changed, unknown, hasApp := false, false, false
	workersAt, lastWorkers := -1, 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, value, hasValue := a, "", false
		if strings.HasPrefix(a, "--") {
			name, value, hasValue = strings.Cut(a, "=")
		}
		switch {
		case name == "--reload" && !hasValue:
			changed = true
		case uvicornReloadValues[name]:
			if !hasValue {
				i++
			}
		case name == "--workers":
			if !hasValue {
				if i+1 >= len(args) {
					return SingleProcessAdvice{}, false
				}
				i++
				value = args[i]
			}
			n, err := strconv.Atoi(value)
			if err != nil {
				return SingleProcessAdvice{}, false
			}
			// click keeps the last value of an option given more than once
			lastWorkers = n
			if workersAt < 0 {
				workersAt = len(kept)
			}
		case uvicornFlags[name] && !hasValue:
			kept = append(kept, a)
		case uvicornNumbers[name]:
			word := a
			if !hasValue {
				if i+1 >= len(args) {
					unknown = true
					break
				}
				i++
				value = args[i]
				word = args[i]
			}
			if !digits.MatchString(value) {
				unknown = true
				break
			}
			if hasValue {
				kept = append(kept, word)
			} else {
				kept = append(kept, a, word)
			}
		case uvicornValues[name]:
			if hasValue {
				kept = append(kept, a)
				break
			}
			if i+1 >= len(args) {
				unknown = true
				break
			}
			i++
			kept = append(kept, a, args[i])
		case !strings.HasPrefix(a, "-") && !hasApp && appSpec.MatchString(a):
			hasApp = true
			kept = append(kept, a)
		default:
			unknown = true
		}
	}
	if lastWorkers > 1 {
		changed = true
	}
	if !changed {
		return SingleProcessAdvice{}, false
	}
	adv := SingleProcessAdvice{
		Server: "uvicorn",
		Reason: "uvicorn starts one process per worker, and a reloader process with --reload; --workers (above 1) and --reload are left out, and --workers 1 is written because WEB_CONCURRENCY or UVICORN_WORKERS in the environment would start workers again; UVICORN_RELOAD in the environment is not covered by --workers 1, and uvicorn has no --no-reload",
	}
	if unknown {
		adv.Withheld = true
		return adv, true
	}
	// --workers 1 stands in place of the first --workers given, else at the end
	at := len(kept)
	if workersAt >= 0 {
		at = workersAt
	}
	kept = append(kept[:at:at], append([]string{"--workers", "1"}, kept[at:]...)...)
	adv.Command = withPrefix(prefix, kept...)
	return adv, true
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

// uvicornWorkers are the worker classes of gunicorn that run uvicorn.
var uvicornWorkers = map[string]bool{
	"uvicorn.workers.UvicornWorker":    true,
	"uvicorn.workers.UvicornH11Worker": true,
	"uvicorn_worker.UvicornWorker":     true,
	"uvicorn_worker.UvicornH11Worker":  true,
}

func gunicornAdvice(args []string) (SingleProcessAdvice, bool) {
	var class, app, bind, chdir string
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
			case "--chdir":
				chdir = value
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
	if !uvicornWorkers[class] {
		return generic, true
	}
	if !parsed {
		generic.Withheld = true
		return generic, true
	}
	if !appSpec.MatchString(app) {
		// a factory or a missing application: nothing is invented
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
	command := []string{"uvicorn", app, "--host", host, "--port", port}
	if chdir != "" {
		command = append(command, "--app-dir", chdir)
	}
	// --workers 1 is explicit: WEB_CONCURRENCY or UVICORN_WORKERS would start workers again
	command = append(command, "--workers", "1")
	adv := SingleProcessAdvice{
		Server:  "gunicorn",
		Command: command,
		Reason:  "gunicorn forks its workers; with the worker class of uvicorn the same application runs under uvicorn in one process (only the bind, the application and --chdir, carried as --app-dir, are read from the command line: the other options of gunicorn are not carried); UVICORN_RELOAD in the environment is not covered by --workers 1, and uvicorn has no --no-reload",
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
// cannot be written so (one with a quote, a backslash, a $, a backtick, a control character or any rune that is not printable):
// no command is made of it.
func QuoteWords(words []string) (string, bool) {
	out := make([]string, len(words))
	for i, w := range words {
		switch {
		case safeWord.MatchString(w):
			out[i] = w
		case strings.IndexFunc(w, func(r rune) bool {
			return !unicode.IsPrint(r) || r == '\'' || r == '"' || r == '\\' || r == '$' || r == '`'
		}) >= 0:
			return "", false
		default:
			out[i] = `"` + w + `"`
		}
	}
	return strings.Join(out, " "), true
}

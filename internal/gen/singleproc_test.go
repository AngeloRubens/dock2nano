package gen

import (
	"slices"
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/compose"
	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/plan"
	"github.com/AngeloRubens/dock2nano/internal/trace"
)

func processesInput(t *testing.T, program string, argv []string) Input {
	t.Helper()
	in, _ := launcherInput(t, program, argv, "")
	in.Services["web"].Observed.Children = 2
	return in
}

func processesMessage(t *testing.T, in Input) string {
	t.Helper()
	got := itemsOf(mustGen(t, in), "web", plan.Unsupported, "processes")
	if len(got) != 1 {
		t.Fatalf("processes items: %d", len(got))
	}
	return got[0].Message
}

// the text the item said before the suggestion existed
const processesBefore = "the program started 2 child processes while it was observed (fork/clone); nanos runs a single process and has no fork, so the program may not start or may not work; threads are fine. Many servers have an option to run in one process (for example no master process); see the documentation of the program"

const gunicornConfigText = "settings of the configuration file (environment, working directory, application, worker class) are not carried; gunicorn also reads gunicorn.conf.py in the working directory and GUNICORN_CMD_ARGS"

func TestProcessesItemSuggestsTheCommand(t *testing.T) {
	uv := "uvicorn.workers.UvicornWorker"
	tests := []struct {
		name    string
		program string
		argv    []string
		want    []string
		not     []string
	}{
		{"django", "/usr/local/bin/python3", []string{"python", "manage.py", "runserver", "0.0.0.0:8000"},
			[]string{"{tool} convert ... --command 'web=python manage.py runserver 0.0.0.0:8000 --noreload'", "review it", "nothing was changed"}, []string{"assumed"}},
		{"uvicorn", "/usr/local/bin/uvicorn", []string{"uvicorn", "app:app", "--workers", "4", "--port", "80"},
			[]string{"--command 'web=uvicorn app:app --workers 1 --port 80'", "review it"}, []string{"--workers 4"}},
		{"gunicorn with the worker of uvicorn", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", uv, "-b", "0.0.0.0:5000", "main:app"},
			[]string{"--command 'web=uvicorn main:app --host 0.0.0.0 --port 5000 --workers 1'", "review it"}, []string{"assumed"}},
		{"gunicorn without a bind", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", uv, "main:app"},
			[]string{"--command 'web=uvicorn main:app --host 0.0.0.0 --port 8000 --workers 1'", "the host and port of this command were assumed"}, nil},
		{"gunicorn with another worker class", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", "gevent", "main:app"},
			[]string{"gunicorn forks its workers", "waitress, uvicorn, hypercorn", "no command is suggested"}, []string{"--command '"}},
		{"a word with a space", "/usr/local/bin/python3", []string{"python", "/my app/manage.py", "runserver"},
			[]string{"--command 'web=python \"/my app/manage.py\" runserver --noreload'"}, nil},
		{"a word with a single quote", "/usr/local/bin/python3", []string{"python", "/it's/manage.py", "runserver"},
			[]string{"no command is suggested", "cannot be quoted safely"}, []string{"--command '"}},
		{"a word with a dollar", "/usr/local/bin/python3", []string{"python", "/$X/manage.py", "runserver"},
			[]string{"no command is suggested"}, []string{"--command '"}},
		{"gunicorn with --chdir", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", uv, "-b", ":80", "--chdir", "/my app", "main:app"},
			[]string{"--command 'web=uvicorn main:app --host 0.0.0.0 --port 80 --app-dir \"/my app\" --workers 1'", "--chdir was written as --app-dir, which does not change the working directory", "working_dir"}, nil},
		{"gunicorn with a configuration file", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", uv, "-c", "conf.py", "main:app"},
			[]string{gunicornConfigText, "were assumed"}, nil},
		{"gunicorn with no -c says it too", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", uv, "-b", ":80", "main:app"},
			[]string{gunicornConfigText}, []string{"--chdir was written as --app-dir"}},
		{"uvicorn has no gunicorn text", "/usr/local/bin/uvicorn", []string{"uvicorn", "app:app", "--workers", "4"},
			nil, []string{"gunicorn.conf.py"}},
	}
	for _, tt := range tests {
		msg := processesMessage(t, processesInput(t, tt.program, tt.argv))
		if !strings.HasPrefix(msg, processesBefore) {
			t.Errorf("%s: the item does not start with the text it had: %q", tt.name, msg)
		}
		for _, w := range tt.want {
			if !strings.Contains(msg, w) {
				t.Errorf("%s: message %q does not hold %q", tt.name, msg, w)
			}
		}
		for _, w := range tt.not {
			if strings.Contains(msg, w) {
				t.Errorf("%s: message %q holds %q", tt.name, msg, w)
			}
		}
	}
}

// an option that may hold a secret: nothing of the command line is repeated
func TestProcessesItemWithholdsTheCommandWithASecret(t *testing.T) {
	for _, c := range []struct {
		name    string
		program string
		argv    []string
	}{
		{"password", "/usr/local/bin/uvicorn", []string{"uvicorn", "app:app", "--workers", "4", "--ssl-keyfile-password", "hunter2"}},
		{"header", "/usr/local/bin/uvicorn", []string{"uvicorn", "app:app", "--reload", "--header", "X-Token:hunter2"}},
		{"django", "/usr/local/bin/python3", []string{"python", "manage.py", "runserver", "--settings=hunter2"}},
	} {
		msg := processesMessage(t, processesInput(t, c.program, c.argv))
		if strings.Contains(msg, "hunter2") || strings.Contains(msg, "--command '") {
			t.Errorf("%s: message %q", c.name, msg)
		}
		if !strings.Contains(msg, "no command is suggested: an option of the command line may hold a secret or is not known") {
			t.Errorf("%s: message %q", c.name, msg)
		}
	}
}

// the program is started through another one: what comes before it is left out
func TestProcessesItemSaysWhenTheServiceStartsTheProgramThroughAnother(t *testing.T) {
	// sh -c runs migrate and then the server
	in := processesInput(t, "/usr/local/bin/python3", []string{"python", "manage.py", "runserver"})
	in.Project.Services[0].Command = compose.Command{Shell: `sh -c "python manage.py migrate && python manage.py runserver"`, Set: true}
	msg := processesMessage(t, in)
	if !strings.Contains(msg, `The service starts this program through "sh"; what that does before it (and any wrapper) is left out of the suggestion`) {
		t.Errorf("sh -c: message = %q", msg)
	}
	// ENTRYPOINT python, CMD start.py, which execs the server
	in = processesInput(t, "/usr/local/bin/python3", []string{"python", "manage.py", "runserver"})
	si := in.Services["web"]
	si.Image = &docker.ImageConfig{Entrypoint: []string{"python"}, Cmd: []string{"start.py"}}
	in.Services["web"] = si
	msg = processesMessage(t, in)
	if !strings.Contains(msg, `through "start.py"; what that does`) || !strings.Contains(msg, "--command 'web=manage.py runserver --noreload'") {
		t.Errorf("entrypoint and start.py: message = %q", msg)
	}
	// the service starts the observed command itself: no such text
	in = processesInput(t, "/usr/local/bin/python3", []string{"python", "manage.py", "runserver"})
	si = in.Services["web"]
	si.Image = &docker.ImageConfig{Cmd: []string{"python", "manage.py", "runserver"}}
	in.Services["web"] = si
	if msg := processesMessage(t, in); strings.Contains(msg, "starts this program through") {
		t.Errorf("message = %q", msg)
	}
}

// the first word that differs is named only when it is a program or a script path
func TestProcessesItemDoesNotNameAnOption(t *testing.T) {
	in := processesInput(t, "/usr/local/bin/python3", []string{"python", "manage.py", "runserver"})
	si := in.Services["web"]
	si.Image = &docker.ImageConfig{Cmd: []string{"python", "-u", "manage.py", "runserver"}}
	in.Services["web"] = si
	msg := processesMessage(t, in)
	if !strings.Contains(msg, "The service starts this program through another command; what that does") || strings.Contains(msg, `"-u"`) {
		t.Errorf("message = %q", msg)
	}
	// ENTRYPOINT python: the program itself is not named, the script is
	in = processesInput(t, "/usr/local/bin/python3", []string{"python", "manage.py", "runserver"})
	si = in.Services["web"]
	si.Image = &docker.ImageConfig{Entrypoint: []string{"python"}, Cmd: []string{"start.py"}}
	in.Services["web"] = si
	if msg := processesMessage(t, in); strings.Contains(msg, `through "python"`) {
		t.Errorf("message = %q", msg)
	}
}

// a console script started through its shebang line: the interpreter and the script are one word
// of the command of the service
func TestProcessesItemReadsAShebangLine(t *testing.T) {
	argv := []string{"/usr/local/bin/python3", "/usr/local/bin/uvicorn", "app:app", "--workers", "4"}
	in := processesInput(t, "/usr/local/bin/python3", argv)
	si := in.Services["web"]
	si.Image = &docker.ImageConfig{Cmd: []string{"uvicorn", "app:app", "--workers", "4"}}
	in.Services["web"] = si
	msg := processesMessage(t, in)
	if strings.Contains(msg, "starts this program through") || !strings.Contains(msg, "--command 'web=uvicorn app:app --workers 1'") {
		t.Errorf("no entrypoint: message = %q", msg)
	}
	// ENTRYPOINT uvicorn: the command is what follows the entrypoint
	in = processesInput(t, "/usr/local/bin/python3", argv)
	si = in.Services["web"]
	si.Image = &docker.ImageConfig{Entrypoint: []string{"uvicorn"}, Cmd: []string{"app:app", "--workers", "4"}}
	in.Services["web"] = si
	msg = processesMessage(t, in)
	if strings.Contains(msg, "starts this program through") || !strings.Contains(msg, "--command 'web=app:app --workers 1'") || strings.Contains(msg, "has an entrypoint") {
		t.Errorf("entrypoint: message = %q", msg)
	}
}

func TestProcessesItemUsesTheNameOfTheTool(t *testing.T) {
	in := processesInput(t, "/usr/local/bin/python3", []string{"python", "manage.py", "runserver"})
	in.ToolName = "mytool"
	msg := processesMessage(t, in)
	if !strings.Contains(msg, "try: mytool convert ... --command 'web=python manage.py runserver --noreload'") || strings.Contains(msg, "{tool}") {
		t.Errorf("message = %q", msg)
	}
}

func TestProcessesItemIsUnchangedForOtherPrograms(t *testing.T) {
	for _, c := range []struct {
		name    string
		program string
		argv    []string
	}{
		{"nginx", "/usr/sbin/nginx", []string{"nginx", "-g", "daemon off;"}},
		{"django without a reloader", "/usr/local/bin/python3", []string{"python", "manage.py", "runserver", "--noreload"}},
		{"uvicorn with one worker", "/usr/local/bin/uvicorn", []string{"uvicorn", "app:app"}},
		{"a script", "/usr/local/bin/python3", []string{"python", "app.py"}},
	} {
		if msg := processesMessage(t, processesInput(t, c.program, c.argv)); msg != processesBefore {
			t.Errorf("%s: message = %q", c.name, msg)
		}
	}
	// no children: no item at all
	in, _ := launcherInput(t, "/usr/local/bin/python3", []string{"python", "manage.py", "runserver"}, "")
	wantNone(t, mustGen(t, in), "web", plan.Unsupported, "processes")
}

func TestProcessesItemWithAnEntrypoint(t *testing.T) {
	// the entrypoint is the start of the command: --command replaces only what follows it
	in := processesInput(t, "/usr/local/bin/python3", []string{"python", "manage.py", "runserver"})
	si := in.Services["web"]
	si.Image = &docker.ImageConfig{Entrypoint: []string{"python"}}
	in.Services["web"] = si
	msg := processesMessage(t, in)
	if !strings.Contains(msg, "--command 'web=manage.py runserver --noreload'") {
		t.Errorf("message = %q", msg)
	}
	// an entrypoint that is not the start of the command: no command is given
	in2 := processesInput(t, "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", "uvicorn.workers.UvicornWorker", "main:app"})
	si = in2.Services["web"]
	si.Image = &docker.ImageConfig{Entrypoint: []string{"gunicorn"}}
	in2.Services["web"] = si
	msg = processesMessage(t, in2)
	if strings.Contains(msg, "--command '") || !strings.Contains(msg, "has an entrypoint") || !strings.Contains(msg, "may still work if the entrypoint execs its arguments") || strings.Contains(msg, "by hand") {
		t.Errorf("message = %q", msg)
	}
}

// The words of a suggested command are read back as they are by the same code that reads a
// --command of the user.
func TestQuoteWordsRoundTripsThroughEffectiveCommand(t *testing.T) {
	for _, words := range [][]string{
		{"uvicorn", "app:app", "--port", "80"},
		{"x", "a*;b"},
		{"x", ""},
		{"x", "a b"},
		{"x", "{x}"},
		{"x", "caf\u00e9"},
		{"x", "a b", "", "c;d", "e|f", "(g)", "h&i", "j#k", "~"},
	} {
		quoted, ok := trace.QuoteWords(words)
		if !ok {
			t.Errorf("%q: not quoted", words)
			continue
		}
		_, cmd, err := EffectiveCommand(&compose.Service{Command: compose.Command{Shell: quoted, Set: true}}, nil, nil)
		if err != nil || !slices.Equal(cmd, words) {
			t.Errorf("%q: written as %s, read back as %q (%v)", words, quoted, cmd, err)
		}
	}
}

const apacheEnvCommand = "env APACHE_RUN_USER=www-data APACHE_RUN_GROUP=www-data APACHE_PID_FILE=/var/run/apache2/apache2.pid APACHE_RUN_DIR=/var/run/apache2 APACHE_LOCK_DIR=/var/lock/apache2 APACHE_LOG_DIR=/var/log/apache2 apache2 -DONE_PROCESS -DFOREGROUND"

func apacheInput(t *testing.T, argv []string, img *docker.ImageConfig) Input {
	t.Helper()
	in := processesInput(t, "/usr/sbin/"+argv[0], argv)
	si := in.Services["web"]
	si.Image = img
	in.Services["web"] = si
	return in
}

// the php:7.2-apache image: ENTRYPOINT docker-php-entrypoint, CMD apache2-foreground
func TestProcessesItemSuggestsApacheWithTheEnvironment(t *testing.T) {
	in := apacheInput(t, []string{"apache2", "-DFOREGROUND"}, &docker.ImageConfig{Entrypoint: []string{"docker-php-entrypoint"}, Cmd: []string{"apache2-foreground"}})
	msg := processesMessage(t, in)
	for _, want := range []string{
		"apache2 starts several processes (fork, prefork)",
		"--command 'web=" + apacheEnvCommand + "'",
		"the environment values (user, group and directories) of this command were assumed",
		"measured on one image only, php:7.2-apache",
		"nextcloud",
		"the cause is not known",
		"the entrypoint docker-php-entrypoint of the service stays",
		"review it; nothing was changed",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %q", want, msg)
		}
	}
	// the program named as started before it is apache2-foreground, not the entrypoint that was said to stay
	if !strings.Contains(msg, `through "apache2-foreground"; what that does`) || strings.Contains(msg, `through "docker-php-entrypoint"`) {
		t.Errorf("message = %q", msg)
	}
}

func TestProcessesItemApacheNeedsTheForegroundScript(t *testing.T) {
	// the service starts apache2 itself, not through apache2-foreground: its environment is not the one assumed
	in := apacheInput(t, []string{"apache2", "-DFOREGROUND"}, &docker.ImageConfig{Cmd: []string{"apache2", "-DFOREGROUND"}})
	msg := processesMessage(t, in)
	if strings.Contains(msg, "--command '") || !strings.Contains(msg, "does not start the program through apache2-foreground") {
		t.Errorf("message = %q", msg)
	}
	// another entrypoint: not the one measured, the generic refusal
	in = apacheInput(t, []string{"apache2", "-DFOREGROUND"}, &docker.ImageConfig{Entrypoint: []string{"/entrypoint.sh"}, Cmd: []string{"apache2-foreground"}})
	msg = processesMessage(t, in)
	if strings.Contains(msg, "--command '") || !strings.Contains(msg, "has an entrypoint") {
		t.Errorf("entrypoint.sh: message = %q", msg)
	}
	// no entrypoint
	in = apacheInput(t, []string{"apache2", "-DFOREGROUND"}, &docker.ImageConfig{Cmd: []string{"apache2-foreground"}})
	if msg := processesMessage(t, in); !strings.Contains(msg, "--command 'web="+apacheEnvCommand+"'") || strings.Contains(msg, "stays and runs") {
		t.Errorf("no entrypoint: message = %q", msg)
	}
}

// the program and the number of arguments are named, the arguments never
func TestProcessesItemApacheDoesNotEchoArguments(t *testing.T) {
	in := apacheInput(t, []string{"apache2", "-DFOREGROUND", "-c", "SetEnv DB_PASSWORD hunter2"}, &docker.ImageConfig{Cmd: []string{"apache2-foreground"}})
	msg := processesMessage(t, in)
	if strings.Contains(msg, "hunter2") || strings.Contains(msg, "DB_PASSWORD") || strings.Contains(msg, "--command '") {
		t.Errorf("message = %q", msg)
	}
	if !strings.Contains(msg, "apache2 was started with 3 arguments") || !strings.Contains(msg, "no command is suggested") {
		t.Errorf("message = %q", msg)
	}
}

func TestProcessesItemHttpdHasNoCommand(t *testing.T) {
	in := apacheInput(t, []string{"httpd", "-DFOREGROUND"}, &docker.ImageConfig{Cmd: []string{"httpd-foreground"}})
	msg := processesMessage(t, in)
	if strings.Contains(msg, "--command '") || !strings.Contains(msg, "httpd starts several processes") || !strings.Contains(msg, "AH00014") || strings.Count(msg, "no command is suggested") != 1 {
		t.Errorf("message = %q", msg)
	}
}

// apache2-foreground inside a shell script is not the service starting it as a program
func TestProcessesItemApacheNeedsTheScriptAsAWordOfItsOwn(t *testing.T) {
	for _, cmd := range []compose.Command{
		{Shell: "sh -c 'chown -R www-data /var/www && exec /usr/local/bin/apache2-foreground'", Set: true},
		{Exec: []string{"sh", "-c", "chown -R www-data /var/www && exec /usr/local/bin/apache2-foreground"}, Set: true},
	} {
		in := apacheInput(t, []string{"apache2", "-DFOREGROUND"}, nil)
		in.Project.Services[0].Command = cmd
		msg := processesMessage(t, in)
		if strings.Contains(msg, "--command '") || !strings.Contains(msg, "does not start the program through apache2-foreground") {
			t.Errorf("message = %q", msg)
		}
	}
}

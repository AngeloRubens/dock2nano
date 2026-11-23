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
			[]string{"--command 'web=uvicorn app:app --port 80 --workers 1'", "review it"}, []string{"--workers 4"}},
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
			[]string{"--command 'web=uvicorn main:app --host 0.0.0.0 --port 80 --app-dir \"/my app\" --workers 1'"}, nil},
		{"gunicorn with a configuration file", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", uv, "-c", "conf.py", "main:app"},
			[]string{"may be set in it as well", "were assumed"}, nil},
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
	if !strings.Contains(msg, `through "python"; what that does`) || !strings.Contains(msg, "--command 'web=manage.py runserver --noreload'") {
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

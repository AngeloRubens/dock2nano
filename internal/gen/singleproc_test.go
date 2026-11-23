package gen

import (
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/plan"
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
			[]string{"--command 'web=uvicorn app:app --port 80'", "review it"}, []string{"--workers 4"}},
		{"gunicorn with the worker of uvicorn", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", uv, "-b", "0.0.0.0:5000", "main:app"},
			[]string{"--command 'web=uvicorn main:app --host 0.0.0.0 --port 5000'", "review it"}, []string{"assumed"}},
		{"gunicorn without a bind", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", uv, "main:app"},
			[]string{"--command 'web=uvicorn main:app --host 0.0.0.0 --port 8000'", "the host and port of this command were assumed"}, nil},
		{"gunicorn with another worker class", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", "gevent", "main:app"},
			[]string{"gunicorn forks its workers", "waitress, uvicorn, hypercorn", "no command is suggested"}, []string{"--command '"}},
		{"a word with a space", "/usr/local/bin/python3", []string{"python", "manage.py", "runserver", "a b"},
			[]string{"--command 'web=python manage.py runserver \"a b\" --noreload'"}, nil},
		{"a word with a single quote", "/usr/local/bin/python3", []string{"python", "manage.py", "runserver", "it's"},
			[]string{"no command is suggested", "cannot be quoted safely"}, []string{"--command '"}},
		{"a word with a dollar", "/usr/local/bin/python3", []string{"python", "manage.py", "runserver", "$X"},
			[]string{"no command is suggested"}, []string{"--command '"}},
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
	if strings.Contains(msg, "--command '") || !strings.Contains(msg, "has an entrypoint") {
		t.Errorf("message = %q", msg)
	}
}

package trace

import (
	"reflect"
	"strings"
	"testing"
)

func TestSingleProcess(t *testing.T) {
	uv := "uvicorn.workers.UvicornWorker"
	tests := []struct {
		name    string
		program string
		argv    []string
		ok      bool
		server  string
		command []string
		assumed string
	}{
		{"django runserver", "/usr/local/bin/python3.12", []string{"python", "manage.py", "runserver", "0.0.0.0:8000"}, true, "django",
			[]string{"python", "manage.py", "runserver", "0.0.0.0:8000", "--noreload"}, ""},
		{"django with a path and an option of python", "/usr/local/bin/python3", []string{"python3", "-u", "/app/manage.py", "runserver", "--insecure", "0.0.0.0:80"}, true, "django",
			[]string{"python3", "-u", "/app/manage.py", "runserver", "--insecure", "0.0.0.0:80", "--noreload"}, ""},
		{"django with -W taking a value", "/usr/local/bin/python3", []string{"python3", "-W", "ignore", "manage.py", "runserver"}, true, "django",
			[]string{"python3", "-W", "ignore", "manage.py", "runserver", "--noreload"}, ""},
		{"django through -m", "/usr/local/bin/python3", []string{"python3", "-m", "django", "runserver"}, true, "django",
			[]string{"python3", "-m", "django", "runserver", "--noreload"}, ""},
		{"django-admin", "/usr/local/bin/django-admin", []string{"django-admin", "runserver"}, true, "django",
			[]string{"django-admin", "runserver", "--noreload"}, ""},
		{"django with --noreload", "/usr/local/bin/python3", []string{"python", "manage.py", "runserver", "--noreload"}, false, "", nil, ""},
		{"django other subcommand", "/usr/local/bin/python3", []string{"python", "manage.py", "migrate"}, false, "", nil, ""},
		{"a script that is not manage.py", "/usr/local/bin/python3", []string{"python", "app.py", "runserver"}, false, "", nil, ""},
		{"python -c", "/usr/local/bin/python3", []string{"python", "-c", "import uvicorn"}, false, "", nil, ""},
		{"python -m of another module", "/usr/local/bin/python3", []string{"python", "-m", "http.server"}, false, "", nil, ""},

		{"uvicorn with workers", "/usr/local/bin/uvicorn", []string{"uvicorn", "app:app", "--host", "0.0.0.0", "--workers", "4"}, true, "uvicorn",
			[]string{"uvicorn", "app:app", "--host", "0.0.0.0"}, ""},
		{"uvicorn with workers=", "/usr/local/bin/uvicorn", []string{"uvicorn", "--workers=2", "app:app"}, true, "uvicorn",
			[]string{"uvicorn", "app:app"}, ""},
		{"uvicorn with reload", "/usr/local/bin/python3", []string{"python", "-m", "uvicorn", "app.main:app", "--reload", "--port", "80"}, true, "uvicorn",
			[]string{"python", "-m", "uvicorn", "app.main:app", "--port", "80"}, ""},
		{"uvicorn with reload and workers 1", "/usr/local/bin/uvicorn", []string{"uvicorn", "app:app", "--workers", "1", "--reload"}, true, "uvicorn",
			[]string{"uvicorn", "app:app", "--workers", "1"}, ""},
		{"uvicorn started by python on its file", "/usr/local/bin/python3", []string{"python", "/usr/local/bin/uvicorn", "app:app", "--workers", "3"}, true, "uvicorn",
			[]string{"python", "/usr/local/bin/uvicorn", "app:app"}, ""},
		{"uvicorn with one worker", "/usr/local/bin/uvicorn", []string{"uvicorn", "app:app", "--workers", "1"}, false, "", nil, ""},
		{"uvicorn plain", "/usr/local/bin/uvicorn", []string{"uvicorn", "app:app"}, false, "", nil, ""},
		{"uvicorn with a workers value that is not a number", "/usr/local/bin/uvicorn", []string{"uvicorn", "app:app", "--workers", "$N"}, false, "", nil, ""},

		{"gunicorn with the worker of uvicorn and a bind", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", uv, "-w", "4", "-b", "0.0.0.0:5000", "main:app"}, true, "gunicorn",
			[]string{"uvicorn", "main:app", "--host", "0.0.0.0", "--port", "5000"}, ""},
		{"gunicorn long options", "/usr/local/bin/gunicorn", []string{"gunicorn", "main:app", "--worker-class", uv, "--bind=127.0.0.1:9000"}, true, "gunicorn",
			[]string{"uvicorn", "main:app", "--host", "127.0.0.1", "--port", "9000"}, ""},
		{"gunicorn worker class with =", "/usr/local/bin/gunicorn", []string{"gunicorn", "--worker-class=" + uv, "-b", ":8080", "main:app"}, true, "gunicorn",
			[]string{"uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8080"}, ""},
		{"gunicorn attached short options", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k" + uv, "-b0.0.0.0:7000", "main:app"}, true, "gunicorn",
			[]string{"uvicorn", "main:app", "--host", "0.0.0.0", "--port", "7000"}, ""},
		{"gunicorn bind with a host only", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", uv, "-b", "10.0.0.1", "main:app"}, true, "gunicorn",
			[]string{"uvicorn", "main:app", "--host", "10.0.0.1", "--port", "8000"}, ""},
		{"gunicorn bind IPv6", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", uv, "-b", "[::1]:8000", "main:app"}, true, "gunicorn",
			[]string{"uvicorn", "main:app", "--host", "::1", "--port", "8000"}, ""},
		{"gunicorn without a bind", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", uv, "main:app"}, true, "gunicorn",
			[]string{"uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"}, "host and port"},
		{"gunicorn bind to a socket", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", uv, "-b", "unix:/run/app.sock", "main:app"}, true, "gunicorn",
			[]string{"uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"}, "host and port"},
		{"gunicorn through python -m", "/usr/local/bin/python3", []string{"python", "-m", "gunicorn", "-k", uv, "-b", ":80", "main:app"}, true, "gunicorn",
			[]string{"uvicorn", "main:app", "--host", "0.0.0.0", "--port", "80"}, ""},
		{"gunicorn with another worker class", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", "gevent", "main:app"}, true, "gunicorn", nil, ""},
		{"gunicorn with the sync worker", "/usr/local/bin/gunicorn", []string{"gunicorn", "-w", "2", "main:app"}, true, "gunicorn", nil, ""},
		{"gunicorn with a factory", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", uv, "main:create_app()"}, true, "gunicorn", nil, ""},
		{"gunicorn with an option that is not known", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", uv, "--strange", "main:app"}, true, "gunicorn", nil, ""},
		{"gunicorn with the other worker of uvicorn", "/usr/local/bin/gunicorn", []string{"gunicorn", "-k", "uvicorn.workers.UvicornH11Worker", "main:app"}, true, "gunicorn", nil, ""},

		{"nginx", "/usr/sbin/nginx", []string{"nginx", "-g", "daemon off;"}, false, "", nil, ""},
		{"no argv", "/usr/local/bin/uvicorn", nil, false, "", nil, ""},
		{"python without a script", "/usr/local/bin/python3", []string{"python"}, false, "", nil, ""},
	}
	for _, tt := range tests {
		adv, ok := SingleProcess(tt.program, tt.argv)
		if ok != tt.ok {
			t.Errorf("%s: ok = %v, want %v", tt.name, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		if adv.Server != tt.server {
			t.Errorf("%s: server = %q, want %q", tt.name, adv.Server, tt.server)
		}
		if !reflect.DeepEqual(adv.Command, tt.command) {
			t.Errorf("%s: command = %q, want %q", tt.name, adv.Command, tt.command)
		}
		if adv.Assumed != tt.assumed {
			t.Errorf("%s: assumed = %q, want %q", tt.name, adv.Assumed, tt.assumed)
		}
		if adv.Reason == "" {
			t.Errorf("%s: no reason", tt.name)
		}
	}
}

// The advice is built in a new slice: the argv of the caller is left as it was, even when it has
// room for one more word.
func TestSingleProcessKeepsArgv(t *testing.T) {
	argv := make([]string, 3, 8)
	copy(argv, []string{"python", "manage.py", "runserver"})
	adv, ok := SingleProcess("/usr/local/bin/python3", argv)
	if !ok || len(adv.Command) != 4 || adv.Command[3] != "--noreload" {
		t.Fatalf("command = %q, ok = %v", adv.Command, ok)
	}
	if len(argv) != 3 || argv[:4:4][3] != "" {
		t.Errorf("argv changed: %q", argv[:4])
	}
}

func TestQuoteWords(t *testing.T) {
	tests := []struct {
		name  string
		words []string
		want  string
		ok    bool
	}{
		{"plain", []string{"python", "manage.py", "runserver", "0.0.0.0:8000", "--noreload"}, "python manage.py runserver 0.0.0.0:8000 --noreload", true},
		{"a space", []string{"uvicorn", "a b:app"}, `uvicorn "a b:app"`, true},
		{"a star and a semicolon", []string{"x", "a*;b"}, `x "a*;b"`, true},
		{"an empty word", []string{"x", ""}, `x ""`, true},
		{"a single quote", []string{"x", "it's"}, "", false},
		{"a double quote", []string{"x", `a"b`}, "", false},
		{"a dollar", []string{"x", "$HOME"}, "", false},
		{"a backslash", []string{"x", `a\b`}, "", false},
		{"a backtick", []string{"x", "`id`"}, "", false},
		{"a newline", []string{"x", "a\nb"}, "", false},
	}
	for _, tt := range tests {
		got, ok := QuoteWords(tt.words)
		if ok != tt.ok || got != tt.want {
			t.Errorf("%s: QuoteWords = %q, %v; want %q, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
	if got, _ := QuoteWords([]string{"a b"}); strings.Contains(got, "'") {
		t.Errorf("a single quote in %q", got)
	}
}

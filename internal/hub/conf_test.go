package hub

import (
	"reflect"
	"strings"
	"testing"
)

const userNginx = `# the user's file
user  nginx;
worker_processes  auto;

error_log  /var/log/nginx/error.log notice;
pid        /var/run/nginx.pid;

events {
    worker_connections  1024;
}

http {
    include       /etc/nginx/mime.types;
    log_format main '$remote_addr "quoted; text" { braces }';

    server {
        listen       80;
        server_name  localhost;
        location / {
            root   /usr/share/nginx/html; # a comment; with a semicolon
        }
    }
    server {
        listen 8443 ssl;
        root /srv;
    }
}
`

func TestParseDirectives(t *testing.T) {
	ds := ParseDirectives(userNginx)
	find := func(block, name string) []Directive {
		var out []Directive
		for _, d := range ds {
			if d.Block == block && d.Name == name {
				out = append(out, d)
			}
		}
		return out
	}
	if d := find("", "worker_processes"); len(d) != 1 || d[0].Args != "auto" || d[0].IsBlock {
		t.Errorf("worker_processes: %+v", d)
	}
	if d := find("", "http"); len(d) != 1 || !d[0].IsBlock {
		t.Errorf("http: %+v", d)
	}
	if d := find("http/server", "listen"); len(d) != 2 || d[0].Args != "80" || d[1].Args != "8443 ssl" {
		t.Errorf("listen: %+v", d)
	}
	if d := find("http/server/location", "root"); len(d) != 1 || d[0].Args != "/usr/share/nginx/html" {
		t.Errorf("root: %+v", d)
	}
	if d := find("http", "log_format"); len(d) != 1 || !strings.Contains(d[0].Args, `"quoted; text"`) || !strings.Contains(d[0].Args, "{ braces }") {
		t.Errorf("a quoted word holds ; and braces: %+v", d)
	}
	if d := find("events", "worker_connections"); len(d) != 1 {
		t.Errorf("events: %+v", d)
	}
}

func TestParseDirectivesOffsets(t *testing.T) {
	text := "a 1;\n  b  two words ;\n"
	ds := ParseDirectives(text)
	if len(ds) != 2 {
		t.Fatalf("%+v", ds)
	}
	if got := text[ds[0].Start:ds[0].End]; got != "a 1;" {
		t.Errorf("first: %q", got)
	}
	if got := text[ds[1].Start:ds[1].End]; got != "b  two words ;" {
		t.Errorf("second: %q", got)
	}
}

func TestParseDirectivesEndsAtAFault(t *testing.T) {
	for _, text := range []string{"a 1;\nb", "a 1;\nb \"unterminated", "a 1;\n}\n}\nb 2;", "{ ; ;", "a ${x"} {
		_ = ParseDirectives(text)
	}
	if ds := ParseDirectives("a 1;\nb"); len(ds) != 1 {
		t.Errorf("a directive without its end is not one: %+v", ds)
	}
	if ds := ParseDirectives("a 1;\n}\n}\nb 2;"); len(ds) != 2 {
		t.Errorf("stray closing braces: %+v", ds)
	}
}

func TestSetTopLevelRewritesAndAdds(t *testing.T) {
	out, changes := SetTopLevel(userNginx, []Setting{{"master_process", "off"}, {"daemon", "off"}, {"worker_processes", "1"}})
	if !strings.HasPrefix(out, "master_process off;\ndaemon off;\n# the user's file\n") {
		t.Errorf("the missing directives are not at the top:\n%s", out)
	}
	if !strings.Contains(out, "worker_processes 1;\n") || strings.Contains(out, "worker_processes  auto") {
		t.Errorf("worker_processes not rewritten:\n%s", out)
	}
	for _, keep := range []string{"listen       80;", "listen 8443 ssl;", "root /srv;", "user  nginx;", "# a comment; with a semicolon", "pid        /var/run/nginx.pid;"} {
		if !strings.Contains(out, keep) {
			t.Errorf("%q was lost:\n%s", keep, out)
		}
	}
	want := []Change{
		{Name: "master_process", New: "off", Added: true},
		{Name: "daemon", New: "off", Added: true},
		{Name: "worker_processes", Old: "auto", New: "1"},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("changes: %+v", changes)
	}
}

func TestSetTopLevelLeavesWhatIsRight(t *testing.T) {
	text := "daemon off;\nworker_processes 1;\nevents {}\nhttp { server { daemon on; } }\n"
	out, changes := SetTopLevel(text, []Setting{{"daemon", "off"}, {"worker_processes", "1"}})
	if out != text || len(changes) != 0 {
		t.Errorf("changed: %q %+v", out, changes)
	}
}

func TestSetTopLevelDoesNotTouchNestedDirectives(t *testing.T) {
	text := "http {\n  daemon on;\n}\n"
	out, changes := SetTopLevel(text, []Setting{{"daemon", "off"}})
	if !strings.Contains(out, "  daemon on;\n") || !strings.HasPrefix(out, "daemon off;\n") || len(changes) != 1 || !changes[0].Added {
		t.Errorf("%q %+v", out, changes)
	}
}

func TestSetTopLevelRewritesEveryCopy(t *testing.T) {
	out, _ := SetTopLevel("daemon on;\nworker_processes 4;\ndaemon on;\n", []Setting{{"daemon", "off"}})
	if strings.Count(out, "daemon off;") != 2 || strings.Contains(out, "daemon on") || !strings.Contains(out, "worker_processes 4;") {
		t.Errorf("%q", out)
	}
}

func TestTopLevelValueAndValues(t *testing.T) {
	if v, ok := TopLevelValue(userNginx, "worker_processes"); !ok || v != "auto" {
		t.Errorf("%q %v", v, ok)
	}
	if _, ok := TopLevelValue(userNginx, "listen"); ok {
		t.Error("listen is not at the top level")
	}
	if got := Values(userNginx, "listen"); !reflect.DeepEqual(got, []string{"80", "8443 ssl"}) {
		t.Errorf("listen: %v", got)
	}
	if got := Values(userNginx, "http"); got != nil {
		t.Errorf("a block is not a value: %v", got)
	}
}

package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/hub"
	"github.com/AngeloRubens/dock2nano/internal/observe"
	"github.com/AngeloRubens/dock2nano/internal/output"
	"github.com/AngeloRubens/dock2nano/internal/plan"
)

// The compose name of the service differs from its package name (my_db is the
// package my-db), because ops names the instance after the package.
const myDB = "services:\n  my_db:\n    image: postgres:16\n    environment:\n"

func TestPostgresServiceGetsAPostUpScriptThatTheWriterPutsNextToCompose(t *testing.T) {
	h := pgWhole(t, myDB+"      POSTGRES_DB: example\n      POSTGRES_USER: app\n      POSTGRES_PASSWORD: hunter2\n", nil)
	if len(h.plan.Services) != 1 {
		t.Fatalf("services: %+v", h.plan.Services)
	}
	svc := h.plan.Services[0]
	script := svc.PostUp
	for _, want := range []string{"#!/bin/sh\n", `'CREATE ROLE "app" LOGIN SUPERUSER'`, `'CREATE DATABASE "example" OWNER "app"'`, "instance='my-db'", "ops instance logs \"$instance\"", "-U 'postgres'", plan.PostUpMarker} {
		if !strings.Contains(script, want) {
			t.Errorf("the script lacks %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "my_db") || strings.Contains(script, "hunter2") {
		t.Errorf("the compose name or the password is in the script:\n%s", script)
	}
	if svc.PostUpFile != "post-up-my-db.sh" {
		t.Errorf("file name %q", svc.PostUpFile)
	}
	out := h.stdout.String()
	for _, want := range []string{"post-up-my-db.sh is written next to compose.yaml", "ops compose up does NOT run it", "run it yourself after ops compose up", "from the variable POSTGRES_PASSWORD of the shell that runs it", `-c 'CREATE DATABASE "example" OWNER "app"'`, "run by hand", "then, once my_db is up:", "post-up-my-db.sh    (ops compose does not run it"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "hunter2") || strings.Contains(out, "was written") {
		t.Errorf("the report has the password, or says a file was written before it was:\n%s", out)
	}

	// the real writer puts it next to compose.yaml, executable
	dir := t.TempDir()
	composePath := filepath.Join(dir, "compose.yaml")
	// the sysroot of the run is gone by now: the writer gets a plan with a new one
	p := *h.plan
	p.Services = append(p.Services[:0:0], h.plan.Services...)
	p.Services[0].Sysroot = filepath.Join(dir, "sysroot")
	if err := os.MkdirAll(p.Services[0].Sysroot, 0o755); err != nil {
		t.Fatal(err)
	}
	written, err := output.Write(&p, output.Options{OpsHome: filepath.Join(dir, "home"), ComposePath: composePath})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "post-up-my-db.sh")
	if len(written) != 3 || written[2] != path {
		t.Fatalf("written: %v", written)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("%v %v", fi, err)
	}
	if sh, err := exec.LookPath("sh"); err == nil {
		if out, err := exec.Command(sh, "-n", path).CombinedOutput(); err != nil {
			t.Errorf("sh -n: %v\n%s", err, out)
		}
	}
}

func TestPostUpScriptIsNotAnnouncedAsWrittenWhenTheWriterFails(t *testing.T) {
	h := pgWholeStatus(t, myDB+"      POSTGRES_DB: example\n", func(h *hubHarness) {
		h.app.Write = func(p *plan.Plan, o output.Options) ([]string, error) {
			return nil, errors.New("disk full")
		}
	}, 1)
	out := h.stdout.String()
	if strings.Contains(out, "was written") || strings.Contains(out, "Written:") || strings.Contains(out, "then, once") {
		t.Errorf("the output says a file was written:\n%s", out)
	}
	if !strings.Contains(out, "is written next to compose.yaml") {
		t.Errorf("the report does not announce the script in the future tense:\n%s", out)
	}
}

func TestPostgresServiceWithANameWithControlCharactersGetsNoScript(t *testing.T) {
	h := pgWhole(t, myDB+"      POSTGRES_DB: \"a\\nb\"\n", nil)
	if got := h.plan.Services[0].PostUp; got != "" {
		t.Errorf("a script was made:\n%s", got)
	}
	if out := h.stdout.String(); !strings.Contains(out, "No script is written for it: a name holds a control") || strings.Contains(out, "then, once") {
		t.Errorf("the report does not say that no script is written:\n%s", out)
	}
}

func TestPostgresServiceWithNothingToCreateGetsNoScript(t *testing.T) {
	h := pgWhole(t, "services:\n  my_db:\n    image: postgres:16\n", nil)
	if got := h.plan.Services[0].PostUp; got != "" {
		t.Errorf("a script was made:\n%s", got)
	}
	if out := h.stdout.String(); !strings.Contains(out, "No script is written for it: the names this tool knows give nothing to create") {
		t.Errorf("%s", out)
	}
}

func TestPostgresServiceWithAnEnvironmentThatIsNotCertainGetsNoScript(t *testing.T) {
	h := pgWhole(t, myDB+"      POSTGRES_DB: example\n    env_file: missing.env\n", nil)
	if got := h.plan.Services[0].PostUp; got != "" {
		t.Errorf("a script was made:\n%s", got)
	}
	if out := h.stdout.String(); !strings.Contains(out, "No script is written for it: the environment of the service could not be read completely") {
		t.Errorf("%s", out)
	}
}

// The shape of the spring-postgres example: POSTGRES_DB and a password that
// comes from a file secret of the compose file.
const springPG = "services:\n  db:\n    image: postgres:16\n    environment:\n      POSTGRES_DB: example\n      POSTGRES_PASSWORD_FILE: /run/secrets/db-password\n    secrets:\n      - db-password\nsecrets:\n  db-password:\n    file: db/password.txt\n"

func TestPostUpScriptReadsThePasswordFromTheFileOfTheSecretOnTheHost(t *testing.T) {
	h := pgWhole(t, springPG, func(h *hubHarness) { h.write("db/password.txt", "x\n") })
	svc := h.plan.Services[0]
	want := filepath.Join(h.dir, "db", "password.txt")
	for _, w := range []string{"pwfile='" + want + "'", `ensure 'database'`, `'ALTER ROLE "postgres" PASSWORD '`} {
		if !strings.Contains(svc.PostUp, w) {
			t.Errorf("the script lacks %q:\n%s", w, svc.PostUp)
		}
	}
	if strings.Contains(svc.PostUp, "/run/secrets") {
		t.Errorf("the path in the container is in the script:\n%s", svc.PostUp)
	}
	out := h.stdout.String()
	for _, w := range []string{"from the file " + want, "read when the script runs", "after ops compose up", "may exit", "timing was not measured"} {
		if !strings.Contains(out, w) {
			t.Errorf("the report lacks %q:\n%s", w, out)
		}
	}
}

func TestPostUpScriptNeverHoldsThePasswordOfTheFileOfTheSecret(t *testing.T) {
	h := pgWhole(t, springPG, func(h *hubHarness) { h.write("db/password.txt", "needle-value-9\n") })
	if strings.Contains(h.plan.Services[0].PostUp, "needle-value-9") || strings.Contains(h.stdout.String(), "needle-value-9") || strings.Contains(h.stderr.String(), "needle-value-9") {
		t.Errorf("the password is in the script or the report")
	}
}

func TestPostUpScriptIsNotWrittenWithNoHub(t *testing.T) {
	h := newHubHarness(t, "postgres:16", func(sysroot string) *observe.Result {
		return &observe.Result{Sysroot: sysroot, Program: "/usr/lib/postgresql/16/bin/postgres", Argv: []string{"postgres"}, UID: -1, GID: -1}
	})
	h.write("compose.yaml", springPG)
	rows, err := hub.DefaultRequired()
	if err != nil {
		t.Fatal(err)
	}
	h.app.HubRequired = rows
	h.stub.pkgs = []hub.Package{hubPkg("francescolavra", "postgres", "16.2")}
	h.stub.wholeFiles = map[string]string{"postgres": "ELF"}
	h.run("convert", "--no-hub")
	if h.plan == nil {
		return
	}
	for _, s := range h.plan.Services {
		if s.PostUp != "" {
			t.Errorf("a script was made with --no-hub:\n%s", s.PostUp)
		}
	}
}

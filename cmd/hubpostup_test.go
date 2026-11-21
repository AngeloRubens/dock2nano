package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/output"
)

func TestPostgresServiceGetsAPostUpScriptThatTheWriterPutsNextToCompose(t *testing.T) {
	h := pgWhole(t, "services:\n  db:\n    image: postgres:16\n    environment:\n      POSTGRES_DB: example\n      POSTGRES_USER: app\n      POSTGRES_PASSWORD: hunter2\n", nil)
	if len(h.plan.Services) != 1 {
		t.Fatalf("services: %+v", h.plan.Services)
	}
	script := h.plan.Services[0].PostUp
	for _, want := range []string{"#!/bin/sh\n", `'CREATE ROLE "app" LOGIN SUPERUSER'`, `'CREATE DATABASE "example" OWNER "app"'`, "service='db'", "-U 'postgres'"} {
		if !strings.Contains(script, want) {
			t.Errorf("the script lacks %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "hunter2") {
		t.Errorf("the password is in the script:\n%s", script)
	}
	out := h.stdout.String()
	for _, want := range []string{"post-up-db.sh", "ops compose up does NOT run it", "run it yourself after ops compose up", "without a password", `-c 'CREATE DATABASE "example" OWNER "app"'`, "run by hand"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "hunter2") {
		t.Errorf("the password is in the report:\n%s", out)
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
	path := filepath.Join(dir, "post-up-db.sh")
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

func TestPostgresServiceWithANameWithControlCharactersGetsNoScript(t *testing.T) {
	h := pgWhole(t, "services:\n  db:\n    image: postgres:16\n    environment:\n      POSTGRES_DB: \"a\\nb\"\n", nil)
	if got := h.plan.Services[0].PostUp; got != "" {
		t.Errorf("a script was made:\n%s", got)
	}
	if out := h.stdout.String(); !strings.Contains(out, "No script (post-up-db.sh) was written") || !strings.Contains(out, "control character") {
		t.Errorf("the report does not say that no script was written:\n%s", out)
	}
}

func TestPostgresServiceWithNothingToCreateGetsNoScript(t *testing.T) {
	h := pgWhole(t, "services:\n  db:\n    image: postgres:16\n", nil)
	if got := h.plan.Services[0].PostUp; got != "" {
		t.Errorf("a script was made:\n%s", got)
	}
	if out := h.stdout.String(); !strings.Contains(out, "No script (post-up-db.sh) was written") {
		t.Errorf("%s", out)
	}
}

package cmd

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/hub"
	"github.com/AngeloRubens/dock2nano/internal/observe"
)

// pgWhole runs the conversion of a postgres service with the shipped row and
// returns the report. The observation has no POSTGRES_DB in its environment.
func pgWhole(t *testing.T, composeText string, setup func(h *hubHarness)) *hubHarness {
	t.Helper()
	pgObserved := func(sysroot string) *observe.Result {
		return &observe.Result{Sysroot: sysroot, Program: "/usr/lib/postgresql/16/bin/postgres", Argv: []string{"postgres"}, UID: -1, GID: -1}
	}
	h := newHubHarness(t, "postgres:16", pgObserved)
	h.write("compose.yaml", composeText)
	rows, err := hub.DefaultRequired()
	if err != nil {
		t.Fatal(err)
	}
	h.app.HubRequired = rows
	h.stub.pkgs = []hub.Package{hubPkg("francescolavra", "postgres", "16.2")}
	h.stub.wholeFiles = map[string]string{"postgres": "ELF"}
	h.stub.args = []string{"./postgres", "-D", "db"}
	if setup != nil {
		setup(h)
	}
	h.mustRun(0, "convert")
	return h
}

func TestEntrypointNoteReadsTheEnvironmentOfTheComposeFileOfTheService(t *testing.T) {
	h := pgWhole(t, "services:\n  db:\n    image: postgres:16\n    environment:\n      POSTGRES_DB: example\n", nil)
	out := h.stdout.String()
	if !strings.Contains(out, `-c 'CREATE DATABASE "example"'`) {
		t.Errorf("the report does not say CREATE DATABASE \"example\":\n%s", out)
	}
	if strings.Contains(out, "No database has to be created") {
		t.Errorf("the report says no database has to be created although the compose file sets POSTGRES_DB:\n%s", out)
	}
}

func TestEntrypointNoteSaysNoDatabaseOnlyWhenTheEnvironmentIsCertain(t *testing.T) {
	// nothing set anywhere: certain
	h := pgWhole(t, "services:\n  db:\n    image: postgres:16\n", nil)
	if !strings.Contains(h.stdout.String(), "No database has to be created") {
		t.Errorf("%s", h.stdout)
	}
	// an env_file that cannot be read: the variables of the service are not known
	h = pgWhole(t, "services:\n  db:\n    image: postgres:16\n    env_file: missing.env\n", nil)
	out := h.stdout.String()
	if strings.Contains(out, "No database has to be created") || !strings.Contains(out, "<your POSTGRES_DB or POSTGRES_USER>") {
		t.Errorf("%s", out)
	}
}

func TestTheFinalRemovalOfTheObservedDirectoriesWarnsWhenItFails(t *testing.T) {
	h := newHubHarness(t, "nginx:1.25.3", nginxObserved(0))
	h.app.RemoveAll = func(p string) error {
		os.RemoveAll(p)
		return errors.New("device busy")
	}
	h.mustRun(0, "convert")
	if len(h.made) != 1 || !strings.Contains(h.stderr.String(), "warning: the directory "+h.made[0]+" could not be removed (device busy)") {
		t.Errorf("made %v, stderr:\n%s", h.made, h.stderr)
	}
}

package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/hub"
)

// postUpUse is the use of a package with the note, the superuser and the script
// of the row that ships in the binary.
func postUpUse(t *testing.T, ns, name, version string) HubUse {
	t.Helper()
	u := shippedUse(t, ns, name, version)
	rows, err := hub.DefaultRequired()
	if err != nil {
		t.Fatal(err)
	}
	u.PostUpScript = rows[0].PostUpScript
	return u
}

func postUpFor(t *testing.T, u HubUse, env ...string) string {
	t.Helper()
	obs := hubWholeObs()
	obs.Env = env
	ApplyHubWhole(u, obs)
	return PostUpScript(u, obs)
}

func needSh(t *testing.T) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on this machine")
	}
	return sh
}

// syntaxOK runs sh -n on the script.
func syntaxOK(t *testing.T, script string) {
	t.Helper()
	sh := needSh(t)
	f := filepath.Join(t.TempDir(), "post-up.sh")
	if err := os.WriteFile(f, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(sh, "-n", f).CombinedOutput(); err != nil {
		t.Errorf("sh -n: %v\n%s\n--- script:\n%s", err, out, script)
	}
}

func TestPostUpScriptCreatesTheDatabaseAsTheSuperuserOfFrancescolavra(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop")
	for _, want := range []string{
		"#!/bin/sh\n", "set -eu\n", "Written by dock2nano", "-U 'postgres' -d template1 -w", "[ \"$i\" -lt 12 ]", "sleep 5",
		"\n  host=$1\n", "command -v psql", "ops instance logs \"$service\"", "service='db'",
		`ensure 'database' 'SELECT 1 FROM pg_database WHERE datname = '\''shop'\''' 'CREATE DATABASE "shop"'` + "\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the script lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "ensure 'role'") || strings.Contains(s, "OWNER") {
		t.Errorf("a role is made although POSTGRES_USER is not set:\n%s", s)
	}
	syntaxOK(t, s)
}

func TestPostUpScriptUsesTheSuperuserOfEyberg(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "eyberg", "postgresql", "11.3.0"), "POSTGRES_DB=shop")
	if !strings.Contains(s, "-U 'eyberg' -d template1 -w") || strings.Contains(s, "-U 'postgres'") {
		t.Errorf("superuser:\n%s", s)
	}
	syntaxOK(t, s)
}

func TestPostUpScriptCreatesTheRoleFirstWhenItIsNotTheSuperuserAndWritesNoPassword(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop", "POSTGRES_USER=app", "POSTGRES_PASSWORD=hunter2", "POSTGRES_INITDB_ARGS=--auth=md5")
	role := strings.Index(s, `ensure 'role' 'SELECT 1 FROM pg_roles WHERE rolname = '\''app'\''' 'CREATE ROLE "app" LOGIN SUPERUSER'`)
	db := strings.Index(s, `ensure 'database' 'SELECT 1 FROM pg_database WHERE datname = '\''shop'\''' 'CREATE DATABASE "shop" OWNER "app"'`)
	if role < 0 || db < 0 || role > db {
		t.Errorf("role at %d, database at %d:\n%s", role, db, s)
	}
	for _, bad := range []string{"hunter2", "POSTGRES_PASSWORD", "PASSWORD '", "PASSWORD="} {
		if strings.Contains(s, bad) {
			t.Errorf("the script holds %q:\n%s", bad, s)
		}
	}
	syntaxOK(t, s)
}

func TestPostUpScriptDoesNotMakeTheRoleWhenItIsTheSuperuser(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop", "POSTGRES_USER=postgres")
	if strings.Contains(s, "ensure 'role'") || !strings.Contains(s, `'CREATE DATABASE "shop"'`) {
		t.Errorf("%s", s)
	}
}

func TestPostUpScriptTakesTheDatabaseFromThePostgresUserWhenNoDatabaseIsSet(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_USER=app")
	if !strings.Contains(s, `'CREATE DATABASE "app" OWNER "app"'`) || !strings.Contains(s, `'CREATE ROLE "app" LOGIN SUPERUSER'`) {
		t.Errorf("%s", s)
	}
}

func TestPostUpScriptQuotesOddNamesForTheShellAndForSQL(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), `POSTGRES_DB=a'b"c`, `POSTGRES_USER=o'neil`)
	for _, want := range []string{
		`'SELECT 1 FROM pg_database WHERE datname = '\''a'\'''\''b"c'\'''`,
		`'CREATE DATABASE "a'\''b""c" OWNER "o'\''neil"'`,
		`'SELECT 1 FROM pg_roles WHERE rolname = '\''o'\'''\''neil'\'''`,
		`'CREATE ROLE "o'\''neil" LOGIN SUPERUSER'`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the script lacks %s:\n%s", want, s)
		}
	}
	syntaxOK(t, s)
}

func TestPostUpScriptIsNotWrittenForANameWithControlCharacters(t *testing.T) {
	for _, env := range [][]string{{"POSTGRES_DB=a\nb"}, {"POSTGRES_DB=a\tb"}, {"POSTGRES_DB=shop", "POSTGRES_USER=x\ry"}} {
		u := postUpUse(t, "francescolavra", "postgres", "16.2")
		obs := hubWholeObs()
		obs.Env = env
		items := ApplyHubWhole(u, obs)
		if s := PostUpScript(u, obs); s != "" {
			t.Errorf("%q: a script was written:\n%s", env, s)
		}
		e := hubFind(items, "hub entrypoint")
		if len(e) != 1 || !strings.Contains(e[0].Message, "No script (post-up-db.sh) was written") {
			t.Errorf("%q: the report does not say that no script was written: %+v", env, e)
		}
	}
}

func TestPostUpScriptIsNotWrittenWhenThereIsNothingToCreate(t *testing.T) {
	for _, env := range [][]string{nil, {"POSTGRES_DB=postgres"}, {"POSTGRES_USER=postgres"}} {
		if s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), env...); s != "" {
			t.Errorf("%q: %s", env, s)
		}
	}
}

func TestPostUpScriptIsNotWrittenWhenTheRowHasNoTemplateOrNoNote(t *testing.T) {
	u := postUpUse(t, "francescolavra", "postgres", "16.2")
	u.PostUpScript = ""
	if s := postUpFor(t, u, "POSTGRES_DB=shop"); s != "" {
		t.Errorf("no template: %s", s)
	}
	u = postUpUse(t, "francescolavra", "postgres", "16.2")
	u.EntrypointNote = ""
	if s := postUpFor(t, u, "POSTGRES_DB=shop"); s != "" {
		t.Errorf("no note: %s", s)
	}
}

func TestPostUpScriptNeverWritesTheValueOfASecretName(t *testing.T) {
	u := postUpUse(t, "francescolavra", "postgres", "16.2")
	u.PostUpScript = "#!/bin/sh\necho {POSTGRES_PASSWORD} {DB_TOKEN:sh} {POSTGRES_PASSWORD,POSTGRES_DB}\nensure 'x' {POSTGRES_DB:lit} y\n"
	s := postUpFor(t, u, "POSTGRES_DB=shop", "POSTGRES_PASSWORD=hunter2", "DB_TOKEN=tok3n")
	if strings.Contains(s, "hunter2") || strings.Contains(s, "tok3n") || strings.Count(s, notShown) != 3 {
		t.Errorf("%s", s)
	}
}

func TestEntrypointItemSaysTheScriptIsRunByTheUserAndNotByOps(t *testing.T) {
	u := postUpUse(t, "francescolavra", "postgres", "16.2")
	obs := hubWholeObs()
	obs.Env = []string{"POSTGRES_DB=shop"}
	e := hubFind(ApplyHubWhole(u, obs), "hub entrypoint")
	if len(e) != 1 {
		t.Fatalf("%+v", e)
	}
	for _, want := range []string{"post-up-db.sh", "next to compose.yaml", "ops compose up does NOT run it", "run it yourself after ops compose up", "without a password", `CREATE DATABASE "shop"`} {
		if !strings.Contains(e[0].Message, want) {
			t.Errorf("the item lacks %q: %s", want, e[0].Message)
		}
	}
	// without a template the item is the one it was
	u.PostUpScript = ""
	e = hubFind(ApplyHubWhole(u, obs), "hub entrypoint")
	if len(e) != 1 || strings.Contains(e[0].Message, "post-up") {
		t.Errorf("%+v", e)
	}
}

func TestFillPlaceholdersQuotesForSQLLiteralAndForTheShell(t *testing.T) {
	if got := fill("{A:lit} {A:sh} {A:ident}", "A=x'y\"z"); got != `'\''x'\'''\''y"z'\'' 'x'\''y"z' "x'\''y""z"` {
		t.Errorf("%s", got)
	}
	if got := fill("{A:lit} {A:sh}", "A=a\nb"); strings.Count(got, "<a name with control characters, to be quoted by hand>") != 2 {
		t.Errorf("%s", got)
	}
	if got := fillText("a\n  b  c\n", nil, nil, false); got != "a\n  b  c\n" {
		t.Errorf("%q", got)
	}
}

// fakes writes programs that stand for psql, ops and sleep, and returns the
// directory that holds them and the log they write.
func fakes(t *testing.T) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "bin"), filepath.Join(dir, "log")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	psql := `#!/bin/sh
printf '%s\n' "psql $*" >> "$LOG"
if [ -n "${FAKE_FAIL-}" ]; then echo "connection refused" >&2; exit 2; fi
for a in "$@"; do
  case "$a" in
    "SELECT 1 FROM"*) if [ -n "${FAKE_FOUND-}" ]; then echo 1; fi ;;
  esac
done
exit 0
`
	ops := "#!/bin/sh\nprintf '%s\\n' \"ops $*\" >> \"$LOG\"\nif [ -n \"${FAKE_LOGS-}\" ]; then printf '%b\\n' \"$FAKE_LOGS\"; fi\n"
	sleep := "#!/bin/sh\nprintf '%s\\n' \"sleep $*\" >> \"$LOG\"\n"
	for _, p := range [][2]string{{"psql", psql}, {"ops", ops}, {"sleep", sleep}} {
		if err := os.WriteFile(filepath.Join(bin, p[0]), []byte(p[1]), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin, log
}

// runScript runs the script with the fake programs first in the path.
func runScript(t *testing.T, script string, path string, env []string, args ...string) (string, string, error) {
	t.Helper()
	sh := needSh(t)
	bin, log := fakes(t)
	f := filepath.Join(t.TempDir(), "post-up-db.sh")
	if err := os.WriteFile(f, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if path == "" {
		path = bin + ":/usr/bin:/bin"
	}
	cmd := exec.Command(sh, append([]string{f}, args...)...)
	cmd.Env = append([]string{"PATH=" + path, "LOG=" + log}, env...)
	out, err := cmd.CombinedOutput()
	logged, _ := os.ReadFile(log)
	return string(out), string(logged), err
}

func TestPostUpScriptRunsTheCreateStatementsOnceAndOnlyWhenTheCheckFindsNothing(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), `POSTGRES_DB=a'b"c`, "POSTGRES_USER=app")
	out, log, err := runScript(t, s, "", nil, "10.0.0.7")
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, out, log)
	}
	for _, want := range []string{
		"psql -X -h 10.0.0.7 -U postgres -d template1 -w -tA -v ON_ERROR_STOP=1 -c SELECT 1 FROM pg_roles WHERE rolname = 'app'",
		`-c CREATE ROLE "app" LOGIN SUPERUSER`,
		`-c SELECT 1 FROM pg_database WHERE datname = 'a''b"c'`,
		`-c CREATE DATABASE "a'b""c" OWNER "app"`,
	} {
		if !strings.Contains(log, want+"\n") && !strings.Contains(log, want) {
			t.Errorf("psql was not called with %q:\n%s", want, log)
		}
	}
	// the already existing things are not created again
	out, log, err = runScript(t, s, "", []string{"FAKE_FOUND=1"}, "10.0.0.7")
	if err != nil || strings.Contains(log, "CREATE") || !strings.Contains(out, "exists already") {
		t.Errorf("%v\nout: %s\nlog: %s", err, out, log)
	}
}

func TestPostUpScriptTakesTheLastAddressOfTheConsoleWhenNoneIsGiven(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop")
	out, log, err := runScript(t, s, "", []string{"FAKE_LOGS=boot\\nassigned 192.168.33.5\\nassigned 192.168.33.9\\nup"})
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, out, log)
	}
	if !strings.Contains(log, "ops instance logs db\n") || !strings.Contains(log, "psql -X -h 192.168.33.9 ") || strings.Contains(log, "192.168.33.5") {
		t.Errorf("%s", log)
	}
	// the variable DB_HOST comes before the console
	_, log, err = runScript(t, s, "", []string{"FAKE_LOGS=assigned 192.168.33.9", "DB_HOST=10.1.1.1"})
	if err != nil || !strings.Contains(log, "psql -X -h 10.1.1.1 ") || strings.Contains(log, "ops instance") {
		t.Errorf("%v\n%s", err, log)
	}
}

func TestPostUpScriptFailsWithAMessageWhenThereIsNoAddress(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop")
	out, log, err := runScript(t, s, "", nil)
	if err == nil || !strings.Contains(out, "no address") || strings.Contains(log, "psql") {
		t.Errorf("%v\n%s\n%s", err, out, log)
	}
}

func TestPostUpScriptSaysWhenPsqlIsMissing(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop")
	empty := t.TempDir()
	out, _, err := runScript(t, s, empty, nil, "10.0.0.7")
	if err == nil || !strings.Contains(out, "psql is not installed") {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestPostUpScriptTries12TimesAndThenFails(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop")
	out, log, err := runScript(t, s, "", []string{"FAKE_FAIL=1"}, "10.0.0.7")
	if err == nil || !strings.Contains(out, "did not answer after 12 tries") {
		t.Errorf("%v\n%s", err, out)
	}
	if n := strings.Count(log, "-c SELECT 1\n"); n != 13 {
		t.Errorf("psql asked %d times, want 12 and one more to show the answer:\n%s", n, log)
	}
	if n := strings.Count(log, "sleep 5\n"); n != 11 {
		t.Errorf("%d sleeps of 5 seconds, want 11:\n%s", n, log)
	}
	if strings.Contains(log, "CREATE") {
		t.Errorf("%s", log)
	}
}

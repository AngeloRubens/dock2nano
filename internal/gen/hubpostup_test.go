package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/hub"
	"github.com/AngeloRubens/dock2nano/internal/plan"
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
		"#!/bin/sh\n", "set -eu\n", "-U 'postgres' -d template1 -w", "[ \"$i\" -lt \"$tries\" ]", "sleep 5",
		"\n  host=$1\n", "command -v psql", "ops instance logs \"$instance\"", "instance='db'", "PGPASSFILE=/dev/null", "unset PGPASSWORD", "host=${PGHOST:-}", "tries=${POSTUP_TRIES:-24}", "PGCONNECT_TIMEOUT=5", "ops instance list", plan.PostUpMarker,
		`ensure 'database' 'SELECT 1 FROM pg_database WHERE datname = '\''shop'\''' 'CREATE DATABASE "shop"'` + "\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the script lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "\nensure 'role'") || strings.Contains(s, `OWNER "`) {
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

func TestPostUpScriptCreatesTheRoleFirstWhenItIsNotTheSuperuserAndNeverWritesThePasswordValue(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop", "POSTGRES_USER=app", "POSTGRES_PASSWORD=hunter2", "POSTGRES_INITDB_ARGS=--auth=md5")
	role := strings.Index(s, `ensure 'role' 'SELECT 1 FROM pg_roles WHERE rolname = '\''app'\''' 'CREATE ROLE "app" LOGIN SUPERUSER'`)
	db := strings.Index(s, `ensure 'database' 'SELECT 1 FROM pg_database WHERE datname = '\''shop'\''' 'CREATE DATABASE "shop" OWNER "app"'`)
	if role < 0 || db < 0 || role > db {
		t.Errorf("role at %d, database at %d:\n%s", role, db, s)
	}
	for _, bad := range []string{"hunter2", "PASSWORD='", "PASSWORD=h"} {
		if strings.Contains(s, bad) {
			t.Errorf("the script holds %q:\n%s", bad, s)
		}
	}
	syntaxOK(t, s)
}

func TestPostUpScriptDoesNotMakeTheRoleWhenItIsTheSuperuser(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop", "POSTGRES_USER=postgres")
	if strings.Contains(s, "\nensure 'role'") || !strings.Contains(s, `'CREATE DATABASE "shop"'`) {
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
		if len(e) != 1 || !strings.Contains(e[0].Message, "No script is written for it: a name holds a control") {
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
	obs := hubWholeObs()
	obs.Env = []string{"POSTGRES_DB=shop", "POSTGRES_PASSWORD=hunter2", "DB_TOKEN=tok3n"}
	env, fixed := noteInputs(u, obs)
	s := fillText(u.PostUpScript, env, fixed, false)
	// a script that holds a secret placeholder is not written at all
	if PostUpScript(u, obs) != "" {
		t.Error("a script with a secret placeholder was written")
	}
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
	for _, want := range []string{"post-up-<the pkg name", "is written next to compose.yaml", "creates the database, each only if it is absent", "2 to 4 minutes at most", "run 44908630495", "sudo --preserve-env", "may exit", "timing was not measured", "start the database first", "ops compose up does NOT run it", "run it yourself after ops compose up", "sets no password", `CREATE DATABASE "shop"`} {
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
case " $* " in *" -c "*) ;; *) sed 's/^/stdin> /' >> "$LOG" ;; esac
printf '%s\n' "env PGPASSFILE=${PGPASSFILE-} PGPASSWORD=${PGPASSWORD-unset}" >> "$LOG"
if [ -n "${FAKE_FAIL-}" ]; then echo "connection refused" >&2; exit 2; fi
for a in "$@"; do
  case "$a" in
    "SELECT 1 FROM"*) if [ -n "${FAKE_FOUND-}" ] || [ -e "$LOG.made" ]; then echo 1; fi ;;
    CREATE*) if [ -n "${FAKE_CREATE_FAILS-}" ]; then : > "$LOG.made"; echo "already exists" >&2; exit 1; fi ;;
  esac
done
exit 0
`
	ops := "#!/bin/sh\nprintf '%s\\n' \"ops $*\" >> \"$LOG\"\nif [ \"$2\" = list ]; then printf '%b\\n' \"${FAKE_LIST-}\"; exit 0; fi\nif [ -n \"${FAKE_LOGS-}\" ]; then printf '%b\\n' \"$FAKE_LOGS\"; fi\n"
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
	// the variable PGHOST comes before the console
	_, log, err = runScript(t, s, "", []string{"FAKE_LOGS=assigned 192.168.33.9", "PGHOST=10.1.1.1"})
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
	if err == nil || !strings.Contains(out, "psql is not installed") || !strings.Contains(out, "apt install postgresql-client") {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestPostUpScriptTries24TimesAndThenFails(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop")
	out, log, err := runScript(t, s, "", []string{"FAKE_FAIL=1"}, "10.0.0.7")
	if err == nil || !strings.Contains(out, "did not answer after 24 tries") {
		t.Errorf("%v\n%s", err, out)
	}
	if n := strings.Count(log, "-c SELECT 1\n"); n != 25 {
		t.Errorf("psql asked %d times, want 24 and one more to show the answer:\n%s", n, log)
	}
	if n := strings.Count(log, "sleep 5\n"); n != 23 {
		t.Errorf("%d sleeps of 5 seconds, want 23:\n%s", n, log)
	}
	// POSTUP_TRIES changes the number of tries; a value that is not a number is not used
	_, log, _ = runScript(t, s, "", []string{"FAKE_FAIL=1", "POSTUP_TRIES=3"}, "10.0.0.7")
	if n := strings.Count(log, "-c SELECT 1\n"); n != 4 {
		t.Errorf("POSTUP_TRIES=3: psql asked %d times, want 4:\n%s", n, log)
	}
	_, log, _ = runScript(t, s, "", []string{"FAKE_FAIL=1", "POSTUP_TRIES=x"}, "10.0.0.7")
	if n := strings.Count(log, "-c SELECT 1\n"); n != 25 {
		t.Errorf("POSTUP_TRIES=x: psql asked %d times, want 25:\n%s", n, log)
	}
	if strings.Contains(log, "CREATE") {
		t.Errorf("%s", log)
	}
}

func TestPostUpScriptTakesAnObjectThatAppearedDuringTheCreateAsDone(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop")
	out, log, err := runScript(t, s, "", []string{"FAKE_CREATE_FAILS=1"}, "10.0.0.7")
	if err != nil || !strings.Contains(out, "exists now") {
		t.Errorf("%v\n%s\n%s", err, out, log)
	}
}

func TestPostUpScriptFailsWhenTheCreateFailsAndTheObjectIsNotThere(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop")
	// the fake that fails the create and never shows the object
	sh := needSh(t)
	bin, log := fakes(t)
	if err := os.WriteFile(filepath.Join(bin, "psql"), []byte("#!/bin/sh\ncase \"$*\" in *CREATE*) exit 1;; esac\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "p.sh")
	os.WriteFile(f, []byte(s), 0o755)
	cmd := exec.Command(sh, f, "10.0.0.7")
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "LOG=" + log}
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "could not create the database") {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestPostUpScriptIgnoresThePasswordOfTheEnvironmentAndTheFileOfPasswords(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop")
	out, log, err := runScript(t, s, "", []string{"PGPASSWORD=hunter2", "PGPASSFILE=/home/me/.pgpass"}, "10.0.0.7")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(log, "env PGPASSFILE=/dev/null PGPASSWORD=unset") || strings.Contains(log, "hunter2") || strings.Contains(log, ".pgpass") {
		t.Errorf("%s", log)
	}
}

func TestPostUpScriptUsesTheNameOfTheInstanceAndNotTheComposeName(t *testing.T) {
	u := postUpUse(t, "francescolavra", "postgres", "16.2")
	u.Service, u.Instance = "my_db", "my-db"
	s := postUpFor(t, u, "POSTGRES_DB=shop")
	if !strings.Contains(s, "instance='my-db'") || strings.Contains(s, "my_db") || !strings.Contains(s, "# Usage: post-up-my-db.sh ") {
		t.Errorf("%s", s)
	}
}

func TestPostUpScriptIsNotWrittenForANameThatIsTooLongOrNotAName(t *testing.T) {
	for _, c := range []struct{ env, say string }{
		{"POSTGRES_DB=" + strings.Repeat("a", 64), "longer than 63 bytes"},
		{"POSTGRES_USER=" + strings.Repeat("é", 32), "longer than 63 bytes"},
		{"POSTGRES_DB=a\u202eb", "control or format character"},
		{"POSTGRES_DB=a\u200bb", "control or format character"},
		{"POSTGRES_DB=a\xffb", "not valid UTF-8"},
	} {
		u := postUpUse(t, "francescolavra", "postgres", "16.2")
		obs := hubWholeObs()
		obs.Env = []string{c.env}
		items := ApplyHubWhole(u, obs)
		if s := PostUpScript(u, obs); s != "" {
			t.Errorf("%q: a script was made:\n%s", c.env, s)
		}
		e := hubFind(items, "hub entrypoint")
		if len(e) != 1 || !strings.Contains(e[0].Message, "No script is written for it: ") || !strings.Contains(e[0].Message, c.say) {
			t.Errorf("%q: %+v", c.env, e)
		}
	}
	// 63 bytes is the longest name that is kept
	if s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB="+strings.Repeat("a", 63)); s == "" {
		t.Error("a name of 63 bytes was refused")
	}
}

func TestPostUpScriptReadsAnEmptyValueOfTheComposeFileAsUnset(t *testing.T) {
	u := postUpUse(t, "francescolavra", "postgres", "16.2")
	u.ImageEnv = []string{"POSTGRES_DB=foo"}
	u.ComposeEnv = []string{"POSTGRES_DB=", "POSTGRES_USER=app"}
	s := postUpFor(t, u)
	if !strings.Contains(s, `'CREATE DATABASE "app" OWNER "app"'`) || strings.Contains(s, "foo") {
		t.Errorf("%s", s)
	}
}

func TestPostUpScriptIsNotWrittenWhenTheEnvironmentIsNotCertain(t *testing.T) {
	u := postUpUse(t, "francescolavra", "postgres", "16.2")
	u.EnvUncertain = true
	obs := hubWholeObs()
	obs.Env = []string{"POSTGRES_DB=shop"}
	items := ApplyHubWhole(u, obs)
	if s := PostUpScript(u, obs); s != "" {
		t.Errorf("a script was made:\n%s", s)
	}
	if e := hubFind(items, "hub entrypoint"); len(e) != 1 || !strings.Contains(e[0].Message, "No script is written for it: the environment of the service could not be read completely") {
		t.Errorf("%+v", e)
	}
}

func TestFillTextKeepsLiteralBraces(t *testing.T) {
	if got := fillText("a ${{X:-}} {{b}} {A}", []string{"A=1"}, nil, false); got != "a ${X:-} {b} 1" {
		t.Errorf("%q", got)
	}
}

// pwLines are the lines of the log of the fakes that psql was called with, one
// per call: the arguments only.
func argvLines(log string) []string {
	var out []string
	for _, l := range strings.Split(log, "\n") {
		if strings.HasPrefix(l, "psql ") {
			out = append(out, l)
		}
	}
	return out
}

func pwFile(t *testing.T, content string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "password.txt")
	if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestPostUpScriptReadsThePasswordFileAtRunTimeAndPassesItOnlyThroughTheEnvironmentAndStdin(t *testing.T) {
	const pw = "s3cr'et$x"
	f := pwFile(t, pw+"\n")
	u := postUpUse(t, "francescolavra", "postgres", "16.2")
	u.PasswordFile = f
	s := postUpFor(t, u, "POSTGRES_DB=shop", "POSTGRES_PASSWORD_FILE=/run/secrets/db_password")
	if !strings.Contains(s, "pwfile='"+f+"'") || strings.Contains(s, "s3cr") || strings.Contains(s, "POSTGRES_PASSWORD") {
		t.Errorf("the script does not name the file, or holds the value:\n%s", s)
	}
	syntaxOK(t, s)
	out, log, err := runScript(t, s, "", nil, "10.0.0.7")
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, out, log)
	}
	if !strings.Contains(log, "PGPASSWORD="+pw+"\n") {
		t.Errorf("psql did not get the password in PGPASSWORD:\n%s", log)
	}
	if !strings.Contains(log, `stdin> ALTER ROLE "postgres" PASSWORD 's3cr''et$x';`) {
		t.Errorf("the statement did not reach psql on standard input:\n%s", log)
	}
	for _, l := range argvLines(log) {
		if strings.Contains(l, "s3cr") || strings.Contains(l, "PASSWORD") {
			t.Errorf("the password or its statement is in an argument: %s", l)
		}
	}
	if strings.Contains(out, "s3cr") {
		t.Errorf("the password was printed:\n%s", out)
	}
}

func TestPostUpScriptTakesThePasswordFromTheShellWhenTheComposeFileGivesItInTheEnvironment(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop", "POSTGRES_USER=app", "POSTGRES_PASSWORD=hunter2")
	if strings.Contains(s, "hunter2") || !strings.Contains(s, "pw=${POSTGRES_PASSWORD:-}") {
		t.Errorf("%s", s)
	}
	out, log, err := runScript(t, s, "", []string{"POSTGRES_PASSWORD=from-the-shell"}, "10.0.0.7")
	if err != nil || !strings.Contains(log, "PGPASSWORD=from-the-shell\n") || !strings.Contains(log, `stdin> ALTER ROLE "app" PASSWORD 'from-the-shell';`) {
		t.Errorf("%v\n%s\n%s", err, out, log)
	}
	for _, l := range argvLines(log) {
		if strings.Contains(l, "from-the-shell") {
			t.Errorf("the password is in an argument: %s", l)
		}
	}
	// the role is made before its password is set
	if strings.Index(log, "CREATE ROLE") > strings.Index(log, "ALTER ROLE") {
		t.Errorf("the password is set before the role exists:\n%s", log)
	}
	// without the variable no password is set, and it says so
	out, log, err = runScript(t, s, "", nil, "10.0.0.7")
	if err != nil || strings.Contains(log, "ALTER ROLE") || !strings.Contains(out, "no password is set") {
		t.Errorf("%v\n%s\n%s", err, out, log)
	}
}

func TestPostUpScriptSetsNoPasswordWhenTheComposeFileGivesNone(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop")
	if strings.Contains(s, "ALTER ROLE") || strings.Contains(s, "pw=") || strings.Contains(s, "export PGPASSWORD") {
		t.Errorf("%s", s)
	}
}

func TestPostUpScriptFailsWhenTheFileOfThePasswordCannotBeRead(t *testing.T) {
	u := postUpUse(t, "francescolavra", "postgres", "16.2")
	u.PasswordFile = filepath.Join(t.TempDir(), "missing.txt")
	s := postUpFor(t, u, "POSTGRES_DB=shop", "POSTGRES_PASSWORD_FILE=/run/secrets/x")
	out, log, err := runScript(t, s, "", nil, "10.0.0.7")
	if err == nil || !strings.Contains(out, "the file of the password cannot be read") || strings.Contains(log, "psql") {
		t.Errorf("%v\n%s\n%s", err, out, log)
	}
}

func TestPostUpScriptWithAPasswordFileThatIsNotKnownSetsNoPasswordAndTheReportSaysWhy(t *testing.T) {
	u := postUpUse(t, "francescolavra", "postgres", "16.2")
	obs := hubWholeObs()
	obs.Env = []string{"POSTGRES_DB=shop", "POSTGRES_PASSWORD_FILE=/run/secrets/x"}
	items := ApplyHubWhole(u, obs)
	s := PostUpScript(u, obs)
	if s == "" || strings.Contains(s, "ALTER ROLE") {
		t.Errorf("%s", s)
	}
	e := hubFind(items, "hub entrypoint")
	if len(e) != 1 || !strings.Contains(e[0].Message, "It sets no password: POSTGRES_PASSWORD_FILE is /run/secrets/x, which is not the target of a file secret") {
		t.Errorf("%+v", e)
	}
}

func TestEntrypointItemSaysWhereThePasswordComesFromAndNeverItsValue(t *testing.T) {
	f := pwFile(t, "topsecretvalue")
	u := postUpUse(t, "francescolavra", "postgres", "16.2")
	u.PasswordFile = f
	obs := hubWholeObs()
	obs.Env = []string{"POSTGRES_DB=shop", "POSTGRES_PASSWORD_FILE=/run/secrets/x"}
	e := hubFind(ApplyHubWhole(u, obs), "hub entrypoint")
	if len(e) != 1 {
		t.Fatalf("%+v", e)
	}
	for _, want := range []string{"It sets the password of the role (ALTER ROLE) from the file " + f, "read when the script runs", "only through PGPASSWORD and standard input, never as an argument"} {
		if !strings.Contains(e[0].Message, want) {
			t.Errorf("the item lacks %q: %s", want, e[0].Message)
		}
	}
	if strings.Contains(e[0].Message, "topsecretvalue") {
		t.Errorf("the value is in the report: %s", e[0].Message)
	}
	// from the environment of the compose file
	obs.Env = []string{"POSTGRES_DB=shop", "POSTGRES_PASSWORD=hunter2"}
	u.PasswordFile = ""
	e = hubFind(ApplyHubWhole(u, obs), "hub entrypoint")
	if len(e) != 1 || !strings.Contains(e[0].Message, "from the variable POSTGRES_PASSWORD of the shell that runs it") || strings.Contains(e[0].Message, "hunter2") {
		t.Errorf("%+v", e)
	}
}

func TestPostUpScriptTakesTheAddressFromOpsInstanceListBeforeTheConsole(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop")
	table := "+-------+------+\\n| \x1b[1;36m PID \x1b[0m | \x1b[1;36m NAME  \x1b[0m | \x1b[1;36m IMAGE \x1b[0m | \x1b[1;36m STATUS \x1b[0m | \x1b[1;36m CREATED \x1b[0m | \x1b[1;36m PRIVATE IPS  \x1b[0m | PORT |\\n" +
		"| 21978 | dns     | /h/dns | Running | 11 seconds ago | 192.168.33.204 |      |\\n" +
		"| 22027 | db      | /h/db  | Running | 7 seconds ago  | 192.168.33.64  |      |\\n" +
		"| 22072 | dbx     | /h/dbx | Running | 3 seconds ago  | 192.168.33.49  | 8000 |\\n"
	out, log, err := runScript(t, s, "", []string{"FAKE_LIST=" + table, "FAKE_LOGS=assigned 192.168.33.9"})
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, out, log)
	}
	if !strings.Contains(log, "psql -X -h 192.168.33.64 ") || strings.Contains(log, "ops instance logs") {
		t.Errorf("the address of the row db was not used:\n%s", log)
	}
}

func TestPostUpScriptAcceptsTheNamesOfTheLettersDigitsUnderscoreAndHyphen(t *testing.T) {
	for _, name := range []string{"shop", "my-db_1", "-lead", "9lead", "UPPER_x", strings.Repeat("a", 63)} {
		s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB="+name, "POSTGRES_USER="+name+"u")
		if len(name+"u") > 63 {
			if s != "" {
				t.Errorf("%s: a name of %d bytes was accepted", name, len(name)+1)
			}
			continue
		}
		if !strings.Contains(s, `'CREATE DATABASE "`+name+`" OWNER "`+name+`u"'`) {
			t.Errorf("%s: %s", name, s)
		}
		syntaxOK(t, s)
		out, log, err := runScript(t, s, "", nil, "10.0.0.7")
		if err != nil || !strings.Contains(log, `-c CREATE DATABASE "`+name+`" OWNER "`+name+`u"`) {
			t.Errorf("%s: %v\n%s\n%s", name, err, out, log)
		}
	}
}

func TestPostUpScriptDoesNotNameTheToolAndCarriesTheMarker(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop")
	if !strings.Contains(s, plan.PostUpMarker) || strings.Contains(s, "dock2nano") {
		t.Errorf("%s", s)
	}
}

func TestPostUpScriptUnsetsTheVariablesOfPsqlThatWouldChangeTheConnectionAndExplainsThePassword(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop", "POSTGRES_PASSWORD=x")
	for _, want := range []string{"unset PGPASSWORD PGPORT PGSSLMODE PGOPTIONS PGSERVICE PGSERVICEFILE PGDATABASE PGUSER", "unset PGHOSTADDR PGREQUIREAUTH PGCHANNELBINDING PGGSSENCMODE PGTARGETSESSIONATTRS", "unset PGSSLCERT PGSSLKEY PGSSLROOTCERT", "standard_conforming_strings", "printf is a builtin", "run 45405259016", "sudo --preserve-env=POSTGRES_PASSWORD"} {
		if !strings.Contains(s, want) {
			t.Errorf("the script lacks %q:\n%s", want, s)
		}
	}
	out, log, err := runScript(t, s, "", []string{"PGPORT=1", "PGUSER=evil", "PGDATABASE=x", "PGOPTIONS=-c x", "PGHOSTADDR=10.9.9.9"}, "10.0.0.7")
	if err != nil || !strings.Contains(log, "-U postgres -d template1") || !strings.Contains(log, "-h 10.0.0.7 ") {
		t.Errorf("%v\n%s\n%s", err, out, log)
	}
}

func TestReportAndFileAgreeWhenTheServiceNameHasAControlCharacter(t *testing.T) {
	u := postUpUse(t, "francescolavra", "postgres", "16.2")
	u.Service = "a\nb"
	obs := hubWholeObs()
	obs.Env = []string{"POSTGRES_DB=shop"}
	items := ApplyHubWhole(u, obs)
	e := hubFind(items, "hub entrypoint")
	if len(e) != 1 || strings.Contains(e[0].Message, "No script is written") {
		t.Errorf("the report refuses a script that the name of the package allows: %+v", e)
	}
	u.Instance = "a-b"
	if PostUpScript(u, obs) == "" {
		t.Error("no script for the instance a-b")
	}
}

func TestEntrypointItemWritesAPasswordNameWithALineBreakAsAnEscape(t *testing.T) {
	u := postUpUse(t, "francescolavra", "postgres", "16.2")
	obs := hubWholeObs()
	obs.Env = []string{"POSTGRES_DB=shop", "POSTGRES_PASSWORD_FILE=/run/secrets/a\nb"}
	e := hubFind(ApplyHubWhole(u, obs), "hub entrypoint")
	if len(e) != 1 || !strings.Contains(e[0].Message, `POSTGRES_PASSWORD_FILE is /run/secrets/a\nb,`) || strings.Contains(e[0].Message, "secrets/a\nb") {
		t.Errorf("%+v", e)
	}
}

func TestPostUpScriptTheCommentOfThePasswordSaysTheFileOnlyWhenThereIsAFile(t *testing.T) {
	s := postUpFor(t, postUpUse(t, "francescolavra", "postgres", "16.2"), "POSTGRES_DB=shop", "POSTGRES_PASSWORD=x")
	if strings.Contains(s, "read from the file") {
		t.Errorf("the comment names a file where there is none:\n%s", s)
	}
	u := postUpUse(t, "francescolavra", "postgres", "16.2")
	u.PasswordFile = "/srv/p.txt"
	s = postUpFor(t, u, "POSTGRES_DB=shop")
	if !strings.Contains(s, "# The password is read from the file '/srv/p.txt' when the script runs.") {
		t.Errorf("%s", s)
	}
}

func TestPostUpScriptIsRefusedForAPasswordPathWithAControlCharacterWithItsOwnReason(t *testing.T) {
	u := postUpUse(t, "francescolavra", "postgres", "16.2")
	u.PasswordFile = "/srv/a\nb/password.txt"
	obs := hubWholeObs()
	obs.Env = []string{"POSTGRES_DB=shop", "POSTGRES_PASSWORD_FILE=/run/secrets/x"}
	items := ApplyHubWhole(u, obs)
	if s := PostUpScript(u, obs); s != "" {
		t.Errorf("a script was made:\n%s", s)
	}
	e := hubFind(items, "hub entrypoint")
	if len(e) != 1 || !strings.Contains(e[0].Message, "No script is written for it: the path of the password file holds a control character") {
		t.Errorf("%+v", e)
	}
}

func TestEntrypointItemSaysWhatWasRunForThePasswordFromAVariable(t *testing.T) {
	u := postUpUse(t, "francescolavra", "postgres", "16.2")
	obs := hubWholeObs()
	obs.Env = []string{"POSTGRES_DB=shop", "POSTGRES_PASSWORD=hunter2"}
	e := hubFind(ApplyHubWhole(u, obs), "hub entrypoint")
	if len(e) != 1 || !strings.Contains(e[0].Message, "run 45409545710") || !strings.Contains(e[0].Message, "only the password from a file was run end to end, in run 45405259016") {
		t.Errorf("%+v", e)
	}
}

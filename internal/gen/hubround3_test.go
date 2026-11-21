package gen

import (
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/compose"
	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/hub"
	"github.com/AngeloRubens/dock2nano/internal/observe"
	"github.com/AngeloRubens/dock2nano/internal/plan"
)

// shippedUse is the use of a package with the note and the superuser of the
// row that ships in the binary.
func shippedUse(t *testing.T, ns, name, version string) HubUse {
	t.Helper()
	rows, err := hub.DefaultRequired()
	if err != nil {
		t.Fatal(err)
	}
	u := hubWholeUse(hub.Exact, version, version)
	u.Package.Namespace, u.Package.Name = ns, name
	u.EntrypointNote = rows[0].EntrypointNote
	u.Superuser = rows[0].Superusers[ns+"/"+name]
	return u
}

func entrypointText(t *testing.T, u HubUse, env ...string) string {
	t.Helper()
	obs := hubWholeObs()
	obs.Env = env
	e := hubFind(ApplyHubWhole(u, obs), "hub entrypoint")
	if len(e) != 1 {
		t.Fatalf("hub entrypoint items: %+v", e)
	}
	if strings.Contains(e[0].Message, "{") || strings.Contains(e[0].Message, "[[") {
		t.Errorf("a placeholder or a section was left in the note: %q", e[0].Message)
	}
	return e[0].Message
}

func TestEntrypointNoteQuotesTheDatabaseAndNamesTheSuperuser(t *testing.T) {
	u := shippedUse(t, "francescolavra", "postgres", "16.2")
	got := entrypointText(t, u, "POSTGRES_DB=shop", "POSTGRES_USER=app")
	for _, want := range []string{
		`-U postgres -d template1 -c 'CREATE DATABASE "shop"'`,
		`-U postgres -d template1 -c 'CREATE ROLE "app" LOGIN'`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the note does not say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "If postgres is not postgres") || strings.Contains(got, "No database has to be created") {
		t.Errorf("%s", got)
	}
	// the name is quoted as the entrypoint of the image quotes it
	if got := entrypointText(t, u, `POSTGRES_DB=a"b`); !strings.Contains(got, `'CREATE DATABASE "a""b"'`) {
		t.Errorf("%s", got)
	}
	// a single quote must not end the quoting of the shell
	if got := entrypointText(t, u, `POSTGRES_DB=x'y`); !strings.Contains(got, `'CREATE DATABASE "x'\''y"'`) {
		t.Errorf("%s", got)
	}
	// the database is POSTGRES_DB, else POSTGRES_USER
	got = entrypointText(t, u, "POSTGRES_USER=app")
	if !strings.Contains(got, `'CREATE DATABASE "app"'`) {
		t.Errorf("%s", got)
	}
	got = entrypointText(t, u, "POSTGRES_DB=shop", "POSTGRES_USER=app")
	if strings.Contains(got, `CREATE DATABASE "app"`) {
		t.Errorf("POSTGRES_DB wins over POSTGRES_USER:\n%s", got)
	}
}

func TestEntrypointNoteSaysWhenNoDatabaseHasToBeCreated(t *testing.T) {
	u := shippedUse(t, "francescolavra", "postgres", "16.2")
	for _, env := range [][]string{nil, {"POSTGRES_USER=postgres"}, {"POSTGRES_DB=postgres"}} {
		got := entrypointText(t, u, env...)
		if !strings.Contains(got, "No database has to be created") || strings.Contains(got, "CREATE DATABASE \"") {
			t.Errorf("env %v:\n%s", env, got)
		}
		if strings.Contains(got, "CREATE ROLE") {
			t.Errorf("env %v: the role is the superuser, nothing to create:\n%s", env, got)
		}
	}
}

func TestEntrypointNoteUsesTheSuperuserOfPostgres11(t *testing.T) {
	u := shippedUse(t, "eyberg", "postgresql", "11.3.0")
	got := entrypointText(t, u, "POSTGRES_DB=shop", "POSTGRES_USER=app")
	for _, want := range []string{`-U eyberg -d template1 -c 'CREATE DATABASE "shop"'`, `-U eyberg -d template1 -c 'CREATE ROLE "app" LOGIN'`} {
		if !strings.Contains(got, want) {
			t.Errorf("the note does not say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "-U postgres") {
		t.Errorf("the role postgres does not exist in this package:\n%s", got)
	}
	// the user of the image is the superuser of the package: no role to create
	if got := entrypointText(t, u, "POSTGRES_DB=shop", "POSTGRES_USER=eyberg"); strings.Contains(got, "CREATE ROLE") {
		t.Errorf("%s", got)
	}
	// postgres is not the superuser here, so it is a role to create
	if got := entrypointText(t, u, "POSTGRES_DB=shop", "POSTGRES_USER=postgres"); !strings.Contains(got, `'CREATE ROLE "postgres" LOGIN'`) {
		t.Errorf("%s", got)
	}
}

func fill(note string, env ...string) string { return fillPlaceholders(note, env, nil) }

func TestFillPlaceholdersNeverWritesASecret(t *testing.T) {
	for _, name := range []string{"POSTGRES_PASSWORD", "API_KEY", "GITHUB_TOKEN", "DB_SECRET", "POSTGRES_PASSWORD_FILE", "db_password"} {
		env := name + "=hunter2"
		if got := fill("x {"+name+"} y", env); got != "x <not shown> y" {
			t.Errorf("%s: %q", name, got)
		}
		if got := fill("x {POSTGRES_DB,"+name+"} y", env, "POSTGRES_DB=shop"); got != "x <not shown> y" {
			t.Errorf("chain with %s: %q", name, got)
		}
		if got := fill("x {"+name+"|hunter3} y", env); got != "x <not shown> y" {
			t.Errorf("default with %s: %q", name, got)
		}
		if got := fill("[[if "+name+"]]hunter2[[end]]", env); got != "" {
			t.Errorf("section with %s: %q", name, got)
		}
	}
}

func TestFillPlaceholdersWritesNoLineBreakOrControlCharacter(t *testing.T) {
	got := fill("db {POSTGRES_DB}.", "POSTGRES_DB=a\nhub files: fake\r\x1b[0m\u2028")
	if strings.ContainsAny(got, "\n\r\x1b\u2028") {
		t.Errorf("a value made a line break or a control character: %q", got)
	}
	if got != `db a\nhub files: fake\r\u001b[0m\u2028.` {
		t.Errorf("%q", got)
	}
}

func TestFillPlaceholdersFillsAfterALiteralBrace(t *testing.T) {
	if got := fill("a } b {X} c {", "X=1"); got != "a } b 1 c {" {
		t.Errorf("%q", got)
	}
	if got := fill("{ not } {X}", "X=1"); got != "{ not } 1" {
		t.Errorf("%q", got)
	}
}

func TestEntrypointPlaceholdersReadTheImageEnvironmentAndTheObservedOneWins(t *testing.T) {
	u := hubWholeUse(hub.Exact, "16.2", "16.2")
	u.EntrypointNote = "db={POSTGRES_DB} user={POSTGRES_USER}"
	u.ImageEnv = []string{"POSTGRES_DB=fromimage", "POSTGRES_USER=fromimage"}
	obs := hubWholeObs()
	obs.Env = []string{"POSTGRES_USER=observed"}
	e := hubFind(ApplyHubWhole(u, obs), "hub entrypoint")
	if len(e) != 1 || !strings.HasSuffix(e[0].Message, "db=fromimage user=observed") {
		t.Errorf("%+v", e)
	}
}

func TestApplyHubWholeClearsWhatTheLaunchersSaidAndNamesTheRecipeKeysItDrops(t *testing.T) {
	u := hubWholeUse(hub.Exact, "16.2", "16.2")
	u.Recipe.Manifest.RunConfig.Ports = []string{"5432"}
	u.Recipe.Manifest.ManifestPassthrough = map[string]any{"cwd": "/x", "uid": 70}
	obs := hubWholeObs()
	obs.PassedOver, obs.NotRun, obs.ChildLaunchers = []string{"npm"}, []string{"id"}, []string{"sh"}
	items := ApplyHubWhole(u, obs)
	if obs.PassedOver != nil || obs.NotRun != nil || obs.ChildLaunchers != nil {
		t.Errorf("what the launchers of the image did was kept: %+v", obs)
	}
	if p := hubFind(items, "hub ports"); len(p) != 1 || !strings.Contains(p[0].Message, "5432") {
		t.Errorf("%+v", p)
	}
	m := hubFind(items, "hub manifest")
	if len(m) != 1 || !strings.Contains(m[0].Message, "cwd") || !strings.Contains(m[0].Message, "uid") || !strings.Contains(m[0].Message, "runs as root in /") || strings.Contains(m[0].Message, "comes from the observation") {
		t.Errorf("%+v", m)
	}
	if p := hubFind(ApplyHubWhole(hubWholeUse(hub.Exact, "16.2", "16.2"), hubWholeObs()), "hub ports"); len(p) != 0 {
		t.Errorf("a recipe without ports: %+v", p)
	}
}

func TestApplyHubWholeEnvironmentSpeaksOfThePackageAndNotOfTheImage(t *testing.T) {
	u := hubWholeUse(hub.Exact, "16.2", "16.2")
	u.Recipe.Manifest.Env = map[string]string{"LANG": "C", "PGDATA": "/db"}
	obs := hubWholeObs()
	obs.Env = []string{"LANG=en_US.UTF-8"}
	items := ApplyHubWhole(u, obs)
	text := hubText(items)
	if strings.Contains(text, "not in your image") || strings.Contains(text, "yours are kept") {
		t.Errorf("the report says what is not true for a package used whole:\n%s", text)
	}
	if !strings.Contains(text, "the package gets the values of your image") {
		t.Errorf("%s", text)
	}
	found := false
	for _, e := range obs.Env {
		found = found || e == "PGDATA=/db"
	}
	if !found {
		t.Errorf("a path of the layout of the package was not added: %v", obs.Env)
	}
}

func TestApplyHubWholeResultPointsToTheRuns(t *testing.T) {
	r := hubFind(ApplyHubWhole(hubWholeUse(hub.Exact, "16.2", "16.2"), hubWholeObs()), "hub result")
	if len(r) != 1 || r[0].Message != "not run by this conversion; see the runs in the hub entrypoint item" {
		t.Errorf("%+v", r)
	}
}

func TestApplyHubWholeDoesNotSayArgumentsWereDroppedWhenTheyAreThoseOfThePackage(t *testing.T) {
	obs := hubWholeObs()
	obs.Argv = []string{"postgres", "-D", "db"}
	if a := hubFind(ApplyHubWhole(hubWholeUse(hub.Exact, "16.2", "16.2"), obs), "hub arguments"); len(a) != 0 {
		t.Errorf("%+v", a)
	}
	obs = hubWholeObs()
	obs.Argv = []string{"postgres", "-D", "other"}
	if a := hubFind(ApplyHubWhole(hubWholeUse(hub.Exact, "16.2", "16.2"), obs), "hub arguments"); len(a) != 1 {
		t.Errorf("%+v", a)
	}
}

func wholeInput(s *compose.Service, img *docker.ImageConfig) Input {
	in := newInput(&fake{}, s)
	si := in.Services[s.Name]
	si.HubWhole = true
	si.Image = img
	in.Services[s.Name] = si
	return in
}

func TestHubWholeSaysWhoThePackageRunsAsAndWhatIsNotApplied(t *testing.T) {
	s := &compose.Service{Name: "db", User: "app", WorkingDir: "/srv"}
	p := mustGen(t, wholeInput(s, &docker.ImageConfig{User: "postgres", WorkingDir: "/var/lib/postgresql"}))
	got := itemsOf(p, "db", plan.Unsupported, "user")
	if len(got) != 1 {
		t.Fatalf("%+v", p.Report)
	}
	for _, want := range []string{"runs as root in /", "user app of the compose file", "the user postgres of the image", "the working directory /var/lib/postgresql of the image", "working_dir /srv of the compose file"} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("the item does not say %q: %s", want, got[0].Message)
		}
	}
	for _, it := range p.Report {
		if it.Service == "db" && (strings.Contains(it.Message, "could not be read") || strings.Contains(it.Message, "was observed in /")) {
			t.Errorf("a whole package is not observed: %+v", it)
		}
	}
	wantNone(t, p, "db", plan.Unsupported, "working_dir")
	wantNone(t, p, "db", plan.Approximated, "working_dir")
	// nothing to drop: the same fact is a note
	p = mustGen(t, wholeInput(&compose.Service{Name: "db"}, &docker.ImageConfig{}))
	want1(t, p, "db", plan.Note, "user", "runs as root in /")
}

func TestHubWholeAddsTheRestartNoteOfTheRowToTheRestartItem(t *testing.T) {
	s := &compose.Service{Name: "db", Restart: "always"}
	in := wholeInput(s, nil)
	si := in.Services["db"]
	si.HubRestartNote = "A restart of the same disk was seen failing"
	in.Services["db"] = si
	want1(t, mustGen(t, in), "db", plan.Approximated, "restart", "RebootOnExit: the instance restarts whenever the program exits, whatever the exit code. A restart of the same disk was seen failing")
	// a service that is not a package used whole does not get it
	in = newInput(&fake{}, &compose.Service{Name: "db", Restart: "always"})
	si = in.Services["db"]
	si.HubRestartNote = "A restart of the same disk was seen failing"
	in.Services["db"] = si
	wantNoText(t, mustGen(t, in), "db", plan.Approximated, "restart", "seen failing")
}

// Each of the notes of an observation that describes the files of the image is
// dropped for a package used whole. The samples are the texts the observation
// makes, one for each prefix; a prefix reworded in observe without its sample
// fails here, and one reworded in gen alone cannot happen, gen reads the list
// of observe.
func TestEveryPrefixOfAFileNoteIsDropped(t *testing.T) {
	samples := map[string]string{
		observe.NoteEntrypointMade:    "the entrypoint made 3 files before the program started (the state the image starts from, such as the data of a database); they are carried as they were at the end of the observation: /a, /b",
		observe.NoteCreatedWhileRan:   "the program created these files while it ran; they are carried as they were at the end of the observation, and a fresh start may not make them: /a",
		observe.NoteNotCarried:        "not carried /big: too large",
		observe.NoteBind:              "the bind /data is mounted read-only: it is big; the program cannot write there as it would in a real run",
		observe.NoteMachineFiles:      "could not write the machine files for the package: disk full",
		observe.NoteHosts:             "could not make /etc/hosts for the package: disk full",
		observe.NoteProcCmdline:       `the command line in /proc ["a"] differs from the one execve was given ["b"]: the one execve was given is used`,
		observe.NoteStoppedBeforeProc: "the program had stopped before /proc could be read: the command line, environment and directory are those of the image",
		observe.NoteUserGroup:         "could not read the user and group of the program from /proc: they are not known",
		observe.NoteImageRuns:         `the image runs as "app", which is not a uid; it was observed as root`,
	}
	if len(samples) != len(observe.FileNotePrefixes) {
		t.Fatalf("%d samples for %d prefixes", len(samples), len(observe.FileNotePrefixes))
	}
	for _, p := range observe.FileNotePrefixes {
		sample, ok := samples[p]
		if !ok {
			t.Errorf("no sample for the prefix %q", p)
			continue
		}
		if !strings.HasPrefix(sample, p) {
			t.Errorf("the sample %q does not begin with %q", sample, p)
		}
		if !IsFileWarning(sample) {
			t.Errorf("not dropped: %q", sample)
		}
	}
	for _, w := range []string{
		"and 4 more paths were not carried",
		"web was observed while this service was running; what web wrote into it (tables) is not in this package",
	} {
		if !IsFileWarning(w) {
			t.Errorf("not dropped: %q", w)
		}
	}
	for _, w := range []string{
		"the container stopped with status 1 before the end of the observation",
		"it stopped with status 1 the first time",
		"and more",
	} {
		if IsFileWarning(w) {
			t.Errorf("dropped, and it is about the run: %q", w)
		}
	}
}

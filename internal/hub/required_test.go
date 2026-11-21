package hub

import (
	"strings"
	"testing"
)

// testRows is a table for the tests only. The table that ships in the binary
// has only the postgres row.
const testRows = `
rows:
  - images: [postgres, postgresql]
    programs: [postgres]
    packages:
      - francescolavra/postgres
      - eyberg/postgresql
    reason: postgres starts a process per connection
`

func TestParseRequired(t *testing.T) {
	rows, err := ParseRequired([]byte(testRows))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Images[1] != "postgresql" || rows[0].Packages[0] != "francescolavra/postgres" || rows[0].Reason == "" {
		t.Errorf("%+v", rows)
	}
}

func TestParseRequiredRefusesWhatIsNotAProperRow(t *testing.T) {
	bad := []string{
		"rows:\n  - images: [a]\n    programs: [a]\n    packages: [x/y]\n",
		"rows:\n  - images: [a]\n    programs: [a]\n    reason: r\n",
		"rows:\n  - images: [a]\n    programs: [a]\n    packages: [nonamespace]\n    reason: r\n",
		"rows:\n  - images: [a]\n    programs: [a]\n    packages: [a/b/c]\n    reason: r\n",
		"rows:\n  - images: [a]\n    programs: [a]\n    packages: [x/y]\n    reason: r\n    typo: 1\n",
		"rows: [",
		"rows:\n  - images: [Postgres]\n    programs: [a]\n    packages: [x/y]\n    reason: r\n",
		"rows:\n  - images: [a]\n    programs: [/usr/bin/a]\n    packages: [x/y]\n    reason: r\n",
	}
	for _, text := range bad {
		if _, err := ParseRequired([]byte(text)); err == nil {
			t.Errorf("accepted: %q", text)
		}
	}
}

func TestParseRequiredEmpty(t *testing.T) {
	for _, text := range []string{"", "rows: []\n", "# only a comment\nrows: []\n"} {
		rows, err := ParseRequired([]byte(text))
		if err != nil || len(rows) != 0 {
			t.Errorf("%q: %v %v", text, rows, err)
		}
	}
}

// The table that ships in the binary has the postgres row, proven on the fork.
func TestTheTableThatShipsHasThePostgresRow(t *testing.T) {
	rows, err := DefaultRequired()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("the shipped table has %d rows: %+v", len(rows), rows)
	}
	if MatchRequired(rows, "postgres", "/usr/lib/postgresql/16/bin/postgres") == nil {
		t.Error("image postgres, program postgres is not matched")
	}
	if MatchRequired(rows, "postgresql", "postgres") == nil {
		t.Error("image postgresql, program postgres is not matched")
	}
	if MatchRequired(rows, "nginx", "/usr/sbin/nginx") != nil {
		t.Error("an unrelated image and program matched")
	}
	if MatchRequired(rows, "postgres", "/bin/sh") != nil {
		t.Error("an unrelated program matched")
	}
	if got := rows[0].Packages; len(got) != 2 || got[0] != "francescolavra/postgres" || got[1] != "eyberg/postgresql" {
		t.Errorf("packages: %v", got)
	}
}

func TestMatchRequired(t *testing.T) {
	rows, err := ParseRequired([]byte(testRows))
	if err != nil {
		t.Fatal(err)
	}
	if r := MatchRequired(rows, "postgres", "/usr/lib/postgresql/16/bin/postgres"); r == nil {
		t.Error("postgres is not matched")
	}
	if r := MatchRequired(rows, "Postgresql", "postgres"); r == nil {
		t.Error("postgresql, in capitals, is not matched")
	}
	if r := MatchRequired(rows, "nginx", "/usr/sbin/postgres"); r != nil {
		t.Error("another image matched")
	}
	if r := MatchRequired(rows, "postgres", "/bin/sh"); r != nil {
		t.Error("another program matched")
	}
	if r := MatchRequired(nil, "postgres", "postgres"); r != nil {
		t.Error("an empty table matched")
	}
}

func TestRequiredCandidates(t *testing.T) {
	rows, _ := ParseRequired([]byte(testRows))
	pkgs := []Package{
		pkg("francescolavra", "postgres", "16.0", "x86_64"),
		pkg("francescolavra", "postgres", "16.2", "x86_64"),
		pkg("eyberg", "postgresql", "11.3.0", "x86_64"),
		pkg("eyberg", "postgrest", "10.1.1", "x86_64"),
		pkg("someone", "postgres", "16.2", "x86_64"),
		pkg("francescolavra", "postgres", "16.3", "arm64"),
	}
	got := rows[0].Candidates(pkgs, "amd64")
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	p, lvl := SelectFrom(got, "16.1")
	if p.Version != "16.0" || lvl != Near {
		// the newest release that is not newer than 16.1
		t.Errorf("16.1: %v %v", p.Version, lvl)
	}
	p, lvl = SelectFrom(got, "16.0")
	if p.Version != "16.0" || lvl != Exact {
		t.Errorf("16.0: %v %v", p.Version, lvl)
	}
	p, lvl = SelectFrom(got, "11.3")
	if p.Ref() != "eyberg/postgresql" || lvl != Near {
		t.Errorf("11.3: %v %v", p.Ref(), lvl)
	}
	p, lvl = SelectFrom(got, "14.5")
	if lvl != Far || p.Version != "16.2" {
		t.Errorf("14.5: %v %v", p.Ref(), lvl)
	}
	p, lvl = SelectFrom(got, "17.1")
	if lvl != Far || p.Version != "16.2" {
		t.Errorf("17.1: %v %v", p.Version, lvl)
	}
}

func TestParseRequiredReadsTheEntrypointNote(t *testing.T) {
	rows, err := ParseRequired([]byte(testRows + "    entrypoint_note: the note of the row\n"))
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].EntrypointNote != "the note of the row" {
		t.Errorf("%+v", rows[0])
	}
	rows, err = ParseRequired([]byte(testRows))
	if err != nil || rows[0].EntrypointNote != "" {
		t.Errorf("a row without the field has a note: %+v %v", rows, err)
	}
}

func TestTheShippedPostgresRowHasTheEntrypointNote(t *testing.T) {
	rows, err := DefaultRequired()
	if err != nil {
		t.Fatal(err)
	}
	n := rows[0].EntrypointNote
	for _, want := range []string{"POSTGRES_DB", "POSTGRES_USER", "POSTGRES_PASSWORD", "CREATE DATABASE", "answered 200",
		"{POSTGRES_DB,POSTGRES_USER:ident}", "{HUB_SUPERUSER}", "CREATE ROLE", "POSTGRES_PASSWORD_FILE", "POSTGRES_INITDB_ARGS", "/docker-entrypoint-initdb.d",
		"44909583390", "44910340290", "13:30:17.893", "was not measured", "pre-existing shared memory block", "5432001"} {
		if !strings.Contains(n, want) {
			t.Errorf("the note does not say %q: %q", want, n)
		}
	}
	if strings.Contains(n, "seen in a run alone") || strings.Contains(n, "needs -d template1") || strings.Contains(n, "is not kept") || strings.Contains(n, "at every start") || strings.Contains(n, "If {POSTGRES_USER") {
		t.Errorf("the note claims more than was seen: %q", n)
	}
	if strings.Contains(rows[0].Reason, "observation of the image") || strings.Contains(rows[0].Reason, "child processes") {
		t.Errorf("the reason says what the row does not match on: %q", rows[0].Reason)
	}
	if got := rows[0].Superusers; got["francescolavra/postgres"] != "postgres" || got["eyberg/postgresql"] != "eyberg" || len(got) != 2 {
		t.Errorf("superusers: %v", got)
	}
	if rn := rows[0].RestartNote; !strings.Contains(rn, "44910340290") || !strings.Contains(rn, "not measured") {
		t.Errorf("restart note: %q", rn)
	}
	if !strings.Contains(n, "comes from the postmaster.pid left by the first boot") || !strings.Contains(n, "the file itself was not seen") || strings.Contains(n, "which leaves postmaster.pid") {
		t.Errorf("the note says the postmaster.pid was seen: %q", n)
	}
	for ref, run := range map[string]string{"francescolavra/postgres": "44908630495", "eyberg/postgresql": "44910340290"} {
		if !strings.Contains(rows[0].PackageFacts[ref], run) {
			t.Errorf("the facts of %s do not cite run %s: %q", ref, run, rows[0].PackageFacts[ref])
		}
	}
	if strings.Contains(rows[0].Reason, "shmget") {
		t.Errorf("the reason says something not measured: %q", rows[0].Reason)
	}
}

func TestParseRequiredReadsSuperusersAndRefusesAForeignPackage(t *testing.T) {
	rows, err := ParseRequired([]byte(testRows + "    superusers:\n      eyberg/postgresql: root\n    restart_note: a note\n"))
	if err != nil || rows[0].Superusers["eyberg/postgresql"] != "root" || rows[0].RestartNote != "a note" {
		t.Fatalf("%+v %v", rows, err)
	}
	if _, err := ParseRequired([]byte(testRows + "    superusers:\n      someone/else: root\n")); err == nil {
		t.Error("a superuser of a package that is not in the row was accepted")
	}
	// the note needs a superuser for every package of the row, and a template that closes
	if _, err := ParseRequired([]byte(testRows + "    entrypoint_note: \"-U {HUB_SUPERUSER}\"\n")); err == nil {
		t.Error("a note that names {HUB_SUPERUSER} was accepted without superusers")
	}
	if _, err := ParseRequired([]byte(testRows + "    entrypoint_note: '[[if A]]x'\n")); err == nil {
		t.Error("a section that is not closed was accepted")
	}
	if _, err := ParseRequired([]byte(testRows + "    package_facts:\n      someone/else: x\n")); err == nil {
		t.Error("facts of a package that is not in the row were accepted")
	}
}

func TestParseRequiredRequiresTheFactsOfEveryPackageThatTheNoteNames(t *testing.T) {
	rows := "    entrypoint_note: \"-U {HUB_SUPERUSER}\"\n"
	if _, err := ParseRequired([]byte(testRows + rows)); err == nil {
		t.Error("a note that names {HUB_SUPERUSER} was accepted without superusers")
	}
	// the condition names it too
	if _, err := ParseRequired([]byte(testRows + "    entrypoint_note: \"[[if A!=HUB_SUPERUSER]]x[[end]]\"\n")); err == nil {
		t.Error("a test that names HUB_SUPERUSER was accepted without superusers")
	}
	if _, err := ParseRequired([]byte(testRows + rows + "    superusers:\n      eyberg/postgresql: root\n")); err == nil {
		t.Error("a note that names {HUB_SUPERUSER} was accepted with a superuser for some of the packages only")
	}
	if _, err := ParseRequired([]byte(testRows + "    entrypoint_note: \"{HUB_FACTS}\"\n")); err == nil {
		t.Error("a note that names {HUB_FACTS} was accepted without package_facts")
	}
	all := "    superusers:\n"
	for _, p := range []string{"francescolavra/postgres", "eyberg/postgresql"} {
		all += "      " + p + ": root\n"
	}
	if _, err := ParseRequired([]byte(testRows + rows + all)); err != nil {
		t.Errorf("%v", err)
	}
}

func TestParseRequiredChecksTheTemplateOfTheNote(t *testing.T) {
	for _, bad := range []string{
		"[[if A]]x",
		"x[[end]]",
		"[[if A]]x[[if B]]y[[end]][[end]]",
		"[[if A!=]]x[[end]]",
		"[[if A or B]]x[[end]]",
		"[[iff A]]x[[end]]",
		"{A:upper}",
	} {
		if _, err := ParseRequired([]byte(testRows + "    entrypoint_note: '" + bad + "'\n")); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	for _, ok := range []string{
		"plain text",
		"[[if A and not B]]x {A|d e}[[end]] [[unless A!=\"q\"]]y[[end]]",
		"a { b } c",
	} {
		if _, err := ParseRequired([]byte(testRows + "    entrypoint_note: '" + ok + "'\n")); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
}

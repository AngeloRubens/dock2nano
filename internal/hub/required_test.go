package hub

import (
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/hints"
)

// testRows is a table for the tests only. The table that ships in the binary
// has no active row.
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

// The table that ships in the binary has no active row until the proof that
// the packages of the hub run on nanos exists.
func TestTheTableThatShipsHasNoActiveRow(t *testing.T) {
	rows, err := DefaultRequired()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("the shipped table has %d active rows: %+v", len(rows), rows)
	}
}

func TestTheShippedTableKeepsTheExampleRowAsAComment(t *testing.T) {
	// the commented example must stay a valid row once the comment marks are taken off
	var b strings.Builder
	in := false
	for _, line := range strings.Split(string(hints.HubRequired), "\n") {
		if strings.HasPrefix(line, "#  - images:") {
			in = true
			b.WriteString("rows:\n")
		}
		if in {
			if !strings.HasPrefix(line, "#") {
				break
			}
			b.WriteString(strings.TrimPrefix(line, "#") + "\n")
		}
	}
	rows, err := ParseRequired([]byte(b.String()))
	if err != nil || len(rows) != 1 || rows[0].Programs[0] != "postgres" {
		t.Fatalf("the example row: %v %v\n%s", rows, err, b.String())
	}
	if rows[0].Packages[0] != "francescolavra/postgres" || rows[0].Packages[1] != "eyberg/postgresql" {
		t.Errorf("packages: %v", rows[0].Packages)
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
	if lvl != Far || p.Version != "16.0" {
		t.Errorf("14.5: %v %v", p.Ref(), lvl)
	}
	p, lvl = SelectFrom(got, "17.1")
	if lvl != Far || p.Version != "16.2" {
		t.Errorf("17.1: %v %v", p.Version, lvl)
	}
}

package gen

import (
	"strings"
	"testing"
)

func entrypointWith(t *testing.T, u HubUse, obsEnv ...string) string {
	t.Helper()
	return entrypointText(t, u, obsEnv...)
}

func TestEntrypointNoteReadsTheEnvironmentOfTheComposeFile(t *testing.T) {
	u := shippedUse(t, "francescolavra", "postgres", "16.2")
	u.ComposeEnv = []string{"POSTGRES_DB=example"}
	got := entrypointWith(t, u)
	if !strings.Contains(got, `-c 'CREATE DATABASE "example"'`) || strings.Contains(got, "No database has to be created") {
		t.Errorf("%s", got)
	}
	// the compose file wins over the image and the observed environment
	u.ImageEnv = []string{"POSTGRES_DB=fromimage"}
	got = entrypointWith(t, u, "POSTGRES_DB=observed")
	if !strings.Contains(got, `CREATE DATABASE "example"`) || strings.Contains(got, "fromimage") || strings.Contains(got, "observed") {
		t.Errorf("%s", got)
	}
}

func TestEntrypointNoteDoesNotSayNoDatabaseWhenTheEnvironmentIsNotCertain(t *testing.T) {
	u := shippedUse(t, "francescolavra", "postgres", "16.2")
	u.EnvUncertain = true
	got := entrypointWith(t, u)
	if strings.Contains(got, "No database has to be created") {
		t.Errorf("%s", got)
	}
	if !strings.Contains(got, `CREATE DATABASE <your POSTGRES_DB or POSTGRES_USER>`) || !strings.Contains(got, "could not be read completely") {
		t.Errorf("%s", got)
	}
	// certain: the sentence is there
	u.EnvUncertain = false
	if got := entrypointWith(t, u); !strings.Contains(got, "No database has to be created") {
		t.Errorf("%s", got)
	}
}

func TestEntrypointNoteSaysTheFactsOfEachPackage(t *testing.T) {
	fl := entrypointWith(t, shippedUse(t, "francescolavra", "postgres", "16.2"))
	ey := entrypointWith(t, shippedUse(t, "eyberg", "postgresql", "11.3.0"))
	for _, want := range []string{"a database named postgres exists", "run 44908630495 ran psql -U postgres without -d"} {
		if !strings.Contains(fl, want) {
			t.Errorf("francescolavra: %q is not in\n%s", want, fl)
		}
	}
	if strings.Contains(fl, "there is no database named") {
		t.Errorf("francescolavra has a database postgres:\n%s", fl)
	}
	if !strings.Contains(ey, "there is no database named eyberg") || strings.Contains(ey, "a database named postgres exists, so psql") {
		t.Errorf("eyberg:\n%s", ey)
	}
}

func TestEntrypointNoteMakesTheRoleASuperuserBeforeTheDatabaseAndSaysItWasNotRun(t *testing.T) {
	u := shippedUse(t, "francescolavra", "postgres", "16.2")
	got := entrypointWith(t, u, "POSTGRES_DB=db", "POSTGRES_USER=app")
	role := strings.Index(got, `CREATE ROLE "app" LOGIN SUPERUSER`)
	db := strings.Index(got, `CREATE DATABASE "db" OWNER "app"`)
	if role < 0 || db < 0 || role > db {
		t.Errorf("role at %d, database at %d:\n%s", role, db, got)
	}
	if !strings.Contains(got, "NOT run in any CI run") {
		t.Errorf("%s", got)
	}
	// the user of the image is the superuser of the package: no role, no owner, nothing said of a run
	got = entrypointWith(t, u, "POSTGRES_DB=db", "POSTGRES_USER=postgres")
	if strings.Contains(got, "CREATE ROLE") || strings.Contains(got, "OWNER") || strings.Contains(got, "NOT run") || !strings.Contains(got, `CREATE DATABASE "db"`) {
		t.Errorf("%s", got)
	}
}

func TestEntrypointNotePrintsNoCommandForANameWithControlCharacters(t *testing.T) {
	u := shippedUse(t, "francescolavra", "postgres", "16.2")
	for _, env := range [][]string{
		{"POSTGRES_DB=a\nb"}, {"POSTGRES_DB=a\tb"}, {"POSTGRES_DB=a\u2028b"},
		{"POSTGRES_DB=ok", "POSTGRES_USER=ap\np"},
	} {
		got := entrypointWith(t, u, env...)
		if strings.Contains(got, "-c 'CREATE") || !strings.Contains(got, "control character") || !strings.Contains(got, "quote the name by hand") {
			t.Errorf("%q:\n%s", env, got)
		}
	}
}

func TestEntrypointNoteFixesTheSuperuserAndNeverReadsItFromTheEnvironment(t *testing.T) {
	u := shippedUse(t, "francescolavra", "postgres", "16.2")
	u.Superuser = ""
	u.ComposeEnv = []string{"HUB_SUPERUSER=evil", "HUB_ENV_UNCERTAIN=x", "HUB_CONTROL=x"}
	got := entrypointWith(t, u, "HUB_SUPERUSER=evil2", "POSTGRES_DB=shop")
	if strings.Contains(got, "evil") || !strings.Contains(got, "-U the superuser of the package (not known) -d template1") {
		t.Errorf("%s", got)
	}
	if strings.Contains(got, "could not be read completely") || strings.Contains(got, "control character") {
		t.Errorf("the tool's own names were read from the environment:\n%s", got)
	}
}

func TestShippedNoteRendersWithoutDoubleSpacesForTheTwoComposeCases(t *testing.T) {
	for _, pkg := range [][3]string{{"francescolavra", "postgres", "16.2"}, {"eyberg", "postgresql", "11.3.0"}} {
		u := shippedUse(t, pkg[0], pkg[1], pkg[2])
		for _, env := range [][]string{{"POSTGRES_DB=example"}, nil, {"POSTGRES_DB=example", "POSTGRES_USER=app"}} {
			u.ComposeEnv = env
			if got := entrypointWith(t, u); strings.Contains(got, "  ") || strings.Contains(got, " .") {
				t.Errorf("%v: %q", env, got)
			}
		}
	}
}

func TestAConditionWhoseRightSideIsASecretIsFalse(t *testing.T) {
	if got := fill(`[[if A!=POSTGRES_PASSWORD]]x[[end]]`, "A=1", "POSTGRES_PASSWORD=2"); got != "" {
		t.Errorf("%q", got)
	}
	if got := fill(`[[if A!=B]]x[[end]]`, "A=1", "B=2"); got != "x" {
		t.Errorf("%q", got)
	}
}

func TestConditionsJoinTermsWithAndAndNot(t *testing.T) {
	for _, tc := range []struct {
		test string
		want string
	}{
		{"A and B", "x"}, {"A and C", ""}, {"not C", "x"}, {"not A", ""}, {"A and not C", "x"}, {`A!="1" and B`, ""},
	} {
		if got := fill("[[if "+tc.test+"]]x[[end]]", "A=1", "B=2"); got != tc.want {
			t.Errorf("%s: %q", tc.test, got)
		}
	}
}

func TestFillPlaceholdersTrimsTheSpacesOfARemovedSection(t *testing.T) {
	if got := fill("a [[if X]]b[[end]] c", ""); got != "a c" {
		t.Errorf("%q", got)
	}
	// the spaces of a value are the user's
	if got := fill("a {X} c", "X=b  b"); got != "a b  b c" {
		t.Errorf("%q", got)
	}
}

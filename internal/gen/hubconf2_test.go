package gen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/hub"
	"github.com/AngeloRubens/dock2nano/internal/plan"
)

// confAfter applies the recipe to a sysroot with these files and argv, and
// returns the items and the text of /etc/nginx/nginx.conf.
func confAfter(t *testing.T, files map[string]string, argv []string) ([]plan.Item, string) {
	t.Helper()
	obs := hubObs(t)
	obs.Argv = argv
	obs.Sysroot = hubSysroot(t, files)
	items := ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	return items, hubConfOf(t, obs.Sysroot, "etc/nginx/nginx.conf")
}

func hasItem(items []plan.Item, kind plan.Kind, key, text string) bool {
	for _, it := range items {
		if it.Kind == kind && it.Key == key && strings.Contains(it.Message, text) {
			return true
		}
	}
	return false
}

func TestHubPackageCredentialsAreSaidOnlyForThePackagesThatWereTried(t *testing.T) {
	rows := []struct {
		ns, name, version string
		want              string
	}{
		{"francescolavra", "postgres", "16.0", "empty password"},
		{"francescolavra", "postgres", "16.2", "empty password"},
		{"eyberg", "mysql", "5.7.29", "root/root"},
		{"francescolavra", "postgres", "16.3", ""},
		{"eyberg", "postgresql", "11.3.0", ""},
		{"eyberg", "mysql", "8.0.1", ""},
	}
	for _, r := range rows {
		u := hubWholeUse(hub.Exact, r.version, r.version)
		u.Package.Namespace, u.Package.Name = r.ns, r.name
		c := hubFind(ApplyHubWhole(u, hubWholeObs()), "hub credentials")
		switch {
		case r.want == "" && len(c) != 0:
			t.Errorf("%s/%s %s: %+v", r.ns, r.name, r.version, c)
		case r.want != "" && (len(c) != 1 || !strings.Contains(c[0].Message, r.want) || strings.Contains(c[0].Message, "trust")):
			t.Errorf("%s/%s %s: %+v", r.ns, r.name, r.version, c)
		}
	}
}

func TestADirectiveSetOutsideTheFileIsNotComparedWithTheHubConfig(t *testing.T) {
	items, _ := confAfter(t, map[string]string{"/etc/nginx/nginx.conf": "events {}\n"}, []string{"nginx", "-g", "daemon off;"})
	// the hub config says daemon off; and the file has none, but the command line has it
	if strings.Contains(hubText(items), "daemon: hub") {
		t.Errorf("%s", hubText(items))
	}
}

func TestNothingToChangeIsSaidAndIsNotAFailure(t *testing.T) {
	items, conf := confAfter(t, map[string]string{"/etc/nginx/nginx.conf": "master_process off;\nworker_processes 1;\nevents {}\n"}, []string{"nginx", "-g", "daemon off;"})
	if conf != "master_process off;\nworker_processes 1;\nevents {}\n" {
		t.Errorf("%s", conf)
	}
	if !hasItem(items, plan.Note, "hub config", "already has master_process off") || !hasItem(items, plan.Note, "hub config", "daemon off is already set by the command line") {
		t.Errorf("%s", hubText(items))
	}
	if !hasItem(items, plan.Note, "hub result", "already single-process") || hasItem(items, plan.Unsupported, "hub result", "") {
		t.Errorf("%s", hubText(items))
	}
}

func TestQuotesAndCaseDoNotMakeADirectiveDifferent(t *testing.T) {
	for _, argv := range [][]string{{"nginx", "-g", `daemon "off";`}, {"nginx", "-g", "daemon OFF;"}, {"nginx", "-g", "daemon 'off';"}} {
		items, conf := confAfter(t, map[string]string{"/etc/nginx/nginx.conf": "events {}\n"}, argv)
		if strings.Contains(conf, "daemon") || !hasItem(items, plan.Note, "hub config", "daemon off is already set") {
			t.Errorf("%v: %s\n%s", argv, conf, hubText(items))
		}
	}
	items, conf := confAfter(t, map[string]string{
		"/etc/nginx/nginx.conf":       "include /etc/nginx/modules/*.conf;\nevents {}\n",
		"/etc/nginx/modules/one.conf": "daemon OFF;\n",
	}, []string{"nginx"})
	if strings.Contains(conf, "daemon") || !hasItem(items, plan.Note, "hub config", "already set by the included file") {
		t.Errorf("%s\n%s", conf, hubText(items))
	}
	// the file itself, in capitals, is already right
	_, conf = confAfter(t, map[string]string{"/etc/nginx/nginx.conf": "DAEMON x;\nmaster_process OFF;\nworker_processes 1;\ndaemon off;\nevents {}\n"}, []string{"nginx"})
	if strings.Count(conf, "master_process") != 1 || strings.Contains(conf, "master_process off") {
		t.Errorf("%s", conf)
	}
}

func TestAWildcardDoesNotMatchADotFile(t *testing.T) {
	_, conf := confAfter(t, map[string]string{
		"/etc/nginx/nginx.conf":         "include /etc/nginx/modules/*.conf;\nevents {}\n",
		"/etc/nginx/modules/.hide.conf": "daemon on;\n",
	}, []string{"nginx"})
	if !strings.Contains(conf, "daemon off;") {
		t.Errorf("a file the wildcard does not match stopped the directive:\n%s", conf)
	}
	items, conf := confAfter(t, map[string]string{
		"/etc/nginx/nginx.conf":         "include /etc/nginx/modules/.*.conf;\nevents {}\n",
		"/etc/nginx/modules/.hide.conf": "daemon on;\n",
	}, []string{"nginx"})
	if strings.Contains(conf, "master_process") || !hasItem(items, plan.Unsupported, "hub config", "daemon is set to on") {
		t.Errorf("a pattern that starts with a dot matches it:\n%s\n%s", conf, hubText(items))
	}
}

func TestOnlyTheLastValueOfTheCommandLineOptionCounts(t *testing.T) {
	items, conf := confAfter(t, map[string]string{"/etc/nginx/nginx.conf": "events {}\n"}, []string{"nginx", "-g", "daemon on;", "-g", "daemon off;"})
	if strings.Contains(conf, "daemon") || !hasItem(items, plan.Note, "hub config", "daemon off is already set by the command line") {
		t.Errorf("%s\n%s", conf, hubText(items))
	}
}

func TestAnIncludeOnTheCommandLineIsSaidNotToBeExpanded(t *testing.T) {
	items, conf := confAfter(t, map[string]string{"/etc/nginx/nginx.conf": "events {}\n"}, []string{"nginx", "-g", "include /etc/x.conf;"})
	if strings.Contains(conf, "master_process") || !hasItem(items, plan.Unsupported, "hub config", "an include on the command line (-g)") {
		t.Errorf("%s\n%s", conf, hubText(items))
	}
}

func TestTheHubWithTheIncludesThatCannotBeFollowed(t *testing.T) {
	var many = map[string]string{"/etc/nginx/nginx.conf": "include /etc/nginx/many/*.conf;\nevents {}\n"}
	for i := 0; i <= maxIncludedFiles; i++ {
		many[fmt.Sprintf("/etc/nginx/many/f%03d.conf", i)] = "load_module /x.so;\n"
	}
	rows := []struct {
		name  string
		files map[string]string
		why   string
	}{
		{"nested include", map[string]string{
			"/etc/nginx/nginx.conf":     "include /etc/nginx/a/*.conf;\nevents {}\n",
			"/etc/nginx/a/one.conf":     "include /etc/nginx/b.conf;\n",
			"/etc/nginx/b.conf":         "daemon off;\n",
			"/etc/nginx/a/ignored.text": "x",
		}, "which includes other files"},
		{"too many files", many, "includes more than"},
		{"wildcard in a directory", map[string]string{"/etc/nginx/nginx.conf": "include /etc/*/x.conf;\nevents {}\n"}, "cannot expand"},
		{"variable", map[string]string{"/etc/nginx/nginx.conf": "include /etc/$x.conf;\nevents {}\n"}, "cannot expand"},
		{"lua in an included file", map[string]string{
			"/etc/nginx/nginx.conf": "include /etc/nginx/a/*.conf;\nevents {}\n",
			"/etc/nginx/a/l.conf":   "init_by_lua_block { x = {1} }\n",
		}, "which has a Lua block"},
	}
	for _, r := range rows {
		items, conf := confAfter(t, r.files, []string{"nginx"})
		first := r.files["/etc/nginx/nginx.conf"]
		if strings.Contains(conf, "master_process") || !strings.HasPrefix(conf, strings.Split(first, "\n")[0]) {
			t.Errorf("%s: the file was changed:\n%s", r.name, conf)
		}
		if !hasItem(items, plan.Unsupported, "hub config", r.why) {
			t.Errorf("%s: the reason is not in the report:\n%s", r.name, hubText(items))
		}
	}
}

func TestAnUnreadableIncludeDirectoryStopsTheAdditions(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a directory cannot be made unreadable for root")
	}
	obs := hubObs(t)
	obs.Argv = []string{"nginx"}
	obs.Sysroot = hubSysroot(t, map[string]string{"/etc/nginx/nginx.conf": "include /etc/nginx/locked/*.conf;\nevents {}\n", "/etc/nginx/locked/a.conf": "x;\n"})
	locked := filepath.Join(obs.Sysroot, "etc/nginx/locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)
	items := ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	if conf := hubConfOf(t, obs.Sysroot, "etc/nginx/nginx.conf"); strings.Contains(conf, "master_process") {
		t.Errorf("%s", conf)
	}
	if !hasItem(items, plan.Unsupported, "hub config", "whose directory cannot be read") {
		t.Errorf("%s", hubText(items))
	}
}

func TestAnIncludedFileReachedThroughALinkIsRead(t *testing.T) {
	sys := hubSysroot(t, map[string]string{
		"/etc/nginx/nginx.conf": "include /etc/nginx/modules/*.conf;\nevents {}\n",
		"/opt/mods/one.conf":    "daemon off;\n",
	})
	if err := os.Symlink("/opt/mods", filepath.Join(sys, "etc/nginx/modules")); err != nil {
		t.Fatal(err)
	}
	obs := hubObs(t)
	obs.Argv = []string{"nginx"}
	obs.Sysroot = sys
	items := ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	conf := hubConfOf(t, sys, "etc/nginx/nginx.conf")
	if strings.Contains(conf, "daemon") || !hasItem(items, plan.Note, "hub config", "already set by the included file /opt/mods/one.conf") {
		t.Errorf("%s\n%s", conf, hubText(items))
	}
}

func TestHubVersionsTextSaysHowFarApartANearVersionIs(t *testing.T) {
	got := hubUse(hub.Near, "1.25.3", "1.28.3").versions()
	if !strings.Contains(got, "3 minor releases apart") || !strings.Contains(got, "not the same behaviour") {
		t.Errorf("%s", got)
	}
}

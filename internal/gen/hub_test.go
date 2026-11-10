package gen

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/compose"
	"github.com/AngeloRubens/dock2nano/internal/hub"
	"github.com/AngeloRubens/dock2nano/internal/observe"
	"github.com/AngeloRubens/dock2nano/internal/plan"
)

const hubUserConf = `user  nginx;
worker_processes  auto;
pid        /var/run/nginx.pid;
events {
    worker_connections  1024;
}
http {
    server {
        listen       80;
        root   /usr/share/nginx/html;
    }
}
`

const hubHubConf = `worker_processes 1;
daemon off;
master_process off;
pid bob.pid;
events { worker_connections 1024; }
http {
    server { listen 8083; root /; }
    server { listen 4443 ssl; root /var/www/html; }
}
`

const hubSum = "7a1fbdceb9f645836eaecae5e119fd99aa45068b1a3000f1d211e9b98b46fd2d"

// hubSysroot writes files (by their path inside the image) into a new
// directory and returns it.
func hubSysroot(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func hubObs(t *testing.T) *observe.Result {
	return &observe.Result{
		Sysroot:  hubSysroot(t, map[string]string{"/etc/nginx/nginx.conf": hubUserConf}),
		Program:  "/usr/sbin/nginx",
		Argv:     []string{"nginx", "-g", "daemon off;"},
		Env:      []string{"PATH=/usr/bin", "NGINX_VERSION=1.25.3"},
		Children: 2,
	}
}

func hubUse(level hub.Level, wanted, version string) HubUse {
	p := hub.Package{Namespace: "eyberg", Name: "nginx", Version: version, Arch: "x86_64", SHA256: hubSum}
	rec := &hub.Recipe{Package: p, Files: map[string][]byte{"/usr/local/nginx/conf/nginx.conf": []byte(hubHubConf)}}
	rec.Manifest.Program = "nginx_" + version + "/nginx"
	rec.Manifest.Args = []string{"nginx"}
	return HubUse{Service: "web", Package: p, Level: level, Wanted: wanted, Why: "the program started 2 child processes", Recipe: rec}
}

func hubFind(items []plan.Item, key string) []plan.Item {
	var out []plan.Item
	for _, it := range items {
		if it.Key == key {
			out = append(out, it)
		}
	}
	return out
}

func hubText(items []plan.Item) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString(string(it.Kind) + " " + it.Key + ": " + it.Message + "\n")
	}
	return b.String()
}

func TestApplyHubRecipeSetsTheSingleProcessDirectivesInTheUsersConfig(t *testing.T) {
	obs := hubObs(t)
	items := ApplyHubRecipe(hubUse(hub.Near, "1.25.3", "1.28.3"), obs)
	got, err := os.ReadFile(filepath.Join(obs.Sysroot, "etc/nginx/nginx.conf"))
	if err != nil {
		t.Fatal(err)
	}
	conf := string(got)
	for _, want := range []string{"master_process off;", "daemon off;", "worker_processes 1;", "listen       80;", "root   /usr/share/nginx/html;", "pid        /var/run/nginx.pid;", "user  nginx;"} {
		if !strings.Contains(conf, want) {
			t.Errorf("%q is not in the config:\n%s", want, conf)
		}
	}
	for _, not := range []string{"8083", "4443", "bob.pid", "worker_processes  auto"} {
		if strings.Contains(conf, not) {
			t.Errorf("%q is in the config, which must stay the user's:\n%s", not, conf)
		}
	}
	text := hubText(items)
	for _, want := range []string{
		"eyberg/nginx 1.28.3",
		"sha256 " + hubSum,
		"published by eyberg",
		"The files of the image are the ones observed from your image",
		"your image has 1.25.3 and the hub package 1.28.3: the same major series",
		"master_process off (was not set)",
		"worker_processes 1 (was auto)",
		"listen: hub [4443 ssl, 8083], yours [80]",
		"yours is kept",
		"not run on nanos",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the report does not say %q:\n%s", want, text)
		}
	}
	if v := hubFind(items, "hub result"); len(v) != 1 || v[0].Kind != plan.Approximated {
		t.Errorf("verdict: %+v", v)
	}
	if !reflect.DeepEqual(obs.Argv, []string{"nginx", "-g", "daemon off;"}) {
		t.Errorf("argv: %v", obs.Argv)
	}
}

func TestHubVersionsText(t *testing.T) {
	rows := []struct {
		level  hub.Level
		wanted string
		want   string
	}{
		{hub.Exact, "1.28.3", "the same version as your image (1.28.3)"},
		{hub.Near, "1.25.3", "the same major series, not the same version"},
		{hub.Far, "2.0.1", "another major series"},
		{hub.Far, "", "the version of your image is not known"},
	}
	for _, r := range rows {
		if got := hubUse(r.level, r.wanted, "1.28.3").versions(); !strings.Contains(got, r.want) {
			t.Errorf("%v %q: %q does not say %q", r.level, r.wanted, got, r.want)
		}
	}
}

func TestApplyHubRecipeKeepsTheUsersFilesAndOnlyTheUsersFiles(t *testing.T) {
	obs := hubObs(t)
	other := filepath.Join(obs.Sysroot, "etc/other.conf")
	if err := os.WriteFile(other, []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	if b, _ := os.ReadFile(other); string(b) != "same" {
		t.Errorf("another file was changed: %q", b)
	}
	var names []string
	filepath.Walk(obs.Sysroot, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			names = append(names, strings.TrimPrefix(p, obs.Sysroot))
		}
		return nil
	})
	if len(names) != 2 {
		t.Errorf("files in the sysroot: %v", names)
	}
}

func TestApplyHubRecipeUsesTheFileNamedByTheProgramOption(t *testing.T) {
	obs := hubObs(t)
	obs.Sysroot = hubSysroot(t, map[string]string{"/opt/n.conf": "worker_processes 8;\nevents {}\n"})
	obs.Argv = []string{"nginx", "-c", "/opt/n.conf"}
	ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	b, _ := os.ReadFile(filepath.Join(obs.Sysroot, "opt/n.conf"))
	if !strings.Contains(string(b), "worker_processes 1;") || !strings.Contains(string(b), "master_process off;") {
		t.Errorf("%s", b)
	}
}

func TestApplyHubRecipeRelativeConfigOptionIsNotFollowed(t *testing.T) {
	obs := hubObs(t)
	obs.Argv = []string{"nginx", "-c", "conf/nginx.conf"}
	items := ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	if v := hubFind(items, "hub result"); len(v) != 1 || v[0].Kind != plan.Unsupported {
		t.Errorf("verdict: %+v", v)
	}
	if c := hubFind(items, "hub config"); len(c) == 0 || c[0].Kind != plan.Unsupported || !strings.Contains(c[0].Message, "relative path") {
		t.Errorf("config: %+v", c)
	}
	b, _ := os.ReadFile(filepath.Join(obs.Sysroot, "etc/nginx/nginx.conf"))
	if string(b) != hubUserConf {
		t.Error("the config was changed")
	}
}

func TestApplyHubRecipeWithoutAConfigFileSaysSo(t *testing.T) {
	obs := hubObs(t)
	obs.Sysroot = t.TempDir()
	items := ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	c := hubFind(items, "hub config")
	if len(c) != 1 || c[0].Kind != plan.Unsupported || !strings.Contains(c[0].Message, "was not found") || !strings.Contains(c[0].Message, "master_process off") {
		t.Errorf("config: %+v", c)
	}
	v := hubFind(items, "hub result")
	if len(v) != 1 || v[0].Kind != plan.Unsupported || !strings.Contains(v[0].Message, "still does not start") {
		t.Errorf("the program was not changed, and the report must not pretend: %+v", v)
	}
}

func TestApplyHubRecipeDoesNotEditALinkedConfigFile(t *testing.T) {
	obs := hubObs(t)
	outside := filepath.Join(t.TempDir(), "host.conf")
	if err := os.WriteFile(outside, []byte("worker_processes 9;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(obs.Sysroot, "etc/nginx/nginx.conf")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	items := ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	if b, _ := os.ReadFile(outside); string(b) != "worker_processes 9;\n" {
		t.Errorf("a file outside the sysroot was written: %q", b)
	}
	if c := hubFind(items, "hub config"); len(c) == 0 || !strings.Contains(c[0].Message, "not a regular file") {
		t.Errorf("config: %+v", c)
	}
}

func TestApplyHubRecipeConfigAlreadyRightChangesNothingAndSaysTheProgramIsNotChanged(t *testing.T) {
	obs := hubObs(t)
	obs.Sysroot = hubSysroot(t, map[string]string{"/etc/nginx/nginx.conf": "master_process off;\ndaemon off;\nworker_processes 1;\nevents {}\n"})
	items := ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	if c := hubFind(items, "hub config"); len(c) == 0 || c[0].Kind != plan.Note || !strings.Contains(c[0].Message, "already has") {
		t.Errorf("config: %+v", c)
	}
	if v := hubFind(items, "hub result"); len(v) != 1 || v[0].Kind != plan.Unsupported {
		t.Errorf("verdict: %+v", v)
	}
}

func TestApplyHubRecipeNeverUsesTheValuesOfTheHubPackage(t *testing.T) {
	obs := hubObs(t)
	u := hubUse(hub.Exact, "1.28.3", "1.28.3")
	u.Recipe.Files["/usr/local/nginx/conf/nginx.conf"] = []byte("worker_processes 2;\ndaemon on;\nmaster_process on;\n")
	items := ApplyHubRecipe(u, obs)
	b, _ := os.ReadFile(filepath.Join(obs.Sysroot, "etc/nginx/nginx.conf"))
	for _, want := range []string{"worker_processes 1;", "daemon off;", "master_process off;"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("%q is not in the config:\n%s", want, b)
		}
	}
	var found bool
	for _, it := range hubFind(items, "hub config") {
		if strings.Contains(it.Message, "worker_processes: hub [2], yours [1]") {
			found = true
		}
	}
	if !found {
		t.Errorf("the values of the hub are not in the report:\n%s", hubText(items))
	}
}

func TestApplyHubRecipeWithoutAHubConfigStillSetsTheValues(t *testing.T) {
	obs := hubObs(t)
	u := hubUse(hub.Far, "", "1.28.3")
	u.Recipe.Files = nil
	items := ApplyHubRecipe(u, obs)
	b, _ := os.ReadFile(filepath.Join(obs.Sysroot, "etc/nginx/nginx.conf"))
	if !strings.Contains(string(b), "master_process off;") {
		t.Errorf("%s", b)
	}
	if c := hubFind(items, "hub config"); len(c) < 2 || !strings.Contains(c[1].Message, "no configuration file") {
		t.Errorf("config: %+v", c)
	}
}

func TestApplyHubRecipeForAProgramWithNoKnownDirectives(t *testing.T) {
	obs := &observe.Result{Sysroot: t.TempDir(), Program: "/usr/bin/other", Argv: []string{"other"}, Children: 1}
	u := hubUse(hub.Exact, "1.0", "1.0")
	items := ApplyHubRecipe(u, obs)
	if c := hubFind(items, "hub config"); len(c) != 1 || c[0].Kind != plan.Note || !strings.Contains(c[0].Message, "no directives") {
		t.Errorf("config: %+v", c)
	}
	if v := hubFind(items, "hub result"); len(v) != 1 || v[0].Kind != plan.Unsupported {
		t.Errorf("verdict: %+v", v)
	}
}

func TestApplyHubRecipeNeverReplacesTheCommandLineOfTheUser(t *testing.T) {
	obs := hubObs(t)
	obs.Sysroot = hubSysroot(t, map[string]string{"/etc/nginx/site.conf": "worker_processes 4;\nevents {}\n", "/etc/nginx/nginx.conf": "worker_processes 4;\nevents {}\n"})
	obs.Argv = []string{"nginx", "-c", "/etc/nginx/site.conf", "-g", "secret-value"}
	u := hubUse(hub.Exact, "1.28.3", "1.28.3")
	u.Recipe.Manifest.Args = []string{"nginx", "-c", "/etc/nginx/nginx.conf"}
	items := ApplyHubRecipe(u, obs)
	if !reflect.DeepEqual(obs.Argv, []string{"nginx", "-c", "/etc/nginx/site.conf", "-g", "secret-value"}) {
		t.Errorf("argv: %v", obs.Argv)
	}
	site, _ := os.ReadFile(filepath.Join(obs.Sysroot, "etc/nginx/site.conf"))
	stock, _ := os.ReadFile(filepath.Join(obs.Sysroot, "etc/nginx/nginx.conf"))
	if !strings.Contains(string(site), "master_process off;") || strings.Contains(string(stock), "master_process") {
		t.Errorf("the file the program is started with must get the directives, and only it:\nsite:\n%s\nstock:\n%s", site, stock)
	}
	text := hubText(items)
	if strings.Contains(text, "secret-value") {
		t.Errorf("an observed argument is in the report:\n%s", text)
	}
	if a := hubFind(items, "hub arguments"); len(a) != 1 || a[0].Kind != plan.Note || !strings.Contains(a[0].Message, "-c /etc/nginx/nginx.conf") || !strings.Contains(a[0].Message, "not applied") {
		t.Errorf("arguments: %+v", a)
	}
}

func TestApplyHubRecipeDoesNotReadOrWriteThroughALinkInTheSysroot(t *testing.T) {
	obs := hubObs(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "nginx.conf"), []byte("worker_processes 9;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sys := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sys, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(sys, "etc/nginx")); err != nil {
		t.Fatal(err)
	}
	obs.Sysroot = sys
	items := ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	if b, _ := os.ReadFile(filepath.Join(outside, "nginx.conf")); string(b) != "worker_processes 9;\n" {
		t.Errorf("a file outside the sysroot was written: %q", b)
	}
	if c := hubFind(items, "hub config"); len(c) == 0 || c[0].Kind != plan.Unsupported || !strings.Contains(c[0].Message, "cannot be reached") {
		t.Errorf("config: %+v", c)
	}
	// the same for the file named by the option of the program
	obs.Argv = []string{"nginx", "-c", "/etc/nginx/nginx.conf"}
	items = ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	if b, _ := os.ReadFile(filepath.Join(outside, "nginx.conf")); string(b) != "worker_processes 9;\n" {
		t.Errorf("a file outside the sysroot was written: %q", b)
	}
	if v := hubFind(items, "hub result"); len(v) != 1 || v[0].Kind != plan.Unsupported {
		t.Errorf("verdict: %+v", v)
	}
}

func TestApplyHubRecipeDoesNotAddWhatAnIncludedFileMayHold(t *testing.T) {
	obs := hubObs(t)
	obs.Sysroot = hubSysroot(t, map[string]string{"/etc/nginx/nginx.conf": "include /etc/nginx/main.d/*.conf;\nworker_processes 4;\nevents {}\n"})
	items := ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	b, _ := os.ReadFile(filepath.Join(obs.Sysroot, "etc/nginx/nginx.conf"))
	if strings.Contains(string(b), "master_process") || strings.Contains(string(b), "daemon") || !strings.Contains(string(b), "worker_processes 1;") {
		t.Errorf("%s", b)
	}
	var found bool
	for _, it := range hubFind(items, "hub config") {
		if it.Kind == plan.Unsupported && strings.Contains(it.Message, "includes other files") && strings.Contains(it.Message, "master_process off") {
			found = true
		}
	}
	if !found {
		t.Errorf("the report does not say the directives were not added:\n%s", hubText(items))
	}
}

func TestApplyHubRecipeSaysWhenTheHubConfigIsALink(t *testing.T) {
	obs := hubObs(t)
	u := hubUse(hub.Exact, "1.28.3", "1.28.3")
	u.Recipe.Files = nil
	u.Recipe.NotRead = []string{"/usr/local/nginx/conf/nginx.conf"}
	items := ApplyHubRecipe(u, obs)
	var found bool
	for _, it := range hubFind(items, "hub config") {
		if strings.Contains(it.Message, "is a link or too large") {
			found = true
		}
	}
	if !found {
		t.Errorf("%s", hubText(items))
	}
}

func TestApplyHubRecipeEnvironment(t *testing.T) {
	obs := hubObs(t)
	u := hubUse(hub.Exact, "1.28.3", "1.28.3")
	u.Recipe.Manifest.Env = map[string]string{"PATH": "/other", "LANG": "C.UTF-8", "TOKEN": "hunter2"}
	items := ApplyHubRecipe(u, obs)
	want := []string{"PATH=/usr/bin", "NGINX_VERSION=1.25.3", "LANG=C.UTF-8", "TOKEN=hunter2"}
	if !reflect.DeepEqual(obs.Env, want) {
		t.Errorf("env: %v", obs.Env)
	}
	text := hubText(items)
	if strings.Contains(text, "hunter2") || strings.Contains(text, "C.UTF-8") {
		t.Errorf("a value is in the report:\n%s", text)
	}
	if e := hubFind(items, "hub environment"); len(e) != 2 || !strings.Contains(e[0].Message, "LANG, TOKEN") || !strings.Contains(e[1].Message, "PATH") {
		t.Errorf("environment: %+v", e)
	}
}

func TestApplyHubRecipeSaysWhatItDoesNotApply(t *testing.T) {
	obs := hubObs(t)
	u := hubUse(hub.Exact, "1.28.3", "1.28.3")
	u.Recipe.Manifest.RunConfig.Ports = []string{"8083"}
	u.Recipe.Manifest.ManifestPassthrough = map[string]any{"cwd": "/x"}
	items := ApplyHubRecipe(u, obs)
	if p := hubFind(items, "hub ports"); len(p) != 1 || !strings.Contains(p[0].Message, "8083") {
		t.Errorf("ports: %+v", p)
	}
	if p := hubFind(items, "hub manifest"); len(p) != 1 || !strings.Contains(p[0].Message, "cwd") {
		t.Errorf("manifest: %+v", p)
	}
}

func hubWholeUse(level hub.Level, wanted, version string) HubUse {
	p := hub.Package{Namespace: "francescolavra", Name: "postgres", Version: version, Arch: "x86_64", SHA256: hubSum}
	rec := &hub.Recipe{Package: p}
	rec.Manifest.Program = "postgres_" + version + "/postgres"
	rec.Manifest.Args = []string{"./postgres", "-D", "db"}
	rec.Manifest.Env = map[string]string{"LANG": "en_US.UTF-8"}
	return HubUse{Service: "db", Package: p, Level: level, Wanted: wanted, Why: "postgres starts a process per connection", Recipe: rec}
}

func hubWholeObs() *observe.Result {
	return &observe.Result{
		Sysroot:            "/tmp/whole",
		Program:            "/usr/lib/postgresql/16/bin/postgres",
		Argv:               []string{"postgres", "-c", "x=y"},
		Env:                []string{"POSTGRES_PASSWORD=secret"},
		WorkingDir:         "/var/lib/postgresql",
		UID:                999,
		GID:                999,
		Children:           3,
		ChildCalls:         []string{"fork"},
		StoppedEarly:       true,
		ConfigurationFiles: []string{"/etc/postgresql/postgresql.conf"},
		CreatedAtRuntime:   []string{"/var/lib/postgresql/data/PG_VERSION"},
		MachineFiles:       []string{"/proc/cpuinfo"},
		Vanished:           2,
		LocalNames:         true,
		Warnings:           []string{"kept"},
	}
}

func TestApplyHubWholeTakesTheProgramAndTheArgumentsOfTheHub(t *testing.T) {
	obs := hubWholeObs()
	items := ApplyHubWhole(hubWholeUse(hub.Near, "16.0", "16.2"), obs)
	if obs.Program != "/postgres" || !reflect.DeepEqual(obs.Argv, []string{"./postgres", "-D", "db"}) || obs.WorkingDir != "/" || obs.UID != 0 || obs.GID != 0 {
		t.Errorf("%+v", obs)
	}
	if !reflect.DeepEqual(obs.Env, []string{"POSTGRES_PASSWORD=secret", "LANG=en_US.UTF-8"}) {
		t.Errorf("env: %v", obs.Env)
	}
	if obs.Children != 0 || obs.ChildCalls != nil || obs.StoppedEarly || obs.ConfigurationFiles != nil || obs.CreatedAtRuntime != nil || obs.MachineFiles != nil || obs.Vanished != 0 || obs.LocalNames {
		t.Errorf("what was seen about the image was not cleared: %+v", obs)
	}
	if !reflect.DeepEqual(obs.Warnings, []string{"kept"}) {
		t.Errorf("warnings: %v", obs.Warnings)
	}
	text := hubText(items)
	for _, want := range []string{"francescolavra/postgres 16.2", "sha256 " + hubSum, "published by francescolavra", "the files observed from your image are not in it", "The 3 child processes", "configuration, extensions and data of your image are not carried", "not run on nanos", "same major series"} {
		if !strings.Contains(text, want) {
			t.Errorf("the report does not say %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "NOT COMPATIBLE") {
		t.Errorf("the same major series is not an incompatibility:\n%s", text)
	}
}

func TestApplyHubWholeSaysLoudlyThatDataIsNotCompatibleAcrossSeries(t *testing.T) {
	for _, wanted := range []string{"14.5", ""} {
		items := ApplyHubWhole(hubWholeUse(hub.Far, wanted, "16.2"), hubWholeObs())
		d := hubFind(items, "hub data")
		if len(d) != 1 || d[0].Kind != plan.Unsupported || !strings.Contains(d[0].Message, "DATA VOLUMES ARE NOT COMPATIBLE") {
			t.Errorf("wanted %q: %+v", wanted, d)
		}
	}
	if d := hubFind(ApplyHubWhole(hubWholeUse(hub.Exact, "16.2", "16.2"), hubWholeObs()), "hub data"); len(d) != 0 {
		t.Errorf("the same version: %+v", d)
	}
}

func TestHubItemsReachTheReport(t *testing.T) {
	in := newInput(&fake{}, &compose.Service{Name: "web"})
	si := in.Services["web"]
	si.HubItems = []plan.Item{{Service: "web", Kind: plan.Approximated, Key: "hub", Message: "from the hub"}}
	in.Services["web"] = si
	p := mustGen(t, in)
	if got := itemsOf(p, "web", plan.Approximated, "hub"); len(got) != 1 || got[0].Message != "from the hub" {
		t.Errorf("%+v", p.Report)
	}
}

func TestApplyHubWholeSaysWhatWasNotWritten(t *testing.T) {
	u := hubWholeUse(hub.Exact, "16.2", "16.2")
	u.Recipe.Skipped = 3
	items := ApplyHubWhole(u, hubWholeObs())
	if f := hubFind(items, "hub files"); len(f) != 2 || !strings.Contains(f[1].Message, "3 entries") {
		t.Errorf("%+v", f)
	}
	if f := hubFind(ApplyHubWhole(hubWholeUse(hub.Exact, "16.2", "16.2"), hubWholeObs()), "hub files"); len(f) != 1 {
		t.Errorf("nothing was left out: %+v", f)
	}
}

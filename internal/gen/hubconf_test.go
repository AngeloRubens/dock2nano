package gen

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/compose"
	"github.com/AngeloRubens/dock2nano/internal/hub"
	"github.com/AngeloRubens/dock2nano/internal/plan"
)

func hubConfOf(t *testing.T, sys, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(sys, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyHubRecipeDoesNotAddWhatAnIncludeItCannotExpandMayHold(t *testing.T) {
	obs := hubObs(t)
	obs.Argv = []string{"nginx"}
	obs.Sysroot = hubSysroot(t, map[string]string{"/etc/nginx/nginx.conf": "include /etc/nginx/$name.conf;\nworker_processes 4;\nevents {}\n"})
	items := ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	b := hubConfOf(t, obs.Sysroot, "etc/nginx/nginx.conf")
	if strings.Contains(b, "master_process") || strings.Contains(b, "daemon") || !strings.Contains(b, "worker_processes 1;") {
		t.Errorf("%s", b)
	}
	var found bool
	for _, it := range hubFind(items, "hub config") {
		if it.Kind == plan.Unsupported && strings.Contains(it.Message, "cannot expand") && strings.Contains(it.Message, "master_process off") {
			found = true
		}
	}
	if !found {
		t.Errorf("the report does not say the directives were not added:\n%s", hubText(items))
	}
}

func TestApplyHubRecipeExpandsTheIncludesOfTheFile(t *testing.T) {
	obs := hubObs(t)
	obs.Argv = []string{"nginx"}
	obs.Sysroot = hubSysroot(t, map[string]string{
		"/etc/nginx/nginx.conf":        "include /etc/nginx/modules/*.conf;\nworker_processes 4;\nevents {}\n",
		"/etc/nginx/modules/mod.conf":  "load_module /x.so;\n",
		"/etc/nginx/modules/other.txt": "daemon on;\n",
	})
	ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	b := hubConfOf(t, obs.Sysroot, "etc/nginx/nginx.conf")
	for _, want := range []string{"master_process off;", "daemon off;", "worker_processes 1;"} {
		if !strings.Contains(b, want) {
			t.Errorf("%q is not in the config:\n%s", want, b)
		}
	}
}

func TestApplyHubRecipeDoesNotRepeatADirectiveOfAnIncludedFile(t *testing.T) {
	obs := hubObs(t)
	obs.Argv = []string{"nginx"}
	obs.Sysroot = hubSysroot(t, map[string]string{
		"/etc/nginx/nginx.conf":       "include /etc/nginx/modules/*.conf;\nevents {}\n",
		"/etc/nginx/modules/one.conf": "daemon off;\n",
	})
	items := ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	b := hubConfOf(t, obs.Sysroot, "etc/nginx/nginx.conf")
	if strings.Contains(b, "daemon") || !strings.Contains(b, "master_process off;") {
		t.Errorf("%s", b)
	}
	if !strings.Contains(hubText(items), "already set by the included file /etc/nginx/modules/one.conf") {
		t.Errorf("%s", hubText(items))
	}
	// another value in the included file: nothing is changed
	obs.Sysroot = hubSysroot(t, map[string]string{
		"/etc/nginx/nginx.conf":       "include /etc/nginx/modules/*.conf;\nevents {}\n",
		"/etc/nginx/modules/one.conf": "daemon on;\n",
	})
	items = ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	if b := hubConfOf(t, obs.Sysroot, "etc/nginx/nginx.conf"); strings.Contains(b, "master_process") {
		t.Errorf("a file was changed although a directive conflicts:\n%s", b)
	}
	if c := hubFind(items, "hub config"); len(c) == 0 || c[len(c)-1].Kind != plan.Unsupported {
		t.Errorf("%s", hubText(items))
	}
}

func TestApplyHubRecipeDoesNotAddWhereThereIsLua(t *testing.T) {
	obs := hubObs(t)
	obs.Argv = []string{"nginx"}
	obs.Sysroot = hubSysroot(t, map[string]string{"/etc/nginx/nginx.conf": "events {}\nhttp {\n  init_by_lua_block { x = {1} }\n}\n"})
	items := ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	b := hubConfOf(t, obs.Sysroot, "etc/nginx/nginx.conf")
	if strings.Contains(b, "master_process") || !strings.Contains(hubText(items), "has a Lua block") {
		t.Errorf("%s\n%s", b, hubText(items))
	}
}

func TestApplyHubRecipeDoesNotRepeatADirectiveOfTheCommandLine(t *testing.T) {
	for _, argv := range [][]string{{"nginx", "-g", "daemon off;"}, {"nginx", "-gdaemon off;"}, {"nginx", "-g", "pid /x.pid; daemon off;"}} {
		obs := hubObs(t)
		obs.Argv = argv
		items := ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
		b := hubConfOf(t, obs.Sysroot, "etc/nginx/nginx.conf")
		if strings.Contains(b, "daemon") || !strings.Contains(b, "master_process off;") || !strings.Contains(b, "worker_processes 1;") {
			t.Errorf("%v:\n%s", argv, b)
		}
		if v := hubFind(items, "hub result"); len(v) != 1 || v[0].Kind != plan.Approximated {
			t.Errorf("%v: %+v", argv, v)
		}
	}
	obs := hubObs(t)
	obs.Argv = []string{"nginx", "-g", "daemon on;"}
	items := ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	if b := hubConfOf(t, obs.Sysroot, "etc/nginx/nginx.conf"); b != hubUserConf {
		t.Errorf("the file was changed although the command line conflicts:\n%s", b)
	}
	var found bool
	for _, it := range hubFind(items, "hub config") {
		if it.Kind == plan.Unsupported && strings.Contains(it.Message, "-g") && strings.Contains(it.Message, "daemon") {
			found = true
		}
	}
	if !found {
		t.Errorf("%s", hubText(items))
	}
}

func TestApplyHubRecipeAcceptsTheAttachedFormOfTheConfigOption(t *testing.T) {
	obs := hubObs(t)
	obs.Sysroot = hubSysroot(t, map[string]string{"/opt/n.conf": "events {}\n"})
	obs.Argv = []string{"nginx", "-c/opt/n.conf"}
	ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	if b := hubConfOf(t, obs.Sysroot, "opt/n.conf"); !strings.Contains(b, "master_process off;") {
		t.Errorf("%s", b)
	}
}

func TestApplyHubRecipeFollowsALinkThatStaysInTheImage(t *testing.T) {
	makers := []func(sys string){
		func(sys string) { os.Symlink("/opt/nginx", filepath.Join(sys, "etc/nginx")) },
		func(sys string) {
			os.MkdirAll(filepath.Join(sys, "etc/nginx"), 0o755)
			os.Symlink("/opt/nginx/nginx.conf", filepath.Join(sys, "etc/nginx/nginx.conf"))
		},
	}
	for i, mk := range makers {
		obs := hubObs(t)
		obs.Argv = []string{"nginx"}
		sys := hubSysroot(t, map[string]string{"/opt/nginx/nginx.conf": "events {}\n"})
		os.MkdirAll(filepath.Join(sys, "etc"), 0o755)
		mk(sys)
		obs.Sysroot = sys
		items := ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
		if b := hubConfOf(t, sys, "opt/nginx/nginx.conf"); !strings.Contains(b, "master_process off;") {
			t.Errorf("%d: the file the link leads to was not edited:\n%s\n%s", i, b, hubText(items))
		}
		link := "etc/nginx"
		if i == 1 {
			link = "etc/nginx/nginx.conf"
		}
		if fi, err := os.Lstat(filepath.Join(sys, link)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%d: the link was replaced: %v %v", i, fi, err)
		}
		if v := hubFind(items, "hub result"); len(v) != 1 || v[0].Kind != plan.Approximated {
			t.Errorf("%d: verdict: %+v", i, v)
		}
		if c := hubFind(items, "hub config"); len(c) == 0 || !strings.Contains(c[0].Message, "/opt/nginx/nginx.conf") || !strings.Contains(c[0].Message, "/etc/nginx/nginx.conf") {
			t.Errorf("%d: the report names only one of the two paths: %+v", i, c)
		}
	}
}

func TestApplyHubRecipePrefersTheConfigurationFileTheProgramRead(t *testing.T) {
	obs := hubObs(t)
	obs.Argv = []string{"nginx"}
	obs.Sysroot = hubSysroot(t, map[string]string{"/etc/nginx/nginx.conf": "events {}\n", "/usr/local/nginx/conf/nginx.conf": "events {}\n"})
	obs.ConfigurationFiles = []string{"/etc/group", "/usr/local/nginx/conf/nginx.conf"}
	ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	read := hubConfOf(t, obs.Sysroot, "usr/local/nginx/conf/nginx.conf")
	other := hubConfOf(t, obs.Sysroot, "etc/nginx/nginx.conf")
	if !strings.Contains(read, "master_process off;") || strings.Contains(other, "master_process") {
		t.Errorf("read:\n%s\nother:\n%s", read, other)
	}
}

func TestReplaceInRootIsAllOrNothing(t *testing.T) {
	sys := hubSysroot(t, nil)
	conf := filepath.Join(sys, "etc/n.conf")
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conf, []byte("original\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(conf, 0o640); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(sys)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	old := writeTemp
	defer func() { writeTemp = old }()
	writeTemp = func(f *os.File, data []byte) error {
		f.Write(data[:3])
		return errors.New("disk full")
	}
	if err := replaceInRoot(root, "etc/n.conf", []byte("new content\n"), 0o640); err == nil {
		t.Fatal("no error")
	}
	if b := hubConfOf(t, sys, "etc/n.conf"); b != "original\n" {
		t.Errorf("the original was changed: %q", b)
	}
	if left, _ := os.ReadDir(filepath.Dir(conf)); len(left) != 1 {
		t.Errorf("a temporary file is left: %v", left)
	}
	writeTemp = old
	if err := replaceInRoot(root, "etc/n.conf", []byte("new content\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(conf)
	if err != nil || fi.Mode().Perm() != 0o640 || hubConfOf(t, sys, "etc/n.conf") != "new content\n" {
		t.Errorf("%v %v", fi, err)
	}
	if left, _ := os.ReadDir(filepath.Dir(conf)); len(left) != 1 {
		t.Errorf("a temporary file is left: %v", left)
	}
}

func TestHubVolume(t *testing.T) {
	in := newInput(&fake{}, &compose.Service{Name: "web"})
	si := in.Services["web"]
	si.MinVolume = "2g"
	in.Services["web"] = si
	p := mustGen(t, in)
	if p.Services[0].Manifest.BaseVolumeSz != "2g" {
		t.Errorf("volume: %q", p.Services[0].Manifest.BaseVolumeSz)
	}
	if got := itemsOf(p, "web", plan.Note, "hub volume"); len(got) != 1 || !strings.Contains(got[0].Message, "at least 2g") {
		t.Errorf("%+v", p.Report)
	}
	si.MinVolume = "100m"
	in.Services["web"] = si
	p = mustGen(t, in)
	if p.Services[0].Manifest.BaseVolumeSz != "256m" {
		t.Errorf("a smaller size must not lower the volume: %q", p.Services[0].Manifest.BaseVolumeSz)
	}
	for _, r := range [][3]string{{"256m", "200m", "256m"}, {"256m", "1g", "1g"}, {"2g", "1500m", "2g"}, {"x", "1g", "x"}, {"256m", "", "256m"}} {
		if got := volumeAtLeast(r[0], r[1]); got != r[2] {
			t.Errorf("volumeAtLeast(%q, %q) = %q, want %q", r[0], r[1], got, r[2])
		}
	}
}

func TestApplyHubRecipeReadsTheEnvironmentOfTheImageToo(t *testing.T) {
	obs := hubObs(t)
	u := hubUse(hub.Exact, "1.28.3", "1.28.3")
	u.ImageEnv = []string{"LANG=C"}
	u.Recipe.Manifest.Env = map[string]string{"LANG": "C.UTF-8"}
	items := ApplyHubRecipe(u, obs)
	for _, e := range obs.Env {
		if strings.HasPrefix(e, "LANG=") {
			t.Errorf("a variable of the image was added again: %v", obs.Env)
		}
	}
	if e := hubFind(items, "hub environment"); len(e) != 1 || e[0].Kind != plan.Note {
		t.Errorf("%+v", e)
	}
}

func TestApplyHubWholeSaysTheCredentialsAreNotThoseOfTheImage(t *testing.T) {
	items := ApplyHubWhole(hubWholeUse(hub.Exact, "16.2", "16.2"), hubWholeObs())
	if c := hubFind(items, "hub credentials"); len(c) != 1 || c[0].Kind != plan.Unsupported || !strings.Contains(c[0].Message, "POSTGRES_PASSWORD") {
		t.Errorf("%+v", c)
	}
	obs := hubWholeObs()
	obs.Children = 1
	if got := hubText(ApplyHubWhole(hubWholeUse(hub.Exact, "16.2", "16.2"), obs)); !strings.Contains(got, "The 1 child process seen") {
		t.Errorf("%s", got)
	}
}

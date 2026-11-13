package gen

import (
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/compose"
	"github.com/AngeloRubens/dock2nano/internal/hub"
	"github.com/AngeloRubens/dock2nano/internal/plan"
)

func TestTheAlreadySingleProcessVerdictFollowsTheTrigger(t *testing.T) {
	obs := hubObs(t)
	obs.Argv = []string{"nginx"}
	obs.Sysroot = hubSysroot(t, map[string]string{"/etc/nginx/nginx.conf": "master_process off;\ndaemon off;\nworker_processes 1;\nevents {}\n"})
	u := hubUse(hub.Exact, "1.28.3", "1.28.3")
	u.Why = "the container stopped with status 1 before the end of the observation"
	v := hubFind(ApplyHubRecipe(u, obs), "hub result")
	if len(v) != 1 || v[0].Kind != plan.Note || !strings.Contains(v[0].Message, "what was seen when the program was observed comes from something else: the container stopped") || strings.Contains(v[0].Message, "child process") {
		t.Errorf("%+v", v)
	}
}

func TestAPartialResultIsNotCalledSuccess(t *testing.T) {
	obs := hubObs(t)
	obs.Argv = []string{"nginx"}
	obs.Sysroot = hubSysroot(t, map[string]string{"/etc/nginx/nginx.conf": "include /etc/$x.conf;\nworker_processes 4;\nevents {}\n"})
	items := ApplyHubRecipe(hubUse(hub.Exact, "1.28.3", "1.28.3"), obs)
	conf := hubConfOf(t, obs.Sysroot, "etc/nginx/nginx.conf")
	if !strings.Contains(conf, "worker_processes 1;") || strings.Contains(conf, "master_process") {
		t.Fatalf("%s", conf)
	}
	v := hubFind(items, "hub result")
	if len(v) != 1 || v[0].Kind != plan.Unsupported || !strings.Contains(v[0].Message, "only some of the directives") {
		t.Errorf("%+v", v)
	}
}

func TestTheVerdictSaysTheEnvironmentWasAddedOnlyWhenItWas(t *testing.T) {
	files := map[string]string{"/etc/nginx/nginx.conf": "events {}\n"}
	obs := hubObs(t)
	obs.Argv = []string{"nginx"}
	obs.Sysroot = hubSysroot(t, files)
	u := hubUse(hub.Exact, "1.28.3", "1.28.3")
	v := hubFind(ApplyHubRecipe(u, obs), "hub result")
	if len(v) != 1 || v[0].Kind != plan.Approximated || strings.Contains(v[0].Message, "environment variables") {
		t.Errorf("%+v", v)
	}
	obs = hubObs(t)
	obs.Argv = []string{"nginx"}
	obs.Sysroot = hubSysroot(t, files)
	u.Recipe.Manifest.Env = map[string]string{"LANG": "C"}
	v = hubFind(ApplyHubRecipe(u, obs), "hub result")
	if len(v) != 1 || !strings.Contains(v[0].Message, "environment variables of the hub recipe were added") {
		t.Errorf("%+v", v)
	}
}

func TestTheVolumeNoteSaysItComesFromTheTableOfTheTool(t *testing.T) {
	in := newInput(&fake{}, &compose.Service{Name: "web"})
	si := in.Services["web"]
	si.MinVolume, si.MinVolumeEvidence = "2g", "seen once"
	in.Services["web"] = si
	p := mustGen(t, in)
	got := itemsOf(p, "web", plan.Note, "hub volume")
	if len(got) != 1 || !strings.Contains(got[0].Message, "the table of this tool (hints/hub-tables.yaml) asks for") || !strings.Contains(got[0].Message, "(seen once;") {
		t.Errorf("%+v", got)
	}
}

func TestAnEnvironmentVariableThatNamesAPathOfTheHubPackageIsNotAdded(t *testing.T) {
	obs := hubObs(t)
	obs.Argv = []string{"nginx"}
	obs.Sysroot = hubSysroot(t, map[string]string{"/etc/nginx/nginx.conf": "events {}\n"})
	u := hubUse(hub.Exact, "1.28.3", "1.28.3")
	u.Recipe.Manifest.Env = map[string]string{
		"PGDATA": "/db",
		"LIST":   "/etc/nginx:/db",
		"KEPT":   "/etc/nginx",
		"PLAIN":  "C.UTF-8",
		"OPT":    "-x /nothing",
	}
	items := ApplyHubRecipe(u, obs)
	have := strings.Join(obs.Env, " ")
	for _, k := range []string{"PGDATA=", "LIST="} {
		if strings.Contains(have, k) {
			t.Errorf("%s was added: %v", k, obs.Env)
		}
	}
	for _, k := range []string{"KEPT=/etc/nginx", "PLAIN=C.UTF-8", "OPT=-x /nothing"} {
		if !strings.Contains(have, k) {
			t.Errorf("%s was not added: %v", k, obs.Env)
		}
	}
	if !hasItem(items, plan.Note, "hub environment", "LIST, PGDATA") {
		t.Errorf("%s", hubText(items))
	}
}

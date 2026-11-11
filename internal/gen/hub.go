package gen

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/AngeloRubens/dock2nano/internal/hub"
	"github.com/AngeloRubens/dock2nano/internal/observe"
	"github.com/AngeloRubens/dock2nano/internal/plan"
)

// HubUse is what the hub of ops packages gave for one service.
type HubUse struct {
	// Service is the compose name of the service.
	Service string

	// Package is the package of the hub the recipe comes from.
	Package hub.Package

	// Level says how near its version is to the version of the user.
	Level hub.Level

	// Wanted is the version of the image of the user, or "" when it is not
	// known.
	Wanted string

	// ImageEnv is the environment of the image of the user, which the observed
	// program may have overwritten; it is read together with the observed one.
	ImageEnv []string

	// Why says why the hub was consulted: what the observation showed, or the
	// reason of the row that requires it.
	Why string

	// Recipe is what was read from the package.
	Recipe *hub.Recipe
}

func (u HubUse) item(kind plan.Kind, key, msg string) plan.Item {
	return plan.Item{Service: u.Service, Kind: kind, Key: key, Message: msg}
}

// source names the package for the report: who published it, which version,
// and its digest.
func (u HubUse) source() string {
	p := u.Package
	return fmt.Sprintf("%s %s (sha256 %s, published by %s)", p.Ref(), p.Version, strings.ToLower(p.SHA256), p.Namespace)
}

// versions says how the version of the hub package differs from the one of
// the image of the user.
func (u HubUse) versions() string {
	v := u.Package.Version
	switch {
	case u.Level == hub.Exact:
		return "the hub package has the same version as your image (" + u.Wanted + ")"
	case u.Level == hub.Near:
		apart := hub.Apart(u.Wanted, v)
		if apart != "" {
			apart = " (" + apart + ")"
		}
		return "your image has " + u.Wanted + " and the hub package " + v + ": the same major series, not the same version" + apart + "; the same series is not the same behaviour"
	case u.Wanted == "":
		return "the version of your image is not known; the hub package, " + v + ", is the newest of that name"
	}
	return "your image has " + u.Wanted + " and the hub package " + v + ": another major series, so what it says may not fit your version"
}

// ApplyHubRecipe takes the recipe of a package of the hub for a program that
// did not start as one process: the arguments, the environment and the
// directives of the configuration that make the program run in one process. It
// changes obs, never the other files of the image, and never the ports and
// servers of the configuration of the user. The items say where the package
// comes from, that its files are not used, what was applied, and what the
// hub and the user have different.
func ApplyHubRecipe(u HubUse, obs *observe.Result) []plan.Item {
	items := []plan.Item{u.item(plan.Approximated, "hub", fmt.Sprintf("the hub of ops packages was consulted because %s. Package %s. The files of the image are the ones observed from your image. From the hub package only environment variables are taken; its arguments are reported, not applied. The directives that make the program run as one process come from hints/hub-tables.yaml of this tool, not from the package. Versions: %s", u.Why, u.source(), u.versions()))}
	items = append(items, hubArgs(u)...)
	envItems := hubEnv(u, obs)
	envAdded := len(hubFindItems(envItems, plan.Approximated)) > 0
	items = append(items, envItems...)
	cfgItems, cfgDone := hubConfig(u, obs)
	items = append(items, cfgItems...)
	items = append(items, hubNotApplied(u)...)
	items = append(items, hubVerdict(u, cfgDone, envAdded))
	return items
}

// hubArgs reports the arguments of the recipe. They are never applied: the
// command line of the program is the one that was observed, which names the
// files of the user.
func hubArgs(u HubUse) []plan.Item {
	args := u.Recipe.Manifest.Args
	if len(args) <= 1 {
		return nil
	}
	return []plan.Item{u.item(plan.Note, "hub arguments", "the hub recipe starts the program with "+strings.Join(args[1:], " ")+"; not applied: the command line is the one observed from your image, which names your files")}
}

// hubEnv adds the variables of the recipe that the image does not set. A
// variable the image sets keeps its value. Only names are reported.
func hubEnv(u HubUse, obs *observe.Result) []plan.Item {
	have := map[string]string{}
	for _, e := range append(slices.Clone(u.ImageEnv), obs.Env...) {
		if k, v, ok := strings.Cut(e, "="); ok {
			have[k] = v
		}
	}
	names := make([]string, 0, len(u.Recipe.Manifest.Env))
	for k := range u.Recipe.Manifest.Env {
		names = append(names, k)
	}
	sort.Strings(names)
	var added, differs, elsewhere []string
	for _, k := range names {
		v := u.Recipe.Manifest.Env[k]
		cur, ok := have[k]
		switch {
		case !ok && pathMissing(obs.Sysroot, v):
			// the value names a file of the layout of the hub package
			elsewhere = append(elsewhere, k)
		case !ok:
			obs.Env = append(obs.Env, k+"="+v)
			added = append(added, k)
		case cur != v:
			differs = append(differs, k)
		}
	}
	var items []plan.Item
	if len(added) > 0 {
		items = append(items, u.item(plan.Approximated, "hub environment", "variables of the hub recipe that your image does not set, added to the package: "+strings.Join(added, ", ")))
	}
	if len(elsewhere) > 0 {
		items = append(items, u.item(plan.Note, "hub environment", "variables of the hub recipe whose value is a path that is not in your image, so they were not added: "+strings.Join(elsewhere, ", ")))
	}
	if len(differs) > 0 {
		items = append(items, u.item(plan.Note, "hub environment", "the hub recipe sets other values than your image for "+strings.Join(differs, ", ")+"; yours are kept"))
	}
	return items
}

// hubNotApplied says what the recipe holds that is not applied because the
// compose file decides it.
func hubNotApplied(u HubUse) []plan.Item {
	var items []plan.Item
	rc := u.Recipe.Manifest.RunConfig
	if ports := append(slices.Clone(rc.Ports), rc.UDPPorts...); len(ports) > 0 {
		items = append(items, u.item(plan.Note, "hub ports", "the hub recipe lists the ports "+strings.Join(ports, ", ")+"; they are not applied: the ports of the package are those of your compose file"))
	}
	if keys := sortedKeys(u.Recipe.Manifest.ManifestPassthrough); len(keys) > 0 {
		items = append(items, u.item(plan.Note, "hub manifest", "the hub recipe sets "+strings.Join(keys, ", ")+" at the root of the manifest; not applied: the generator sets them from what was observed"))
	}
	return items
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// hubVerdict says what the result is, without more certainty than there is:
// nothing here is run on nanos.
func hubVerdict(u HubUse, state cfgState, envAdded bool) plan.Item {
	if state == cfgPartial {
		return u.item(plan.Unsupported, "hub result", "only some of the directives for one process could be set (see the hub config items), so the program is not made single-process by them; what was seen when the program was observed is not addressed: "+u.Why)
	}
	if state == cfgSatisfied {
		return u.item(plan.Note, "hub result", "the configuration is already single-process (every directive for one process is set in the file, on the command line or in an included file), so nothing was changed, and what was seen when the program was observed comes from something else: "+u.Why)
	}
	if state == cfgChanged {
		return u.item(plan.Approximated, "hub result", "the directives for one process were set in the copy of your configuration in the package"+envSaid(envAdded)+". It was not run on nanos here: it is not known whether the program now starts and keeps running. What was seen when it was observed: "+u.Why)
	}
	return u.item(plan.Unsupported, "hub result", "no directive for one process could be set in the configuration file (and the arguments of the recipe are never applied), so what was seen when the program was observed is not addressed: "+u.Why+". The package is as it was observed")
}

// hubFindItems are the items of a kind.
func hubFindItems(items []plan.Item, kind plan.Kind) []plan.Item {
	var out []plan.Item
	for _, it := range items {
		if it.Kind == kind {
			out = append(out, it)
		}
	}
	return out
}

func envSaid(added bool) string {
	if added {
		return ", and the environment variables of the hub recipe were added"
	}
	return ""
}

func settingsText(settings []hub.Setting) string {
	parts := make([]string, len(settings))
	for i, s := range settings {
		parts[i] = s.Name + " " + s.Value
	}
	return strings.Join(parts, "; ")
}

func changesText(changes []hub.Change) string {
	parts := make([]string, len(changes))
	for i, c := range changes {
		was := "was " + c.Old
		if c.Added {
			was = "was not set"
		}
		parts[i] = c.Name + " " + c.New + " (" + was + ")"
	}
	return strings.Join(parts, "; ")
}

// hubDivergence lists the directives whose values differ between the
// configuration of the hub package and the one of the user, as it is now. The
// directives that are set for one process are compared too: that is where the
// values of the hub package, which are not used, show.
func hubDivergence(u HubUse, sp hub.SingleProcess, hubText, userText string, elsewhere map[string]bool) []plan.Item {
	if hubText == "" {
		why := "the hub package has no configuration file at the usual places, so the two configurations were not compared"
		if len(u.Recipe.NotRead) > 0 {
			why = "the configuration file of the hub package (" + strings.Join(u.Recipe.NotRead, ", ") + ") is a link or too large to read, so the two configurations were not compared"
		}
		return []plan.Item{u.item(plan.Note, "hub config", why)}
	}
	names := slices.Clone(sp.Compare)
	// a setting that is set outside the file (on the command line, in an
	// included file) cannot be compared with the text of the file alone
	for _, s := range sp.Settings {
		if !elsewhere[s.Name] {
			names = append(names, s.Name)
		}
	}
	var parts []string
	for _, name := range names {
		h, m := hub.Values(hubText, name), hub.Values(userText, name)
		if !slices.Equal(h, m) {
			parts = append(parts, fmt.Sprintf("%s: hub [%s], yours [%s]", name, strings.Join(h, ", "), strings.Join(m, ", ")))
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return []plan.Item{u.item(plan.Note, "hub config", "the configuration of the hub package and yours differ ("+strings.Join(parts, "; ")+"); yours is kept, with the directives above")}
}

// ApplyHubWhole makes obs describe a package of the hub that is used whole:
// its files are in obs.Sysroot already (the caller wrote them there), its
// program and arguments are the ones of the recipe. What the observation of
// the image said about its own files and processes does not apply to it and is
// cleared. The items say that the files are the hub's, and, when the version is
// not in the same major series, that data of the user is not compatible.
func ApplyHubWhole(u HubUse, obs *observe.Result) []plan.Item {
	m := u.Recipe.Manifest
	base := path.Base(path.Clean(m.Program))
	obs.Program = "/" + base
	obs.Argv = slices.Clone(m.Args)
	if len(obs.Argv) == 0 {
		obs.Argv = []string{"./" + base}
	}
	obs.WorkingDir = "/"
	obs.UID, obs.GID = 0, 0
	dropped := ""
	if obs.Children > 0 {
		dropped = fmt.Sprintf(" The %d %s seen when your image was observed do not apply to this build.", obs.Children, plural(obs.Children, "child process", "child processes"))
	}
	obs.Children, obs.ChildCalls = 0, nil
	obs.StoppedEarly = false
	obs.ConfigurationFiles, obs.CreatedAtRuntime, obs.MachineFiles = nil, nil, nil
	obs.Vanished = 0
	obs.LocalNames = false
	envItems := hubEnv(u, obs)
	items := []plan.Item{
		u.item(plan.Approximated, "hub package", fmt.Sprintf("this program must use the build of the hub whole (%s). The package is %s, with its own files, program and arguments; the files observed from your image are not in it.%s Versions: %s", u.Why, u.source(), dropped, u.versions())),
		u.item(plan.Unsupported, "hub files", "the configuration, extensions and data of your image are not carried: the package holds the files of the hub build, so your configuration files do not apply to it"),
		u.item(plan.Note, "hub result", "it was not run on nanos here: it is not known whether the package starts and answers"),
	}
	items = append(items, hubCredentials(u)...)
	if u.Recipe.Skipped > 0 {
		items = append(items, u.item(plan.Note, "hub files", fmt.Sprintf("%d entries of the package (devices, or links that point at nothing written) were not written", u.Recipe.Skipped)))
	}
	items = append(items, envItems...)
	switch u.Level {
	case hub.Near:
		items = append(items, u.item(plan.Note, "hub data", "your image and this build are in the same major series; the data of your image is not carried anyway"))
	case hub.Exact:
	default:
		items = append(items, u.item(plan.Unsupported, "hub data", "DATA VOLUMES ARE NOT COMPATIBLE: this build is the nearest of the hub, and its version is in another major series than the one of your image ("+u.versions()+"); data written by your version cannot be read by it"))
	}
	return items
}

// plural gives the one form for 1 and the other for the rest.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// hubCredentials says what was seen about the passwords of the packages of the
// hub that were tried in a CI run, and of no other.
func hubCredentials(u HubUse) []plan.Item {
	p := u.Package
	switch {
	case p.Ref() == "francescolavra/postgres" && (p.Version == "16.0" || p.Version == "16.2"):
		return []plan.Item{u.item(plan.Unsupported, "hub credentials", "in a CI run the hub package francescolavra/postgres "+p.Version+" accepted the user postgres without a password (none was sent), whether or not POSTGRES_PASSWORD was set; the password of your image should not be taken to be in effect")}
	case p.Ref() == "eyberg/mysql" && p.Version == "5.7.29":
		return []plan.Item{u.item(plan.Unsupported, "hub credentials", "in a CI run the hub package eyberg/mysql 5.7.29 accepted root/root, which its notes give as the default login; whether MYSQL_ROOT_PASSWORD is applied was not told apart from that")}
	}
	return nil
}

package gen

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
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
		return "your image has " + u.Wanted + " and the hub package " + v + ": the same major series, not the same version"
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
	items := []plan.Item{u.item(plan.Approximated, "hub", fmt.Sprintf("the hub of ops packages was consulted because %s. Package %s. The files of the image are the ones observed from your image; from the hub only the recipe is taken (arguments, directives of the configuration, environment). Versions: %s", u.Why, u.source(), u.versions()))}
	items = append(items, hubArgs(u)...)
	items = append(items, hubEnv(u, obs)...)
	cfgItems, cfgDone := hubConfig(u, obs)
	items = append(items, cfgItems...)
	items = append(items, hubNotApplied(u)...)
	items = append(items, hubVerdict(u, cfgDone))
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
	for _, e := range obs.Env {
		if k, v, ok := strings.Cut(e, "="); ok {
			have[k] = v
		}
	}
	names := make([]string, 0, len(u.Recipe.Manifest.Env))
	for k := range u.Recipe.Manifest.Env {
		names = append(names, k)
	}
	sort.Strings(names)
	var added, differs []string
	for _, k := range names {
		v := u.Recipe.Manifest.Env[k]
		cur, ok := have[k]
		switch {
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
func hubVerdict(u HubUse, changed bool) plan.Item {
	if changed {
		return u.item(plan.Approximated, "hub result", "the hub recipe was applied to the files of your image. It was not run on nanos here: it is not known whether the program now starts and keeps running. What was seen when it was observed: "+u.Why)
	}
	return u.item(plan.Unsupported, "hub result", "nothing was applied that changes how the program is started (the arguments of the recipe are never applied, and no single-process directive could be set in the configuration file), so the program is as it was observed and it still does not start as one process. What was seen when it was observed: "+u.Why)
}

// hubConfig sets, in the configuration file of the user, the directives that
// make the program run as one process, with the values of the table (never
// the values of the hub package). It reports the directives and where the
// configuration of the hub package and the one of the user differ. The file
// is reached only through the sysroot: a link that leaves it is refused. The
// second result tells whether the file was changed.
func hubConfig(u HubUse, obs *observe.Result) ([]plan.Item, bool) {
	program := path.Base(obs.Program)
	sp, ok := hub.SingleProcessFor(program)
	if !ok {
		return []plan.Item{u.item(plan.Note, "hub config", "no directives that make "+program+" run as one process are known; its configuration was not changed")}, false
	}
	hubText := ""
	for _, p := range sp.ConfigPaths {
		if data, ok := u.Recipe.Files[p]; ok {
			hubText = string(data)
			break
		}
	}
	fail := func(why string) ([]plan.Item, bool) {
		return []plan.Item{u.item(plan.Unsupported, "hub config", why+"; the directives "+settingsText(sp.Settings)+" were not applied")}, false
	}
	root, err := os.OpenRoot(obs.Sysroot)
	if err != nil {
		return fail("the files carried from your image cannot be opened (" + err.Error() + ")")
	}
	defer root.Close()
	inImage, err := userConfig(root, obs, sp)
	if err != nil {
		return fail(err.Error())
	}
	data, mode, err := readInRoot(root, inImage)
	if err != nil {
		return fail("the configuration file " + inImage + " cannot be read (" + err.Error() + ")")
	}
	addMissing := !hub.HasTopLevelInclude(string(data))
	out, changes, skipped := hub.SetTopLevel(string(data), sp.Settings, addMissing)
	var items []plan.Item
	if len(changes) > 0 {
		f, err := root.OpenFile(strings.TrimPrefix(inImage, "/"), os.O_WRONLY|os.O_TRUNC, mode)
		if err == nil {
			_, err = f.WriteString(out)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			return fail("the configuration file " + inImage + " cannot be written (" + err.Error() + ")")
		}
		items = append(items, u.item(plan.Approximated, "hub config", "in your "+inImage+", at the top level: "+changesText(changes)+" (the values for one process; the values of the hub package are not used). Everything else in your file, the ports, servers and paths included, is as it was"))
	} else if len(skipped) == 0 {
		items = append(items, u.item(plan.Note, "hub config", "your "+inImage+" already has "+settingsText(sp.Settings)+" at the top level; nothing was changed"))
	}
	if len(skipped) > 0 {
		items = append(items, u.item(plan.Unsupported, "hub config", "your "+inImage+" includes other files at the top level, which may hold the directives; "+settingsText(skipped)+" were not added, because a second copy would stop the program; if it still starts several processes, set them in the included file"))
	}
	return append(items, hubDivergence(u, sp, hubText, out)...), len(changes) > 0
}

// userConfig finds the configuration file of the user among the files carried
// from the image: the one named by the option of the program, else the first
// of the usual places that exists. It returns the path inside the image.
func userConfig(root *os.Root, obs *observe.Result, sp hub.SingleProcess) (string, error) {
	places := sp.ConfigPaths
	for i, a := range obs.Argv {
		if a == sp.ConfigFlag && i+1 < len(obs.Argv) {
			if !strings.HasPrefix(obs.Argv[i+1], "/") {
				return "", fmt.Errorf("the program is started with %s %s, a relative path, which is not followed", sp.ConfigFlag, obs.Argv[i+1])
			}
			places = []string{obs.Argv[i+1]}
			break
		}
	}
	for _, p := range places {
		fi, err := root.Lstat(strings.TrimPrefix(path.Clean(p), "/"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("the configuration file %s cannot be reached inside the files carried from your image (%v), so it is not edited", p, err)
		}
		if !fi.Mode().IsRegular() {
			return "", fmt.Errorf("the configuration file %s is not a regular file (a link, for example), so it is not edited", p)
		}
		return p, nil
	}
	return "", fmt.Errorf("the configuration file was not found among the files carried from your image (looked at %s)", strings.Join(places, ", "))
}

// readInRoot reads a file of the image through the root of the sysroot.
func readInRoot(root *os.Root, p string) ([]byte, fs.FileMode, error) {
	f, err := root.Open(strings.TrimPrefix(p, "/"))
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	data, err := io.ReadAll(f)
	return data, fi.Mode().Perm(), err
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
func hubDivergence(u HubUse, sp hub.SingleProcess, hubText, userText string) []plan.Item {
	if hubText == "" {
		why := "the hub package has no configuration file at the usual places, so the two configurations were not compared"
		if len(u.Recipe.NotRead) > 0 {
			why = "the configuration file of the hub package (" + strings.Join(u.Recipe.NotRead, ", ") + ") is a link or too large to read, so the two configurations were not compared"
		}
		return []plan.Item{u.item(plan.Note, "hub config", why)}
	}
	names := slices.Clone(sp.Compare)
	for _, s := range sp.Settings {
		names = append(names, s.Name)
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
		dropped = fmt.Sprintf(" The %d child processes seen when your image was observed do not apply to this build.", obs.Children)
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

package gen

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

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

	// ComposeEnv is the environment that the compose file gives the service
	// (environment and env_file, as ServiceEnv reads them); it wins over the
	// image and the observed one when the note is filled.
	ComposeEnv []string

	// EnvUncertain says that ServiceEnv had something to report for the
	// service (a variable it could not expand or leave set, a file it could
	// not read), so the environment above may not be the one of the service.
	EnvUncertain bool

	// PackageFacts is what was seen of this package that the note needs, from
	// the row; the note names it as {HUB_FACTS}.
	PackageFacts string

	// Why says why the hub was consulted: what the observation showed, or the
	// reason of the row that requires it.
	Why string

	// EntrypointNote is the note of the row of the required builds about what
	// the entrypoint of the image does and the package does not; empty when
	// there is none.
	EntrypointNote string

	// Superuser is the role the package has as its superuser, from the row of
	// the required builds; empty when it is not known. The note of the row can
	// name it as {HUB_SUPERUSER}.
	Superuser string

	// PostUpScript is the template of the script that the user runs after
	// "ops compose up", from the row of the required builds; empty when the row
	// has none.
	PostUpScript string

	// Instance is the name of the instance that ops gives the service: the pkg
	// name of compose.yaml. Empty means the compose name of the service.
	Instance string

	// PasswordFile is the path on this machine of the file that the compose
	// file gives to POSTGRES_PASSWORD_FILE (the file of a secret, as the host
	// has it); empty when there is none or it is not known. The script reads
	// the file when it runs; the tool never reads it.
	PasswordFile string

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
	envItems := hubEnv(u, obs, false)
	envAdded := len(hubFindItems(envItems, plan.Approximated)) > 0
	items = append(items, envItems...)
	cfgItems, cfgDone := hubConfig(u, obs)
	items = append(items, cfgItems...)
	items = append(items, hubNotApplied(u, false)...)
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
// variable the image sets keeps its value. Only names are reported. For a
// package used whole (whole) the files are those of the package, so a value
// that names a path is checked against the package, and the image of the user
// is not the place the paths are looked for.
func hubEnv(u HubUse, obs *observe.Result, whole bool) []plan.Item {
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
		case !ok && !whole && pathMissing(obs.Sysroot, v):
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
		if whole {
			items = append(items, u.item(plan.Note, "hub environment", "the hub recipe sets other values than your image for "+strings.Join(differs, ", ")+"; the package gets the values of your image, the ones of the recipe are not applied"))
		} else {
			items = append(items, u.item(plan.Note, "hub environment", "the hub recipe sets other values than your image for "+strings.Join(differs, ", ")+"; yours are kept"))
		}
	}
	return items
}

// hubNotApplied says what the recipe holds that is not applied because the
// compose file decides it. For a package used whole (whole) the keys of the
// root of the manifest are not read either, and the words say why.
func hubNotApplied(u HubUse, whole bool) []plan.Item {
	var items []plan.Item
	rc := u.Recipe.Manifest.RunConfig
	if ports := append(slices.Clone(rc.Ports), rc.UDPPorts...); len(ports) > 0 {
		items = append(items, u.item(plan.Note, "hub ports", "the hub recipe lists the ports "+strings.Join(ports, ", ")+"; they are not applied: the ports of the package are those of your compose file"))
	}
	if keys := sortedKeys(u.Recipe.Manifest.ManifestPassthrough); len(keys) > 0 {
		reasons := make([]string, len(keys))
		for i, k := range keys {
			reasons[i] = k + " " + hubKeyReason(k, whole)
		}
		items = append(items, u.item(plan.Note, "hub manifest", "the hub recipe sets "+strings.Join(keys, ", ")+" at the root of the manifest; not applied: "+strings.Join(reasons, "; ")))
	}
	return items
}

// hubKeyReason says why a key of the root of the recipe manifest is not applied.
func hubKeyReason(key string, whole bool) string {
	switch key {
	case "cwd", "uid", "gid":
		if whole {
			return "is not read: the package of the hub runs as root in /, and the generator sets cwd / and no uid"
		}
		return "comes from the observation"
	case "exec_wait_for_ip4_secs":
		return "is set by the generator for a service that depends_on a converted service, and not otherwise"
	}
	return "is not read by the generator"
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
	var droppedArgs []string
	if len(obs.Argv) > 1 {
		droppedArgs = slices.Clone(obs.Argv[1:])
	}
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
	obs.PassedOver, obs.NotRun, obs.ChildLaunchers = nil, nil, nil
	obs.StoppedEarly = false
	obs.ConfigurationFiles, obs.CreatedAtRuntime, obs.MachineFiles = nil, nil, nil
	obs.Vanished = 0
	obs.LocalNames = false
	obs.Warnings = dropFileWarnings(obs.Warnings)
	envItems := hubEnv(u, obs, true)
	items := []plan.Item{
		u.item(plan.Approximated, "hub package", fmt.Sprintf("this program must use the build of the hub whole (%s). The package is %s, with its own files, program and arguments; the files observed from your image are not in it.%s Versions: %s", u.Why, u.source(), dropped, u.versions())),
		u.item(plan.Unsupported, "hub files", "the configuration, extensions and data of your image are not carried: the package holds the files of the hub build, so your configuration files do not apply to it"),
		u.item(plan.Note, "hub result", "not run by this conversion; see the runs in the hub entrypoint item"),
	}
	if len(droppedArgs) > 0 && !slices.Equal(droppedArgs, argsAfterName(m.Args)) {
		items = append(items, u.item(plan.Unsupported, "hub arguments", "the arguments of the program of your image and compose file are not used, the package starts with its own ("+strings.Join(m.Args, " ")+"); "+fmt.Sprintf("%d %s dropped, not printed: they can hold a secret", len(droppedArgs), plural(len(droppedArgs), "argument was", "arguments were"))))
	}
	items = append(items, hubCredentials(u)...)
	if u.EntrypointNote != "" {
		env, fixed := noteInputs(u, obs)
		msg := "the entrypoint of your image does not run with the package of the hub: " + fillPlaceholders(u.EntrypointNote, env, fixed)
		if u.PostUpScript != "" {
			msg += " " + postUpSaid(u, obs)
		}
		items = append(items, u.item(plan.Unsupported, "hub entrypoint", msg))
	}
	items = append(items, hubNotApplied(u, true)...)
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

// noteInputs are the environment and the fixed variables a note or a script of
// the row is filled with.
func noteInputs(u HubUse, obs *observe.Result) ([]string, map[string]string) {
	instance := u.Instance
	if instance == "" {
		instance = u.Service
	}
	fixed := map[string]string{"HUB_SUPERUSER": "the superuser of the package (not known)", "HUB_FACTS": "nothing was recorded of the databases of this package", "HUB_INSTANCE": instance}
	if u.Superuser != "" {
		fixed["HUB_SUPERUSER"] = u.Superuser
	}
	if u.PackageFacts != "" {
		fixed["HUB_FACTS"] = u.PackageFacts
	}
	if u.EnvUncertain {
		fixed["HUB_ENV_UNCERTAIN"] = "yes"
	}
	// the image first, the observed environment over it, what the compose file
	// gives the service over both: the order in which the manifest gets them
	env := append(slices.Clone(u.ImageEnv), obs.Env...)
	env = append(env, u.ComposeEnv...)
	// whether a password is given is read from the names only, never the value
	last := map[string]string{}
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok {
			last[k] = v
		}
	}
	if last["POSTGRES_PASSWORD"] != "" {
	}
	if f := last["POSTGRES_PASSWORD_FILE"]; f != "" && u.PasswordFile == "" {
		fixed["HUB_PWLOST"] = f
	}
	return env, fixed
}

// createsRe finds the lines of a filled script that create something.
var createsRe = regexp.MustCompile(`(?m)^ensure '(role|database)'`)

// postUp fills the script of the row for the service and says why there is
// none: the second result is "" when there is a script, or when the row has
// none (nothing is said then), and the reason otherwise. The instance is the
// name of the package in compose.yaml, which ops gives the instance.
func postUp(u HubUse, obs *observe.Result) (script, why string) {
	// without a note the report has no item that says the script exists
	if u.PostUpScript == "" || u.EntrypointNote == "" {
		return "", ""
	}
	if u.Superuser == "" {
		return "", "the superuser of the package is not known"
	}
	if u.EnvUncertain {
		return "", "the environment of the service could not be read completely (see the other items), so the names of the database and the role are not certain"
	}
	env, fixed := noteInputs(u, obs)
	have := noteHave(u.PostUpScript, env, fixed)
	switch {
	case have["HUB_BADUTF8"] != "":
		return "", "a name is not valid UTF-8, and quoting it would make another name"
	case have["HUB_CONTROL"] != "":
		return "", "a name holds a control or format character (a line break, a tab, a right-to-left mark), and quoting it would make another name"
	case have["HUB_TOOLONG"] != "":
		return "", "a name is longer than 63 bytes, which postgres would cut to another name"
	}
	text := fillText(u.PostUpScript, env, fixed, false)
	for _, bad := range []string{"<your ", notShown, "<a name with control"} {
		if strings.Contains(text, bad) {
			return "", "the script needs a value that is not set here (or is a secret, which is never written)"
		}
	}
	if !createsRe.MatchString(text) {
		return "", "the names this tool knows give nothing to create"
	}
	return text, ""
}

// PostUpScript is the script that the user runs after "ops compose up": the
// template of the row filled for the service (see postUp), "" when there is
// none. u.Instance is the name of the instance. It is called after
// ApplyHubWhole, so that obs.Env is the one of the package.
func PostUpScript(u HubUse, obs *observe.Result) string {
	s, _ := postUp(u, obs)
	return s
}

// postUpSaid is what the entrypoint item says of the script. It does not say
// that a file was written: the items are made before the files are, and the
// list of the written files is what says it.
func postUpSaid(u HubUse, obs *observe.Result) string {
	script, why := postUp(u, obs)
	if script == "" {
		return "No script is written for it: " + why + "."
	}
	var what []string
	if strings.Contains(script, "\nensure 'role'") {
		what = append(what, "the role POSTGRES_USER")
	}
	if strings.Contains(script, "\nensure 'database'") {
		what = append(what, "the database")
	}
	_, fixed := noteInputs(u, obs)
	pw := "It sets no password."
	switch {
	case fixed["HUB_PWFILE"] != "":
		pw = "It sets the password of the role (ALTER ROLE) from the file " + fixed["HUB_PWFILE"] + ", the file of the secret of the compose file on this machine, read when the script runs (the variable POSTGRES_PASSWORD of the shell comes first, if it is set); the value is not in the script or in this report, and goes to psql only through PGPASSWORD and standard input, never as an argument."
	case fixed["HUB_PWENV"] != "":
		pw = "It sets the password of the role (ALTER ROLE) from the variable POSTGRES_PASSWORD of the shell that runs it (export it first; the value is not in the script or in this report, and goes to psql only through PGPASSWORD and standard input, never as an argument); if the variable is empty no password is set."
	case fixed["HUB_PWLOST"] != "":
		pw = "It sets no password: POSTGRES_PASSWORD_FILE is " + fixed["HUB_PWLOST"] + ", which is not the target of a file secret of the compose file, so the file on this machine is not known."
	}
	return "A script, post-up-<the pkg name of the service in compose.yaml>.sh (named in the item post-up script, and in the list of the written files), is written next to compose.yaml; it creates " + strings.Join(what, " and ") + ", each only if it is absent. It does not do what the rest of the entrypoint does (the files of /docker-entrypoint-initdb.d, initdb). ops compose up does NOT run it and neither does this tool: run it yourself after ops compose up, once the database is up (it waits up to 120 seconds for the port 5432). A service that connects to the database before the script has made it may exit; the timing was not measured (in one CI run a Spring backend first connected about 50 seconds after the start, after the database had been made), so start the database first, run the script, then the rest, if that happens. It needs psql on your machine (the script says how to install it). " + pw + " It was not run against the package by this tool."
}

// argsAfterName is the arguments of a command line without its first word.
func argsAfterName(args []string) []string {
	if len(args) <= 1 {
		return nil
	}
	return args[1:]
}

// IsFileWarning tells whether an observe note describes the files of the image
// (what was carried, made or not carried), which does not apply to a package
// of the hub used whole. Notes about the run itself (a stop with a status, the
// services that ran beside it) are not.
func IsFileWarning(w string) bool {
	if strings.HasPrefix(w, observe.NoteMorePrefix) && strings.HasSuffix(w, observe.NoteMoreSuffix) {
		return true
	}
	if strings.Contains(w, observe.NoteObservedBeside) {
		return true
	}
	for _, p := range observe.FileNotePrefixes {
		if strings.HasPrefix(w, p) {
			return true
		}
	}
	return false
}

func dropFileWarnings(ws []string) []string {
	var out []string
	for _, w := range ws {
		if !IsFileWarning(w) {
			out = append(out, w)
		}
	}
	return out
}

// secretName tells whether the name of a variable says that it holds a secret.
// The value of such a variable is never written into a note.
func secretName(name string) bool { return hub.SecretName(name) }

// notShown stands in a note for the value of a variable that holds a secret.
const notShown = "<not shown>"

var (
	placeholderRe = hub.PlaceholderRe
	sectionRe     = hub.SectionRe
	spacesRe      = regexp.MustCompile(` {2,}`)
	bracesRe      = regexp.MustCompile(`\{([^{}]*)\}`)
)

// firstSet is the value of the first of the names that is set. A name that
// says a secret is never read.
func firstSet(have map[string]string, names string) (string, bool) {
	for _, n := range strings.Split(names, ",") {
		n = strings.TrimSpace(n)
		if secretName(n) {
			continue
		}
		if v, ok := have[n]; ok {
			return v, true
		}
	}
	return "", false
}

// condition tells whether the test of a section holds: terms joined by " and ",
// all of which must hold. A term is NAMES (one of them is set) or
// NAMES!=OTHER (it is set and differs from the variable OTHER, or from the
// text between quotes), and "not " before it turns it round.
func condition(have map[string]string, expr string) bool {
	for _, t := range strings.Split(expr, " and ") {
		t = strings.TrimSpace(t)
		neg := strings.HasPrefix(t, "not ")
		if neg {
			t = strings.TrimSpace(strings.TrimPrefix(t, "not "))
		}
		if term(have, t) == neg {
			return false
		}
	}
	return true
}

func term(have map[string]string, expr string) bool {
	lhs, rhs, ne := strings.Cut(expr, "!=")
	v, ok := firstSet(have, lhs)
	if !ok {
		return false
	}
	if !ne {
		return true
	}
	rhs = strings.TrimSpace(rhs)
	if len(rhs) >= 2 && strings.HasPrefix(rhs, `"`) && strings.HasSuffix(rhs, `"`) {
		return v != rhs[1:len(rhs)-1]
	}
	if secretName(rhs) {
		// the value of a secret is not read, so it cannot be compared
		return false
	}
	return v != have[rhs]
}

// plainText makes a value safe to write in a note: a line break or another
// control character would let a value fake a line of the report, so it is
// written as an escape.
func plainText(v string) string {
	var b strings.Builder
	for _, r := range v {
		switch {
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == 0x2028 || r == 0x2029:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sqlIdent writes a value as the identifier of a command that is inside single
// quotes of a shell: in double quotes, with the double quotes doubled, as the
// entrypoint of the image does for the name of a database, and with a single
// quote written as the shell needs it.
func sqlIdent(v string) string {
	q := `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
	return strings.ReplaceAll(q, `'`, `'\''`)
}

// sqlLiteral writes a value as a string literal of SQL (a single quote doubled)
// for a command that is inside single quotes of a shell, with each single
// quote written as the shell needs it.
func sqlLiteral(v string) string {
	q := `'` + strings.ReplaceAll(v, `'`, `''`) + `'`
	return strings.ReplaceAll(q, `'`, `'\''`)
}

// shellWord writes a value as one word of a shell, in single quotes.
func shellWord(v string) string {
	return `'` + strings.ReplaceAll(v, `'`, `'\''`) + `'`
}

// fillPlaceholders fills a note of a row of the table of required builds.
// {NAME} and {NAME|default} are the value of the variable NAME in env, or the
// default, or "<your NAME>"; {A,B} takes the first of A and B that is set;
// {NAME:ident} writes the value as a quoted identifier. fixed holds variables
// of the tool itself (they win over env). [[if TEST]]text[[end]] and
// [[unless TEST]]text[[end]] keep the text when the test holds or does not;
// TEST is NAMES or NAMES!=OTHER (see condition); sections do not nest. The
// value of a variable whose name says PASSWORD, SECRET, TOKEN or KEY is never
// written: the note says <not shown>. Values are written without line breaks
// and control characters.
func fillPlaceholders(note string, env []string, fixed map[string]string) string {
	return fillText(note, env, fixed, true)
}

// fillText is fillPlaceholders; squeeze says whether runs of spaces become one
// space and the text is trimmed (a note is prose, a script keeps its layout).
func fillText(note string, env []string, fixed map[string]string, squeeze bool) string {
	// {{ and }} are literal braces: they are put aside while the text is read
	note = strings.ReplaceAll(strings.ReplaceAll(note, "{{", "\x01"), "}}", "\x02")
	have := noteHave(note, env, fixed)
	text := fillWith(note, have, squeeze)
	return strings.ReplaceAll(strings.ReplaceAll(text, "\x01", "{"), "\x02", "}")
}

// maxIdent is the longest name that postgres keeps (63 bytes); a longer one is
// cut, and the cut name is another name.
const maxIdent = 63

// noteHave is the variables a note is filled from: the environment (the last
// entry of a name wins, and an empty value unsets the name, as the entrypoint
// of the image reads it), then the fixed ones. It also sets the flags of the
// tool: HUB_CONTROL (a name that is quoted holds a control or format character
// or is not UTF-8, so quoting it would make another name), HUB_BADUTF8 (the
// last case) and HUB_TOOLONG (a quoted name longer than 63 bytes).
func noteHave(note string, env []string, fixed map[string]string) map[string]string {
	have := map[string]string{}
	for _, e := range env {
		// the names the tool fills itself (HUB_...) are never read from the environment
		if k, v, ok := strings.Cut(e, "="); ok && !strings.HasPrefix(k, "HUB_") {
			if v == "" {
				delete(have, k)
			} else {
				have[k] = v
			}
		}
	}
	for k, v := range fixed {
		have[k] = v
	}
	for _, m := range bracesRe.FindAllStringSubmatch(note, -1) {
		if sub := placeholderRe.FindStringSubmatch(m[1]); sub != nil && sub[2] != "" {
			v, ok := firstSet(have, sub[1])
			if !ok {
				continue
			}
			if !utf8.ValidString(v) {
				have["HUB_BADUTF8"] = "yes"
				have["HUB_CONTROL"] = "yes"
			} else if plainText(v) != v {
				have["HUB_CONTROL"] = "yes"
			}
			if len(v) > maxIdent && !strings.HasPrefix(sub[1], "HUB_PWFILE") {
				have["HUB_TOOLONG"] = "yes"
			}
		}
	}
	return have
}

// fillWith is the second half of fillText.
func fillWith(note string, have map[string]string, squeeze bool) string {
	note = sectionRe.ReplaceAllStringFunc(note, func(m string) string {
		sub := sectionRe.FindStringSubmatch(m)
		if condition(have, strings.TrimSpace(sub[2])) == (sub[1] == "if") {
			return sub[3]
		}
		return ""
	})
	// a removed section leaves its neighbours' spaces
	if squeeze {
		note = strings.TrimSpace(spacesRe.ReplaceAllString(note, " "))
	}
	var b strings.Builder
	for {
		i := strings.IndexByte(note, '{')
		if i < 0 {
			b.WriteString(note)
			return b.String()
		}
		j := strings.IndexByte(note[i:], '}')
		if j < 0 {
			b.WriteString(note)
			return b.String()
		}
		j += i
		sub := placeholderRe.FindStringSubmatch(note[i+1 : j])
		if sub == nil {
			// not a placeholder: the brace is text
			b.WriteString(note[:i+1])
			note = note[i+1:]
			continue
		}
		b.WriteString(note[:i])
		names, quote, def, hasDef := sub[1], sub[2], sub[3], strings.Contains(note[i+1:j], "|")
		secret := false
		for _, n := range strings.Split(names, ",") {
			secret = secret || secretName(n)
		}
		v, ok := firstSet(have, names)
		switch {
		case secret:
			b.WriteString(notShown)
		case ok:
			b.WriteString(fillValue(v, quote))
		case hasDef:
			b.WriteString(fillValue(def, quote))
		default:
			b.WriteString("<your " + strings.ReplaceAll(names, ",", " or ") + ">")
		}
		note = note[j+1:]
	}
}

func fillValue(v, quote string) string {
	p := plainText(v)
	if quote != "" && p != v {
		return "<a name with control characters, to be quoted by hand>"
	}
	switch quote {
	case ":ident":
		return sqlIdent(p)
	case ":lit":
		return sqlLiteral(p)
	case ":sh":
		return shellWord(p)
	}
	return p
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
	case p.Ref() == "eyberg/postgresql" && p.Version == "11.3.0":
		return []plan.Item{u.item(plan.Unsupported, "hub credentials", "in the hub package eyberg/postgresql 11.3.0 the superuser is eyberg; role postgres does not exist (run 44909583390); an application that connects as postgres or as POSTGRES_USER is refused")}
	case p.Ref() == "eyberg/mysql" && p.Version == "5.7.29":
		return []plan.Item{u.item(plan.Unsupported, "hub credentials", "in a CI run the hub package eyberg/mysql 5.7.29 accepted root/root, which its notes give as the default login; whether MYSQL_ROOT_PASSWORD is applied was not told apart from that")}
	}
	return nil
}

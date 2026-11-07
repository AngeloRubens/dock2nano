// Package gen turns a parsed docker-compose.yml, plus what was observed from
// the image of each service, into a plan.Plan: one package manifest per
// service, the compose.yaml that "ops compose up" reads, and a report.
//
// The rules are generic. Nothing is decided by the name of a program or of an
// image. Whatever does not convert is written to the report with its key and
// the reason, and is never dropped in silence. What comes out is what was
// observed, to be reviewed; Render says so on its first line.
package gen

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/AngeloRubens/dock2nano/internal/compose"
	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/dockerfile"
	"github.com/AngeloRubens/dock2nano/internal/observe"
	"github.com/AngeloRubens/dock2nano/internal/plan"
)

// maxListed is how many names a report line lists before "and N more".
const maxListed = 20

// maxPkgLen is the longest package name: a tap device is named after it.
const maxPkgLen = 15

// goarch is runtime.GOARCH; tests replace it.
var goarch = runtime.GOARCH

// ServiceInput is what is known about one service besides the compose file.
type ServiceInput struct {
	// Observed is the result of observing the image of the service. A service
	// whose Observed is nil is reported as not converted.
	Observed *observe.Result

	// Image is the configuration of the image. It may be nil.
	Image *docker.ImageConfig

	// Stage is the final stage of the Dockerfile of the service, when the
	// service is built from one. It may be nil.
	Stage *dockerfile.Stage
}

// Input is everything Generate needs.
type Input struct {
	// Project is the parsed compose file.
	Project *compose.Project

	// Dir is the directory of the compose file; relative paths are resolved
	// against it.
	Dir string

	// Services holds the input of each service by its compose name. A service
	// without an entry is reported as not converted.
	Services map[string]ServiceInput

	// Skipped holds, by compose name, the reason a service was not prepared
	// (for example that its image could not be built). A service with a
	// reason is reported as unsupported with that reason.
	Skipped map[string]string

	// Arch is the architecture of the packages, "amd64" or "arm64". The default
	// is that of the machine that runs the tool; for any other machine it is
	// an error.
	Arch string

	// HostEnv looks up a variable of the machine that runs the tool. The
	// default is os.LookupEnv.
	HostEnv func(string) (string, bool)

	// ReadFile reads a file, for env_file. The default is os.ReadFile.
	ReadFile func(string) ([]byte, error)

	// Stat tells whether a host path exists, for bind mounts. The default is
	// os.Stat.
	Stat func(string) (fs.FileInfo, error)
}

type generator struct {
	in   Input
	plan *plan.Plan
}

func (g *generator) add(service string, kind plan.Kind, key, msg string) {
	g.plan.Report = append(g.plan.Report, plan.Item{Service: service, Kind: kind, Key: key, Message: msg})
}

// Generate builds the plan. It returns an error for input that cannot be
// converted at all: a depends_on cycle, a depends_on on a service that does
// not exist, a service name that cannot be a package name, a service whose
// observation found no program, or an architecture that is not known. When no
// service could be converted it returns the plan, with the report filled, and
// an error.
func Generate(in Input) (*plan.Plan, error) {
	if in.Project == nil {
		return nil, errors.New("no compose project")
	}
	if in.Arch == "" {
		switch goarch {
		case "amd64", "arm64":
			in.Arch = goarch
		default:
			return nil, fmt.Errorf("the architecture %s of this machine is not supported; set the architecture to amd64 or arm64", goarch)
		}
	}
	if in.HostEnv == nil {
		in.HostEnv = os.LookupEnv
	}
	if in.ReadFile == nil {
		in.ReadFile = os.ReadFile
	}
	if in.Stat == nil {
		in.Stat = os.Stat
	}
	ordered, err := order(in.Project)
	if err != nil {
		return nil, err
	}
	g := &generator{in: in, plan: &plan.Plan{Arch: in.Arch}}
	project := projectName(in)
	for _, u := range in.Project.Unknown {
		g.add("", plan.Unsupported, u, "this top-level key of the compose file is not converted")
	}
	skipped := map[string]bool{}
	var converted []*compose.Service
	var convertedNames []string
	for _, s := range ordered {
		si, ok := in.Services[s.Name]
		if key, msg := g.skipReason(s, si, ok); key != "" {
			skipped[s.Name] = true
			g.add(s.Name, plan.Unsupported, key, msg)
			continue
		}
		converted = append(converted, s)
		convertedNames = append(convertedNames, s.Name)
	}
	pkgOf, nameItems, err := PackageNames(in.Project, convertedNames)
	if err != nil {
		return nil, err
	}
	g.plan.Report = append(g.plan.Report, nameItems...)
	folders := map[string]string{}
	for _, s := range converted {
		svc, err := g.service(s, in.Services[s.Name], skipped, pkgOf[s.Name])
		if err != nil {
			return nil, err
		}
		svc.PackageName = folderName(project, s.Name)
		if other, dup := folders[svc.PackageName]; dup {
			return nil, fmt.Errorf("services %q and %q both become the package folder %q", other, s.Name, svc.PackageName)
		}
		folders[svc.PackageName] = s.Name
		g.plan.Services = append(g.plan.Services, *svc)
		g.plan.Compose.Packages = append(g.plan.Compose.Packages, plan.ComposePackage{Pkg: pkgOf[s.Name], Name: svc.PackageName, Local: true, Arch: in.Arch})
	}
	g.networkNote(pkgOf)
	g.envNote()
	if len(g.plan.Services) == 0 {
		return g.plan, errors.New("no service could be converted: see the report")
	}
	return g.plan, nil
}

// skipReason returns the key and the message of the report item when the
// service is not converted at all, and an empty key otherwise.
func (g *generator) skipReason(s *compose.Service, si ServiceInput, ok bool) (key, msg string) {
	switch {
	case len(s.Profiles) > 0:
		return "profiles", "the service has profiles (" + strings.Join(s.Profiles, ", ") + "), so it starts only with --profile; it is not converted"
	case s.Replicas != nil && *s.Replicas == 0:
		return "replicas", "the service has 0 replicas; it is not converted"
	case g.in.Skipped[s.Name] != "":
		return "service", g.in.Skipped[s.Name] + "; it is not converted"
	case !ok || si.Observed == nil:
		return "service", "the service was not observed (no image to observe or the observation was not run); it is not converted"
	}
	return "", ""
}

// order sorts the services by depends_on, keeping the file order for ties.
func order(p *compose.Project) ([]*compose.Service, error) {
	for _, s := range p.Services {
		for _, d := range s.DependsOn {
			if p.Service(d.Service) == nil {
				return nil, fmt.Errorf("service %q depends_on %q, which is not a service of the file", s.Name, d.Service)
			}
		}
	}
	placed := map[string]bool{}
	var out []*compose.Service
	for len(out) < len(p.Services) {
		progress := false
		for _, s := range p.Services {
			if placed[s.Name] || !depsPlaced(s, placed) {
				continue
			}
			placed[s.Name] = true
			out = append(out, s)
			progress = true
			break
		}
		if !progress {
			break
		}
	}
	if len(out) == len(p.Services) {
		return out, nil
	}
	// What is left holds the cycles and what hangs behind them. Drop the
	// services nothing left depends on, until only the cycles stay.
	left := map[string]bool{}
	for _, s := range p.Services {
		if !placed[s.Name] {
			left[s.Name] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for name := range left {
			needed := false
			for other := range left {
				if other == name {
					continue
				}
				for _, d := range p.Service(other).DependsOn {
					if d.Service == name {
						needed = true
					}
				}
			}
			if !needed && !dependsOnSelf(p.Service(name)) {
				delete(left, name)
				changed = true
			}
		}
	}
	var names []string
	for _, s := range p.Services {
		if left[s.Name] {
			names = append(names, s.Name)
		}
	}
	return nil, fmt.Errorf("depends_on forms a cycle between the services %s", strings.Join(names, ", "))
}

func dependsOnSelf(s *compose.Service) bool {
	for _, d := range s.DependsOn {
		if d.Service == s.Name {
			return true
		}
	}
	return false
}

func depsPlaced(s *compose.Service, placed map[string]bool) bool {
	for _, d := range s.DependsOn {
		if !placed[d.Service] {
			return false
		}
	}
	return true
}

// projectName is the compose project name, or the folder of the compose file.
func projectName(in Input) string {
	if in.Project.Name != "" {
		return in.Project.Name
	}
	dir := in.Dir
	if dir == "" {
		dir = "."
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	base := filepath.Base(dir)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "project"
	}
	return base
}

// folderName is the name of the package folder of a service: the project and
// the service joined by "_", lower-cased, with every character outside
// [a-z0-9_.-] turned into '-' and ".." (which a path would read as the parent)
// turned into ".".
func folderName(project, service string) string {
	name := sanitize(project + "_" + service)
	for strings.Contains(name, "..") {
		name = strings.ReplaceAll(name, "..", ".")
	}
	return name
}

// sanitize lower-cases s and turns every character outside [a-z0-9_.-] into '-'.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r - 'A' + 'a'
		}
		return '-'
	}, s)
}

func (g *generator) service(s *compose.Service, si ServiceInput, skipped map[string]bool, pkg string) (*plan.Service, error) {
	obs := si.Observed
	if obs.Program == "" {
		return nil, fmt.Errorf("service %q: the observation found no program to run", s.Name)
	}
	m := plan.Manifest{Program: obs.Program}
	// argv[0] stays as the program saw it: a multi-call binary such as busybox
	// picks what to do from it ("httpd" is a link to /bin/busybox-extras).
	m.Args = slices.Clone(obs.Argv)
	if len(m.Args) == 0 {
		m.Args = []string{obs.Program}
	}
	// nanos starts the program in "/"; ops would look for a host file named
	// like Args[1] in its own working directory and copy it into the image.
	m.DisableArgsCopy = true
	msg := "program " + obs.Program
	if n := len(m.Args) - 1; n > 0 {
		msg += fmt.Sprintf(" and %d arguments, which are in package.manifest", n)
	}
	g.add(s.Name, plan.Observed, "program", msg+" (observed from the image; the choice of the program is an estimate)")

	g.replicas(s)
	m.Env = g.env(s, si)
	g.ports(s, si, &m, pkg)
	g.volumes(s, si, &m)
	g.restart(s, &m)
	g.dependencies(s, skipped)
	g.healthcheck(s, si)
	g.networks(s)
	g.identity(s, obs)
	g.unknown(s, si)
	g.observed(s, obs)
	return &plan.Service{Name: s.Name, Sysroot: obs.Sysroot, Manifest: m}, nil
}

func (g *generator) replicas(s *compose.Service) {
	if s.Replicas != nil && *s.Replicas > 1 {
		g.add(s.Name, plan.Unsupported, "replicas", fmt.Sprintf("%d replicas were asked; one instance is generated", *s.Replicas))
	}
}

// env builds the environment: the image, then what Compose gives the service
// (ServiceEnv).
func (g *generator) env(s *compose.Service, si ServiceInput) map[string]string {
	env := map[string]string{}
	for _, e := range si.Observed.Env {
		if name, val, ok := strings.Cut(e, "="); ok && name != "" {
			env[name] = val
		}
	}
	fromService, items := ServiceEnv(s, g.in.Dir, g.in.HostEnv, g.in.ReadFile)
	for k, v := range fromService {
		env[k] = v
	}
	g.plan.Report = append(g.plan.Report, items...)
	if len(env) == 0 {
		return nil
	}
	return env
}

func (g *generator) ports(s *compose.Service, si ServiceInput, m *plan.Manifest, pkg string) {
	for i, p := range s.Ports {
		key := fmt.Sprintf("ports[%d]", i)
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		if proto != "tcp" && proto != "udp" {
			g.add(s.Name, plan.Unsupported, key, fmt.Sprintf("the protocol %s is not supported by ops; the port %s is not published", proto, p.Target))
			continue
		}
		if strings.Contains(p.Published, "$") || strings.Contains(p.Target, "$") {
			g.add(s.Name, plan.Unsupported, key, "the port holds a variable, which is not interpolated; it is not published")
			continue
		}
		if p.Published == "" {
			g.add(s.Name, plan.Note, key, fmt.Sprintf("port %s/%s is not published; it is reachable only from other services", p.Target, proto))
			continue
		}
		if strings.Contains(p.Published, "-") || strings.Contains(p.Target, "-") {
			g.add(s.Name, plan.Unsupported, key, fmt.Sprintf("the port range %s:%s is not supported (ops reads A-B as host port A to guest port B); it is not published", p.Published, p.Target))
			continue
		}
		if p.HostIP != "" {
			g.add(s.Name, plan.Unsupported, key, fmt.Sprintf("the host address %s is not supported; port %s is published on every address", p.HostIP, p.Published))
		}
		if p.Published != p.Target {
			g.add(s.Name, plan.Unsupported, key, fmt.Sprintf("remapping a port is not supported by ops: %s is used, the container port is %s", p.Published, p.Target))
		}
		g.add(s.Name, plan.Approximated, key, fmt.Sprintf("reachable at %s.service and at the address of the instance on the ops0 bridge; not forwarded to the host", pkg))
		if proto == "udp" {
			m.RunConfig.UDPPorts = append(m.RunConfig.UDPPorts, p.Published)
		} else {
			m.RunConfig.Ports = append(m.RunConfig.Ports, p.Published)
		}
	}
	var exposed []string
	exposed = append(exposed, s.Expose...)
	if si.Image != nil {
		exposed = append(exposed, si.Image.ExposedPorts...)
	}
	if len(exposed) > 0 {
		g.add(s.Name, plan.Note, "expose", "expose and the EXPOSE of the image ("+listed(exposed)+") do not publish a port; only ports with a published value are published")
	}
}

func (g *generator) volumes(s *compose.Service, si ServiceInput, m *plan.Manifest) {
	for i, v := range s.Volumes {
		key := fmt.Sprintf("volumes[%d]", i)
		switch {
		case strings.Contains(v.Source, "$") || v.Type == "unresolved":
			g.add(s.Name, plan.Unsupported, key, "the source holds a variable, so it is not known whether it is a bind mount or a named volume; it is not converted")
		case v.Type == "bind":
			g.bind(s.Name, key, v)
		case v.Type == "volume" && v.Source == "":
			g.add(s.Name, plan.Unsupported, key, "anonymous volume at "+v.Target+": nanos has no volumes; the data is not kept")
		case v.Type == "volume":
			g.add(s.Name, plan.Unsupported, key, "named volume "+v.Source+" at "+v.Target+": nanos has no volumes; the data is not shared or kept")
		case v.Type == "tmpfs":
			g.add(s.Name, plan.Unsupported, key, "tmpfs at "+v.Target+" is not supported")
		default:
			g.add(s.Name, plan.Unsupported, key, "the volume type "+v.Type+" is not supported")
		}
	}
	if si.Image != nil && len(si.Image.Volumes) > 0 {
		g.add(s.Name, plan.Note, "volumes (image)", "the VOLUME paths of the image ("+listed(si.Image.Volumes)+") are not kept as volumes")
	}
}

func (g *generator) bind(service, key string, v compose.Volume) {
	if strings.Contains(v.Source, ":") {
		g.add(service, plan.Unsupported, key, "the source "+v.Source+" holds ':', which a mapping cannot carry; it is not converted")
		return
	}
	if strings.HasPrefix(v.Source, "~") {
		g.add(service, plan.Unsupported, key, "the source "+v.Source+" starts with ~, which is not expanded; it is not converted")
		return
	}
	host := v.Source
	if !filepath.IsAbs(host) {
		host = filepath.Join(g.in.Dir, host)
	}
	if _, err := g.in.Stat(host); err != nil {
		g.add(service, plan.Unsupported, key, "the host path "+host+" cannot be read ("+err.Error()+"); it is not converted")
		return
	}
	msg := "bind mount " + host + " at " + v.Target + ": only the files the program opened while it was observed are carried; the rest of the directory is not; changes made while running are not written back"
	if v.ReadOnly {
		msg += "; it was read-only, which is not enforced"
	}
	if v.Options != "" {
		msg += "; the options " + v.Options + " are ignored"
	}
	g.add(service, plan.Approximated, key, msg)
}

func (g *generator) restart(s *compose.Service, m *plan.Manifest) {
	switch r := s.Restart; {
	case r == "" || r == "no":
	case r == "always" || r == "unless-stopped" || strings.HasPrefix(r, "on-failure"):
		m.RebootOnExit = true
		g.add(s.Name, plan.Approximated, "restart", "restart "+r+" becomes RebootOnExit: the instance restarts whenever the program exits, whatever the exit code")
	default:
		g.add(s.Name, plan.Unsupported, "restart", "the restart policy "+r+" is not known; it is not converted")
	}
}

func (g *generator) dependencies(s *compose.Service, skipped map[string]bool) {
	for _, d := range s.DependsOn {
		key := "depends_on." + d.Service
		if d.Condition != "" && d.Condition != "service_started" {
			g.add(s.Name, plan.Approximated, key, "the condition "+d.Condition+" is kept as start order only; ops compose does not wait for it")
		}
		if skipped[d.Service] {
			g.add(s.Name, plan.Note, key, d.Service+" is not converted, so this service starts without it")
		}
	}
}

func (g *generator) healthcheck(s *compose.Service, si ServiceInput) {
	switch h := s.Healthcheck; {
	case h != nil && h.Disable:
	case h != nil:
		g.add(s.Name, plan.Unsupported, "healthcheck", "ops compose runs no healthchecks; "+testSummary(h.Test.Shell, h.Test.Exec))
	case si.Stage != nil && si.Stage.Healthcheck != nil && !si.Stage.Healthcheck.None:
		t := si.Stage.Healthcheck.Test
		g.add(s.Name, plan.Unsupported, "healthcheck", "the HEALTHCHECK of the Dockerfile is not run by ops compose; "+testSummary(t.Shell, t.Exec))
	}
}

// testSummary describes a healthcheck test by its first word and the number of
// words, because the rest may hold a password.
func testSummary(shell string, exec []string) string {
	w := exec
	if shell != "" {
		w = strings.Fields(shell)
	} else if len(w) > 0 && w[0] == "CMD-SHELL" {
		w = strings.Fields(strings.Join(w[1:], " "))
	} else if len(w) > 0 && (w[0] == "CMD" || w[0] == "NONE") {
		w = w[1:]
	}
	if len(w) == 0 {
		return "the test is empty"
	}
	return fmt.Sprintf("the test starts with %s and has %d words (it is in the file)", w[0], len(w))
}

func (g *generator) networks(s *compose.Service) {
	var extra []string
	for _, n := range s.Networks {
		if n != "default" {
			extra = append(extra, n)
		}
	}
	if len(extra) > 0 {
		g.add(s.Name, plan.Unsupported, "networks", "the networks "+strings.Join(extra, ", ")+" are not converted; every package is on the bridge ops0")
	}
	if s.NetworkMode != "" {
		g.add(s.Name, plan.Unsupported, "network_mode", "network_mode "+s.NetworkMode+" is not converted; every package is on the bridge ops0")
	}
}

func (g *generator) identity(s *compose.Service, obs *observe.Result) {
	if u, _, _ := strings.Cut(s.User, ":"); u != "" && u != "root" && u != "0" {
		g.add(s.Name, plan.Unsupported, "user", "user "+s.User+" is not applied; nanos runs the program as one user")
	}
	// One item for the directory, whatever its source: nanos starts the
	// program in "/".
	var msg string
	switch {
	case obs.WorkingDir != "" && obs.WorkingDir != "/":
		msg = "the program ran in " + obs.WorkingDir + " while observed; nanos starts it in /; relative paths may differ"
		if s.WorkingDir != "" && s.WorkingDir != obs.WorkingDir {
			msg += "; working_dir " + s.WorkingDir + " of the file is not applied"
		}
	case s.WorkingDir != "" && s.WorkingDir != obs.WorkingDir:
		msg = "working_dir " + s.WorkingDir + " is not applied; the program was observed in " + obs.WorkingDir + " and nanos starts it in /"
	}
	if msg != "" {
		g.add(s.Name, plan.Unsupported, "working_dir", msg)
	}
}

// unknown reports what the parsers recorded as not modelled.
func (g *generator) unknown(s *compose.Service, si ServiceInput) {
	for _, u := range s.Unknown {
		g.add(s.Name, plan.Unsupported, u, "this key of the service is not converted")
	}
	if si.Stage != nil {
		for _, u := range si.Stage.Unknown {
			g.add(s.Name, plan.Unsupported, "dockerfile", "not converted: "+u)
		}
	}
}

func (g *generator) observed(s *compose.Service, obs *observe.Result) {
	for _, w := range obs.Warnings {
		g.add(s.Name, plan.Note, "observe", w)
	}
	if len(obs.ConfigurationFiles) > 0 {
		g.add(s.Name, plan.Observed, "configuration files", "files the program reads that you are likely to replace: "+listed(obs.ConfigurationFiles))
	}
	if len(obs.CreatedAtRuntime) > 0 {
		g.add(s.Name, plan.Observed, "created at runtime", "files the program created while it ran: "+listed(obs.CreatedAtRuntime))
	}
	if obs.Vanished > 0 {
		g.add(s.Name, plan.Note, "vanished", fmt.Sprintf("%d files that the program created while it ran were gone when the observation ended (temporary files); they are not in the package", obs.Vanished))
	}
	if obs.Children > 0 {
		g.add(s.Name, plan.Unsupported, "processes", fmt.Sprintf("the program started %d child processes while it was observed (fork/clone); nanos runs a single process and has no fork, so the program may not start or may not work; threads are fine. Many servers have an option to run in one process (for example no master process); see the documentation of the program", obs.Children))
	}
}

func refersToOther(tokens []string, self string, services []plan.Service) bool {
	for _, other := range services {
		if other.Name == self {
			continue
		}
		for _, t := range tokens {
			if t == other.Name {
				return true
			}
		}
	}
	return false
}

// sortedEnvNames returns the names of the variables of a manifest, sorted.
func sortedEnvNames(env map[string]string) []string {
	names := make([]string, 0, len(env))
	for n := range env {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// networkNote explains how services find each other, once for the project. It
// names the variables whose value names another service, never the values.
func (g *generator) networkNote(pkgOf map[string]string) {
	if len(g.plan.Services) < 2 {
		return
	}
	var pairs []string
	for _, svc := range g.plan.Services {
		pairs = append(pairs, svc.Name+" -> "+pkgOf[svc.Name]+".service")
	}
	msg := "a service reaches another as <pkg>.service, not by its compose name: " + strings.Join(pairs, ", ") + "."
	var refs []string
	for _, svc := range g.plan.Services {
		var vars []string
		for _, n := range sortedEnvNames(svc.Manifest.Env) {
			tokens := strings.FieldsFunc(svc.Manifest.Env[n], func(r rune) bool {
				return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-')
			})
			if refersToOther(tokens, svc.Name, g.plan.Services) {
				vars = append(vars, n)
			}
		}
		if len(vars) > 0 {
			refs = append(refs, svc.Name+": "+strings.Join(vars, ", "))
		}
	}
	if len(refs) > 0 {
		msg += " Environment variables whose value names another service were not rewritten: " + strings.Join(refs, "; ") + "."
	}
	g.add("", plan.Note, "service names", msg)
}

// envNote says that ops compose shares one configuration between the packages.
// It lists the names of the variables of each service, never the values.
func (g *generator) envNote() {
	if len(g.plan.Services) < 2 {
		return
	}
	var parts []string
	for _, svc := range g.plan.Services {
		if len(svc.Manifest.Env) > 0 {
			parts = append(parts, svc.Name+": "+listed(sortedEnvNames(svc.Manifest.Env)))
		}
	}
	if len(parts) == 0 {
		return
	}
	g.add("", plan.Note, "environment", "ops compose reads all packages into one configuration: environment variables of one service stay set for the services after it. Variables per service: "+strings.Join(parts, "; ")+".")
}

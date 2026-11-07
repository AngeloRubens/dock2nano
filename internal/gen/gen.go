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
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

	// Arch is the architecture of the packages. The default is "amd64".
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
// not exist, a service name that cannot be a package name, or a service whose
// observation found no program.
func Generate(in Input) (*plan.Plan, error) {
	if in.Project == nil {
		return nil, errors.New("no compose project")
	}
	if in.Arch == "" {
		in.Arch = "amd64"
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
	pkgOf := map[string]string{}
	owner := map[string]string{}
	for _, s := range ordered {
		si, ok := in.Services[s.Name]
		if key, msg := skipReason(s, si, ok); key != "" {
			skipped[s.Name] = true
			g.add(s.Name, plan.Unsupported, key, msg)
			continue
		}
		pkg, err := pkgName(s.Name)
		if err != nil {
			return nil, err
		}
		if other, dup := owner[pkg]; dup {
			return nil, fmt.Errorf("service %q: the package name %q is already used by service %q", s.Name, pkg, other)
		}
		owner[pkg] = s.Name
		pkgOf[s.Name] = pkg
		svc, err := g.service(s, si, skipped)
		if err != nil {
			return nil, err
		}
		svc.PackageName = sanitize(project + "_" + s.Name)
		g.plan.Services = append(g.plan.Services, *svc)
		g.plan.Compose.Packages = append(g.plan.Compose.Packages, plan.ComposePackage{Pkg: pkg, Name: svc.PackageName, Local: true, Arch: in.Arch})
	}
	g.networkNote(pkgOf)
	return g.plan, nil
}

// skipReason returns the key and the message of the report item when the
// service is not converted at all, and an empty key otherwise.
func skipReason(s *compose.Service, si ServiceInput, ok bool) (key, msg string) {
	switch {
	case len(s.Profiles) > 0:
		return "profiles", "the service has profiles (" + strings.Join(s.Profiles, ", ") + "), so it starts only with --profile; it is not converted"
	case s.Replicas != nil && *s.Replicas == 0:
		return "replicas", "the service has 0 replicas; it is not converted"
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

// pkgName is the pkg of a service in compose.yaml. It is the name of a tap
// device, so it is at most 15 characters and has only [a-z0-9-].
func pkgName(service string) (string, error) {
	name := strings.ToLower(service)
	if name == "" || len(name) > maxPkgLen {
		return "", fmt.Errorf("service %q: the name must be 1 to %d characters to be a package name (a tap device is named after it)", service, maxPkgLen)
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return "", fmt.Errorf("service %q: the name has the character %q; a package name may hold only letters, digits and '-'", service, r)
		}
	}
	return name, nil
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

func (g *generator) service(s *compose.Service, si ServiceInput, skipped map[string]bool) (*plan.Service, error) {
	obs := si.Observed
	if obs.Program == "" {
		return nil, fmt.Errorf("service %q: the observation found no program to run", s.Name)
	}
	m := plan.Manifest{Program: obs.Program}
	m.Args = []string{obs.Program}
	if len(obs.Argv) > 1 {
		m.Args = append(m.Args, obs.Argv[1:]...)
	}
	g.add(s.Name, plan.Observed, "program", "program "+strings.Join(m.Args, " ")+" (observed from the image; the choice of the program is an estimate)")

	g.replicas(s)
	m.Env = g.env(s, si)
	g.ports(s, si, &m)
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

// env builds the environment: the image, then env_file, then environment.
func (g *generator) env(s *compose.Service, si ServiceInput) map[string]string {
	env := map[string]string{}
	for _, e := range si.Observed.Env {
		if name, val, ok := strings.Cut(e, "="); ok && name != "" {
			env[name] = val
		}
	}
	for i, path := range s.EnvFile {
		g.envFile(s.Name, i, path, env)
	}
	for _, e := range s.Environment {
		key := "environment." + e.Name
		if !e.Set {
			g.fromHost(s.Name, key, e.Name, env)
			continue
		}
		if strings.Contains(e.Value, "${") {
			g.varNote(s.Name, key)
		}
		env[e.Name] = e.Value
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func (g *generator) varNote(service, key string) {
	g.add(service, plan.Unsupported, key, "the value holds ${...}, which is not interpolated; it is kept as written")
}

// fromHost sets a variable that has no value in the file from the environment
// of the machine, or reports that it is not there.
func (g *generator) fromHost(service, key, name string, env map[string]string) {
	if v, ok := g.in.HostEnv(name); ok {
		env[name] = v
		return
	}
	g.add(service, plan.Unsupported, key, "the variable has no value in the file and is not set on this machine; it is not set in the package")
}

func (g *generator) envFile(service string, i int, path string, env map[string]string) {
	key := fmt.Sprintf("env_file[%d]", i)
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(g.in.Dir, full)
	}
	data, err := g.in.ReadFile(full)
	if err != nil {
		g.add(service, plan.Unsupported, key, fmt.Sprintf("%s cannot be read (%v); its variables are not set", path, err))
		return
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "export "); ok {
			line = strings.TrimSpace(rest)
		}
		name, raw, hasValue := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		lineKey := fmt.Sprintf("%s line %d", key, n+1)
		if name == "" {
			g.add(service, plan.Unsupported, lineKey, "the line is not NAME=value; it is ignored")
			continue
		}
		if !hasValue {
			g.fromHost(service, lineKey, name, env)
			continue
		}
		val, literal := envValue(strings.TrimSpace(raw))
		if !literal && strings.Contains(val, "${") {
			g.varNote(service, lineKey)
		}
		env[name] = val
	}
}

// envValue reads the value of an env_file line. literal is true for a value
// in single quotes, where Compose does not interpolate.
func envValue(raw string) (val string, literal bool) {
	if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		return raw[1 : len(raw)-1], true
	}
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		return raw[1 : len(raw)-1], false
	}
	if i := strings.Index(raw, " #"); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.Index(raw, "\t#"); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(raw), false
}

func (g *generator) ports(s *compose.Service, si ServiceInput, m *plan.Manifest) {
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
		if p.HostIP != "" {
			g.add(s.Name, plan.Unsupported, key, fmt.Sprintf("the host address %s is not supported; port %s is published on every address", p.HostIP, p.Published))
		}
		if p.Published != p.Target {
			g.add(s.Name, plan.Approximated, key, fmt.Sprintf("ops does not remap ports: %s is used, the container port is %s", p.Published, p.Target))
		}
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
		test := h.Test.Shell
		if test == "" {
			test = strings.Join(h.Test.Exec, " ")
		}
		g.add(s.Name, plan.Unsupported, "healthcheck", "ops compose runs no healthchecks; test: "+test)
	case si.Stage != nil && si.Stage.Healthcheck != nil && !si.Stage.Healthcheck.None:
		t := si.Stage.Healthcheck.Test
		test := t.Shell
		if test == "" {
			test = strings.Join(t.Exec, " ")
		}
		g.add(s.Name, plan.Unsupported, "healthcheck", "the HEALTHCHECK of the Dockerfile is not run by ops compose; test: "+test)
	}
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
	if s.WorkingDir != "" && s.WorkingDir != obs.WorkingDir {
		g.add(s.Name, plan.Unsupported, "working_dir", "working_dir "+s.WorkingDir+" is not applied; the program was observed in "+obs.WorkingDir+" and nanos has no directory per program")
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

// networkNote explains how services find each other, once for the project.
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
		names := make([]string, 0, len(svc.Manifest.Env))
		for n := range svc.Manifest.Env {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			val := svc.Manifest.Env[n]
			tokens := strings.FieldsFunc(val, func(r rune) bool {
				return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-')
			})
			if refersToOther(tokens, svc.Name, g.plan.Services) {
				refs = append(refs, svc.Name+": "+n+"="+val)
			}
		}
	}
	if len(refs) > 0 {
		msg += " Environment values that name another service were not rewritten: " + strings.Join(refs, "; ") + "."
	}
	g.add("", plan.Note, "service names", msg)
}

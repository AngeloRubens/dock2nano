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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/AngeloRubens/dock2nano/internal/compose"
	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/dockerfile"
	"github.com/AngeloRubens/dock2nano/internal/observe"
	"github.com/AngeloRubens/dock2nano/internal/plan"
	"github.com/AngeloRubens/dock2nano/internal/trace"
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

	// ShortNames makes the services reachable by the name of the compose file too, with
	// dns_search: [service] in compose.yaml.
	ShortNames bool

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
// observation found no program, or an architecture that is not amd64 or arm64. When no
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
	// ops maps every architecture but arm64 to amd64 and would look in the
	// wrong folder, so whatever the source of the value, it is checked here.
	if in.Arch != "amd64" && in.Arch != "arm64" {
		return nil, fmt.Errorf("the architecture %q is not supported; it must be amd64 or arm64", in.Arch)
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
	if in.ShortNames {
		g.plan.Compose.DNSSearch = []string{"service"}
	}
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
		g.plan.Compose.Packages = append(g.plan.Compose.Packages, plan.ComposePackage{Pkg: pkgOf[s.Name], Name: svc.PackageName, Local: true, Arch: in.Arch, BaseVolumeSz: svc.Manifest.BaseVolumeSz})
	}
	g.networkNote(pkgOf)
	g.startOrderNote()
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
	g.identity(s, si, obs, &m)
	g.unknown(s, si)
	g.observed(s, obs)
	m.BaseVolumeSz = volumeSize(obs.Sysroot)
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
			g.add(s.Name, plan.Unsupported, key, fmt.Sprintf("the port mapping %s:%s is not applied: the service is reachable on the container port %s, on the bridge ops0 at %s and at the address of the instance; the published port %s does not exist (ops does not forward ports in compose mode)", p.Published, p.Target, p.Target, g.dnsName(pkg), p.Published))
		}
		g.add(s.Name, plan.Approximated, key, fmt.Sprintf("reachable at %s and at the address of the instance on the ops0 bridge; not forwarded to the host", g.dnsName(pkg)))
		if proto == "udp" {
			m.RunConfig.UDPPorts = append(m.RunConfig.UDPPorts, p.Target)
		} else {
			m.RunConfig.Ports = append(m.RunConfig.Ports, p.Target)
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
	if v.Secret {
		msg = "secret file mounted at " + v.Target + " (" + msg + "); the content of the secret is in the package"
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
		msg := "ops compose runs no healthchecks; " + testSummary(h.Test.Shell, h.Test.Exec)
		if opts := healthcheckOptions(s); len(opts) > 0 {
			msg += "; options not converted: " + strings.Join(opts, ", ")
		}
		g.add(s.Name, plan.Unsupported, "healthcheck", msg)
	case si.Stage != nil && si.Stage.Healthcheck != nil && !si.Stage.Healthcheck.None:
		t := si.Stage.Healthcheck.Test
		g.add(s.Name, plan.Unsupported, "healthcheck", "the HEALTHCHECK of the Dockerfile is not run by ops compose; "+testSummary(t.Shell, t.Exec))
	}
}

// healthcheckOptions are the options of the healthcheck of the service that the compose parser
// recorded as not modelled, by name (interval, timeout...). They are named in the healthcheck item
// when there is one, and have no item of their own then.
func healthcheckOptions(s *compose.Service) []string {
	if s.Healthcheck == nil || s.Healthcheck.Disable {
		return nil
	}
	var out []string
	for _, u := range s.Unknown {
		if name, ok := strings.CutPrefix(u, "healthcheck."); ok && healthcheckOptionNames[name] {
			out = append(out, name)
		}
	}
	return out
}

var healthcheckOptionNames = map[string]bool{"interval": true, "timeout": true, "retries": true, "start_period": true, "start_interval": true}

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

// nonRootUser tells whether a user of a file or of an image, "name", "uid" or
// "user:group", is not root.
func nonRootUser(user string) bool {
	u, _, _ := strings.Cut(user, ":")
	return u != "" && u != "root" && u != "0"
}

// numericUser reads "uid" or "uid:gid" with numbers and a uid above zero. gid is -1 when the text has
// none.
func numericUser(user string) (uid, gid int, ok bool) {
	u, gtext, hasGroup := strings.Cut(user, ":")
	uid, err := strconv.Atoi(u)
	if err != nil || uid <= 0 {
		return 0, 0, false
	}
	gid = -1
	if hasGroup {
		if gid, err = strconv.Atoi(gtext); err != nil || gid < 0 {
			return 0, 0, false
		}
	}
	return uid, gid, true
}

func (g *generator) identity(s *compose.Service, si ServiceInput, obs *observe.Result, m *plan.Manifest) {
	composeNonRoot := nonRootUser(s.User)
	// The file may say root while the entrypoint drops to another user, so the observed user wins
	// when it is known. nanos reads uid and gid from the root of the manifest (nanovms/nanos#2189,
	// merged on 2026-10-31, not in release 0.1.56) and tells the program who it is; an older nanos
	// runs the program as root, as before. ops copies ManifestPassthrough into the manifest.
	uid, gid := obs.UID, obs.GID
	imageUser := ""
	if si.Image != nil {
		imageUser = si.Image.User
	}
	// When the user could not be read, a numeric USER of the file (or, without one, of the image)
	// is the next best thing to what was observed.
	fallback := false
	if obs.UID < 0 {
		declared := s.User
		if declared == "" {
			declared = imageUser
		}
		if u, gr, ok := numericUser(declared); ok {
			uid, gid, fallback = u, gr, true
		}
	}
	switch {
	case fallback:
		g.add(s.Name, plan.Approximated, "user", fmt.Sprintf("it was not possible to read which user the program ran as; the numeric user of the %s, uid %d, is set in the package manifest, which a nanos that reads uid and gid from the manifest honours (nanovms/nanos#2189, in master since 2026-10-31, not in release 0.1.56); an older nanos runs the program as root", userSource(s), uid))
	case uid > 0:
		g.add(s.Name, plan.Approximated, "user", fmt.Sprintf("the program ran as uid %d (gid %d) while it was observed; uid and gid are set in the package manifest, which a nanos that reads them from the manifest honours (nanovms/nanos#2189, in master since 2026-10-31, not in release 0.1.56); an older nanos runs the program as root, and a program that refuses to run as root, or that depends on who owns its files, may not work", uid, gid))
	case composeNonRoot:
		g.add(s.Name, plan.Unsupported, "user", "user "+s.User+" is not applied; the user the program ran as could not be read, and nanos runs the program as root")
	}
	if obs.UID < 0 && !fallback && s.User == "" && nonRootUser(imageUser) {
		g.add(s.Name, plan.Unsupported, "user", fmt.Sprintf("the program was started as user %s in the image; it was not possible to read which user it ran as; nanos runs everything as root", imageUser))
	}
	// nanos reads "cwd" from the root of the manifest and changes to it before
	// it starts the program; ops copies ManifestPassthrough into the manifest of
	// the package. It is written for every service, "/" when the directory is
	// not known: ops compose reads all manifests into one configuration, and a
	// map merges, so the cwd of one service would stay set for the next.
	cwd := obs.WorkingDir
	if cwd == "" {
		cwd = "/"
	}
	m.ManifestPassthrough = map[string]any{"cwd": cwd}
	if uid > 0 {
		// strings: the writer of the file system in ops encodes strings, vectors and tuples, and
		// nanos reads the number from the text
		m.ManifestPassthrough["uid"] = strconv.Itoa(uid)
		if gid >= 0 {
			m.ManifestPassthrough["gid"] = strconv.Itoa(gid)
		}
	}
	switch {
	case s.WorkingDir != "" && path.Clean(s.WorkingDir) != path.Clean(cwd):
		g.add(s.Name, plan.Unsupported, "working_dir", "working_dir "+s.WorkingDir+" of the file is not applied: the program was observed in "+cwd+", and cwd "+cwd+" is set in the package manifest")
	case cwd == "/":
	case s.WorkingDir != "":
		g.add(s.Name, plan.Converted, "working_dir", "cwd "+cwd+" is set in the package manifest (working_dir of the file, as observed)")
	default:
		g.add(s.Name, plan.Approximated, "working_dir", "cwd "+cwd+" is set in the package manifest (the working directory of the image, as observed)")
	}
}

// unknown reports what the parsers recorded as not modelled.
func (g *generator) unknown(s *compose.Service, si ServiceInput) {
	folded := map[string]bool{}
	for _, name := range healthcheckOptions(s) {
		folded["healthcheck."+name] = true
	}
	for _, u := range s.Unknown {
		if folded[u] {
			continue
		}
		g.add(s.Name, plan.Unsupported, u, "this key of the service is not converted")
	}
	if si.Stage != nil {
		for _, u := range si.Stage.Unknown {
			if what, ok := variableNote(u); ok {
				g.add(s.Name, plan.Note, "dockerfile", what+" holds a variable; the value in the package is the one observed while the image ran")
				continue
			}
			g.add(s.Name, plan.Unsupported, "dockerfile", "not converted: "+u)
		}
	}
}

// variableNote recognizes the entries of the Dockerfile parser of the form
// "line N: <instruction> holds a variable (kept as written)" and returns the instruction.
func variableNote(u string) (string, bool) {
	rest := u
	if strings.HasPrefix(rest, "line ") {
		if _, after, ok := strings.Cut(rest, ": "); ok {
			rest = after
		}
	}
	what, ok := strings.CutSuffix(rest, " holds a variable (kept as written)")
	return what, ok && what != ""
}

func (g *generator) observed(s *compose.Service, obs *observe.Result) {
	if obs.LocalNames {
		g.add(s.Name, plan.Note, "hosts", "the /etc/hosts of the container is not carried (docker writes it for that container); the package gets one that names localhost")
	}
	if len(obs.MachineFiles) > 0 {
		g.add(s.Name, plan.Note, "machine", "the program uses "+strings.Join(obs.MachineFiles, ", ")+" of /proc, /sys or /dev, which nanos does not provide; the package carries one of ours for each: one processor in cpuinfo, 0-63 as the processors a machine could have, an empty directory for /dev/shm")
	}
	if obs.StoppedEarly {
		msg := fmt.Sprintf("the container stopped with status %d before the end of the observation, so what was observed is not a running server; the program chosen is the last one it started", obs.ExitCode)
		if head := firstLines(obs.StoppedLog, 3); head != "" {
			msg += ". Log: " + head
		}
		g.add(s.Name, plan.Unsupported, "observation", msg)
	}
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
	launcher := g.launcherNote(s, obs)
	g.passedOverNote(s, obs)
	g.childLauncherNote(s, obs)
	supervisor := g.supervisorNote(s, obs)
	if obs.Children > 0 {
		msg := fmt.Sprintf("the program started %d child processes while it was observed (fork/clone); nanos runs a single process and has no fork, so the program may not start or may not work; threads are fine. Many servers have an option to run in one process (for example no master process); see the documentation of the program", obs.Children)
		if launcher {
			msg += ". The program is a launcher: see the launcher item"
		}
		if supervisor {
			msg += ". The program is a supervisor: see the supervisor item"
		}
		g.add(s.Name, plan.Unsupported, "processes", msg)
	}
}

// maxPackageJSON is the largest package.json that is read.
const maxPackageJSON = 1 << 20

// launcherNote says, for a program that is a package manager launcher (npm, npx, yarn, pnpm), that
// it cannot run on nanos as it is: it starts the script as a child process, and nanos has no fork.
// When the script is named and package.json was carried with the package, the command of the script
// is quoted. Nothing is changed. It tells whether it added the item.
func (g *generator) launcherNote(s *compose.Service, obs *observe.Result) bool {
	name, rest, ok := trace.Launcher(obs.Program, obs.Argv)
	if !ok {
		return false
	}
	verb := "is"
	if runsThroughShell(obs) {
		verb = "runs"
	}
	msg := fmt.Sprintf("the observed program %s %s, a launcher that starts your command as a child process; nanos has no fork, so it cannot run on nanos as it is", verb, name)
	advice := "if the script runs a single node program, run that program directly (node <file>) and give it to the image as its command; a script that starts other launchers or several commands needs the same at each step"
	if name == "npx" || name == "pnpx" {
		advice = "if the package it runs is a single node program, run its file directly (node <file>) and give it to the image as its command"
	}
	script, elsewhere, viaRun := trace.ScriptRun(name, rest)
	switch {
	case script == "":
		msg += ". " + advice
	case elsewhere:
		msg += fmt.Sprintf(". It was asked to run the script %q of another package or directory (an option of the command line points there), which was not read. %s", script, advice)
	default:
		_, state := packageScript(obs.Sysroot, obs.WorkingDir, script)
		const option = "; if that word is the value of an option, it is not a script"
		switch {
		case state == scriptFound:
			msg += fmt.Sprintf(". It was asked to run the package.json script %q: read its command in package.json, under scripts. %s", script, advice)
		case state == scriptMissing && name == "npm" && script == "start":
			msg += ". npm start was asked and package.json has no start script: npm then runs node server.js if that file exists; run that directly"
		case state == scriptMissing && name == "npm" && script == "restart":
			msg += ". npm restart was asked and package.json has no restart script: npm then runs its stop and start scripts. " + advice
		case state == scriptMissing && name == "npm":
			msg += fmt.Sprintf(". It was asked to run the script %q, which package.json does not have: npm run fails for it%s", script, option)
		case state == scriptMissing && script == "start":
			msg += fmt.Sprintf(". It was asked to run the script \"start\", which package.json does not have: %s then may run node server.js if that file exists%s", name, option)
		case state == scriptMissing && name == "pnpm" && viaRun:
			msg += fmt.Sprintf(". It was asked to run the script %q, which package.json does not have: pnpm run fails for it%s", script, option)
		case state == scriptMissing && name == "pnpm":
			msg += fmt.Sprintf(". It was asked to run the script %q, which package.json does not have: pnpm then falls back to running a command of that name (pnpm exec)%s", script, option)
		case state == scriptMissing:
			msg += fmt.Sprintf(". It was asked to run the script %q, which package.json does not have: %s then runs the binary of that name from node_modules/.bin if there is one%s", script, name, option)
		default:
			msg += fmt.Sprintf(". It was asked to run the package.json script %q, which could not be read from the files of the package: read it in package.json. %s", script, advice)
		}
	}
	g.add(s.Name, plan.Unsupported, "launcher", msg)
	return true
}

// runsThroughShell tells whether the observed program is a shell (or busybox) that runs the
// launcher, as against the launcher itself.
func runsThroughShell(obs *observe.Result) bool {
	for _, p := range []string{obs.Program, firstArg(obs.Argv)} {
		switch path.Base(p) {
		case "sh", "bash", "dash", "ash", "busybox":
			return true
		}
	}
	return false
}

func firstArg(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	return argv[0]
}

// passedOverNote says what the observation passed over: the commands the image starts (a launcher,
// the shells around it) and the package runs the program the launcher started, which is a different
// thing. The commands are named, never printed: the arguments of a command line are user text, and
// the report points to where the original is. An Unsupported item says which commands ran before
// the program or beside it and are not run by the package.
func (g *generator) passedOverNote(s *compose.Service, obs *observe.Result) {
	const where = "Read the original commands in the CMD or ENTRYPOINT of the Dockerfile and in the scripts of package.json"
	if len(obs.PassedOver) > 0 {
		g.add(s.Name, plan.Note, "launcher passed over", fmt.Sprintf("the commands passed over were %s (outermost first), and the package runs the command the launcher started (in package.manifest). The npm_* variables are kept as observed, and what the launcher did around the command is lost: the pre and post scripts, if the launcher runs them, and the signals it passes to the command. %s", strings.Join(obs.PassedOver, ", then "), where))
	}
	if len(obs.NotRun) > 0 {
		g.add(s.Name, plan.Unsupported, "commands not run", fmt.Sprintf("these commands ran before the program or beside it, and ended: %s. The package does not run them: run them at build time, or as a separate one-shot service. %s", strings.Join(obs.NotRun, ", "), where))
	}
}

// childLauncherNote says that the program started a launcher as a child process: it is probably a
// script of the image (a script is not told from a program by its name) that runs npm.
func (g *generator) childLauncherNote(s *compose.Service, obs *observe.Result) {
	if len(obs.ChildLaunchers) == 0 {
		return
	}
	g.add(s.Name, plan.Unsupported, "launcher child", fmt.Sprintf("the observed program started %s as a child process: it is probably a script of the image that runs a launcher, and a launcher cannot run on nanos (no fork). Run the node command it leads to directly. Read the script in the CMD or ENTRYPOINT of the Dockerfile", strings.Join(obs.ChildLaunchers, ", ")))
}

// supervisorNote says, for a program that is a supervisor (nodemon, pm2-runtime, ...), that it
// starts the program of the user as a child process, and what to run instead. Nothing is passed over
// for it by name: the report only says it. It tells whether it added the item.
func (g *generator) supervisorNote(s *compose.Service, obs *observe.Result) bool {
	name := trace.Supervisor(obs.Program, obs.Argv)
	if name == "" {
		return false
	}
	msg := name + " is a supervisor that starts your program as a child process; nanos has no fork, so it cannot run on nanos as it is. Run the program it supervises directly (for nodemon: node <file>)"
	if name == "concurrently" || name == "npm-run-all" || name == "run-p" || name == "run-s" {
		msg += "; it starts several commands, which need a service each"
	}
	g.add(s.Name, plan.Unsupported, "supervisor", msg)
	return true
}

const (
	scriptUnreadable = iota
	scriptMissing
	scriptFound
)

// packageScript reads the command of a script from the package.json in the working directory of the
// observed program, inside the sysroot: the path is resolved in the sysroot, where a link of the
// image stays, and only a regular file of at most maxPackageJSON bytes is read. A file that cannot
// be read or is not JSON is scriptUnreadable; a readable one without the script (or with an empty
// one) is scriptMissing.
func packageScript(sysroot, dir, script string) (string, int) {
	if sysroot == "" {
		return "", scriptUnreadable
	}
	// the links of the image are followed inside the sysroot; os.Root refuses what still leads out of
	// it (a chain of links that InSysroot gave up on, a link to a path of this machine)
	inside := trace.InSysroot(sysroot, path.Join("/", dir, "package.json"))
	root, err := os.OpenRoot(sysroot)
	if err != nil {
		return "", scriptUnreadable
	}
	defer root.Close()
	f, err := root.Open(strings.TrimPrefix(inside, "/"))
	if err != nil {
		return "", scriptUnreadable
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return "", scriptUnreadable
	}
	data, err := io.ReadAll(io.LimitReader(f, maxPackageJSON+1))
	if err != nil || len(data) > maxPackageJSON {
		return "", scriptUnreadable
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return "", scriptUnreadable
	}
	cmd, ok := pkg.Scripts[script]
	if !ok || cmd == "" {
		return "", scriptMissing
	}
	return cmd, scriptFound
}

// firstLines returns the first n non-empty lines of s, trimmed, joined by " | ".
func firstLines(s string, n int) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		out = append(out, l)
		if len(out) == n {
			break
		}
	}
	return strings.Join(out, " | ")
}

// nameTokens splits a value into the words a service name can be found among.
func nameTokens(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-')
	})
}

// startOrderNote says, for a service that names another one in an environment variable and starts
// before it, that ops compose starts the packages in the order of compose.yaml and waits only for
// an address: nothing says the other one is there when this one starts. A compose file that
// declares depends_on has the order already, so it is not said for it. Only the names of the
// variables are given, never the values.
func (g *generator) startOrderNote() {
	for i, svc := range g.plan.Services {
		declared := map[string]bool{}
		if cs := g.in.Project.Service(svc.Name); cs != nil {
			for _, d := range cs.DependsOn {
				declared[d.Service] = true
			}
		}
		for _, other := range g.plan.Services[i+1:] {
			if declared[other.Name] {
				continue
			}
			var vars []string
			for _, n := range sortedEnvNames(svc.Manifest.Env) {
				for _, t := range nameTokens(svc.Manifest.Env[n]) {
					if t == other.Name {
						vars = append(vars, n)
						break
					}
				}
			}
			if len(vars) > 0 {
				g.add(svc.Name, plan.Note, "start order", fmt.Sprintf("%s names %s in %s, but compose.yaml starts %s before %s and the compose file does not say that %s depends on %s: ops compose starts the packages in that order and waits only for an address, so %s may start before %s is there; list %s first in compose.yaml if it does not connect", svc.Name, other.Name, strings.Join(vars, ", "), svc.Name, other.Name, svc.Name, other.Name, svc.Name, other.Name, other.Name))
			}
		}
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
// dnsName is the name a service has in the dns of ops compose, as the report says it.
func (g *generator) dnsName(pkg string) string {
	if g.in.ShortNames {
		return pkg + " (or " + pkg + ".service)"
	}
	return pkg + ".service"
}

func (g *generator) networkNote(pkgOf map[string]string) {
	if len(g.plan.Services) < 2 {
		return
	}
	var pairs []string
	for _, svc := range g.plan.Services {
		pairs = append(pairs, svc.Name+" -> "+g.dnsName(pkgOf[svc.Name]))
	}
	var msg string
	if g.in.ShortNames {
		msg = "a service reaches another by its compose name, or as <pkg>.service (dns_search: [service] in compose.yaml puts service in the search line of resolv.conf, which needs an ops that reads it): " + strings.Join(pairs, ", ") + "."
	} else {
		msg = "a service reaches another as <pkg>.service, not by its compose name: " + strings.Join(pairs, ", ") + "."
	}
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
	// with the names of the compose file in the dns, those values need no rewriting
	if len(refs) > 0 && !g.in.ShortNames {
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

// userSource says where the numeric user came from: the file or the image.
func userSource(s *compose.Service) string {
	if s.User != "" {
		return "file"
	}
	return "image"
}

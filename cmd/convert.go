package cmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/AngeloRubens/dock2nano/internal/compose"
	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/dockerfile"
	"github.com/AngeloRubens/dock2nano/internal/gen"
	"github.com/AngeloRubens/dock2nano/internal/observe"
	"github.com/AngeloRubens/dock2nano/internal/output"
	"github.com/AngeloRubens/dock2nano/internal/plan"
)

// opsDNSPackage is the package that "ops compose" needs once on the machine.
const opsDNSPackage = "eyberg/ops-dns:0.0.1"

// convert runs the whole conversion. It returns the exit status for a run
// that finished, and an error for one that did not.
func (a *App) convert(ctx context.Context, o *options) (int, error) {
	wd, err := a.Getwd()
	if err != nil {
		return 0, err
	}
	project, dir, err := a.load(o, wd)
	if err != nil {
		return 0, err
	}
	only, err := selection(project, o.services)
	if err != nil {
		return 0, err
	}
	ordered, err := dependencyOrder(project)
	if err != nil {
		return 0, err
	}

	inputs := map[string]gen.ServiceInput{}
	var sysroots []string
	defer func() {
		for _, s := range sysroots {
			a.RemoveAll(s)
		}
	}()
	for _, s := range ordered {
		if only != nil && !only[s.Name] {
			continue
		}
		if !observable(s) {
			continue
		}
		si, err := a.prepare(ctx, o, project, dir, s)
		if si.Observed != nil && si.Observed.Sysroot != "" {
			sysroots = append(sysroots, si.Observed.Sysroot)
		}
		if err != nil {
			return 0, fmt.Errorf("service %s: %w", s.Name, err)
		}
		inputs[s.Name] = si
	}

	in := gen.Input{Project: project, Dir: dir, Services: inputs}
	in.HostEnv = a.LookupEnv
	in.ReadFile = a.ReadFile
	in.Stat = a.Stat
	p, err := a.Generate(in)
	if err != nil {
		return 0, err
	}
	composePath := o.out
	if !filepath.IsAbs(composePath) {
		composePath = filepath.Join(wd, composePath)
	}
	composePath = filepath.Join(composePath, "compose.yaml")
	written, err := a.Write(p, output.Options{OpsHome: o.opsHome, ComposePath: composePath, Force: o.force})
	if err != nil {
		return 0, err
	}

	fmt.Fprint(a.Stdout, gen.Render(p.Report))
	fmt.Fprintln(a.Stdout)
	fmt.Fprintln(a.Stdout, "Written:")
	for _, w := range written {
		fmt.Fprintf(a.Stdout, "  %s\n", w)
	}
	a.nextSteps(o, composePath)

	if o.strict {
		n := 0
		for _, it := range p.Report {
			if it.Kind == plan.Unsupported {
				n++
			}
		}
		if n > 0 {
			fmt.Fprintf(a.Stderr, "%s: --strict: the report has %d unsupported item(s)\n", ToolName, n)
			return 2, nil
		}
	}
	return 0, nil
}

// nextSteps prints what to run to start the result.
func (a *App) nextSteps(o *options, composePath string) {
	prefix := ""
	if o.opsHome != "" {
		prefix = "OPS_HOME=" + shellQuote(o.opsHome) + " "
	}
	fmt.Fprintln(a.Stdout)
	fmt.Fprintln(a.Stdout, "Next steps:")
	fmt.Fprintf(a.Stdout, "  %sops pkg get %s    (once, if you do not have it yet)\n", prefix, opsDNSPackage)
	fmt.Fprintf(a.Stdout, "  %sops compose up -f %s\n", prefix, shellQuote(composePath))
}

// shellQuote quotes s for a POSIX shell when it needs it.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./:=+,@%") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// load reads the compose file, or makes a project of one service for
// --dockerfile. dir is the directory that relative paths are resolved against.
func (a *App) load(o *options, wd string) (*compose.Project, string, error) {
	if o.dockerfile != "" {
		return a.dockerfileProject(o, wd)
	}
	path := o.file
	if path == "" {
		for _, n := range composeNames {
			if _, err := a.Stat(filepath.Join(wd, n)); err == nil {
				path = n
				break
			}
		}
		if path == "" {
			return nil, "", fmt.Errorf("no compose file in %s (looked for %s); use -f or --dockerfile", wd, strings.Join(composeNames, ", "))
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(wd, path)
	}
	project, err := compose.ParseFile(path)
	if err != nil {
		return nil, "", err
	}
	return project, filepath.Dir(path), nil
}

// dockerfileProject is the project for --dockerfile: one service built from
// that file.
func (a *App) dockerfileProject(o *options, wd string) (*compose.Project, string, error) {
	df := absPath(wd, o.dockerfile)
	ctxDir := filepath.Dir(df)
	if o.context != "" {
		ctxDir = absPath(wd, o.context)
	}
	name := o.name
	if name == "" {
		name = sanitizeName(filepath.Base(ctxDir))
		if name == "" || name == "." || name == "-" {
			name = "app"
		}
	}
	s := &compose.Service{Name: name, Build: &compose.Build{Context: ctxDir, Dockerfile: df}}
	return &compose.Project{Name: name, Services: []*compose.Service{s}}, wd, nil
}

func absPath(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// sanitizeName lower-cases s and turns every character outside [a-z0-9_.-]
// into '-'.
func sanitizeName(s string) string {
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

// selection returns the set of services named by --service, or nil for all.
func selection(p *compose.Project, names []string) (map[string]bool, error) {
	if len(names) == 0 {
		return nil, nil
	}
	only := map[string]bool{}
	for _, n := range names {
		if p.Service(n) == nil {
			return nil, fmt.Errorf("--service %s: the file has no such service", n)
		}
		only[n] = true
	}
	return only, nil
}

// observable reports whether the service is run for observation. The ones
// that are not are left to the generator, which reports them.
func observable(s *compose.Service) bool {
	if len(s.Profiles) > 0 || s.Replicas != nil && *s.Replicas == 0 {
		return false
	}
	return s.Image != "" || s.Build != nil
}

// dependencyOrder sorts the services by depends_on, keeping the file order
// for ties.
func dependencyOrder(p *compose.Project) ([]*compose.Service, error) {
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
		var next *compose.Service
		for _, s := range p.Services {
			if placed[s.Name] {
				continue
			}
			ready := true
			for _, d := range s.DependsOn {
				if !placed[d.Service] {
					ready = false
				}
			}
			if ready {
				next = s
				break
			}
		}
		if next == nil {
			var left []string
			for _, s := range p.Services {
				if !placed[s.Name] {
					left = append(left, s.Name)
				}
			}
			return nil, fmt.Errorf("depends_on forms a cycle among or behind the services %s", strings.Join(left, ", "))
		}
		placed[next.Name] = true
		out = append(out, next)
	}
	return out, nil
}

// prepare gets the image of one service, inspects it and observes it. The
// returned input holds the observation even when the error is not nil, so that
// the caller can remove its sysroot.
func (a *App) prepare(ctx context.Context, o *options, p *compose.Project, dir string, s *compose.Service) (gen.ServiceInput, error) {
	var si gen.ServiceInput
	image := s.Image
	dfPath := ""
	if s.Build != nil {
		image = imageTag(p, dir, s)
		var err error
		dfPath, err = a.build(ctx, dir, s, image)
		if err != nil {
			return si, err
		}
	} else if err := a.fetch(ctx, o, image); err != nil {
		return si, err
	}
	img, err := a.Docker.Inspect(ctx, image)
	if err != nil {
		return si, err
	}
	si.Image = img
	if dfPath != "" {
		si.Stage = a.stage(s, dfPath)
	}

	ep, cmd := gen.EffectiveCommand(s, img)
	// The entrypoint is always passed, even when empty, so that the CMD of the
	// image is replaced by cmd and an entrypoint cleared in the file stays
	// cleared.
	if ep == nil {
		ep = &[]string{}
	}
	oo := observe.Options{Image: image, Entrypoint: ep, Cmd: cmd}
	oo.Env = a.serviceEnv(s, dir)
	oo.Binds = a.binds(s, dir)
	oo.Seconds = o.seconds
	oo.Log = func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		fmt.Fprintf(a.Stderr, "%s: %s\n", s.Name, strings.TrimRight(msg, "\n"))
	}
	fmt.Fprintf(a.Stderr, "%s: observing %s for %d seconds\n", s.Name, image, o.seconds)
	res, err := a.Observe(ctx, oo)
	si.Observed = res
	if err != nil {
		return si, err
	}
	return si, nil
}

// imageTag is the tag of the image built for a service.
func imageTag(p *compose.Project, dir string, s *compose.Service) string {
	project := p.Name
	if project == "" {
		project = filepath.Base(dir)
	}
	return ToolName + "/" + sanitizeName(project) + "-" + sanitizeName(s.Name) + ":latest"
}

// build builds the image of a service and returns the path of its Dockerfile.
func (a *App) build(ctx context.Context, dir string, s *compose.Service, tag string) (string, error) {
	b := s.Build
	ctxDir := b.Context
	if ctxDir == "" {
		ctxDir = "."
	}
	if strings.Contains(ctxDir, "://") || strings.HasPrefix(ctxDir, "git@") {
		return "", fmt.Errorf("the build context %q is not a local directory", ctxDir)
	}
	ctxDir = absPath(dir, ctxDir)
	df := b.Dockerfile
	if df == "" {
		df = "Dockerfile"
	}
	df = absPath(ctxDir, df)
	bo := docker.BuildOptions{Context: ctxDir, Dockerfile: df, Target: b.Target, Tag: tag, Quiet: true}
	for _, arg := range b.Args {
		val := arg.Value
		if !arg.Set {
			v, ok := a.LookupEnv(arg.Name)
			if !ok {
				continue
			}
			val = v
		} else if strings.Contains(val, "$") {
			v, err := expand(val, a.LookupEnv)
			if err != nil {
				return "", fmt.Errorf("build argument %s: %w", arg.Name, err)
			}
			val = v
		}
		if bo.Args == nil {
			bo.Args = map[string]string{}
		}
		bo.Args[arg.Name] = val
	}
	fmt.Fprintf(a.Stderr, "%s: building %s\n", s.Name, tag)
	if err := a.Docker.Build(ctx, bo); err != nil {
		return "", err
	}
	return df, nil
}

// fetch makes sure the image of a service that is not built is on this
// machine.
func (a *App) fetch(ctx context.Context, o *options, image string) error {
	has, err := a.Docker.HasImage(ctx, image)
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	if !o.pull {
		return fmt.Errorf("the image %q is not on this machine; pull it, or run again with --pull", image)
	}
	fmt.Fprintf(a.Stderr, "pulling %s\n", image)
	return a.Docker.Pull(ctx, image)
}

// stage parses the Dockerfile of a built service and returns the stage that
// was built. A Dockerfile that cannot be read is a warning: the image exists.
func (a *App) stage(s *compose.Service, path string) *dockerfile.Stage {
	f, err := dockerfile.ParseFile(path)
	if err != nil {
		fmt.Fprintf(a.Stderr, "%s: warning: the Dockerfile is not read for the report: %v\n", s.Name, err)
		return nil
	}
	if t := s.Build.Target; t != "" {
		st := f.Stage(t)
		if st == nil {
			fmt.Fprintf(a.Stderr, "%s: warning: the build target %q is not a stage of %s\n", s.Name, t, path)
		}
		return st
	}
	return f.Final()
}

// serviceEnv is the environment given to the observation: the env_file values
// and then the environment values of the file, which win. Values that hold a
// ${ are left out; the generator reports them.
func (a *App) serviceEnv(s *compose.Service, dir string) []string {
	var order []string
	seen := map[string]bool{}
	vals := map[string]string{}
	put := func(k, v string) {
		if !seen[k] {
			seen[k] = true
			order = append(order, k)
		}
		vals[k] = v
	}
	for _, f := range s.EnvFile {
		data, err := a.ReadFile(absPath(dir, f))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			k, v, ok := envLine(line)
			if !ok {
				continue
			}
			if strings.Contains(v, "${") {
				delete(vals, k)
				continue
			}
			put(k, v)
		}
	}
	for _, e := range s.Environment {
		if !e.Set {
			continue
		}
		if strings.Contains(e.Value, "${") {
			delete(vals, e.Name)
			continue
		}
		put(e.Name, e.Value)
	}
	var out []string
	for _, k := range order {
		if v, ok := vals[k]; ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// envLine reads one line of an env_file: KEY=VALUE, with an optional "export "
// and quotes around the value.
func envLine(line string) (key, val string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	key, val, ok = strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false
	}
	val = strings.TrimSpace(val)
	if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
		return key, val[1 : len(val)-1], true
	}
	if i := strings.Index(val, " #"); i >= 0 {
		val = strings.TrimSpace(val[:i])
	}
	return key, val, true
}

// binds are the bind volumes of a service whose host path exists, as
// "host:container[:ro]".
func (a *App) binds(s *compose.Service, dir string) []string {
	var out []string
	for _, v := range s.Volumes {
		if v.Type != "bind" || v.Source == "" || v.Target == "" || strings.Contains(v.Source, "$") {
			continue
		}
		host := absPath(dir, v.Source)
		if _, err := a.Stat(host); err != nil {
			continue
		}
		b := host + ":" + v.Target
		if v.ReadOnly {
			b += ":ro"
		}
		out = append(out, b)
	}
	return out
}

// expand replaces $NAME, ${NAME}, ${NAME:-default} and ${NAME-default} with
// the value of the variable. $$ is a dollar sign. A variable that is not set
// and has no default is an error naming it.
func expand(s string, lookup func(string) (string, bool)) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '$' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		n := s[i+1]
		switch {
		case n == '$':
			b.WriteByte('$')
			i++
		case n == '{':
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				return "", fmt.Errorf("%q has a ${ that is not closed", s)
			}
			v, err := expandBraced(s[i+2:i+2+end], lookup)
			if err != nil {
				return "", err
			}
			b.WriteString(v)
			i += 2 + end
		case nameStart(n):
			j := i + 1
			for j < len(s) && nameChar(s[j]) {
				j++
			}
			v, ok := lookup(s[i+1 : j])
			if !ok {
				return "", unsetError(s[i+1 : j])
			}
			b.WriteString(v)
			i = j - 1
		default:
			b.WriteByte('$')
		}
	}
	return b.String(), nil
}

func expandBraced(body string, lookup func(string) (string, bool)) (string, error) {
	j := 0
	for j < len(body) && (j == 0 && nameStart(body[j]) || j > 0 && nameChar(body[j])) {
		j++
	}
	name, rest := body[:j], body[j:]
	if name == "" {
		return "", fmt.Errorf("${%s} does not name a variable", body)
	}
	v, ok := lookup(name)
	switch {
	case rest == "":
		if !ok {
			return "", unsetError(name)
		}
		return v, nil
	case strings.HasPrefix(rest, ":-"):
		if !ok || v == "" {
			return rest[2:], nil
		}
		return v, nil
	case strings.HasPrefix(rest, "-"):
		if !ok {
			return rest[1:], nil
		}
		return v, nil
	}
	return "", fmt.Errorf("${%s} is not supported; use ${NAME}, ${NAME:-default} or ${NAME-default}", body)
}

func unsetError(name string) error {
	return errors.New("the variable " + name + " is not set in the environment")
}

func nameStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func nameChar(c byte) bool {
	return nameStart(c) || c >= '0' && c <= '9'
}

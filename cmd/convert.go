package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AngeloRubens/dock2nano/internal/compose"
	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/dockerfile"
	"github.com/AngeloRubens/dock2nano/internal/gen"
	"github.com/AngeloRubens/dock2nano/internal/interp"
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
	if !filepath.IsAbs(wd) {
		return 0, fmt.Errorf("the current directory %q is not an absolute path", wd)
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

	if project.Name == "" {
		project.Name = projectNameOf(dir)
	}

	// The package names are checked before anything is built or run.
	var converted []string
	skipped := map[string]string{}
	for _, s := range ordered {
		if reason := notConverted(s, only); reason != "" {
			skipped[s.Name] = reason
			continue
		}
		converted = append(converted, s.Name)
	}
	if len(converted) > 0 {
		if _, _, err := gen.PackageNames(project, converted); err != nil {
			return 0, err
		}
	}

	inputs := map[string]gen.ServiceInput{}
	var sysroots []string
	defer func() {
		for _, s := range sysroots {
			a.RemoveAll(s)
		}
	}()
	arch, archOwner := "", ""
	for _, s := range ordered {
		if _, no := skipped[s.Name]; no {
			continue
		}
		si, err := a.prepare(ctx, o, project, dir, s)
		if si.Observed != nil && si.Observed.Sysroot != "" {
			sysroots = append(sysroots, si.Observed.Sysroot)
		}
		if err != nil {
			return 0, fmt.Errorf("service %s: %w", s.Name, err)
		}
		if si.Image != nil && si.Image.Architecture != "" {
			ia, err := packageArch(si.Image.Architecture, si.Image.Variant)
			if err != nil {
				return 0, fmt.Errorf("service %s: %w", s.Name, err)
			}
			if arch == "" {
				arch, archOwner = ia, s.Name
			} else if ia != arch {
				return 0, fmt.Errorf("the images of the services %s (%s) and %s (%s) have different architectures; one run converts one architecture", archOwner, arch, s.Name, ia)
			}
		}
		inputs[s.Name] = si
	}

	in := gen.Input{Project: project, Dir: dir, Services: inputs, Skipped: skipped, Arch: arch}
	in.HostEnv = a.LookupEnv
	in.ReadFile = a.ReadFile
	in.Stat = a.Stat
	p, err := a.Generate(in)
	if err != nil {
		if p != nil {
			fmt.Fprint(a.Stdout, gen.Render(p.Report))
		}
		return 0, err
	}
	composePath := o.out
	if !filepath.IsAbs(composePath) {
		composePath = filepath.Join(wd, composePath)
	}
	composePath = filepath.Join(composePath, "compose.yaml")
	home := a.opsHome(o, wd)
	written, err := a.Write(p, output.Options{OpsHome: home, ComposePath: composePath, Force: o.force})
	if err != nil {
		fmt.Fprint(a.Stdout, gen.Render(p.Report))
		return 0, err
	}

	fmt.Fprint(a.Stdout, gen.Render(p.Report))
	fmt.Fprintln(a.Stdout)
	fmt.Fprintln(a.Stdout, "Written:")
	for _, w := range written {
		fmt.Fprintf(a.Stdout, "  %s\n", w)
	}
	a.nextSteps(home, composePath)

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

// opsHome is the OPS_HOME given to the writer: the flag, else the variable,
// else the home folder of the user, made absolute against wd. Empty leaves the
// choice to the writer.
func (a *App) opsHome(o *options, wd string) string {
	h := o.opsHome
	if h == "" {
		if v, ok := a.LookupEnv("OPS_HOME"); ok && v != "" {
			h = v
		}
	}
	if h == "" && a.UserHomeDir != nil {
		if d, err := a.UserHomeDir(); err == nil {
			h = d
		}
	}
	if h == "" {
		return ""
	}
	return absPath(wd, h)
}

// packageArch is the architecture of the packages for an image: docker's
// spelling mapped to the two that ops knows. arm64 with any variant (v8) is
// fine; 32-bit arm and the rest are errors.
func packageArch(arch, variant string) (string, error) {
	switch arch {
	case "amd64", "x86_64":
		return "amd64", nil
	case "arm64", "aarch64":
		return "arm64", nil
	}
	name := arch
	if variant != "" {
		name += "/" + variant
	}
	return "", fmt.Errorf("the architecture %s of the image is not supported; nanos packages are amd64 or arm64", name)
}

// notConverted returns why a service is not converted, or "" when it is.
func notConverted(s *compose.Service, only map[string]bool) string {
	switch {
	case only != nil && !only[s.Name]:
		return "not selected with --service"
	case len(s.Profiles) > 0:
		return "has profiles (" + strings.Join(s.Profiles, ", ") + ")"
	case s.Replicas != nil && *s.Replicas == 0:
		return "has 0 replicas"
	case s.Image == "" && s.Build == nil:
		return "has no image and no build"
	}
	return ""
}

// projectNameOf is the project name when the file gives none: the folder of
// the compose file.
func projectNameOf(dir string) string {
	base := filepath.Base(dir)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "project"
	}
	return base
}

// nextSteps prints what to run to start the result. The second command is the
// form that the e2e job runs: ops compose creates network interfaces and
// starts qemu, so it needs root, and root needs the OPS_HOME and the PATH of
// the user. home and composePath are absolute.
func (a *App) nextSteps(home, composePath string) {
	prefix := ""
	if home != "" {
		prefix = "OPS_HOME=" + shellQuote(home) + " "
	}
	fmt.Fprintln(a.Stdout)
	fmt.Fprintln(a.Stdout, "Next steps:")
	fmt.Fprintf(a.Stdout, "  %sops pkg get %s    (once, if you do not have it yet)\n", prefix, opsDNSPackage)
	fmt.Fprintf(a.Stdout, "  sudo env %sPATH=\"$PATH\" ops compose up -f %s\n", prefix, shellQuote(composePath))
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
		if strings.Trim(name, "-._") == "" {
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

	ep, cmd, err := gen.EffectiveCommand(s, img, a.LookupEnv)
	if err != nil {
		return si, err
	}
	// The entrypoint is always passed, even when empty, so that the CMD of the
	// image is replaced by cmd and an entrypoint cleared in the file stays
	// cleared.
	if ep == nil {
		ep = &[]string{}
	}
	oo := observe.Options{Image: image, Entrypoint: ep, Cmd: cmd}
	oo.Env = a.serviceEnv(s, dir)
	oo.WorkingDir = s.WorkingDir
	oo.User = s.User
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

// imageTag is the tag of the image built for a service: the tool, the project
// and the service, each one a valid component of an image name.
func imageTag(p *compose.Project, dir string, s *compose.Service) string {
	project := p.Name
	if project == "" {
		project = filepath.Base(dir)
	}
	return ToolName + "/" + tagComponent(project) + "/" + tagComponent(s.Name) + ":latest"
}

// tagComponent turns s into a component of an image name: lower case, every
// character outside [a-z0-9_.-] becomes '-', a run of separators becomes its
// first character, and the ends are alphanumeric. An empty result is "x".
func tagComponent(s string) string {
	var b strings.Builder
	for _, r := range sanitizeName(s) {
		sep := r == '-' || r == '_' || r == '.'
		if sep && b.Len() > 0 {
			last := b.String()[b.Len()-1]
			if last == '-' || last == '_' || last == '.' {
				continue
			}
		}
		b.WriteRune(r)
	}
	out := strings.Trim(b.String(), "-_.")
	if out == "" {
		return "x"
	}
	return out
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
		} else {
			v, unset, err := interp.Expand(val, a.LookupEnv)
			if err != nil {
				return "", fmt.Errorf("build argument %s: %w", arg.Name, err)
			}
			for _, u := range unset {
				fmt.Fprintf(a.Stderr, "%s: warning: build argument %s uses the variable %s, which is not set; it is replaced by an empty string\n", s.Name, arg.Name, u)
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

// serviceEnv is the environment given to the observation: what the generator
// computes from environment and env_file, so that both use one source.
func (a *App) serviceEnv(s *compose.Service, dir string) []string {
	env, _ := gen.ServiceEnv(s, dir, a.LookupEnv, a.ReadFile)
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// binds are the bind volumes of a service whose host path exists, as
// "host:container[:ro]".
func (a *App) binds(s *compose.Service, dir string) []string {
	var out []string
	for _, v := range s.Volumes {
		if v.Type != "bind" || v.Source == "" || v.Target == "" || strings.Contains(v.Source, "$") {
			continue
		}
		if strings.Contains(v.Source, ":") {
			fmt.Fprintf(a.Stderr, "%s: warning: the bind source %q has a ':' and is not passed to the observation\n", s.Name, v.Source)
			continue
		}
		host := absPath(dir, v.Source)
		if !filepath.IsAbs(host) || !strings.HasPrefix(v.Target, "/") {
			fmt.Fprintf(a.Stderr, "%s: warning: the bind %q to %q needs absolute paths and is not passed to the observation\n", s.Name, host, v.Target)
			continue
		}
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

package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

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
	extra, err := applyWith(project, o.with, only)
	if err != nil {
		return 0, err
	}
	for _, m := range extra {
		fmt.Fprintln(a.Stderr, m)
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

	// One network for the whole run: the service under observation and the
	// services it depends on reach each other by the names of the file, as they
	// do with docker compose. It is removed however the run ends.
	rs := &runState{noWith: o.noWith, retried: map[string]bool{}, hubArgs: map[string]hubArgs{}, extra: map[string][]string{}, firstUp: map[string][]string{}, crashed: map[string]bool{}, firstImg: map[string]builtImage{}, again: map[string]bool{}, ordered: ordered, skipped: skipped, starts: map[string]observe.StartOptions{}, running: map[string]Stopper{}, noted: map[string][]string{}, served: map[string][]string{}, failed: map[string]string{}, late: map[string]string{}}
	// The copies of the binds are removed however the run ends, after the
	// containers that use them are stopped.
	defer rs.removeCopies(a)
	rs.rwSources = rwSources(ordered, skipped, dir)
	if len(converted) > 0 {
		name, err := networkName()
		if err != nil {
			return 0, err
		}
		if err := a.Docker.NetworkCreate(ctx, name, "dock2nano=1"); err != nil {
			// Docker may have made the network even though the call failed (a
			// cancelled context): remove it, best effort.
			cctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancel()
			a.Docker.NetworkRemove(cctx, name)
			return 0, fmt.Errorf("creating the docker network %s: %w", name, err)
		}
		rs.network = name
		defer func() {
			cctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancel()
			if err := a.Docker.NetworkRemove(cctx, name); err != nil {
				fmt.Fprintf(a.Stderr, "%s: warning: the docker network %s is not removed: %v\n", ToolName, name, err)
			}
		}()
		// registered after the removal of the network, so it runs before it
		defer rs.stopAll(a)
	}

	inputs := map[string]gen.ServiceInput{}
	var sysroots []string
	defer func() {
		for _, s := range sysroots {
			a.RemoveAll(s)
		}
	}()
	arch, archOwner := "", ""
	checkArch := func(name string, si gen.ServiceInput) error {
		if si.Image == nil || si.Image.Architecture == "" {
			return nil
		}
		ia, err := packageArch(si.Image.Architecture, si.Image.Variant)
		if err != nil {
			return fmt.Errorf("service %s: %w", name, err)
		}
		if arch == "" {
			arch, archOwner = ia, name
		} else if ia != arch {
			return fmt.Errorf("the images of the services %s (%s) and %s (%s) have different architectures; one run converts one architecture", archOwner, arch, name, ia)
		}
		return nil
	}
	for _, s := range ordered {
		if _, no := skipped[s.Name]; no {
			continue
		}
		si, err := a.prepare(ctx, o, project, dir, s, rs)
		if si.Observed != nil && si.Observed.Sysroot != "" {
			sysroots = append(sysroots, si.Observed.Sysroot)
		}
		if err != nil {
			return 0, fmt.Errorf("service %s: %w", s.Name, err)
		}
		if err := checkArch(s.Name, si); err != nil {
			return 0, err
		}
		inputs[s.Name] = si
	}

	// A service that stopped with an error while none of the others ran with it, and that the file
	// does not make depend on any, is observed once more with some of the others running. The file
	// and the compose.yaml are not changed by it: when the second observation does not stop, the
	// report suggests the depends_on to write.
	for _, name := range rs.retry {
		s := project.Service(name)
		first := inputs[name]
		ha := rs.hubArgs[name]
		// a container of this service that was started for a dependent, or that has stopped since,
		// is stopped: a second container of the same name would share its name on the network
		stoppedOwn := rs.running[name] != nil
		cands := rs.pickCandidates(rs.candidates(s, true))
		if len(cands) == 0 {
			// none of the others could run with it: the stop stands, and the hub is read as it
			// is for a service that needs nobody
			if err := a.hubStep(ctx, o, rs, s, ha.image, ha.built, &first); err != nil {
				return 0, fmt.Errorf("service %s: %w", name, err)
			}
			inputs[name] = first
			continue
		}
		status := first.Observed.ExitCode
		if stoppedOwn {
			rs.stopOne(a, name)
		}
		rs.extra[name] = cands
		rs.retried[name] = true
		rs.again[name] = true
		fmt.Fprintf(a.Stderr, "%s: it stopped with status %d while %s did not run; observing it again with them running (--no-with turns this off)\n", name, status, strings.Join(cands, ", "))
		binds := rs.resetBinds(a, project, dir, s)
		si, err := a.prepare(ctx, o, project, dir, s, rs)
		if si.Observed != nil && si.Observed.Sysroot != "" {
			sysroots = append(sysroots, si.Observed.Sysroot)
		}
		all := rs.extraList(s)
		delete(rs.extra, name)
		if err != nil {
			if ctx.Err() != nil {
				return 0, fmt.Errorf("service %s: %w", name, err)
			}
			// the first observation stands
			msg := fmt.Sprintf("it was observed once more with %s running, which failed (%v); this is the first observation, which stopped with status %d", strings.Join(cands, ", "), err, status)
			fmt.Fprintf(a.Stderr, "%s: warning: %s\n", name, msg)
			first.Observed.Warnings = append(first.Observed.Warnings, msg)
			if err := a.hubStep(ctx, o, rs, s, ha.image, ha.built, &first); err != nil {
				return 0, fmt.Errorf("service %s: %w", name, err)
			}
			inputs[name] = first
			continue
		}
		if first.Observed.Sysroot != "" {
			a.RemoveAll(first.Observed.Sysroot)
			sysroots = slices.DeleteFunc(sysroots, func(p string) bool { return p == first.Observed.Sysroot })
		}
		if err := checkArch(name, si); err != nil {
			return 0, err
		}
		var ran, notRan []string
		for _, c := range all {
			if rs.up(c) {
				ran = append(ran, c)
			} else {
				why := rs.failed[c]
				if why == "" {
					why = "it stopped while the observation ran"
				}
				notRan = append(notRan, fmt.Sprintf("%s (%s)", c, why))
			}
		}
		if si.Observed != nil {
			w := &si.Observed.Warnings
			*w = append(*w, binds...)
			if len(notRan) > 0 {
				*w = append(*w, "these services were meant to run beside it for the second observation and did not: "+strings.Join(notRan, "; "))
			}
			still := si.Observed.StoppedEarly && si.Observed.ExitCode != 0
			switch {
			case len(ran) == 0:
				*w = append(*w, fmt.Sprintf("observed alone it stopped with status %d; none of the other services could run beside it, so the second observation shows nothing new", status))
			case still:
				*w = append(*w, fmt.Sprintf("observed alone it stopped with status %d; with %s running it stopped with status %d as well; nothing is concluded from it", status, strings.Join(ran, ", "), si.Observed.ExitCode))
			default:
				list := strings.Join(ran, ", ")
				var msg string
				if len(ran) == 1 {
					msg = fmt.Sprintf("observed alone it stopped with status %d; with %s running it did not stop with an error in %d seconds; if %s needs it, add it to depends_on of %s in docker-compose.yml (nothing was changed in the output for it)", status, list, o.seconds, name, name)
				} else {
					msg = fmt.Sprintf("observed alone it stopped with status %d; with %s running it did not stop with an error in %d seconds; one or more of them may be needed: if so, add them to depends_on of %s in docker-compose.yml; the tool did not tell which one (nothing was changed in the output for it)", status, list, o.seconds, name)
				}
				si.HubItems = append(si.HubItems, plan.Item{Service: name, Kind: plan.Note, Key: "depends_on", Message: msg})
			}
		}
		// what was observed before the second observation of this service did not have it
		for other, osi := range inputs {
			if other == name || osi.Observed == nil {
				continue
			}
			if x := project.Service(other); x != nil && slices.Contains(rs.closure(x), name) {
				if slices.Contains(rs.served[name], other) {
					osi.Observed.Warnings = append(osi.Observed.Warnings, fmt.Sprintf("it was observed with the container of %s started from the file; %s was observed once more later, with other services running", name, name))
				} else {
					osi.Observed.Warnings = append(osi.Observed.Warnings, fmt.Sprintf("it was observed without %s (%s did not start); %s was observed once more later, with other services running", name, name, name))
				}
			}
		}
		inputs[name] = si
	}

	// The package of a service holds its files as they were at the end of its own observation. What
	// the services observed beside it did to it afterwards (a schema, rows, files) is not in it.
	for name, by := range rs.served {
		if si, ok := inputs[name]; ok && si.Observed != nil {
			si.Observed.Warnings = append(si.Observed.Warnings, fmt.Sprintf("%s was observed while this service was running; what %s wrote into it (tables, rows, files) is not in this package, which holds the files as they were at the end of its own observation", strings.Join(by, ", "), strings.Join(by, ", ")))
		}
	}

	for name, why := range rs.late {
		skipped[name] = why
	}
	in := gen.Input{Project: project, Dir: dir, Services: inputs, Skipped: skipped, Arch: arch, ShortNames: o.shortNames}
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

// applyWith adds a depends_on on each service of --with to every other service that the service
// does not itself need (that would be a cycle), so that it is prepared, started for their
// observation and listed before them. It returns a line to say for each service that got one.
func applyWith(p *compose.Project, with []string, only map[string]bool) ([]string, error) {
	var out []string
	for _, w := range with {
		x := p.Service(w)
		if x == nil {
			return nil, fmt.Errorf("--with %s: the file has no such service", w)
		}
		if only != nil && !only[w] {
			return nil, fmt.Errorf("--with %s: the service is not one of those chosen with --service", w)
		}
		// what x needs, directly or not, cannot need x
		needs := map[string]bool{}
		var visit func(name string)
		visit = func(name string) {
			if s := p.Service(name); s != nil {
				for _, d := range s.DependsOn {
					if !needs[d.Service] {
						needs[d.Service] = true
						visit(d.Service)
					}
				}
			}
		}
		visit(w)
		for _, s := range p.Services {
			if s.Name == w || needs[s.Name] {
				continue
			}
			already := false
			for _, d := range s.DependsOn {
				if d.Service == w {
					already = true
				}
			}
			if already {
				continue
			}
			s.DependsOn = append(s.DependsOn, compose.Dependency{Service: w, Condition: "service_started"})
			out = append(out, fmt.Sprintf("%s: %s is started with it for the observation (--with %s)", s.Name, w, w))
		}
	}
	return out, nil
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
func (a *App) prepare(ctx context.Context, o *options, p *compose.Project, dir string, s *compose.Service, rs *runState) (gen.ServiceInput, error) {
	var si gen.ServiceInput
	image := s.Image
	dfPath := ""
	if prev, ok := rs.firstImg[s.Name]; ok && rs.again[s.Name] {
		// the second observation is of the image of the first one, not of a new build or pull
		image, dfPath = prev.image, prev.dfPath
	} else {
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
		rs.firstImg[s.Name] = builtImage{image: image, dfPath: dfPath}
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
	var bindNotes []string
	oo.Binds, bindNotes = rs.mountBinds(a, s.Name, a.binds(s, dir))
	oo.Binds = append(oo.Binds, hiddenVolumes(s)...)
	oo.Seconds = o.seconds
	oo.Exercise = exerciseFor(s.Name, o.exercise, p)
	if cmds := exerciseFor(s.Name, o.exerciseCmd, p); len(cmds) > 0 {
		oo.ExerciseCmd = strings.Join(cmds, " && ")
	}
	oo.Log = func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		fmt.Fprintf(a.Stderr, "%s: %s\n", s.Name, strings.TrimRight(msg, "\n"))
	}
	// The same command, environment and binds start the service when another
	// one needs it.
	rs.starts[s.Name] = observe.StartOptions{Image: image, Entrypoint: ep, Cmd: cmd, Env: oo.Env, WorkingDir: oo.WorkingDir, User: oo.User, Binds: oo.Binds, Ports: declaredPorts(s)}

	notes, err := rs.startDependencies(ctx, a, s)
	if err != nil {
		return si, err
	}
	oo.Network = rs.network
	oo.NetworkAliases = []string{s.Name}
	fmt.Fprintf(a.Stderr, "%s: observing %s for %d seconds\n", s.Name, image, o.seconds)
	res, err := a.Observe(ctx, oo)
	si.Observed = res
	if err != nil {
		return si, err
	}
	if res != nil {
		res.Warnings = append(res.Warnings, bindNotes...)
		res.Warnings = append(res.Warnings, notes...)
	}
	rs.findCrashed(ctx, a, s, res)
	if res != nil {
		if up := rs.runningDeps(s); len(up) > 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("the services running during this observation: %s (its depends_on or --with)", strings.Join(up, ", ")))
		}
		var beside []string
		for _, n := range rs.extraList(s) {
			if rs.up(n) {
				beside = append(beside, n)
			}
		}
		if len(beside) > 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("the services started beside it for this second observation: %s (the file does not make it depend on them)", strings.Join(beside, ", ")))
		}
	}
	// what ran, even if it died during the observation: it is no new evidence for a second one.
	// (The hub step of a service that waits for its second observation runs later, so the note
	// that the list of the hub is an old copy goes to whichever service reads it first.)
	rs.firstUp[s.Name] = rs.runningNames()
	// A stop with an error while none of the dependencies ran says nothing about the program: the
	// service is observed again, when the others are all prepared, and the hub waits for that.
	if rs.wantsRetry(s, res) {
		rs.retry = append(rs.retry, s.Name)
		rs.hubArgs[s.Name] = hubArgs{image: image, built: s.Build != nil}
		return si, nil
	}
	if err := a.hubStep(ctx, o, rs, s, image, s.Build != nil, &si); err != nil {
		return si, err
	}
	return si, nil
}

// builtImage is the image that a service was observed from and the Dockerfile it was built with.
type builtImage struct {
	image  string
	dfPath string
}

// hubArgs is what the hub step of a service whose observation is put off needs.
type hubArgs struct {
	image string
	built bool
}

// retryMax is the most services that a second observation starts beside the one observed.
const retryMax = 5

// up says whether a service was started for an observation and was still running after the last
// one.
func (rs *runState) up(name string) bool {
	return rs.running[name] != nil && !rs.crashed[name]
}

// runningNames returns the services that have a container started for an observation, up or
// not, in file order.
func (rs *runState) runningNames() []string {
	var out []string
	for _, x := range rs.ordered {
		if rs.running[x.Name] != nil {
			out = append(out, x.Name)
		}
	}
	return out
}

// findCrashed marks the started services that stopped while s was observed, and says so in the
// report of s. A stopper that cannot tell is taken to be running.
func (rs *runState) findCrashed(ctx context.Context, a *App, s *compose.Service, res *observe.Result) {
	for _, x := range rs.ordered {
		n := x.Name
		st := rs.running[n]
		if st == nil || rs.crashed[n] {
			continue
		}
		if u, ok := st.(interface{ Up(context.Context) bool }); ok && !u.Up(ctx) {
			rs.crashed[n] = true
			msg := fmt.Sprintf("the service %s stopped while %s was observed, so it is not counted as running", n, s.Name)
			fmt.Fprintf(a.Stderr, "%s: warning: %s\n", s.Name, msg)
			if res != nil {
				res.Warnings = append(res.Warnings, msg)
			}
		}
	}
}

// startList returns the services started for the observation of s: those it needs and the ones
// of a second observation, with what each of them needs, in dependency order.
func (rs *runState) startList(s *compose.Service) []string {
	need := map[string]bool{}
	for _, n := range rs.closure(s) {
		need[n] = true
	}
	for _, e := range rs.extra[s.Name] {
		need[e] = true
		if x := rs.service(e); x != nil {
			for _, n := range rs.closure(x) {
				need[n] = true
			}
		}
	}
	var out []string
	for _, x := range rs.ordered {
		if need[x.Name] && x.Name != s.Name {
			out = append(out, x.Name)
		}
	}
	return out
}

// extraList returns what is started for a second observation of s beside what the file makes it
// need: the candidates and what they need, in dependency order.
func (rs *runState) extraList(s *compose.Service) []string {
	declared := map[string]bool{}
	for _, n := range rs.closure(s) {
		declared[n] = true
	}
	var out []string
	for _, n := range rs.startList(s) {
		if !declared[n] {
			out = append(out, n)
		}
	}
	return out
}

// pickCandidates keeps the candidates that fit in retryMax started containers, counting what each
// needs that does not run yet. A candidate that would make it more is left out.
func (rs *runState) pickCandidates(cands []string) []string {
	total := map[string]bool{}
	var out []string
	for _, c := range cands {
		need := map[string]bool{}
		names := []string{c}
		if x := rs.service(c); x != nil {
			names = append(names, rs.closure(x)...)
		}
		for _, n := range names {
			if !rs.up(n) && !total[n] {
				need[n] = true
			}
		}
		if len(total)+len(need) > retryMax {
			continue
		}
		for n := range need {
			total[n] = true
		}
		out = append(out, c)
	}
	return out
}

// stopOne stops the container started for an observation and forgets it, so that it can be
// started again.
func (rs *runState) stopOne(a *App, name string) {
	st := rs.running[name]
	if st == nil {
		return
	}
	if err := st.Stop(context.Background()); err != nil {
		fmt.Fprintf(a.Stderr, "%s: warning: stopping %s: %v\n", ToolName, name, err)
	}
	delete(rs.running, name)
	delete(rs.crashed, name)
	rs.order = slices.DeleteFunc(rs.order, func(n string) bool { return n == name })
}

func (rs *runState) service(name string) *compose.Service {
	for _, x := range rs.ordered {
		if x.Name == name {
			return x
		}
	}
	return nil
}

// runningDeps returns the services that s needs by the file and that are running, in dependency
// order.
func (rs *runState) runningDeps(s *compose.Service) []string {
	var out []string
	for _, n := range rs.closure(s) {
		if rs.up(n) {
			out = append(out, n)
		}
	}
	return out
}

// missingDeps returns the services that s needs by the file and that are not running.
func (rs *runState) missingDeps(s *compose.Service) []string {
	var out []string
	for _, n := range rs.closure(s) {
		if !rs.up(n) {
			out = append(out, n)
		}
	}
	return out
}

// candidates returns the services that could run beside s for a second observation: the others
// that do not need s, that did not run when its first observation ended (they would show nothing
// new), that did not stop with an error alone themselves, that were not found unable to start and, with prepared, that were prepared.
func (rs *runState) candidates(s *compose.Service, prepared bool) []string {
	was := map[string]bool{}
	for _, n := range rs.firstUp[s.Name] {
		was[n] = true
	}
	var out []string
	for _, x := range rs.ordered {
		if x.Name == s.Name || was[x.Name] || rs.failed[x.Name] != "" {
			continue
		}
		if _, no := rs.skipped[x.Name]; no {
			continue
		}
		if _, no := rs.late[x.Name]; no {
			continue
		}
		// a service that stopped with an error when it was alone gives no evidence as a neighbour
		if slices.Contains(rs.retry, x.Name) {
			continue
		}
		if prepared {
			if _, ok := rs.starts[x.Name]; !ok {
				continue
			}
		}
		if slices.Contains(rs.closure(x), s.Name) {
			continue
		}
		out = append(out, x.Name)
	}
	return out
}

// wantsRetry says whether the observation of s stopped with an error, the file makes it depend on
// nothing, and others could run beside it for a second observation.
func (rs *runState) wantsRetry(s *compose.Service, res *observe.Result) bool {
	if rs.noWith || res == nil || !res.StoppedEarly || res.ExitCode == 0 || res.Children > 0 || rs.retried[s.Name] {
		return false
	}
	if len(s.DependsOn) > 0 {
		return false
	}
	return len(rs.candidates(s, false)) > 0
}

// resetBinds makes a new copy of each read-write bind of s that no other service mounts, so that
// the second observation starts from the files of the user and not from what the first one wrote.
// For a bind that another service mounts too it returns a note: that copy is kept.
func (rs *runState) resetBinds(a *App, p *compose.Project, dir string, s *compose.Service) []string {
	var notes []string
	for _, b := range a.binds(s, dir) {
		if strings.HasSuffix(b, ":ro") {
			continue
		}
		host, _, _ := strings.Cut(b, ":")
		m := rs.binds[host]
		if m == nil || m.path == "" || m.readOnly {
			continue
		}
		shared := ""
		for _, x := range p.Services {
			if x.Name == s.Name {
				continue
			}
			for _, xb := range a.binds(x, dir) {
				if h, _, _ := strings.Cut(xb, ":"); h == host {
					shared = x.Name
				}
			}
		}
		if shared != "" {
			notes = append(notes, fmt.Sprintf("the bind %s is shared with %s: the second observation started from what the first one left there", host, shared))
			continue
		}
		delete(rs.binds, host)
	}
	return notes
}

// cleanupTimeout bounds the removal of the network and of the containers at
// the end of a run.
const cleanupTimeout = 2 * time.Minute

// networkName is a name for the network of one run: dock2nano- and 8 random
// hex digits.
func networkName() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("making a network name: %w", err)
	}
	return "dock2nano-" + hex.EncodeToString(b), nil
}

// runState is what the services of one run share: the network, how to start
// each prepared service and the ones that are running for the observation of
// others.
type runState struct {
	network string
	// noWith is --no-with; retry holds the services to observe again with the others running;
	// retried the ones that were.
	noWith  bool
	retry   []string
	retried map[string]bool
	hubArgs map[string]hubArgs
	// extra holds, by service, the services started beside it for its second observation; they
	// are not in the file as its dependencies. firstUp holds the services that ran when its first
	// observation ended. crashed holds the started services found stopped after an observation.
	extra   map[string][]string
	firstUp map[string][]string
	crashed map[string]bool
	// firstImg holds the image of each service as the first observation had it; again is set for
	// the one that is observed a second time, which reuses it: no new build or pull.
	firstImg map[string]builtImage
	again    map[string]bool

	ordered []*compose.Service
	skipped map[string]string
	// starts holds, by service name, how to start a service that was prepared.
	starts map[string]observe.StartOptions
	// running holds the services started for the observation of others; order
	// is the order they were started in.
	running map[string]Stopper
	// noted holds what Start noticed about each started service; every service
	// observed with it running gets the notes, not only the first one.
	noted map[string][]string
	// served holds, by service name, the services that were observed while it was running.
	served map[string][]string
	order  []string
	// failed holds why a service could not be started, so that it is not tried
	// again for the next service that needs it.
	failed map[string]string
	// late holds why a service was found not convertible after it was observed
	// (the hub). It is kept apart from skipped, which refuses to start a service
	// for the observation of the ones that need it.
	late map[string]string
	// staleSaid is set once the report has said that the list of the hub is an
	// old copy, which is said for one service only.
	staleSaid bool
	// binds holds, by host path, the mount of each read-write bind: the copy
	// that the observation and the started services share. tmpRoot is the
	// private directory with the copies; copies counts them.
	binds map[string]*bindMount
	// rwSources are the host paths that some service mounts read-write.
	rwSources map[string]bool
	tmpRoot   string
	copies    int
}

// closure returns the services that s depends on, directly or not, in
// dependency order.
func (rs *runState) closure(s *compose.Service) []string {
	deps := map[string][]compose.Dependency{}
	for _, x := range rs.ordered {
		deps[x.Name] = x.DependsOn
	}
	need := map[string]bool{}
	var visit func(name string)
	visit = func(name string) {
		for _, d := range deps[name] {
			if !need[d.Service] {
				need[d.Service] = true
				visit(d.Service)
			}
		}
	}
	visit(s.Name)
	var out []string
	for _, x := range rs.ordered {
		if need[x.Name] {
			out = append(out, x.Name)
		}
	}
	return out
}

// startDependencies starts, in dependency order, the services that s needs and
// that are not running yet. A service that cannot be started is not an error:
// it gives a note for the report, and s is observed without it. The error is
// the one of a context that has ended.
func (rs *runState) startDependencies(ctx context.Context, a *App, s *compose.Service) ([]string, error) {
	var notes []string
	if rs.noWith {
		if need := rs.closure(s); len(need) > 0 {
			msg := fmt.Sprintf("the services %s were not started for the observation of %s (--no-with)", strings.Join(need, ", "), s.Name)
			fmt.Fprintf(a.Stderr, "%s: warning: %s\n", s.Name, msg)
			notes = append(notes, msg)
		}
		return notes, nil
	}
	for _, name := range rs.startList(s) {
		if rs.running[name] != nil && rs.crashed[name] {
			msg := fmt.Sprintf("the service %s stopped during an earlier observation; it is started again for the observation of %s", name, s.Name)
			fmt.Fprintf(a.Stderr, "%s: warning: %s\n", s.Name, msg)
			notes = append(notes, msg)
			rs.stopOne(a, name)
		}
		if rs.running[name] != nil {
			rs.served[name] = append(rs.served[name], s.Name)
			for _, n := range rs.noted[name] {
				msg := fmt.Sprintf("the service %s was started for the observation of %s: %s", name, s.Name, n)
				fmt.Fprintf(a.Stderr, "%s: warning: %s\n", s.Name, msg)
				notes = append(notes, msg)
			}
			continue
		}
		why := rs.failed[name]
		if why == "" {
			if reason, no := rs.skipped[name]; no {
				why = reason
			} else if _, ok := rs.starts[name]; !ok {
				why = "it was not prepared"
			}
		}
		if why == "" {
			so := rs.starts[name]
			so.Network = rs.network
			so.NetworkAliases = []string{name}
			so.Log = func(format string, args ...any) {
				fmt.Fprintf(a.Stderr, "%s: %s\n", name, strings.TrimRight(fmt.Sprintf(format, args...), "\n"))
			}
			fmt.Fprintf(a.Stderr, "%s: starting %s for %s\n", name, so.Image, s.Name)
			st, err := a.Start(ctx, so)
			if err != nil {
				if cerr := ctx.Err(); cerr != nil {
					return nil, cerr
				}
				why = "it did not start: " + err.Error()
				rs.failed[name] = why
			} else {
				rs.running[name] = st
				rs.served[name] = append(rs.served[name], s.Name)
				rs.order = append(rs.order, name)
				// what Start noticed (a container that runs and listens on nothing)
				if nl, ok := st.(interface{ NoteList() []string }); ok {
					rs.noted[name] = nl.NoteList()
				}
				for _, n := range rs.noted[name] {
					msg := fmt.Sprintf("the service %s was started for the observation of %s: %s", name, s.Name, n)
					fmt.Fprintf(a.Stderr, "%s: warning: %s\n", s.Name, msg)
					notes = append(notes, msg)
				}
			}
		}
		if why != "" {
			msg := fmt.Sprintf("the service %s was not started for the observation (%s); %s was observed without it", name, why, s.Name)
			fmt.Fprintf(a.Stderr, "%s: warning: %s\n", s.Name, msg)
			notes = append(notes, msg)
		}
	}
	return notes, nil
}

// stopAll stops the services started for the observations, the last one
// first.
func (rs *runState) stopAll(a *App) {
	for i := len(rs.order) - 1; i >= 0; i-- {
		name := rs.order[i]
		if err := rs.running[name].Stop(context.Background()); err != nil {
			fmt.Fprintf(a.Stderr, "%s: warning: stopping %s: %v\n", ToolName, name, err)
		}
	}
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

// hiddenVolumes are the container paths of the volumes of a service that docker fills from the
// image: an anonymous volume, or a named one under a bind mount. They are passed to the observation
// as anonymous volumes, because a bind mount over a directory hides what the image has there (the
// node_modules of an image, with a bind of the source over its parent) and the volume is what docker
// puts back. What a volume holds is not kept by the package, as the report says for every volume.
func hiddenVolumes(s *compose.Service) []string {
	var binds []string
	for _, v := range s.Volumes {
		if v.Type == "bind" && v.Target != "" {
			binds = append(binds, path.Clean(v.Target))
		}
	}
	var out []string
	for _, v := range s.Volumes {
		if v.Type != "volume" || !strings.HasPrefix(v.Target, "/") || strings.Contains(v.Target, ":") {
			continue
		}
		t := path.Clean(v.Target)
		under := false
		for _, b := range binds {
			if t != b && strings.HasPrefix(t, b+"/") {
				under = true
			}
		}
		if v.Source == "" || under {
			out = append(out, t)
		}
	}
	return out
}

// exerciseFor gives the values of --exercise (or --exercise-cmd) that are meant for the service:
// the ones that start with its name and "=", and the ones that name no service of the project.
func exerciseFor(service string, values []string, project *compose.Project) []string {
	var out []string
	for _, v := range values {
		name, rest, ok := strings.Cut(v, "=")
		if ok && !strings.ContainsAny(name, "/: $") && project.Service(name) != nil {
			if name == service {
				out = append(out, rest)
			}
			continue
		}
		out = append(out, v)
	}
	return out
}

// declaredPorts are the tcp ports that the file says the service listens on: the targets of ports
// and the entries of expose. A range, a port that is not a number and a udp port are left out.
func declaredPorts(s *compose.Service) []int {
	var out []int
	add := func(text, proto string) {
		if proto != "" && proto != "tcp" {
			return
		}
		if n, err := strconv.Atoi(text); err == nil && n > 0 && n < 65536 {
			out = append(out, n)
		}
	}
	for _, p := range s.Ports {
		add(p.Target, p.Protocol)
	}
	for _, e := range s.Expose {
		number, proto, _ := strings.Cut(e, "/")
		add(number, strings.ToLower(proto))
	}
	return out
}

// rwSources are the host paths of the bind mounts that some converted service mounts read-write.
func rwSources(services []*compose.Service, skipped map[string]string, dir string) map[string]bool {
	out := map[string]bool{}
	for _, s := range services {
		if _, no := skipped[s.Name]; no {
			continue
		}
		for _, v := range s.Volumes {
			if v.Type != "bind" || v.ReadOnly || v.Source == "" || strings.ContainsAny(v.Source, "$:") {
				continue
			}
			out[absPath(dir, v.Source)] = true
		}
	}
	return out
}

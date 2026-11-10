package cmd

import (
	"context"
	"fmt"
	"os"
	"path"
	"runtime"
	"strings"

	"github.com/AngeloRubens/dock2nano/internal/compose"
	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/gen"
	"github.com/AngeloRubens/dock2nano/internal/hub"
	"github.com/AngeloRubens/dock2nano/internal/observe"
	"github.com/AngeloRubens/dock2nano/internal/plan"
)

// hubCase is what the hub step knows about one observed service.
type hubCase struct {
	service *compose.Service

	// imageName is the name of the image, or "" for an image built here,
	// whose name is ours.
	imageName string

	// wanted is the version the user runs, or "".
	wanted string

	// arch is amd64 or arm64.
	arch string
}

// hubStep consults the hub of ops packages for a service that was observed.
// The default is not to: the program is observed and started, and that is all.
// The hub is read only when the program did not start as one process (the
// container stopped, or the program started child processes), or when the
// program is in the table of builds that the hub holds and must be used whole.
// It never fails the conversion, except that the context ended: what the hub
// did or could not do is in si.HubItems, or in the skip reason of the service.
func (a *App) hubStep(ctx context.Context, o *options, late map[string]string, s *compose.Service, image string, built bool, si *gen.ServiceInput) error {
	obs := si.Observed
	if obs == nil {
		return nil
	}
	arch := hubArch(si.Image)
	if arch == "" {
		return nil
	}
	hc := hubCase{service: s, arch: arch}
	stand := hub.ProgramName(obs.Program)
	if built {
		hc.wanted = hub.ImageVersion(stand, obs.Env)
	} else {
		hc.imageName = hub.ImageName(image)
		hc.wanted = hub.ImageVersion(image, obs.Env)
		stand = hc.imageName
	}
	if row := hub.MatchRequired(a.HubRequired, stand, obs.Program); row != nil {
		return a.hubWhole(ctx, o, late, hc, row, si)
	}
	why := hubTrigger(obs)
	if why == "" {
		return nil
	}
	return a.hubRecipe(ctx, o, hc, why, si)
}

// hubTrigger says why the program did not start as one process, or "" when
// it did.
func hubTrigger(obs *observe.Result) string {
	switch {
	case obs.StoppedEarly:
		return fmt.Sprintf("the container stopped with status %d before the end of the observation", obs.ExitCode)
	case obs.Children > 0:
		return fmt.Sprintf("the program started %d child processes (fork or clone) and nanos runs one process", obs.Children)
	}
	return ""
}

// hubArch is the architecture of the packages for the image, or "" when it is
// not one that ops knows.
func hubArch(img *docker.ImageConfig) string {
	if img == nil || img.Architecture == "" {
		if runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64" {
			return runtime.GOARCH
		}
		return ""
	}
	arch, err := packageArch(img.Architecture, img.Variant)
	if err != nil {
		return ""
	}
	return arch
}

func hubNote(service, key, msg string) plan.Item {
	return plan.Item{Service: service, Kind: plan.Note, Key: key, Message: msg}
}

// hubRecipe takes the recipe of the nearest package of the hub, for a program
// that did not start as one process.
func (a *App) hubRecipe(ctx context.Context, o *options, hc hubCase, why string, si *gen.ServiceInput) error {
	name := hc.service.Name
	if o.noHub {
		si.HubItems = append(si.HubItems, hubNote(name, "hub", "the hub of ops packages was not consulted because --no-hub is set, although "+why+"; nothing was taken from it"))
		return nil
	}
	if a.Hub == nil {
		return nil
	}
	obs := si.Observed
	pkgs, err := a.Hub.Manifest(ctx)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		si.HubItems = append(si.HubItems, hubNote(name, "hub", fmt.Sprintf("the hub of ops packages could not be read (%v) although %s; nothing was taken from it", err, why)))
		return nil
	}
	var pkg hub.Package
	level := hub.NotFound
	names := hub.Names(hc.imageName, obs.Program)
	for _, n := range names {
		if p, l := hub.Select(pkgs, n, hc.wanted, hc.arch); l != hub.NotFound {
			pkg, level = p, l
			break
		}
	}
	if level == hub.NotFound {
		si.HubItems = append(si.HubItems, hubNote(name, "hub", fmt.Sprintf("the hub of ops packages was consulted because %s, and has no package named %s for %s; nothing was taken from it", why, strings.Join(names, " or "), hc.arch)))
		return nil
	}
	var files []string
	if sp, ok := hub.SingleProcessFor(path.Base(obs.Program)); ok {
		files = sp.ConfigPaths
	}
	rec, err := a.Hub.FetchRecipe(ctx, pkg, files)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		si.HubItems = append(si.HubItems, hubNote(name, "hub", fmt.Sprintf("the package %s %s of the hub of ops packages could not be fetched or checked (%v) although %s; nothing was taken from it", pkg.Ref(), pkg.Version, err, why)))
		return nil
	}
	use := gen.HubUse{Service: name, Package: pkg, Level: level, Wanted: hc.wanted, Why: why, Recipe: rec}
	si.HubItems = append(si.HubItems, gen.ApplyHubRecipe(use, obs)...)
	return nil
}

// hubWhole uses a package of the hub whole, for a program of the table of
// required builds. Without the hub the service is not convertible, and the
// service is skipped with the reason.
func (a *App) hubWhole(ctx context.Context, o *options, late map[string]string, hc hubCase, row *hub.Required, si *gen.ServiceInput) error {
	name := hc.service.Name
	obs := si.Observed
	program := path.Base(obs.Program)
	skip := func(why string) error {
		late[name] = fmt.Sprintf("%s runs as several processes and the hub holds a build that must be used whole (%s; %s); %s", program, strings.Join(row.Packages, ", "), row.Reason, why)
		return nil
	}
	if o.noHub {
		return skip("--no-hub turns the hub off, so it cannot be converted")
	}
	if a.Hub == nil {
		return skip("no hub client is available, so it cannot be converted")
	}
	pkgs, err := a.Hub.Manifest(ctx)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		return skip(fmt.Sprintf("the hub could not be read (%v), so it cannot be converted", err))
	}
	pkg, level := hub.SelectFrom(row.Candidates(pkgs, hc.arch), hc.wanted)
	if level == hub.NotFound {
		return skip("the hub has none of those packages for " + hc.arch + ", so it cannot be converted")
	}
	dest, err := a.mkdirTemp()
	if err != nil {
		return skip(fmt.Sprintf("a directory for the package could not be made (%v)", err))
	}
	rec, err := a.Hub.FetchWhole(ctx, pkg, dest)
	if err != nil {
		a.RemoveAll(dest)
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		return skip(fmt.Sprintf("the package %s %s could not be fetched or checked (%v), so it cannot be converted", pkg.Ref(), pkg.Version, err))
	}
	a.RemoveAll(obs.Sysroot)
	obs.Sysroot = dest
	use := gen.HubUse{Service: name, Package: pkg, Level: level, Wanted: hc.wanted, Why: row.Reason, Recipe: rec}
	si.HubItems = append(si.HubItems, gen.ApplyHubWhole(use, obs)...)
	return nil
}

// mkdirTemp makes the directory that a package of the hub is written to.
func (a *App) mkdirTemp() (string, error) {
	if a.MkdirTemp != nil {
		return a.MkdirTemp("", ToolName+"-hub-")
	}
	return os.MkdirTemp("", ToolName+"-hub-")
}

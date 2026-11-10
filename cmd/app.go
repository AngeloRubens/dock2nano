package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/gen"
	"github.com/AngeloRubens/dock2nano/internal/observe"
	"github.com/AngeloRubens/dock2nano/internal/output"
	"github.com/AngeloRubens/dock2nano/internal/plan"
)

// Version is the version printed by the version command. A release build
// replaces it with -ldflags "-X <module>/cmd.Version=...".
var Version = "dev"

// defaultSeconds is how long each service is observed unless --seconds says
// otherwise.
const defaultSeconds = 30

// composeNames are the compose files looked for in the current directory, in
// this order, when -f is not given.
var composeNames = []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}

// usageText is the help text; {tool} stands for ToolName.
const usageText = `Usage:
  {tool} convert [flags]
  {tool} version

convert reads a docker-compose.yml (or one Dockerfile), builds the images,
observes each service while it runs and writes one nanos package per service
and a compose.yaml for "ops compose". What it writes is what was observed:
review it. What could not be converted is printed, never dropped in silence.

Flags of convert:
  -f FILE            the compose file; default: the first of docker-compose.yml,
                     docker-compose.yaml, compose.yml, compose.yaml in the
                     current directory
  --dockerfile FILE  convert one Dockerfile as a single service instead of a
                     compose file (cannot be used with -f)
  --context DIR      build context with --dockerfile; default: the directory
                     of the Dockerfile
  --name NAME        service and project name with --dockerfile; default: the
                     name of the context directory
  -o DIR             directory where compose.yaml goes; default: .
  --ops-home DIR     like the OPS_HOME variable of ops: the folder that holds .ops;
                     default: $OPS_HOME, then the home folder of the user
  --seconds N        how long each service is observed; default: 30
  --pull             pull the images that are missing on this machine
  --force            replace package folders and a compose.yaml that exist
  --strict           exit with status 2 if the report has an unsupported item
  --short-names      let the services reach each other by the name of the compose file (db) and
                     not only as db.service: writes dns_search: [service] in compose.yaml, which
                     needs an ops that reads it
  --exercise [SVC=]REQ  ask the program for something once it is up, so that the files it opens
                     only to answer are carried: /health is asked of the first port the image
                     exposes, http://container:9000/ping names the port (can be repeated); with
                     SVC= only that service is asked
  --exercise-cmd [SVC=]CMD  run a command here once the program is up, for one that does not speak
                     HTTP; it sees DOCK2NANO_HOST and DOCK2NANO_PORT
  --with SERVICE     start this service for the observation of the others, as if each of them
                     had it in depends_on (can be repeated): for a program that needs a database
                     the compose file does not say it needs; the service is also listed before
                     them in compose.yaml
  --service NAME     convert only this service (can be repeated)

Exit status: 0 on success, 1 on an error, 2 with --strict and an unsupported
item in the report.
`

// Docker is what the conversion needs from Docker. *docker.Client implements it.
type Docker interface {
	Build(ctx context.Context, o docker.BuildOptions) error
	Pull(ctx context.Context, image string) error
	HasImage(ctx context.Context, image string) (bool, error)
	Inspect(ctx context.Context, image string) (*docker.ImageConfig, error)
	NetworkCreate(ctx context.Context, name string, labels ...string) error
	NetworkRemove(ctx context.Context, name string) error
}

// Stopper is a container that was started for the observation of another
// service. Stop removes it; it does not depend on the context of the run.
type Stopper interface {
	Stop(ctx context.Context) error
}

var _ Docker = (*docker.Client)(nil)

// App is the command line with everything it touches passed in, so that tests
// can replace it.
type App struct {
	// Docker builds, pulls and inspects images.
	Docker Docker
	// Observe runs an image and reports what it did.
	Observe func(ctx context.Context, o observe.Options) (*observe.Result, error)
	// Start runs the image of a service that another one needs while it is
	// observed, and returns when it is ready.
	Start func(ctx context.Context, o observe.StartOptions) (Stopper, error)
	// Generate turns the project and the observations into a plan.
	Generate func(in gen.Input) (*plan.Plan, error)
	// Write puts the plan on disk.
	Write func(p *plan.Plan, o output.Options) ([]string, error)
	// Stdout receives the report and the next steps.
	Stdout io.Writer
	// Stderr receives the progress and the errors.
	Stderr io.Writer
	// LookupEnv reads a variable of the environment.
	LookupEnv func(string) (string, bool)
	// Getwd returns the current directory.
	Getwd func() (string, error)
	// Stat tells whether a path exists.
	Stat func(string) (fs.FileInfo, error)
	// ReadFile reads a file, for env_file.
	ReadFile func(string) ([]byte, error)
	// UserHomeDir returns the home folder of the user.
	UserHomeDir func() (string, error)
	// RemoveAll removes the sysroots that are not used any more.
	RemoveAll func(string) error
}

// Run runs the command line with the real Docker, the real observer and the
// real disk. args are the arguments after the program name. It returns the
// exit status.
func Run(args []string) int {
	ctx, stop := notifyContext(context.Background())
	defer stop()
	return newApp().Run(ctx, args)
}

// notifyContext returns a context that ends at the first SIGINT, SIGTERM or
// SIGHUP. The handler is removed BEFORE the context ends, so that a second
// signal, which may come at once, ends the process in the default way even
// when the cleanup is stuck.
func notifyContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		select {
		case <-ch:
			signal.Stop(ch)
			cancel()
		case <-ctx.Done():
			signal.Stop(ch)
		}
	}()
	return ctx, cancel
}

// newApp is the App that works on this machine.
func newApp() *App {
	d := docker.Default()
	a := &App{Docker: d, Generate: gen.Generate, Write: output.Write}
	a.Observe = func(ctx context.Context, o observe.Options) (*observe.Result, error) {
		return observe.Observe(ctx, d, o)
	}
	a.Start = func(ctx context.Context, o observe.StartOptions) (Stopper, error) {
		r, err := observe.Start(ctx, d, o)
		if err != nil {
			return nil, err
		}
		return r, nil
	}
	a.Stdout = os.Stdout
	a.Stderr = os.Stderr
	a.LookupEnv = os.LookupEnv
	a.Getwd = os.Getwd
	a.Stat = os.Stat
	a.ReadFile = os.ReadFile
	a.UserHomeDir = os.UserHomeDir
	a.RemoveAll = os.RemoveAll
	return a
}

// options are the flags of convert.
type options struct {
	file        string
	dockerfile  string
	context     string
	name        string
	out         string
	opsHome     string
	seconds     int
	pull        bool
	force       bool
	strict      bool
	shortNames  bool
	exercise    []string
	exerciseCmd []string
	with        []string
	services    []string
}

// Run runs the command line and returns the exit status.
func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		a.usage(a.Stderr)
		return 1
	}
	switch args[0] {
	case "version", "--version", "-version":
		fmt.Fprintf(a.Stdout, "%s %s\n", ToolName, Version)
		return 0
	case "help", "-h", "--help", "-help":
		a.usage(a.Stdout)
		return 0
	case "convert":
		o, err := a.parseConvert(args[1:])
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		if err != nil {
			fmt.Fprintf(a.Stderr, "%s: %v\n", ToolName, err)
			return 1
		}
		code, err := a.convert(ctx, o)
		if err != nil {
			fmt.Fprintf(a.Stderr, "%s: %v\n", ToolName, err)
			return 1
		}
		return code
	}
	fmt.Fprintf(a.Stderr, "%s: unknown command %q\n\n", ToolName, args[0])
	a.usage(a.Stderr)
	return 1
}

func (a *App) usage(w io.Writer) {
	io.WriteString(w, strings.ReplaceAll(usageText, "{tool}", ToolName))
}

// parseConvert reads the flags of convert and checks that they fit together.
func (a *App) parseConvert(args []string) (*options, error) {
	o := &options{}
	set := flag.NewFlagSet(ToolName+" convert", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.StringVar(&o.file, "f", "", "")
	set.StringVar(&o.dockerfile, "dockerfile", "", "")
	set.StringVar(&o.context, "context", "", "")
	set.StringVar(&o.name, "name", "", "")
	set.StringVar(&o.out, "o", ".", "")
	set.StringVar(&o.opsHome, "ops-home", "", "")
	set.IntVar(&o.seconds, "seconds", defaultSeconds, "")
	set.BoolVar(&o.pull, "pull", false, "")
	set.BoolVar(&o.force, "force", false, "")
	set.BoolVar(&o.strict, "strict", false, "")
	set.BoolVar(&o.shortNames, "short-names", false, "")
	set.Func("with", "", func(v string) error {
		if v == "" {
			return errors.New("the service of --with is empty")
		}
		o.with = append(o.with, v)
		return nil
	})
	set.Func("exercise", "", func(v string) error {
		if v == "" {
			return errors.New("the request of --exercise is empty")
		}
		o.exercise = append(o.exercise, v)
		return nil
	})
	set.Func("exercise-cmd", "", func(v string) error {
		if v == "" {
			return errors.New("the command of --exercise-cmd is empty")
		}
		o.exerciseCmd = append(o.exerciseCmd, v)
		return nil
	})
	set.Func("service", "", func(v string) error {
		if v == "" {
			return errors.New("the service name is empty")
		}
		o.services = append(o.services, v)
		return nil
	})
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			a.usage(a.Stdout)
			return nil, err
		}
		return nil, fmt.Errorf("%v (see \"%s help\")", err, ToolName)
	}
	if set.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q (see \"%s help\")", set.Arg(0), ToolName)
	}
	if o.file != "" && o.dockerfile != "" {
		return nil, errors.New("-f and --dockerfile cannot be used together")
	}
	if o.dockerfile == "" && (o.context != "" || o.name != "") {
		return nil, errors.New("--context and --name go with --dockerfile")
	}
	if o.seconds <= 0 {
		return nil, fmt.Errorf("--seconds must be greater than 0, not %d", o.seconds)
	}
	if o.out == "" {
		return nil, errors.New("-o is empty")
	}
	return o, nil
}

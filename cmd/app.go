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
  --ops-home DIR     the ops home folder; default: $OPS_HOME, then ~/.ops
  --seconds N        how long each service is observed; default: 30
  --pull             pull the images that are missing on this machine
  --force            replace package folders and a compose.yaml that exist
  --strict           exit with status 2 if the report has an unsupported item
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
}

var _ Docker = (*docker.Client)(nil)

// App is the command line with everything it touches passed in, so that tests
// can replace it.
type App struct {
	// Docker builds, pulls and inspects images.
	Docker Docker
	// Observe runs an image and reports what it did.
	Observe func(ctx context.Context, o observe.Options) (*observe.Result, error)
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
	// RemoveAll removes the sysroots that are not used any more.
	RemoveAll func(string) error
}

// Run runs the command line with the real Docker, the real observer and the
// real disk. args are the arguments after the program name. It returns the
// exit status.
func Run(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return newApp().Run(ctx, args)
}

// newApp is the App that works on this machine.
func newApp() *App {
	d := docker.Default()
	a := &App{Docker: d, Generate: gen.Generate, Write: output.Write}
	a.Observe = func(ctx context.Context, o observe.Options) (*observe.Result, error) {
		return observe.Observe(ctx, d, o)
	}
	a.Stdout = os.Stdout
	a.Stderr = os.Stderr
	a.LookupEnv = os.LookupEnv
	a.Getwd = os.Getwd
	a.Stat = os.Stat
	a.ReadFile = os.ReadFile
	a.RemoveAll = os.RemoveAll
	return a
}

// options are the flags of convert.
type options struct {
	file       string
	dockerfile string
	context    string
	name       string
	out        string
	opsHome    string
	seconds    int
	pull       bool
	force      bool
	strict     bool
	services   []string
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

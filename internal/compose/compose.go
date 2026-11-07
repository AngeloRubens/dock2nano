// Package compose reads a docker-compose.yml without changing it.
//
// The parser keeps what the converter needs as typed fields and records every
// key it does not model in Unknown, so that nothing is dropped in silence.
// Values are taken as written: ${VAR} references are not interpolated.
package compose

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Project is a parsed compose file.
type Project struct {
	Name     string
	Services []*Service // in file order
	Networks []string   // names declared at top level
	Volumes  []string   // names declared at top level
	// Unknown lists top-level keys that are not modelled, such as configs.
	Unknown []string
}

// Service returns the service with the given name, or nil.
func (p *Project) Service(name string) *Service {
	for _, s := range p.Services {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// Service is one entry under "services".
type Service struct {
	Name        string
	Image       string
	Build       *Build
	Command     Command
	Entrypoint  Command
	WorkingDir  string
	User        string
	Restart     string
	Environment []EnvVar
	EnvFile     []string
	Ports       []Port
	Expose      []string
	Volumes     []Volume
	DependsOn   []Dependency
	Networks    []string
	NetworkMode string
	Healthcheck *Healthcheck
	// Replicas is deploy.replicas or scale; 0 means not set.
	Replicas int
	Profiles []string
	// Unknown lists keys that are not modelled, as dotted paths such as
	// "privileged" or "deploy.resources". Extension keys (x-...) are not listed.
	Unknown []string
}

// Build is the build section of a service.
type Build struct {
	Context    string
	Dockerfile string
	Target     string
	Args       []EnvVar
}

// Command is a command or entrypoint, either as a shell string or as a list.
type Command struct {
	Shell string
	Exec  []string
}

// IsSet reports whether the command was given.
func (c Command) IsSet() bool { return c.Shell != "" || c.Exec != nil }

// EnvVar is one variable. Set is false when the file gives a name with no
// value, which means "take it from the environment of the caller".
type EnvVar struct {
	Name  string
	Value string
	Set   bool
}

// Port is a published port. Published and Target can be ranges ("8000-8010").
type Port struct {
	HostIP    string
	Published string
	Target    string
	Protocol  string
}

// Volume is a volume entry of a service.
type Volume struct {
	Type     string // "bind", "volume" or "tmpfs"
	Source   string // empty for an anonymous volume or tmpfs
	Target   string
	ReadOnly bool
}

// Dependency is one depends_on entry.
type Dependency struct {
	Service   string
	Condition string // service_started when not given
}

// Healthcheck is a healthcheck; only the test is read.
type Healthcheck struct {
	Test    Command
	Disable bool
}

// ParseFile reads and parses the compose file at path.
func ParseFile(path string) (*Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// Parse parses the content of a compose file.
func Parse(data []byte) (*Project, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("the file is empty")
	}
	top, err := entries(doc.Content[0])
	if err != nil {
		return nil, err
	}
	p := &Project{}
	hasServices := false
	for _, e := range top {
		switch {
		case e.key == "name":
			if p.Name, err = scalar(e.val); err != nil {
				return nil, err
			}
		case e.key == "version":
			// obsolete and ignored by Compose itself
		case e.key == "services":
			hasServices = true
			if err := p.parseServices(e.val); err != nil {
				return nil, err
			}
		case e.key == "networks":
			if p.Networks, err = keyNames(e.val); err != nil {
				return nil, err
			}
		case e.key == "volumes":
			if p.Volumes, err = keyNames(e.val); err != nil {
				return nil, err
			}
		case isExtension(e.key):
		default:
			p.Unknown = append(p.Unknown, e.key)
		}
	}
	if !hasServices || len(p.Services) == 0 {
		return nil, fmt.Errorf("the file has no services")
	}
	return p, nil
}

// keyNames returns the keys of a mapping; a null value gives no names.
func keyNames(n *yaml.Node) ([]string, error) {
	if isNull(n) {
		return nil, nil
	}
	es, err := entries(n)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range es {
		if !isExtension(e.key) {
			out = append(out, e.key)
		}
	}
	return out, nil
}

func (p *Project) parseServices(n *yaml.Node) error {
	es, err := entries(n)
	if err != nil {
		return err
	}
	for _, e := range es {
		if isExtension(e.key) {
			continue
		}
		s, err := parseService(e.key, e.val)
		if err != nil {
			return fmt.Errorf("service %q: %w", e.key, err)
		}
		p.Services = append(p.Services, s)
	}
	return nil
}

func parseService(name string, n *yaml.Node) (*Service, error) {
	es, err := entries(n)
	if err != nil {
		return nil, err
	}
	s := &Service{Name: name}
	for _, e := range es {
		if err := s.set(e); err != nil {
			return nil, fmt.Errorf("%s: %w", e.key, err)
		}
	}
	return s, nil
}

func (s *Service) set(e entry) (err error) {
	switch e.key {
	case "image":
		s.Image, err = scalar(e.val)
	case "build":
		s.Build, err = parseBuild(e.val, s)
	case "command":
		s.Command, err = parseCommand(e.val)
	case "entrypoint":
		s.Entrypoint, err = parseCommand(e.val)
	case "working_dir":
		s.WorkingDir, err = scalar(e.val)
	case "user":
		s.User, err = scalar(e.val)
	case "restart":
		s.Restart, err = scalar(e.val)
	case "network_mode":
		s.NetworkMode, err = scalar(e.val)
	case "environment":
		s.Environment, err = parseEnv(e.val)
	case "env_file":
		s.EnvFile, err = parseEnvFile(e.val)
	case "ports":
		s.Ports, err = parsePorts(e.val, s)
	case "expose":
		s.Expose, err = stringList(e.val)
	case "volumes":
		s.Volumes, err = parseVolumes(e.val, s)
	case "depends_on":
		s.DependsOn, err = parseDependsOn(e.val, s)
	case "networks":
		s.Networks, err = parseServiceNetworks(e.val, s)
	case "healthcheck":
		s.Healthcheck, err = parseHealthcheck(e.val)
	case "profiles":
		s.Profiles, err = stringList(e.val)
	case "scale":
		s.Replicas, err = parseCount(e.val)
	case "deploy":
		err = s.parseDeploy(e.val)
	default:
		if !isExtension(e.key) {
			s.Unknown = append(s.Unknown, e.key)
		}
	}
	return err
}

func parseCount(n *yaml.Node) (int, error) {
	v, err := scalar(n)
	if err != nil {
		return 0, err
	}
	c, err := strconv.Atoi(v)
	if err != nil || c < 0 {
		return 0, errAt(deref(n), "%q is not a count", v)
	}
	return c, nil
}

func (s *Service) parseDeploy(n *yaml.Node) error {
	if isNull(n) {
		return nil
	}
	es, err := entries(n)
	if err != nil {
		return err
	}
	for _, e := range es {
		switch {
		case e.key == "replicas":
			if s.Replicas, err = parseCount(e.val); err != nil {
				return fmt.Errorf("replicas: %w", err)
			}
		case isExtension(e.key):
		default:
			s.Unknown = append(s.Unknown, "deploy."+e.key)
		}
	}
	return nil
}

func parseBuild(n *yaml.Node, s *Service) (*Build, error) {
	b := &Build{}
	if d := deref(n); d.Kind == yaml.ScalarNode {
		var err error
		b.Context, err = scalar(d)
		return b, err
	}
	es, err := entries(n)
	if err != nil {
		return nil, err
	}
	for _, e := range es {
		switch e.key {
		case "context":
			b.Context, err = scalar(e.val)
		case "dockerfile":
			b.Dockerfile, err = scalar(e.val)
		case "target":
			b.Target, err = scalar(e.val)
		case "args":
			b.Args, err = parseEnv(e.val)
		default:
			if !isExtension(e.key) {
				s.Unknown = append(s.Unknown, "build."+e.key)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.key, err)
		}
	}
	return b, nil
}

func parseCommand(n *yaml.Node) (Command, error) {
	d := deref(n)
	if d.Kind == yaml.SequenceNode {
		l, err := stringList(d)
		return Command{Exec: l}, err
	}
	s, err := scalar(d)
	return Command{Shell: s}, err
}

// parseEnv reads a mapping or a list of NAME=value, for environment and for
// build args.
func parseEnv(n *yaml.Node) ([]EnvVar, error) {
	d := deref(n)
	var out []EnvVar
	switch d.Kind {
	case yaml.MappingNode:
		es, err := entries(d)
		if err != nil {
			return nil, err
		}
		for _, e := range es {
			if isExtension(e.key) {
				continue
			}
			v := EnvVar{Name: e.key}
			if !isNull(e.val) {
				val, err := scalar(e.val)
				if err != nil {
					return nil, err
				}
				v.Value, v.Set = val, true
			}
			out = append(out, v)
		}
	case yaml.SequenceNode:
		items, err := stringList(d)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			name, val, ok := strings.Cut(it, "=")
			if name == "" {
				return nil, errAt(d, "empty variable name in %q", it)
			}
			out = append(out, EnvVar{Name: name, Value: val, Set: ok})
		}
	default:
		return nil, errAt(d, "expected a mapping or a list")
	}
	return out, nil
}

func parseEnvFile(n *yaml.Node) ([]string, error) {
	d := deref(n)
	if d.Kind == yaml.ScalarNode {
		return stringOrList(d)
	}
	if d.Kind != yaml.SequenceNode {
		return nil, errAt(d, "expected a path or a list")
	}
	var out []string
	for _, c := range d.Content {
		if deref(c).Kind == yaml.MappingNode {
			es, err := entries(c)
			if err != nil {
				return nil, err
			}
			found := false
			for _, e := range es {
				if e.key == "path" {
					p, err := scalar(e.val)
					if err != nil {
						return nil, err
					}
					out = append(out, p)
					found = true
				}
			}
			if !found {
				return nil, errAt(deref(c), "env_file entry has no path")
			}
			continue
		}
		p, err := scalar(c)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func parsePorts(n *yaml.Node, s *Service) ([]Port, error) {
	d := deref(n)
	if d.Kind != yaml.SequenceNode {
		return nil, errAt(d, "expected a list")
	}
	var out []Port
	for i, c := range d.Content {
		c = deref(c)
		if c.Kind == yaml.ScalarNode {
			p, err := parseShortPort(c.Value)
			if err != nil {
				return nil, errAt(c, "%v", err)
			}
			out = append(out, p)
			continue
		}
		es, err := entries(c)
		if err != nil {
			return nil, err
		}
		p := Port{Protocol: "tcp"}
		for _, e := range es {
			switch e.key {
			case "target":
				p.Target, err = scalar(e.val)
			case "published":
				p.Published, err = scalar(e.val)
			case "host_ip":
				p.HostIP, err = scalar(e.val)
			case "protocol":
				p.Protocol, err = scalar(e.val)
			default:
				if !isExtension(e.key) {
					s.Unknown = append(s.Unknown, fmt.Sprintf("ports[%d].%s", i, e.key))
				}
			}
			if err != nil {
				return nil, fmt.Errorf("ports[%d].%s: %w", i, e.key, err)
			}
		}
		if p.Target == "" {
			return nil, errAt(c, "port has no target")
		}
		out = append(out, p)
	}
	return out, nil
}

// parseShortPort reads [HOST_IP:][PUBLISHED:]TARGET[/PROTOCOL].
func parseShortPort(v string) (Port, error) {
	p := Port{Protocol: "tcp"}
	rest := v
	if i := strings.LastIndex(rest, "/"); i >= 0 {
		p.Protocol = rest[i+1:]
		rest = rest[:i]
		if p.Protocol == "" {
			return p, fmt.Errorf("port %q has an empty protocol", v)
		}
	}
	if strings.HasPrefix(rest, "[") {
		j := strings.Index(rest, "]")
		if j < 0 {
			return p, fmt.Errorf("port %q has an unclosed address", v)
		}
		p.HostIP = rest[1:j]
		rest = strings.TrimPrefix(rest[j+1:], ":")
		parts := strings.Split(rest, ":")
		switch len(parts) {
		case 1:
			p.Target = parts[0]
		case 2:
			p.Published, p.Target = parts[0], parts[1]
		default:
			return p, fmt.Errorf("port %q is not valid", v)
		}
	} else {
		parts := strings.Split(rest, ":")
		switch len(parts) {
		case 1:
			p.Target = parts[0]
		case 2:
			p.Published, p.Target = parts[0], parts[1]
		case 3:
			p.HostIP, p.Published, p.Target = parts[0], parts[1], parts[2]
		default:
			return p, fmt.Errorf("port %q is not valid", v)
		}
	}
	if p.Target == "" {
		return p, fmt.Errorf("port %q has no target", v)
	}
	return p, nil
}

func parseVolumes(n *yaml.Node, s *Service) ([]Volume, error) {
	d := deref(n)
	if d.Kind != yaml.SequenceNode {
		return nil, errAt(d, "expected a list")
	}
	var out []Volume
	for i, c := range d.Content {
		c = deref(c)
		if c.Kind == yaml.ScalarNode {
			v, err := parseShortVolume(c.Value)
			if err != nil {
				return nil, errAt(c, "%v", err)
			}
			out = append(out, v)
			continue
		}
		es, err := entries(c)
		if err != nil {
			return nil, err
		}
		var v Volume
		for _, e := range es {
			switch e.key {
			case "type":
				v.Type, err = scalar(e.val)
			case "source":
				v.Source, err = scalar(e.val)
			case "target":
				v.Target, err = scalar(e.val)
			case "read_only":
				var b string
				if b, err = scalar(e.val); err == nil {
					v.ReadOnly = b == "true"
				}
			default:
				if !isExtension(e.key) {
					s.Unknown = append(s.Unknown, fmt.Sprintf("volumes[%d].%s", i, e.key))
				}
			}
			if err != nil {
				return nil, fmt.Errorf("volumes[%d].%s: %w", i, e.key, err)
			}
		}
		if v.Target == "" {
			return nil, errAt(c, "volume has no target")
		}
		if v.Type == "" {
			v.Type = "volume"
		}
		out = append(out, v)
	}
	return out, nil
}

// parseShortVolume reads [SOURCE:]TARGET[:MODE].
func parseShortVolume(v string) (Volume, error) {
	parts := strings.Split(v, ":")
	var out Volume
	switch len(parts) {
	case 1:
		out.Target = parts[0]
	case 2:
		out.Source, out.Target = parts[0], parts[1]
	case 3:
		out.Source, out.Target = parts[0], parts[1]
		for _, m := range strings.Split(parts[2], ",") {
			if m == "ro" {
				out.ReadOnly = true
			}
		}
	default:
		return out, fmt.Errorf("volume %q is not valid", v)
	}
	if out.Target == "" {
		return out, fmt.Errorf("volume %q has no target", v)
	}
	switch {
	case out.Source == "":
		out.Type = "volume"
	case out.Source == "." || out.Source == ".." || strings.HasPrefix(out.Source, "/") ||
		strings.HasPrefix(out.Source, "./") || strings.HasPrefix(out.Source, "../") ||
		strings.HasPrefix(out.Source, "~"):
		out.Type = "bind"
	default:
		out.Type = "volume"
	}
	return out, nil
}

func parseDependsOn(n *yaml.Node, s *Service) ([]Dependency, error) {
	d := deref(n)
	if d.Kind == yaml.SequenceNode {
		names, err := stringList(d)
		if err != nil {
			return nil, err
		}
		out := make([]Dependency, 0, len(names))
		for _, nm := range names {
			out = append(out, Dependency{Service: nm, Condition: "service_started"})
		}
		return out, nil
	}
	es, err := entries(d)
	if err != nil {
		return nil, err
	}
	var out []Dependency
	for _, e := range es {
		if isExtension(e.key) {
			continue
		}
		dep := Dependency{Service: e.key, Condition: "service_started"}
		if !isNull(e.val) {
			sub, err := entries(e.val)
			if err != nil {
				return nil, err
			}
			for _, se := range sub {
				switch se.key {
				case "condition":
					if dep.Condition, err = scalar(se.val); err != nil {
						return nil, err
					}
				default:
					if !isExtension(se.key) {
						s.Unknown = append(s.Unknown, "depends_on."+e.key+"."+se.key)
					}
				}
			}
		}
		out = append(out, dep)
	}
	return out, nil
}

func parseServiceNetworks(n *yaml.Node, s *Service) ([]string, error) {
	d := deref(n)
	if d.Kind == yaml.SequenceNode {
		return stringList(d)
	}
	es, err := entries(d)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range es {
		if isExtension(e.key) {
			continue
		}
		out = append(out, e.key)
		if !isNull(e.val) {
			sub, err := entries(e.val)
			if err != nil {
				return nil, err
			}
			for _, se := range sub {
				if !isExtension(se.key) {
					s.Unknown = append(s.Unknown, "networks."+e.key+"."+se.key)
				}
			}
		}
	}
	return out, nil
}

func parseHealthcheck(n *yaml.Node) (*Healthcheck, error) {
	h := &Healthcheck{}
	if isNull(n) {
		return h, nil
	}
	es, err := entries(n)
	if err != nil {
		return nil, err
	}
	for _, e := range es {
		switch e.key {
		case "test":
			h.Test, err = parseCommand(e.val)
		case "disable":
			var b string
			if b, err = scalar(e.val); err == nil {
				h.Disable = b == "true"
			}
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.key, err)
		}
	}
	return h, nil
}

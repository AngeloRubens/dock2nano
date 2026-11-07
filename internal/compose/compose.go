// Package compose reads a docker-compose.yml without changing it.
//
// The parser keeps what the converter needs as typed fields and records every
// key it does not model in Unknown, so that nothing is dropped in silence.
// Values are taken as written: ${VAR} references are not interpolated, and
// numbers keep their spelling ("1.10" stays "1.10").
package compose

import (
	"bytes"
	"fmt"
	"io"
	"math"
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
	// Replicas is deploy.replicas or scale; nil means not set, 0 is a real value.
	Replicas *int
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
// Set is true when the file gives the key with a value, even an empty one: an
// empty string or list means "ignore the default of the image", while an
// absent or null key means "keep it".
type Command struct {
	Shell string
	Exec  []string
	Set   bool
}

// IsSet reports whether the command was given.
func (c Command) IsSet() bool { return c.Set }

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

// Volume is a volume entry of a service. Type is "unresolved" when the source
// holds a ${VAR}, which decides between a bind mount and a named volume.
// Options holds the modes other than ro and rw, such as z or nocopy, comma
// separated.
type Volume struct {
	Type     string // "bind", "volume" or "tmpfs"
	Source   string // empty for an anonymous volume or tmpfs
	Target   string
	ReadOnly bool
	Options  string
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
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("the file is empty")
		}
		return nil, err
	}
	var next yaml.Node
	if err := dec.Decode(&next); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("the file has more than one YAML document")
		}
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("the file is empty")
	}
	if doc.Content[0].Tag == "!reset" {
		return nil, errAt(doc.Content[0], "the top level cannot be reset")
	}
	if err := applyReset(&doc, map[*yaml.Node]bool{}); err != nil {
		return nil, err
	}
	if err := checkSize(&doc); err != nil {
		return nil, err
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
			if p.Networks, err = keyNames(e.val, "networks", &p.Unknown); err != nil {
				return nil, err
			}
		case e.key == "volumes":
			if p.Volumes, err = keyNames(e.val, "volumes", &p.Unknown); err != nil {
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

// keyNames returns the keys of a mapping; a null value gives no names. The
// settings of each entry (driver, external...) are listed in unknown as
// prefix.name.key, because they change what a converter must do.
func keyNames(n *yaml.Node, prefix string, unknown *[]string) ([]string, error) {
	if isNull(n) {
		return nil, nil
	}
	es, err := entries(n)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range es {
		out = append(out, e.key)
		if isNull(e.val) {
			continue
		}
		sub, err := entries(e.val)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", prefix, e.key, err)
		}
		for _, se := range sub {
			if !isExtension(se.key) {
				*unknown = append(*unknown, prefix+"."+e.key+"."+se.key)
			}
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

// modeled lists the service keys that set understands. A null value for one
// of them means the same as leaving it out.
var modeled = map[string]bool{
	"image": true, "build": true, "command": true, "entrypoint": true,
	"working_dir": true, "user": true, "restart": true, "network_mode": true,
	"environment": true, "env_file": true, "ports": true, "expose": true,
	"volumes": true, "depends_on": true, "networks": true, "healthcheck": true,
	"profiles": true, "scale": true, "deploy": true,
}

func (s *Service) set(e entry) (err error) {
	if modeled[e.key] && isNull(e.val) {
		return nil
	}
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
		s.Environment, err = parseEnv(e.val, s, "environment")
	case "env_file":
		s.EnvFile, err = parseEnvFile(e.val, s)
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
		s.Healthcheck, err = parseHealthcheck(e.val, s)
	case "profiles":
		s.Profiles, err = stringList(e.val)
	case "scale":
		err = s.setReplicas(e.val, "scale")
	case "deploy":
		err = s.parseDeploy(e.val)
	default:
		if !isExtension(e.key) {
			s.Unknown = append(s.Unknown, e.key)
		}
	}
	return err
}

// parseCount reads a count. ok is false when the value holds a ${VAR}, which
// is not interpolated: the caller must list it.
func parseCount(n *yaml.Node) (c int, ok bool, err error) {
	v, err := scalar(n)
	if err != nil {
		return 0, false, err
	}
	if strings.Contains(v, "$") {
		return 0, false, nil
	}
	if deref(n).Tag == "!!float" {
		// 2.0 and 1e3 are whole numbers: Compose reads them as integers.
		f, ferr := strconv.ParseFloat(strings.ReplaceAll(v, "_", ""), 64)
		if ferr == nil && f == math.Trunc(f) && f >= 0 && f <= math.MaxInt32 {
			return int(f), true, nil
		}
	}
	// Base 0 as in yaml.v3: 010 is 8, 0x10 is 16 and 1_000 is 1000.
	c64, err := strconv.ParseInt(strings.ReplaceAll(v, "_", ""), 0, 32)
	if err != nil || c64 < 0 {
		return 0, false, errAt(deref(n), "%q is not a count", v)
	}
	return int(c64), true, nil
}

// setReplicas stores deploy.replicas or scale; the two must agree if both are
// given.
func (s *Service) setReplicas(n *yaml.Node, label string) error {
	c, ok, err := parseCount(n)
	if err != nil {
		return err
	}
	if !ok {
		s.Unknown = append(s.Unknown, label+" (holds a variable)")
		return nil
	}
	if s.Replicas != nil && *s.Replicas != c {
		return errAt(deref(n), "scale and deploy.replicas disagree")
	}
	s.Replicas = &c
	return nil
}

func (s *Service) parseDeploy(n *yaml.Node) error {
	es, err := entries(n)
	if err != nil {
		return err
	}
	for _, e := range es {
		switch {
		case e.key == "replicas" && isNull(e.val):
		case e.key == "replicas":
			if err = s.setReplicas(e.val, "deploy.replicas"); err != nil {
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
			if !isNull(e.val) {
				b.Args, err = parseEnv(e.val, s, "build.args")
			}
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
	if isNull(d) {
		return Command{}, nil
	}
	if d.Kind == yaml.SequenceNode {
		l, err := stringList(d)
		return Command{Exec: l, Set: true}, err
	}
	s, err := scalar(d)
	return Command{Shell: s, Set: true}, err
}

// parseEnv reads a mapping or a list of NAME=value, for environment and for
// build args.
func parseEnv(n *yaml.Node, s *Service, label string) ([]EnvVar, error) {
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
				s.Unknown = append(s.Unknown, label+"."+e.key+" (Compose reads it as an extension and drops it)")
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

func parseEnvFile(n *yaml.Node, s *Service) ([]string, error) {
	d := deref(n)
	if d.Kind == yaml.ScalarNode {
		if d.Value == "" {
			return nil, errAt(d, "env_file path is empty")
		}
		return stringOrList(d)
	}
	if d.Kind != yaml.SequenceNode {
		return nil, errAt(d, "expected a path or a list")
	}
	var out []string
	for i, c := range d.Content {
		if deref(c).Kind == yaml.MappingNode {
			es, err := entries(c)
			if err != nil {
				return nil, err
			}
			found := false
			for _, e := range es {
				switch {
				case e.key == "path":
					p, err := scalar(e.val)
					if err != nil {
						return nil, err
					}
					if p == "" {
						return nil, errAt(deref(e.val), "env_file path is empty")
					}
					out = append(out, p)
					found = true
				case !isExtension(e.key):
					s.Unknown = append(s.Unknown, fmt.Sprintf("env_file[%d].%s", i, e.key))
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
		if p == "" {
			return nil, errAt(deref(c), "env_file path is empty")
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
		if isNull(c) {
			return nil, errAt(c, "empty entry in ports")
		}
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
			if isNull(e.val) && (e.key == "published" || e.key == "host_ip" || e.key == "protocol") {
				continue
			}
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
		if !validProtocol(p.Protocol) {
			return nil, errAt(c, "port has the protocol %q", p.Protocol)
		}
		out = append(out, p)
	}
	return out, nil
}

// openVar reports whether s has a ${ with no closing brace.
func openVar(s string) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '$' && i+1 < len(s) && s[i+1] == '{':
			depth++
			i++
		case s[i] == '}' && depth > 0:
			depth--
		}
	}
	return depth > 0
}

func validProtocol(p string) bool { return p == "tcp" || p == "udp" || p == "sctp" }

// splitOutsideVars splits s at sep, except inside a ${...} reference, so that
// "${PORT:-8080}:80" has two parts.
func splitOutsideVars(s string, sep byte) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '$' && i+1 < len(s) && s[i+1] == '{':
			depth++
			i++
		case s[i] == '}' && depth > 0:
			depth--
		case s[i] == sep && depth == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

// parseShortPort reads [HOST_IP:][PUBLISHED:]TARGET[/PROTOCOL]. An IPv6
// address must be in brackets and be followed by PUBLISHED:TARGET.
func parseShortPort(v string) (Port, error) {
	p := Port{Protocol: "tcp"}
	if openVar(v) {
		return p, fmt.Errorf("port %q has a ${ that is not closed", v)
	}
	rest := v
	if i := strings.LastIndex(rest, "/"); i >= 0 && !strings.Contains(rest[i:], "}") {
		p.Protocol = rest[i+1:]
		rest = rest[:i]
		if !validProtocol(p.Protocol) {
			return p, fmt.Errorf("port %q has the protocol %q", v, p.Protocol)
		}
		if strings.Contains(rest, "/") {
			return p, fmt.Errorf("port %q is not valid", v)
		}
	}
	if strings.HasPrefix(rest, "[") {
		j := strings.Index(rest, "]")
		if j < 0 || !strings.HasPrefix(rest[j+1:], ":") {
			return p, fmt.Errorf("port %q is not valid", v)
		}
		p.HostIP = rest[1:j]
		parts := splitOutsideVars(rest[j+2:], ':')
		if len(parts) != 2 {
			return p, fmt.Errorf("port %q is not valid", v)
		}
		p.Published, p.Target = parts[0], parts[1]
	} else {
		parts := splitOutsideVars(rest, ':')
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
		if isNull(c) {
			return nil, errAt(c, "empty entry in volumes")
		}
		if c.Kind == yaml.ScalarNode {
			v, err := parseShortVolume(c.Value)
			if err != nil {
				return nil, errAt(c, "%v", err)
			}
			if v.Type == "unresolved" {
				s.Unknown = append(s.Unknown, fmt.Sprintf("volumes[%d].source (holds a variable, bind or named volume not decided)", i))
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
				var ok bool
				if v.ReadOnly, ok, err = boolean(e.val); err == nil && !ok {
					s.Unknown = append(s.Unknown, fmt.Sprintf("volumes[%d].read_only (holds a variable)", i))
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
		switch v.Type {
		case "":
			return nil, errAt(c, "volume has no type")
		case "bind", "volume", "tmpfs":
		default:
			s.Unknown = append(s.Unknown, fmt.Sprintf("volumes[%d].type (%s)", i, v.Type))
		}
		out = append(out, v)
	}
	return out, nil
}

// parseShortVolume reads [SOURCE:]TARGET[:MODE]. A Windows drive letter
// followed by a backslash at the start of SOURCE is kept in it; "a:/b" is a
// volume called a, as on Linux.
func parseShortVolume(v string) (Volume, error) {
	if openVar(v) {
		return Volume{}, fmt.Errorf("volume %q has a ${ that is not closed", v)
	}
	drive := ""
	rest := v
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	if len(v) >= 3 && v[1] == ':' && strings.ContainsAny(v[:1], letters) {
		if v[2] == '\\' {
			drive, rest = v[:2], v[2:]
		} else if v[2] == '/' {
			// "C:/data:/data" is a drive; "a:/b:ro" is a volume called a.
			if more := splitOutsideVars(v[2:], ':'); len(more) >= 2 && strings.HasPrefix(more[1], "/") {
				drive, rest = v[:2], v[2:]
			}
		}
	}
	parts := splitOutsideVars(rest, ':')
	parts[0] = drive + parts[0]
	var out Volume
	switch len(parts) {
	case 1:
		out.Target = parts[0]
	case 2:
		out.Source, out.Target = parts[0], parts[1]
	case 3:
		out.Source, out.Target = parts[0], parts[1]
		var opts []string
		for _, m := range strings.Split(parts[2], ",") {
			switch m {
			case "ro":
				out.ReadOnly = true
			case "rw":
			case "z", "Z", "nocopy", "consistent", "cached", "delegated",
				"private", "rprivate", "shared", "rshared", "slave", "rslave":
				opts = append(opts, m)
			default:
				return out, fmt.Errorf("volume %q has the mode %q", v, m)
			}
		}
		out.Options = strings.Join(opts, ",")
	default:
		return out, fmt.Errorf("volume %q is not valid", v)
	}
	if out.Target == "" {
		return out, fmt.Errorf("volume %q has no target", v)
	}
	switch {
	case out.Source == "":
		out.Type = "volume"
	case strings.ContainsAny(out.Source[:1], "./~") || drive != "":
		out.Type = "bind"
	case strings.Contains(out.Source, "$"):
		out.Type = "unresolved"
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
		dep := Dependency{Service: e.key, Condition: "service_started"}
		if !isNull(e.val) {
			sub, err := entries(e.val)
			if err != nil {
				return nil, err
			}
			for _, se := range sub {
				switch se.key {
				case "condition":
					if isNull(se.val) {
						break
					}
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
		out = append(out, e.key)
		if isExtension(e.key) {
			s.Unknown = append(s.Unknown, "networks."+e.key+" (Compose reads it as an extension and drops it)")
		}
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

func parseHealthcheck(n *yaml.Node, s *Service) (*Healthcheck, error) {
	h := &Healthcheck{}
	es, err := entries(n)
	if err != nil {
		return nil, err
	}
	for _, e := range es {
		switch e.key {
		case "test":
			h.Test, err = parseCommand(e.val)
		case "disable":
			var ok bool
			if h.Disable, ok, err = boolean(e.val); err == nil && !ok {
				s.Unknown = append(s.Unknown, "healthcheck.disable (holds a variable)")
			}
		default:
			if !isExtension(e.key) {
				s.Unknown = append(s.Unknown, "healthcheck."+e.key)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.key, err)
		}
	}
	if len(h.Test.Exec) == 1 && h.Test.Exec[0] == "NONE" {
		h.Disable = true
	}
	return h, nil
}

// Package plan holds the types that carry the result of the conversion from
// the generator (internal/gen) to the writer (internal/output): the package
// manifest of each service, the compose.yaml that ops reads, and the report of
// what did not convert. It has no logic of its own.
package plan

import "encoding/json"

// Plan is everything the converter decided.
type Plan struct {
	// Arch is the architecture of the packages, such as "amd64".
	Arch string
	// Compose is the content of compose.yaml, in start order.
	Compose ComposeFile
	// Services are the services that become packages, in start order.
	Services []Service
	// Report lists what was observed, approximated or not converted.
	Report []Item
}

// ComposeFile is what "ops compose" reads (ComposeFile in the ops source).
type ComposeFile struct {
	Packages []ComposePackage `yaml:"packages"`
}

// ComposePackage is one entry of compose.yaml. For a package kept in the local
// packages of ops, Local is true and Name is the folder name.
type ComposePackage struct {
	Pkg          string `yaml:"pkg"`
	Name         string `yaml:"name"`
	Local        bool   `yaml:"local,omitempty"`
	Arch         string `yaml:"arch,omitempty"`
	BaseVolumeSz string `yaml:"base_volume_sz,omitempty"`
}

// Service is one package to write.
type Service struct {
	// Name is the compose name of the service.
	Name string
	// PackageName is the folder name under .ops/local_packages/<arch>/.
	PackageName string
	// Sysroot is a host directory holding the files of the package. The writer
	// moves or copies it to <package>/sysroot.
	Sysroot string
	// Manifest is written as package.manifest.
	Manifest Manifest
}

// Manifest is the part of the ops configuration (types.Config) that a package
// carries. ops reads it with unknown fields refused, so only fields that
// exist there are listed, with the same JSON names.
type Manifest struct {
	Program string            `json:",omitempty"`
	Args    []string          `json:",omitempty"`
	Env     map[string]string `json:",omitempty"`
	Version string            `json:",omitempty"`
	MapDirs map[string]string `json:",omitempty"`

	// RebootOnExit is always written: ops compose reads every manifest into
	// one shared configuration, so a key left out would keep the value of the
	// service before it.
	RebootOnExit bool `json:"RebootOnExit"`

	// DisableArgsCopy stops ops from copying a host file named like Args[1]
	// into the image.
	DisableArgsCopy bool `json:",omitempty"`

	RunConfig RunConfig `json:",omitempty"`
}

// RunConfig is the part of types.RunConfig the converter sets.
type RunConfig struct {
	Ports    []string
	UDPPorts []string
}

// MarshalJSON writes both lists always, as [] when empty, so that the ports of
// one service do not stay set for the next one when ops compose reads all the
// manifests into one configuration.
func (r RunConfig) MarshalJSON() ([]byte, error) {
	out := struct {
		Ports    []string
		UDPPorts []string
	}{Ports: r.Ports, UDPPorts: r.UDPPorts}
	if out.Ports == nil {
		out.Ports = []string{}
	}
	if out.UDPPorts == nil {
		out.UDPPorts = []string{}
	}
	return json.Marshal(out)
}

// Kind says how a piece of the input was treated.
type Kind string

const (
	// Observed: taken from what the program did while it ran.
	Observed Kind = "observed"
	// Converted: carried over with the same meaning.
	Converted Kind = "converted"
	// Approximated: carried over with a different meaning; the item says how.
	Approximated Kind = "approximated"
	// Unsupported: not carried over.
	Unsupported Kind = "unsupported"
	// Note: something to know that is neither of the above.
	Note Kind = "note"
)

// Item is one line of the report. Service is empty for the whole project.
type Item struct {
	Service string
	Kind    Kind
	// Key is the input it is about, such as "volumes[0]" or "healthcheck".
	Key string
	// Message is plain text.
	Message string
}

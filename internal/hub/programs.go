package hub

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/AngeloRubens/dock2nano/hints"
)

// SingleProcess says how a program that starts several processes is made to
// run as one, by its configuration file.
type SingleProcess struct {
	// ConfigPaths are the places the configuration file has in an image or in
	// a package of the hub; the first one that exists is used.
	ConfigPaths []string `yaml:"config_paths"`

	// ConfigFlag is the option of the program that names the file.
	ConfigFlag string `yaml:"config_flag"`

	// Settings are the top-level directives that make it run in one process,
	// with the values that are always used.
	Settings []Setting `yaml:"settings"`

	// Compare are the directives whose values are compared between the
	// configuration of the hub and the one of the user, for the report.
	Compare []string `yaml:"compare"`
}

// Tables are the decisions that are kept in hints/hub-tables.yaml.
type Tables struct {
	// NamespacePreference lists the namespaces that win a tie, first first.
	NamespacePreference []string `yaml:"namespace_preference"`

	// VersionEnv lists, by image name, the variables that hold a version.
	VersionEnv map[string][]string `yaml:"version_env"`

	// SingleProcess holds, by program name, how to make it run as one process.
	SingleProcess map[string]SingleProcess `yaml:"single_process"`
}

// ParseTables reads the text of a table of decisions.
func ParseTables(data []byte) (*Tables, error) {
	var t Tables
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&t); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("the table of hub decisions is not readable: %w", err)
	}
	for name, sp := range t.SingleProcess {
		if len(sp.ConfigPaths) == 0 || len(sp.Settings) == 0 {
			return nil, fmt.Errorf("the table of hub decisions: %s needs config_paths and settings", name)
		}
		for _, s := range sp.Settings {
			if s.Name == "" || s.Value == "" {
				return nil, fmt.Errorf("the table of hub decisions: %s has a setting without a name or a value", name)
			}
		}
	}
	return &t, nil
}

var (
	tablesOnce sync.Once
	tables     *Tables
)

// loadedTables is the table that ships in the binary. A table that cannot be
// read is a mistake of the build, which a test catches.
func loadedTables() *Tables {
	tablesOnce.Do(func() {
		t, err := ParseTables(hints.HubTables)
		if err != nil {
			panic(err)
		}
		tables = t
	})
	return tables
}

// SingleProcessFor returns what is known about a program, by the name of its
// executable.
func SingleProcessFor(program string) (SingleProcess, bool) {
	sp, ok := loadedTables().SingleProcess[strings.ToLower(program)]
	return sp, ok
}

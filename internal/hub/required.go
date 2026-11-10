package hub

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/AngeloRubens/dock2nano/hints"
)

// Required is a row of hints/hub-required.yaml: a program for which the hub
// holds a special build that must be used whole.
type Required struct {
	// Images are the names of the images (nginx, not docker.io/library/nginx)
	// that the row is for.
	Images []string `yaml:"images"`

	// Programs are the names of the observed programs the row is for.
	Programs []string `yaml:"programs"`

	// Packages are the packages of the hub that may be used, as
	// namespace/name.
	Packages []string `yaml:"packages"`

	// Reason says why the build of the hub is needed.
	Reason string `yaml:"reason"`
}

// ParseRequired reads the text of a table of required builds.
func ParseRequired(data []byte) ([]Required, error) {
	var doc struct {
		Rows []Required `yaml:"rows"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("the table of required builds is not readable: %w", err)
	}
	for i, r := range doc.Rows {
		if len(r.Images) == 0 || len(r.Programs) == 0 || len(r.Packages) == 0 || r.Reason == "" {
			return nil, fmt.Errorf("row %d of the table of required builds needs images, programs, packages and a reason", i+1)
		}
		for _, p := range r.Packages {
			ns, name, ok := strings.Cut(p, "/")
			if !ok || ns == "" || name == "" || strings.Contains(name, "/") {
				return nil, fmt.Errorf("row %d of the table of required builds: the package %q is not namespace/name", i+1, p)
			}
		}
	}
	return doc.Rows, nil
}

// DefaultRequired is the table that ships inside the binary.
func DefaultRequired() ([]Required, error) {
	return ParseRequired(hints.HubRequired)
}

// MatchRequired returns the row for an image name and an observed program, or
// nil. The image name is what ImageName gives; the program is a path or a
// name.
func MatchRequired(rows []Required, image, program string) *Required {
	image = strings.ToLower(image)
	program = strings.ToLower(path.Base(program))
	for i := range rows {
		if contains(rows[i].Images, image) && contains(rows[i].Programs, program) {
			return &rows[i]
		}
	}
	return nil
}

// Candidates are the packages of the list that the row allows, for the
// architecture.
func (r *Required) Candidates(pkgs []Package, arch string) []Package {
	var out []Package
	for _, p := range pkgs {
		if p.Usable() && contains(r.Packages, p.Ref()) && NormalizeArch(p.Arch) == NormalizeArch(arch) {
			out = append(out, p)
		}
	}
	return out
}

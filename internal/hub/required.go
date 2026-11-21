package hub

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
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

	// EntrypointNote says what the entrypoint of the image does that the
	// package of the hub does not do. Empty means that nothing is said.
	EntrypointNote string `yaml:"entrypoint_note,omitempty"`

	// Superusers is the superuser role of each package, by namespace/name; the
	// note names it as {HUB_SUPERUSER}. A note that names it needs an entry for
	// every package of the row.
	Superusers map[string]string `yaml:"superusers,omitempty"`

	// PackageFacts says, by namespace/name, what was seen of that package that
	// the note needs; the note names it as {HUB_FACTS}.
	PackageFacts map[string]string `yaml:"package_facts,omitempty"`

	// PostUpScript is the text of a POSIX shell script that the user runs after
	// "ops compose up" and that does what the entrypoint of the image does and
	// the package does not. It is written with the same placeholders and
	// sections as entrypoint_note, with :lit and :sh besides :ident, and with
	// {HUB_INSTANCE} (the name of the instance, which is the pkg name of
	// compose.yaml). {{ and }} stand for literal braces. The script has one line that starts with "ensure '" for each
	// thing it creates; when the text filled for a service has none, no script
	// is written. Empty means there is no script.
	PostUpScript string `yaml:"post_up_script,omitempty"`

	// RestartNote is added to the report of the restart policy of a service
	// that uses a package of this row. Empty means nothing is added.
	RestartNote string `yaml:"restart_note,omitempty"`
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
		for _, n := range append(slices.Clone(r.Images), r.Programs...) {
			if n != strings.ToLower(n) || strings.Contains(n, "/") {
				return nil, fmt.Errorf("row %d of the table of required builds: %q must be lower case, a name and not a path", i+1, n)
			}
		}
		for _, p := range r.Packages {
			ns, name, ok := strings.Cut(p, "/")
			if !ok || ns == "" || name == "" || strings.Contains(name, "/") {
				return nil, fmt.Errorf("row %d of the table of required builds: the package %q is not namespace/name", i+1, p)
			}
		}
		for field, m := range map[string]map[string]string{"superusers": r.Superusers, "package_facts": r.PackageFacts} {
			for p := range m {
				if !contains(r.Packages, p) {
					return nil, fmt.Errorf("row %d of the table of required builds: %s names %q, which is not one of its packages", i+1, field, p)
				}
			}
		}
		if r.PostUpScript != "" && r.EntrypointNote == "" {
			return nil, fmt.Errorf("row %d of the table of required builds: post_up_script needs an entrypoint_note, because the report of the note is where the script is announced", i+1)
		}
		for field, note := range map[string]string{"entrypoint_note": r.EntrypointNote, "post_up_script": r.PostUpScript} {
			if note == "" {
				continue
			}
			if err := ValidateNote(note); err != nil {
				return nil, fmt.Errorf("row %d of the table of required builds: %s: %w", i+1, field, err)
			}
			if field == "post_up_script" && !strings.HasPrefix(note, "#!/bin/sh\n") {
				return nil, fmt.Errorf("row %d of the table of required builds: post_up_script must start with the line #!/bin/sh", i+1)
			}
			if field == "post_up_script" {
				if err := ScriptPlaceholders(note); err != nil {
					return nil, fmt.Errorf("row %d of the table of required builds: post_up_script: %w", i+1, err)
				}
			}
			fixedNames := make([]string, 0, len(NoteFixed))
			for name := range NoteFixed {
				fixedNames = append(fixedNames, name)
			}
			slices.Sort(fixedNames)
			for _, name := range fixedNames {
				src := NoteFixed[name]
				if !NoteUses(note, name) {
					continue
				}
				have := r.Superusers
				if src == "package_facts" {
					have = r.PackageFacts
				}
				for _, p := range r.Packages {
					if have[p] == "" {
						return nil, fmt.Errorf("row %d of the table of required builds: %s uses {%s}, so %s needs an entry for %q", i+1, field, name, src, p)
					}
				}
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

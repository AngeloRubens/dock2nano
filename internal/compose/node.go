package compose

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// entry is one key of a YAML mapping, with the nodes kept for line numbers.
type entry struct {
	key string
	kn  *yaml.Node
	val *yaml.Node
}

func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

func errAt(n *yaml.Node, format string, args ...any) error {
	return fmt.Errorf("line %d: %s", n.Line, fmt.Sprintf(format, args...))
}

// maxMergeSteps bounds how many mappings one call of entries may expand, so
// that a file with deeply repeated merges ends in an error and not in a hang.
const maxMergeSteps = 10000

// entries lists the keys of a mapping in file order. Aliases are followed and
// merge keys ("<<") are expanded: an explicit key wins over a merged one, and
// among merged mappings the first wins. A repeated key is an error, and so is
// a merge that leads back to the mapping being expanded.
func entries(n *yaml.Node) ([]entry, error) {
	steps := 0
	return expand(n, map[*yaml.Node]bool{}, &steps)
}

func expand(n *yaml.Node, active map[*yaml.Node]bool, steps *int) ([]entry, error) {
	n = deref(n)
	if n == nil {
		return nil, fmt.Errorf("expected a mapping")
	}
	if n.Kind != yaml.MappingNode {
		return nil, errAt(n, "expected a mapping")
	}
	if active[n] {
		return nil, errAt(n, "a merge refers to the mapping that contains it")
	}
	active[n] = true
	defer delete(active, n)
	*steps++
	if *steps > maxMergeSteps {
		return nil, errAt(n, "too many merged mappings")
	}
	var out, merged []entry
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Kind == yaml.ScalarNode && k.Tag == "!!merge" {
			srcs := []*yaml.Node{deref(v)}
			if srcs[0].Kind == yaml.SequenceNode {
				srcs = srcs[0].Content
			}
			for _, s := range srcs {
				es, err := expand(s, active, steps)
				if err != nil {
					return nil, err
				}
				merged = append(merged, es...)
			}
			continue
		}
		if k.Kind != yaml.ScalarNode {
			return nil, errAt(k, "mapping keys must be plain strings")
		}
		if seen[k.Value] {
			return nil, errAt(k, "key %q appears twice", k.Value)
		}
		seen[k.Value] = true
		out = append(out, entry{k.Value, k, v})
	}
	for _, e := range merged {
		if !seen[e.key] {
			seen[e.key] = true
			out = append(out, e)
		}
	}
	return out, nil
}

// isExtension reports whether a key is a Compose extension ("x-...").
func isExtension(key string) bool { return strings.HasPrefix(key, "x-") }

func isNull(n *yaml.Node) bool {
	n = deref(n)
	// "!reset" is the Compose tag that clears a value: it counts as null.
	return n == nil || (n.Kind == yaml.ScalarNode && (n.Tag == "!!null" || n.Tag == "!reset"))
}

func scalar(n *yaml.Node) (string, error) {
	n = deref(n)
	if n.Kind != yaml.ScalarNode {
		return "", errAt(n, "expected a single value")
	}
	if isNull(n) {
		return "", nil
	}
	return n.Value, nil
}

func stringList(n *yaml.Node) ([]string, error) {
	n = deref(n)
	if n.Kind != yaml.SequenceNode {
		return nil, errAt(n, "expected a list")
	}
	out := make([]string, 0, len(n.Content))
	for _, c := range n.Content {
		s, err := scalar(c)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func stringOrList(n *yaml.Node) ([]string, error) {
	if d := deref(n); d.Kind == yaml.ScalarNode {
		s, err := scalar(d)
		if err != nil {
			return nil, err
		}
		return []string{s}, nil
	}
	return stringList(n)
}

// boolean reads a YAML boolean, including the yes/no and on/off spellings
// that Compose accepts.
func boolean(n *yaml.Node) (bool, error) {
	v, err := scalar(n)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(v) {
	case "true", "yes", "on":
		return true, nil
	case "false", "no", "off":
		return false, nil
	}
	return false, errAt(deref(n), "%q is not a boolean", v)
}

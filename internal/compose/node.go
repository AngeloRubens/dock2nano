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

// entries lists the keys of a mapping in file order. Aliases are followed and
// merge keys ("<<") are expanded: an explicit key wins over a merged one, and
// among merged mappings the first wins. A repeated key is an error.
func entries(n *yaml.Node) ([]entry, error) {
	n = deref(n)
	if n == nil || n.Kind != yaml.MappingNode {
		if n == nil {
			return nil, fmt.Errorf("expected a mapping")
		}
		return nil, errAt(n, "expected a mapping")
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
				es, err := entries(s)
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
	return n == nil || (n.Kind == yaml.ScalarNode && n.Tag == "!!null")
}

func scalar(n *yaml.Node) (string, error) {
	n = deref(n)
	if n.Kind != yaml.ScalarNode {
		return "", errAt(n, "expected a single value")
	}
	if n.Tag == "!!null" {
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

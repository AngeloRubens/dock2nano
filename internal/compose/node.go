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

// maxMergeSteps bounds the work of one call of entries: each mapping expanded
// and each entry merged counts one. A file with repeated or wide merges then
// ends in an error and not in a hang or in memory exhaustion.
const maxMergeSteps = 100000

// entries lists the keys of a mapping in file order. Aliases are followed and
// merge keys ("<<") are expanded: an explicit key wins over a merged one, and
// among merged mappings the first wins. Parse has already refused repeated
// keys, including a second merge key (applyReset), and any alias that leads
// back to the node containing it (checkSize), so the expansion always ends.
func entries(n *yaml.Node) ([]entry, error) {
	steps := 0
	return expand(n, &steps)
}

func expand(n *yaml.Node, steps *int) ([]entry, error) {
	n = deref(n)
	if n == nil {
		return nil, fmt.Errorf("expected a mapping")
	}
	if n.Kind != yaml.MappingNode {
		return nil, errAt(n, "expected a mapping")
	}
	*steps++
	if *steps > maxMergeSteps {
		return nil, errAt(n, "too many merged mappings")
	}
	var out, merged []entry
	seen := map[string]bool{}
	mergedSeen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Kind == yaml.ScalarNode && k.Tag == "!!merge" && k.Value == "<<" {
			// A list of mappings is allowed only when written inline; an alias
			// to a list is not, as in yaml.v3.
			srcs := []*yaml.Node{v}
			if v.Kind == yaml.SequenceNode {
				srcs = v.Content
			}
			for _, s := range srcs {
				es, err := expand(s, steps)
				if err != nil {
					return nil, err
				}
				*steps += len(es)
				if *steps > maxMergeSteps {
					return nil, errAt(n, "too many merged mappings")
				}
				for _, e := range es {
					if !mergedSeen[e.key] {
						mergedSeen[e.key] = true
						merged = append(merged, e)
					}
				}
			}
			continue
		}
		if k.Kind != yaml.ScalarNode {
			return nil, errAt(k, "mapping keys must be plain strings")
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
		if isNull(c) {
			return nil, errAt(deref(c), "empty entry in the list")
		}
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
// that Compose accepts. A null is false. ok is false when the value holds a
// ${VAR}, which is not interpolated: the caller must list it.
func boolean(n *yaml.Node) (val, ok bool, err error) {
	v, err := scalar(n)
	if err != nil {
		return false, false, err
	}
	if strings.Contains(v, "$") {
		return false, false, nil
	}
	switch strings.ToLower(v) {
	case "true", "yes", "on":
		return true, true, nil
	case "false", "no", "off", "":
		return false, true, nil
	}
	return false, false, errAt(deref(n), "%q is not a boolean", v)
}

// applyReset removes, at every level, the mapping pairs and the list items
// whose value carries the Compose "!reset" tag, as Compose does when it loads
// a file, also when the value is reached through an alias. A key that appears
// twice in a mapping is an error even if one of its values is reset: compose-go
// (loader/reset.go) records each key before it resolves the reset, so it refuses
// the file too. The seen
// set makes it end on any graph, cyclic ones included, so it runs before
// checkSize: a cycle that only exists under a reset node is gone by then.
func applyReset(n *yaml.Node, seen map[*yaml.Node]bool) error {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.AliasNode {
		return applyReset(n.Alias, seen)
	}
	if seen[n] {
		return nil
	}
	seen[n] = true
	switch n.Kind {
	case yaml.MappingNode:
		keys := map[string]bool{}
		var kept []*yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Kind == yaml.ScalarNode {
				if keys[k.Value] {
					return errAt(k, "key %q appears twice", k.Value)
				}
				keys[k.Value] = true
			}
			if deref(v).Tag != "!reset" {
				kept = append(kept, k, v)
			}
		}
		n.Content = kept
	case yaml.SequenceNode:
		var kept []*yaml.Node
		for _, c := range n.Content {
			if deref(c).Tag != "!reset" {
				kept = append(kept, c)
			}
		}
		n.Content = kept
	}
	for _, c := range n.Content {
		if err := applyReset(c, seen); err != nil {
			return err
		}
	}
	return nil
}

// maxExpandedNodes bounds how large the file becomes once every alias and
// merge is followed, so that aliases cannot be used to multiply a big list or
// mapping into an amount of work that grows with the square of the file.
const maxExpandedNodes = 1000000

// checkSize fails when following the aliases of the document gives more than
// maxExpandedNodes nodes, or when an alias leads back to a node that contains
// it (Compose refuses those too). With no cycle the counts in memo are exact.
func checkSize(root *yaml.Node) error {
	memo := map[*yaml.Node]int{}
	active := map[*yaml.Node]bool{}
	var cycleAt *yaml.Node
	var size func(n *yaml.Node) int
	size = func(n *yaml.Node) int {
		if n == nil {
			return 0
		}
		if n.Kind == yaml.AliasNode {
			return 1 + size(n.Alias)
		}
		if v, ok := memo[n]; ok {
			return v
		}
		if active[n] {
			if cycleAt == nil {
				cycleAt = n
			}
			return 1
		}
		active[n] = true
		total := 1
		for _, c := range n.Content {
			total += size(c)
			if total > maxExpandedNodes {
				total = maxExpandedNodes + 1
				break
			}
		}
		delete(active, n)
		memo[n] = total
		return total
	}
	total := size(root)
	if cycleAt != nil {
		return errAt(cycleAt, "an alias refers to the node that contains it")
	}
	if total > maxExpandedNodes {
		return fmt.Errorf("the file expands to more than %d nodes through aliases and merges", maxExpandedNodes)
	}
	return nil
}

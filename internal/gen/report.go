package gen

import (
	"fmt"
	"strings"

	"github.com/AngeloRubens/dock2nano/internal/plan"
)

// kindOrder is the order in which Render prints the kinds: what needs
// attention first.
var kindOrder = []plan.Kind{plan.Unsupported, plan.Approximated, plan.Note, plan.Observed, plan.Converted}

// reviewLine is the first line of the report.
const reviewLine = "This is what was observed from the images and read from the compose file, not a guarantee: review it before you use it."

// Render writes the report as plain text for the terminal. The first line says
// that the output must be reviewed. Then come the items of the project, then
// those of each service in the order of its first item, grouped by kind in
// the order unsupported, approximated, note, observed (converted last); inside
// a group the order of items is kept. Lines are not wrapped.
func Render(items []plan.Item) string {
	var b strings.Builder
	b.WriteString(reviewLine)
	b.WriteString("\n")
	seen := map[string]bool{"": true}
	owners := []string{""}
	for _, it := range items {
		if !seen[it.Service] {
			seen[it.Service] = true
			owners = append(owners, it.Service)
		}
	}
	for _, owner := range owners {
		var mine []plan.Item
		for _, it := range items {
			if it.Service == owner {
				mine = append(mine, it)
			}
		}
		if len(mine) == 0 {
			continue
		}
		if owner == "" {
			b.WriteString("\nproject:\n")
		} else {
			fmt.Fprintf(&b, "\nservice %s:\n", owner)
		}
		for _, k := range kindOrder {
			for _, it := range mine {
				if it.Kind == k {
					fmt.Fprintf(&b, "  %s %s: %s\n", it.Kind, it.Key, it.Message)
				}
			}
		}
	}
	return b.String()
}

// listed joins names with commas and stops after maxListed of them.
func listed(names []string) string {
	if len(names) <= maxListed {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:maxListed], ", "), len(names)-maxListed)
}

package gen

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/AngeloRubens/dock2nano/internal/compose"
	"github.com/AngeloRubens/dock2nano/internal/plan"
)

// reservedPkg is a package name that ops-dns takes in the compose file of
// every project, and the name that replaces it.
const (
	reservedPkg    = "dns"
	reservedPkgNew = "svc-dns"
)

// PackageNames returns the pkg name in compose.yaml of each of the converted
// services (a list of service names of p). The name is the name of a tap
// device, so it has at most 15 characters, all in a-z0-9-.
//
// The service name is lower-cased, every other character becomes "-", repeated
// "-" are collapsed and "-" at the ends is removed. A name of more than 15
// characters becomes its first 8 characters, "-" and the first 6 hex digits of
// the SHA-1 of the original service name. "dns" becomes "svc-dns". A name that
// is empty after cleaning, or two services with the same package name, is an
// error that names the service(s). The items are a Note for each service whose
// package name is not its own name.
func PackageNames(p *compose.Project, converted []string) (map[string]string, []plan.Item, error) {
	names := map[string]string{}
	owner := map[string]string{}
	var items []plan.Item
	for _, service := range converted {
		pkg := cleanName(service)
		if pkg == "" {
			return nil, nil, fmt.Errorf("service %q: the name has no letter or digit to make a package name from", service)
		}
		reason := ""
		if len(pkg) > maxPkgLen {
			sum := sha1.Sum([]byte(service))
			pkg = pkg[:8] + "-" + hex.EncodeToString(sum[:])[:6]
			reason = fmt.Sprintf("a tap device name is at most %d characters, a-z0-9-", maxPkgLen)
		} else if pkg != service {
			reason = fmt.Sprintf("a tap device name is at most %d characters, a-z0-9-", maxPkgLen)
		}
		if pkg == reservedPkg {
			pkg = reservedPkgNew
			reason = "dns is the name of the package ops-dns in the same compose file"
		}
		if other, dup := owner[pkg]; dup {
			return nil, nil, fmt.Errorf("services %q and %q both become the package name %q", other, service, pkg)
		}
		owner[pkg] = service
		names[service] = pkg
		if reason != "" {
			items = append(items, plan.Item{Service: service, Kind: plan.Note, Key: "package name", Message: fmt.Sprintf("service %s is package %s because %s", service, pkg, reason)})
		}
	}
	return names, items, nil
}

// cleanName lower-cases s, turns every character outside a-z0-9 into '-',
// collapses repeated '-' and trims '-' at both ends.
func cleanName(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

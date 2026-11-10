package hub

import (
	"sort"
	"strconv"
	"strings"
)

// Version is a version read as numbers.
type Version struct {
	// Nums are the dotted numbers; a missing one counts as zero.
	Nums []int

	// Suffix is the text after the last number ("rc1" in 2.7.15rc1); empty when
	// there is none.
	Suffix string
}

// ParseVersion reads a version such as "1.28.3", "v11.5.0" or "2.7.15rc1". It
// reports false when the text does not start with a number.
func ParseVersion(s string) (Version, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	var v Version
	i := 0
	for {
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == i {
			break
		}
		n, err := strconv.Atoi(s[i:j])
		if err != nil {
			return Version{}, false
		}
		v.Nums = append(v.Nums, n)
		i = j
		if i+1 < len(s) && s[i] == '.' && s[i+1] >= '0' && s[i+1] <= '9' {
			i++
			continue
		}
		break
	}
	if len(v.Nums) == 0 {
		return Version{}, false
	}
	v.Suffix = s[i:]
	return v, true
}

// String writes the numbers joined with dots, without the suffix.
func (v Version) String() string {
	parts := make([]string, len(v.Nums))
	for i, n := range v.Nums {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ".")
}

// cmpNums compares two lists of numbers, a missing number counting as zero.
func cmpNums(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := 0, 0
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}

// Compare orders two versions: the numbers first, then the suffix as text.
func (v Version) Compare(o Version) int {
	if c := cmpNums(v.Nums, o.Nums); c != 0 {
		return c
	}
	return strings.Compare(v.Suffix, o.Suffix)
}

// series is the part of the numbers that names the major series: the first
// number, or the first two when the first is zero (0.9 and 0.10 are not the
// same series).
func (v Version) series() []int {
	if len(v.Nums) > 1 && v.Nums[0] == 0 {
		return v.Nums[:2]
	}
	return v.Nums[:1]
}

// SameSeries tells whether two versions are in the same major series.
func (v Version) SameSeries(o Version) bool {
	return cmpNums(v.series(), o.series()) == 0
}

// distance is the absolute difference of the numbers, the most significant
// first, so that comparing two distances as lists tells which is nearer.
func (v Version) distance(o Version) []int {
	n := max(len(v.Nums), len(o.Nums))
	out := make([]int, n)
	for i := range out {
		x, y := 0, 0
		if i < len(v.Nums) {
			x = v.Nums[i]
		}
		if i < len(o.Nums) {
			y = o.Nums[i]
		}
		out[i] = max(x-y, y-x)
	}
	return out
}

// Level says how well a package of the hub fits the version asked for.
type Level int

const (
	// NotFound: the hub has no package of that name for the architecture.
	NotFound Level = iota
	// Exact: the same version.
	Exact
	// Near: another version of the same major series.
	Near
	// Far: a version of another major series, or the version asked for is not
	// known.
	Far
)

func (l Level) String() string {
	switch l {
	case Exact:
		return "exact"
	case Near:
		return "near"
	case Far:
		return "far"
	}
	return "none"
}

// NormalizeArch maps the spellings of the hub (x86_64) and of docker (amd64)
// to one.
func NormalizeArch(s string) string {
	switch a := strings.ToLower(strings.TrimSpace(s)); a {
	case "x86_64", "amd64", "x86-64", "x64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	default:
		return a
	}
}

// namespaceRank puts the namespaces of the maintainers of ops first when two
// packages fit equally.
func namespaceRank(ns string) int {
	switch ns {
	case "eyberg":
		return 0
	case "nanovms":
		return 1
	}
	return 2
}

// Select picks the package of the hub that fits name, version and architecture
// best. want may be empty when the version of the user is not known.
func Select(pkgs []Package, name, want, arch string) (Package, Level) {
	var cands []Package
	for _, p := range pkgs {
		if p.Usable() && strings.EqualFold(p.Name, name) && NormalizeArch(p.Arch) == NormalizeArch(arch) {
			cands = append(cands, p)
		}
	}
	return SelectFrom(cands, want)
}

// SelectFrom picks among packages that are already filtered (usable, of the
// right architecture): the same version, else the nearest of the same major
// series, else the nearest of all, else, when want is not known, the newest.
func SelectFrom(cands []Package, want string) (Package, Level) {
	if len(cands) == 0 {
		return Package{}, NotFound
	}
	w, known := ParseVersion(want)
	sorted := append([]Package(nil), cands...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, _ := ParseVersion(sorted[i].Version)
		b, _ := ParseVersion(sorted[j].Version)
		if known {
			if c := cmpNums(w.distance(a), w.distance(b)); c != 0 {
				return c < 0
			}
		}
		if c := a.Compare(b); c != 0 {
			return c > 0
		}
		return namespaceRank(sorted[i].Namespace) < namespaceRank(sorted[j].Namespace)
	})
	best := sorted[0]
	if !known {
		return best, Far
	}
	got, _ := ParseVersion(best.Version)
	switch {
	case got.Compare(w) == 0:
		return best, Exact
	case got.SameSeries(w):
		return best, Near
	}
	return best, Far
}

// ImageName is the name of an image without registry, path, tag and digest:
// "registry:5000/library/nginx:1.25@sha256:ab" is "nginx".
func ImageName(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		ref = ref[i+1:]
	}
	if i := strings.Index(ref, ":"); i >= 0 {
		ref = ref[:i]
	}
	return strings.ToLower(ref)
}

// imageTag is the tag of an image reference, or "".
func imageTag(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		ref = ref[i+1:]
	}
	if i := strings.Index(ref, ":"); i >= 0 {
		return ref[i+1:]
	}
	return ""
}

// ImageVersion is the version the user runs, as numbers only: the tag of the
// image when it starts with one ("1.25-alpine" is "1.25"), else the value of
// the variable NAME_VERSION that many images set (NGINX_VERSION), else "".
func ImageVersion(ref string, env []string) string {
	if v, ok := ParseVersion(imageTag(ref)); ok {
		return v.String()
	}
	name := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(ImageName(ref)))
	names := []string{name + "_VERSION"}
	if name == "POSTGRES" || name == "POSTGRESQL" {
		names = append(names, "PG_VERSION")
	}
	for _, want := range names {
		for _, e := range env {
			if k, val, ok := strings.Cut(e, "="); ok && k == want {
				if v, ok := ParseVersion(val); ok {
					return v.String()
				}
			}
		}
	}
	return ""
}

// ProgramName is the name of a program for the hub: the last element of its
// path, with a trailing version taken off when nothing is left otherwise
// (python3.11 is python).
func ProgramName(program string) string {
	base := strings.ToLower(program)
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if trimmed := strings.TrimRight(base, "0123456789."); trimmed != "" {
		return trimmed
	}
	return base
}

// Names are the names to look for in the hub, the image first: the ones that
// are not empty and not repeated.
func Names(image, program string) []string {
	var out []string
	for _, n := range []string{image, ProgramName(program)} {
		if n != "" && !contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

package hub

import (
	"fmt"
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
	// 1.2.3rc1 comes before 1.2.3
	switch {
	case v.Suffix == o.Suffix:
		return 0
	case v.Suffix == "":
		return 1
	case o.Suffix == "":
		return -1
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

// namespaceRank puts the namespaces of hints/hub-tables.yaml first when two
// packages fit equally.
func namespaceRank(ns string) int {
	for i, n := range loadedTables().NamespacePreference {
		if n == ns {
			return i
		}
	}
	return len(loadedTables().NamespacePreference)
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
// right architecture). With a version asked for: the nearest major series
// (the same one, else the one whose number is nearest, the older on a tie).
// In the series of the user, the release nearest to the version asked for
// (of the newest release that is not newer and the oldest that is newer, the
// one nearer at the first number that differs, the older when they are as
// near); in another series, its newest release. A prerelease is not the newest
// while a release exists. When
// want is not known: the newest package. Of equal versions the one spelled with as many numbers as want comes first,
// then the namespace preference, then the name.
func SelectFrom(cands []Package, want string) (Package, Level) {
	if len(cands) == 0 {
		return Package{}, NotFound
	}
	w, known := ParseVersion(want)
	type cand struct {
		p Package
		v Version
	}
	all := make([]cand, len(cands))
	for i, p := range cands {
		v, _ := ParseVersion(p.Version)
		all[i] = cand{p, v}
	}
	// tie says that a comes before b when their versions are equal
	tie := func(a, b cand) bool {
		if known {
			if ea, eb := len(a.v.Nums) == len(w.Nums), len(b.v.Nums) == len(w.Nums); ea != eb {
				return ea
			}
		}
		if ra, rb := namespaceRank(a.p.Namespace), namespaceRank(b.p.Namespace); ra != rb {
			return ra < rb
		}
		return a.p.Ref() < b.p.Ref()
	}
	// pick returns the newest (or the oldest) of the list
	pick := func(list []cand, newest bool) cand {
		best := list[0]
		for _, c := range list[1:] {
			switch cmp := c.v.Compare(best.v); {
			case cmp == 0 && tie(c, best):
				best = c
			case cmp > 0 && newest, cmp < 0 && !newest:
				best = c
			}
		}
		return best
	}
	// releasesFirst keeps the releases of a list when there are any, so that a
	// prerelease is not the newest while a release exists
	releasesFirst := func(list []cand) []cand {
		var out []cand
		for _, c := range list {
			if c.v.Suffix == "" {
				out = append(out, c)
			}
		}
		if len(out) > 0 {
			return out
		}
		return list
	}
	if !known {
		return pick(releasesFirst(all), true).p, Far
	}
	// the nearest major series: the one of the user, else the one whose first
	// number is nearest, the older on a tie. (The 0.x versions are a series each
	// by their second number; a user at "0" with several of them is too rare to
	// be treated further.)
	md := func(nums []int) int {
		x := nums[0] - w.series()[0]
		return max(x, -x)
	}
	series := all[0].v.series()
	for _, c := range all[1:] {
		cs := c.v.series()
		switch {
		case cmpNums(series, w.series()) == 0:
			// the series is the one of the user already
		case cmpNums(cs, w.series()) == 0:
			series = cs
		case md(cs) < md(series), md(cs) == md(series) && cmpNums(cs, series) < 0:
			series = cs
		}
	}
	var group []cand
	for _, c := range all {
		if cmpNums(c.v.series(), series) == 0 {
			group = append(group, c)
		}
	}
	var best cand
	if cmpNums(series, w.series()) != 0 {
		// another series: its newest release, above or below the version asked
		best = pick(releasesFirst(group), true)
	} else {
		// the series of the user: of the newest release that is not newer and
		// the oldest that is newer, the one that is nearer at the first number
		// that differs from the version asked, the older when they are as near
		var below, above []cand
		for _, c := range group {
			if c.v.Compare(w) <= 0 {
				below = append(below, c)
			} else {
				above = append(above, c)
			}
		}
		switch {
		case len(above) == 0:
			best = pick(below, true)
		case len(below) == 0:
			best = pick(above, false)
		default:
			bb, ba := pick(below, true), pick(above, false)
			ib, ia := firstDiff(bb.v, w), firstDiff(ba.v, w)
			switch {
			case ib > ia:
				best = bb
			case ia > ib:
				best = ba
			case numDiff(ba.v, w, ia) < numDiff(bb.v, w, ib):
				best = ba
			default:
				best = bb
			}
		}
	}
	switch {
	case best.v.Compare(w) == 0 && len(w.Nums) == len(best.v.Nums):
		// "1.25" is not the version 1.25.0: it may be any 1.25.x, and the
		// other way round
		return best.p, Exact
	case best.v.SameSeries(w):
		return best.p, Near
	}
	return best.p, Far
}

// numAt is the number of a version at a place, zero past the end.
func numAt(v Version, i int) int {
	if i < len(v.Nums) {
		return v.Nums[i]
	}
	return 0
}

// firstDiff is the place of the first number in which two versions differ; past
// the numbers when they do not.
func firstDiff(v, w Version) int {
	n := max(len(v.Nums), len(w.Nums))
	for i := 0; i < n; i++ {
		if numAt(v, i) != numAt(w, i) {
			return i
		}
	}
	return n
}

// numDiff is how far two versions are at a place.
func numDiff(v, w Version, i int) int {
	x := numAt(v, i) - numAt(w, i)
	return max(x, -x)
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
// the variable NAME_VERSION that many images set (NGINX_VERSION), or one of
// those named for the image in hints/hub-tables.yaml; else "". When the tag
// gives fewer numbers than the variable and the variable starts with them
// (tag 1.25, NGINX_VERSION=1.25.5), the variable is the more exact one.
func ImageVersion(ref string, env []string) string {
	tag, tagOK := ParseVersion(imageTag(ref))
	name := ImageName(ref)
	up := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name))
	names := append([]string{up + "_VERSION"}, loadedTables().VersionEnv[name]...)
	var fromEnv Version
	envOK := false
	for _, want := range names {
		for _, e := range env {
			if k, val, ok := strings.Cut(e, "="); ok && k == want && !envOK {
				fromEnv, envOK = ParseVersion(val)
			}
		}
	}
	switch {
	case tagOK && envOK && len(fromEnv.Nums) > len(tag.Nums) && cmpNums(fromEnv.Nums[:len(tag.Nums)], tag.Nums) == 0:
		return fromEnv.String()
	case tagOK:
		return tag.String()
	case envOK:
		return fromEnv.String()
	}
	return ""
}

// ProgramName is the name of a program for the hub: the last element of its
// path, with a trailing version taken off when at least two characters are left
// (python3.11 is python; x264 stays x264).
func ProgramName(program string) string {
	base := strings.ToLower(program)
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if trimmed := strings.TrimRight(base, "0123456789."); len(trimmed) >= 2 {
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

// Apart says how far two versions of the same series are, in words: "N patch
// releases apart" or "N minor releases apart". It is empty when the versions
// are in different series, or differ only in the number of places or in a
// suffix.
func Apart(a, b string) string {
	x, ok1 := ParseVersion(a)
	y, ok2 := ParseVersion(b)
	if !ok1 || !ok2 || !x.SameSeries(y) {
		return ""
	}
	d := x.distance(y)
	// the first number is the series; the next ones say how far
	for i := 1; i < len(d); i++ {
		if d[i] != 0 {
			kind, unit := "patch", "releases"
			if i == 1 {
				kind = "minor"
			}
			if d[i] == 1 {
				unit = "release"
			}
			return fmt.Sprintf("%d %s %s apart", d[i], kind, unit)
		}
	}
	// only the number of places, or a suffix, differs
	return ""
}

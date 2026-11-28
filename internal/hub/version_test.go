package hub

import (
	"reflect"
	"strings"
	"testing"
)

const goodSum = "7a1fbdceb9f645836eaecae5e119fd99aa45068b1a3000f1d211e9b98b46fd2d"

func pkg(ns, name, version, arch string) Package {
	return Package{Namespace: ns, Name: name, Version: version, Arch: arch, SHA256: goodSum}
}

func TestParseVersion(t *testing.T) {
	rows := []struct {
		in     string
		nums   []int
		suffix string
		ok     bool
	}{
		{"1.28.3", []int{1, 28, 3}, "", true},
		{"v11.5.0", []int{11, 5, 0}, "", true},
		{"V2.1", []int{2, 1}, "", true},
		{"2.7.15rc1", []int{2, 7, 15}, "rc1", true},
		{"1.25-alpine", []int{1, 25}, "-alpine", true},
		{"16", []int{16}, "", true},
		{"1.2.", []int{1, 2}, ".", true},
		{"latest", nil, "", false},
		{"", nil, "", false},
		{"alpine3.19", nil, "", false},
	}
	for _, r := range rows {
		v, ok := ParseVersion(r.in)
		if ok != r.ok || !reflect.DeepEqual(v.Nums, r.nums) || v.Suffix != r.suffix {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v, %q, %v", r.in, v, ok, r.nums, r.suffix, r.ok)
		}
	}
}

func TestVersionString(t *testing.T) {
	v, _ := ParseVersion("v1.25-alpine")
	if v.String() != "1.25" {
		t.Errorf("String() = %q", v.String())
	}
}

func TestCompareAndSeries(t *testing.T) {
	a, _ := ParseVersion("1.25")
	b, _ := ParseVersion("1.25.0")
	c, _ := ParseVersion("1.26")
	d, _ := ParseVersion("2.0")
	if a.Compare(b) != 0 || a.Compare(c) >= 0 || d.Compare(c) <= 0 {
		t.Errorf("order: %d %d %d", a.Compare(b), a.Compare(c), d.Compare(c))
	}
	if !a.SameSeries(c) || a.SameSeries(d) {
		t.Errorf("series: %v %v", a.SameSeries(c), a.SameSeries(d))
	}
	z1, _ := ParseVersion("0.9.1")
	z2, _ := ParseVersion("0.10.0")
	z3, _ := ParseVersion("0.9.7")
	if z1.SameSeries(z2) || !z1.SameSeries(z3) {
		t.Errorf("a zero major keeps the minor in the series: %v %v", z1.SameSeries(z2), z1.SameSeries(z3))
	}
}

func TestNormalizeArch(t *testing.T) {
	for _, r := range [][2]string{{"x86_64", "amd64"}, {"amd64", "amd64"}, {"X86_64", "amd64"}, {"arm64", "arm64"}, {"aarch64", "arm64"}, {"riscv", "riscv"}} {
		if got := NormalizeArch(r[0]); got != r[1] {
			t.Errorf("NormalizeArch(%q) = %q, want %q", r[0], got, r[1])
		}
	}
}

func TestSelectExact(t *testing.T) {
	pkgs := []Package{pkg("eyberg", "nginx", "1.15.6", "x86_64"), pkg("eyberg", "nginx", "1.28.3", "x86_64"), pkg("eyberg", "nginx", "1.18.0", "x86_64")}
	p, lvl := Select(pkgs, "nginx", "1.18.0", "amd64")
	if lvl != Exact || p.Version != "1.18.0" {
		t.Errorf("got %v %v", p.Version, lvl)
	}
	p, lvl = Select(pkgs, "nginx", "1.18", "amd64")
	if lvl != Near || p.Version != "1.18.0" {
		t.Errorf("1.18 may be any 1.18.x, so it is not exactly 1.18.0: got %v %v", p.Version, lvl)
	}
}

func TestSelectNearIsTheClosestOfTheSameSeries(t *testing.T) {
	pkgs := []Package{pkg("eyberg", "nginx", "1.15.6", "x86_64"), pkg("eyberg", "nginx", "1.28.3", "x86_64"), pkg("eyberg", "nginx", "1.18.0", "x86_64"), pkg("eyberg", "nginx", "2.0.0", "x86_64")}
	p, lvl := Select(pkgs, "nginx", "1.25.3", "amd64")
	if lvl != Near || p.Version != "1.28.3" {
		t.Errorf("got %v %v", p.Version, lvl)
	}
	p, lvl = Select(pkgs, "nginx", "1.17.9", "amd64")
	if lvl != Near || p.Version != "1.18.0" {
		t.Errorf("got %v %v", p.Version, lvl)
	}
}

func TestSelectFarAndUnknown(t *testing.T) {
	pkgs := []Package{pkg("eyberg", "node", "v11.5.0", "x86_64"), pkg("eyberg", "node", "v18.12.1", "x86_64"), pkg("eyberg", "node", "v14.2.0", "x86_64")}
	p, lvl := Select(pkgs, "node", "20.5.0", "amd64")
	if lvl != Far || p.Version != "v18.12.1" {
		t.Errorf("a version of another series: got %v %v", p.Version, lvl)
	}
	p, lvl = Select(pkgs, "node", "", "amd64")
	if lvl != Far || p.Version != "v18.12.1" {
		t.Errorf("no version known takes the newest: got %v %v", p.Version, lvl)
	}
}

func TestSelectNone(t *testing.T) {
	pkgs := []Package{pkg("eyberg", "nginx", "1.28.3", "x86_64")}
	if _, lvl := Select(pkgs, "caddy", "2.7", "amd64"); lvl != NotFound {
		t.Errorf("another name: %v", lvl)
	}
	if _, lvl := Select(nil, "nginx", "1", "amd64"); lvl != NotFound {
		t.Errorf("an empty list: %v", lvl)
	}
}

func TestSelectFiltersTheArchitecture(t *testing.T) {
	pkgs := []Package{pkg("eyberg", "redis", "6.2.6", "arm64"), pkg("eyberg", "redis", "5.0.5", "x86_64")}
	p, lvl := Select(pkgs, "redis", "6.2.6", "amd64")
	if p.Version != "5.0.5" || lvl != Far {
		t.Errorf("amd64 must not get the arm64 package: %v %v", p.Version, lvl)
	}
	p, lvl = Select(pkgs, "redis", "6.2.6", "arm64")
	if p.Version != "6.2.6" || lvl != Exact {
		t.Errorf("arm64: %v %v", p.Version, lvl)
	}
	if _, lvl := Select(pkgs, "redis", "6.2.6", "riscv64"); lvl != NotFound {
		t.Errorf("an architecture the hub does not have: %v", lvl)
	}
	// the spellings of docker and of the hub are the same architecture
	if _, lvl := Select(pkgs, "redis", "5.0.5", "x86_64"); lvl != Exact {
		t.Errorf("x86_64 asked: %v", lvl)
	}
}

func TestSelectSkipsPackagesThatCannotBeUsed(t *testing.T) {
	empty := pkg("eyberg", "nginx", "1.28.3", "x86_64")
	empty.SHA256 = emptySHA256
	missing := pkg("eyberg", "nginx", "1.28.4", "x86_64")
	missing.SHA256 = ""
	odd := pkg("eyberg", "nginx", "latest", "x86_64")
	pkgs := []Package{empty, missing, odd, pkg("eyberg", "nginx", "1.15.6", "x86_64")}
	p, lvl := Select(pkgs, "nginx", "1.28.3", "amd64")
	if p.Version != "1.15.6" || lvl != Near {
		t.Errorf("got %v %v", p.Version, lvl)
	}
	if _, lvl := Select(pkgs[:3], "nginx", "1.28.3", "amd64"); lvl != NotFound {
		t.Errorf("only unusable packages: %v", lvl)
	}
}

func TestSelectPrefersTheMaintainers(t *testing.T) {
	pkgs := []Package{pkg("zeta", "nginx", "1.28.3", "x86_64"), pkg("nanovms", "nginx", "1.28.3", "x86_64"), pkg("eyberg", "nginx", "1.28.3", "x86_64")}
	p, _ := Select(pkgs, "nginx", "1.28.3", "amd64")
	if p.Namespace != "eyberg" {
		t.Errorf("got %s", p.Namespace)
	}
	p, _ = Select(pkgs[:2], "nginx", "1.28.3", "amd64")
	if p.Namespace != "nanovms" {
		t.Errorf("got %s", p.Namespace)
	}
}

func TestUsable(t *testing.T) {
	ok := pkg("eyberg", "nginx", "1.28.3", "x86_64")
	if !ok.Usable() {
		t.Fatal("a good package is not usable")
	}
	rows := []struct {
		name string
		mut  func(p *Package)
	}{
		{"empty digest", func(p *Package) { p.SHA256 = emptySHA256 }},
		{"upper empty", func(p *Package) { p.SHA256 = strings.ToUpper(emptySHA256) }},
		{"no digest", func(p *Package) { p.SHA256 = "" }},
		{"short digest", func(p *Package) { p.SHA256 = "abc" }},
		{"not hex", func(p *Package) { p.SHA256 = strings.Repeat("g", 64) }},
		{"slash in name", func(p *Package) { p.Name = "a/b" }},
		{"dots in name", func(p *Package) { p.Name = ".." }},
		{"no namespace", func(p *Package) { p.Namespace = "" }},
		{"no arch", func(p *Package) { p.Arch = "" }},
		{"version not read", func(p *Package) { p.Version = "latest" }},
		{"query in name", func(p *Package) { p.Name = "a?b" }},
	}
	for _, r := range rows {
		p := ok
		r.mut(&p)
		if p.Usable() {
			t.Errorf("%s: the package is usable", r.name)
		}
	}
}

func TestImageNameAndVersion(t *testing.T) {
	names := [][2]string{
		{"nginx", "nginx"},
		{"nginx:1.25", "nginx"},
		{"docker.io/library/nginx:1.25@sha256:ab", "nginx"},
		{"localhost:5000/team/App:v1", "app"},
		{"dock2nano/proj/web:latest", "web"},
	}
	for _, n := range names {
		if got := ImageName(n[0]); got != n[1] {
			t.Errorf("ImageName(%q) = %q, want %q", n[0], got, n[1])
		}
	}
	rows := []struct {
		ref  string
		env  []string
		want string
	}{
		{"nginx:1.25.3-alpine", nil, "1.25.3"},
		{"nginx:latest", []string{"PATH=/bin", "NGINX_VERSION=1.29.1"}, "1.29.1"},
		{"nginx", []string{"NGINX_VERSION=1.29.1"}, "1.29.1"},
		{"nginx:alpine", nil, ""},
		{"postgres:latest", []string{"PG_VERSION=16.2-1.pgdg120+1"}, "16.2"},
		{"my-app:latest", []string{"MY_APP_VERSION=3.1"}, "3.1"},
		{"redis:7", []string{"REDIS_VERSION=7.2.4"}, "7.2.4"},
		{"nginx:1.25", []string{"NGINX_VERSION=1.25.5"}, "1.25.5"},
		{"nginx:1.25", []string{"NGINX_VERSION=1.26.0"}, "1.25"},
		{"nginx:1.25.3", []string{"NGINX_VERSION=1.25.5"}, "1.25.3"},
		{"nginx:1.25", nil, "1.25"},
	}
	for _, r := range rows {
		if got := ImageVersion(r.ref, r.env); got != r.want {
			t.Errorf("ImageVersion(%q, %v) = %q, want %q", r.ref, r.env, got, r.want)
		}
	}
}

func TestProgramNameAndNames(t *testing.T) {
	for _, r := range [][2]string{{"/usr/sbin/nginx", "nginx"}, {"python3.11", "python"}, {"/usr/local/bin/node", "node"}, {"/bin/123", "123"}, {"x264", "x264"}, {"/usr/bin/s3", "s3"}} {
		if got := ProgramName(r[0]); got != r[1] {
			t.Errorf("ProgramName(%q) = %q, want %q", r[0], got, r[1])
		}
	}
	if got := Names("nginx", "/usr/sbin/nginx"); !reflect.DeepEqual(got, []string{"nginx"}) {
		t.Errorf("repeated names: %v", got)
	}
	if got := Names("", "/usr/sbin/nginx"); !reflect.DeepEqual(got, []string{"nginx"}) {
		t.Errorf("no image name: %v", got)
	}
	if got := Names("bitnami-nginx", "/opt/nginx"); !reflect.DeepEqual(got, []string{"bitnami-nginx", "nginx"}) {
		t.Errorf("two names: %v", got)
	}
}

func TestLevelString(t *testing.T) {
	rows := []struct {
		l    Level
		want string
	}{{Exact, "exact"}, {Near, "near"}, {Far, "far"}, {NotFound, "none"}}
	for _, r := range rows {
		if r.l.String() != r.want {
			t.Errorf("%d: %q", r.l, r.l.String())
		}
	}
}

func TestTagLessExactThanTheVariableIsRefinedByIt(t *testing.T) {
	pkgs := []Package{pkg("eyberg", "nginx", "1.25.0", "x86_64"), pkg("eyberg", "nginx", "1.25.5", "x86_64")}
	want := ImageVersion("nginx:1.25", []string{"NGINX_VERSION=1.25.5"})
	p, lvl := Select(pkgs, "nginx", want, "amd64")
	if p.Version != "1.25.5" || lvl != Exact {
		t.Errorf("%v %v", p.Version, lvl)
	}
	// without the variable, 1.25 is not known to be 1.25.0
	p, lvl = Select(pkgs[:1], "nginx", ImageVersion("nginx:1.25", nil), "amd64")
	if p.Version != "1.25.0" || lvl != Near {
		t.Errorf("%v %v", p.Version, lvl)
	}
}

func TestAPrereleaseComesBeforeTheRelease(t *testing.T) {
	rc, _ := ParseVersion("1.2.3rc1")
	rel, _ := ParseVersion("1.2.3")
	if rc.Compare(rel) >= 0 || rel.Compare(rc) <= 0 {
		t.Errorf("%d %d", rc.Compare(rel), rel.Compare(rc))
	}
	pkgs := []Package{pkg("eyberg", "x", "1.2.3rc1", "x86_64"), pkg("eyberg", "x", "1.2.3", "x86_64")}
	p, lvl := Select(pkgs, "x", "", "amd64")
	if p.Version != "1.2.3" || lvl != Far {
		t.Errorf("the newest is the release: %v %v", p.Version, lvl)
	}
}

func TestEqualPackagesAreOrderedByTheirName(t *testing.T) {
	a := pkg("zeta", "x", "1.0", "x86_64")
	b := pkg("alpha", "x", "1.0", "x86_64")
	for _, order := range [][]Package{{a, b}, {b, a}} {
		if p, _ := Select(order, "x", "1.0", "amd64"); p.Namespace != "alpha" {
			t.Errorf("got %s", p.Namespace)
		}
	}
}

func TestTheShippedTablesAreRead(t *testing.T) {
	tb := loadedTables()
	if len(tb.NamespacePreference) == 0 || tb.NamespacePreference[0] != "eyberg" {
		t.Errorf("namespaces: %v", tb.NamespacePreference)
	}
	if !contains(tb.VersionEnv["postgres"], "PG_VERSION") {
		t.Errorf("version variables: %v", tb.VersionEnv)
	}
	sp, ok := SingleProcessFor("NGINX")
	if !ok || sp.ConfigFlag != "-c" || len(sp.Settings) != 3 || sp.Settings[0] != (Setting{"master_process", "off"}) || sp.Settings[2] != (Setting{"worker_processes", "1"}) {
		t.Errorf("nginx: %+v %v", sp, ok)
	}
	if _, ok := SingleProcessFor("other"); ok {
		t.Error("a program that is not in the table")
	}
}

func TestParseTables(t *testing.T) {
	row := func(extra string) string {
		return "single_process:\n  x:\n    config_paths: [/x.conf]\n    config_flag: -c\n    settings:\n      - {name: n, value: v}\n" + extra
	}
	tb, err := ParseTables([]byte("namespace_preference: [a]\n" + row("    params_flag: -g\n    base_volume_size: 200m\n    base_volume_evidence: seen\n")))
	if err != nil || tb.SingleProcess["x"].Settings[0].Value != "v" || tb.SingleProcess["x"].ParamsFlag != "-g" || tb.SingleProcess["x"].BaseVolumeSize != "200m" {
		t.Fatalf("%+v %v", tb, err)
	}
	for _, bad := range []string{
		"typo: 1\n",
		"single_process:\n  x:\n    config_flag: -c\n    settings:\n      - {name: n, value: v}\n",
		"single_process:\n  x:\n    config_paths: [/x]\n    config_flag: -c\n",
		"single_process:\n  x:\n    config_paths: [/x]\n    config_flag: -c\n    settings:\n      - {name: n}\n",
		"single_process:\n  x:\n    config_paths: [/x]\n    settings:\n      - {name: n, value: v}\n",
		"single_process:\n  X:\n    config_paths: [/x]\n    config_flag: -c\n    settings:\n      - {name: n, value: v}\n",
		"single_process:\n  x:\n    config_paths: [x.conf]\n    config_flag: -c\n    settings:\n      - {name: n, value: v}\n",
		"single_process:\n  x:\n    config_paths: [/a/../x]\n    config_flag: -c\n    settings:\n      - {name: n, value: v}\n",
		"single_process:\n  x:\n    config_paths: [/x]\n    config_flag: -c\n    settings:\n      - {name: n, value: \"a;b\"}\n",
		"single_process:\n  x:\n    config_paths: [/x]\n    config_flag: -c\n    settings:\n      - {name: n, value: \"a b\"}\n",
		"single_process:\n  x:\n    config_paths: [/x]\n    config_flag: -c\n    settings:\n      - {name: n, value: \"a#\"}\n",
		"single_process:\n  x:\n    config_paths: [/x]\n    config_flag: -c\n    settings:\n      - {name: n, value: \"{\"}\n",
		row("    base_volume_size: lots\n    base_volume_evidence: e\n"),
		row("    base_volume_size: 200m\n"),
		"single_process:\n  x:\n    config_paths: [/x]\n    config_flag: c\n    settings:\n      - {name: n, value: v}\n",
		row("    params_flag: g\n"),
		row("    compare: [\"a b\"]\n"),
		"single_process:\n  x:\n    config_paths: [/x]\n    config_flag: -c\n    settings:\n      - {name: n, value: v}\n      - {name: n, value: w}\n",
		"version_env:\n  Postgres: [PG_VERSION]\n",
		"version_env:\n  postgres: []\n",
	} {
		if _, err := ParseTables([]byte(bad)); err == nil {
			t.Errorf("accepted: %q", bad)
		}
	}
}

func TestSelectTakesTheNearestReleaseOfTheSeriesOfTheUser(t *testing.T) {
	rows := []struct {
		want     string
		versions []string
		got      string
		level    Level
	}{
		{"17", []string{"16.1", "16.4"}, "16.4", Far},
		{"1.25.3", []string{"1.24.0", "1.24.9"}, "1.24.9", Near},
		{"1.25.3", []string{"1.24.9", "1.24.0"}, "1.24.9", Near},
		{"1.25.3", []string{"1.24.9", "1.26.0"}, "1.24.9", Near},
		{"1.25.3", []string{"1.26.0", "1.24.9"}, "1.24.9", Near},
		{"1.24.5", []string{"1.24.4", "1.24.2"}, "1.24.4", Near},
		{"1.24.5", []string{"1.24.2", "1.24.4"}, "1.24.4", Near},
		{"1.24.5", []string{"1.24.7", "1.24.9"}, "1.24.7", Near},
		{"1.24.5", []string{"1.24.9", "1.24.7"}, "1.24.7", Near},
		{"1.25.3", []string{"1.25.3rc1", "1.25.3"}, "1.25.3", Exact},
		{"1.25.3", []string{"1.25.3", "1.25.3rc1"}, "1.25.3", Exact},
		{"1.25.3", []string{"1.24.0", "1.25.9"}, "1.25.9", Near},
		{"1", []string{"0.9", "2.0"}, "0.9", Far},
		{"", []string{"2.0.0rc1", "1.9.0"}, "1.9.0", Far},
		{"3.0", []string{"2.1.0rc1", "2.0.5"}, "2.0.5", Far},
		{"1.25.3", []string{"1.15.6", "1.28.3"}, "1.28.3", Near},
		{"1.24.0", []string{"1.22.0", "1.26.0"}, "1.22.0", Near},
		{"1.25.3", []string{"1.25.1", "1.25.5"}, "1.25.1", Near},
		{"1.25.3", []string{"1.25.1", "1.25.4"}, "1.25.4", Near},
		{"16.1", []string{"16.0", "16.2"}, "16.0", Near},
		{"14.5", []string{"11.3.0", "16.0", "16.2"}, "16.2", Far},
		{"17.1", []string{"11.3.0", "16.0", "16.2"}, "16.2", Far},
		{"1.0", []string{"1.5", "1.2"}, "1.2", Near},
		{"16.3", []string{"15.9", "16.1", "17.0"}, "16.1", Near},
		{"16", []string{"15.0", "17.0"}, "15.0", Far},
	}
	for _, r := range rows {
		var pkgs []Package
		for _, v := range r.versions {
			pkgs = append(pkgs, pkg("eyberg", "x", v, "x86_64"))
		}
		if p, lvl := Select(pkgs, "x", r.want, "amd64"); p.Version != r.got || lvl != r.level {
			t.Errorf("want %s of %v: got %s %v, want %s %v", r.want, r.versions, p.Version, lvl, r.got, r.level)
		}
	}
}

func TestAShortHubVersionIsNotExactForALongerOne(t *testing.T) {
	pkgs := []Package{pkg("eyberg", "x", "1.25", "x86_64")}
	if _, lvl := Select(pkgs, "x", "1.25.0", "amd64"); lvl != Near {
		t.Errorf("%v", lvl)
	}
	both := []Package{pkg("eyberg", "x", "1.25", "x86_64"), pkg("zeta", "x", "1.25.0", "x86_64")}
	if p, lvl := Select(both, "x", "1.25.0", "amd64"); p.Version != "1.25.0" || lvl != Exact {
		t.Errorf("%v %v", p.Version, lvl)
	}
}

func TestApart(t *testing.T) {
	rows := [][3]string{
		{"1.25.3", "1.28.3", "3 minor releases apart"},
		{"1.25.3", "1.26.0", "1 minor release apart"},
		{"1.25.3", "1.25.4", "1 patch release apart"},
		{"1.25", "1.25.0", ""},
		{"1.2.3", "1.2.3rc1", ""},
		{"1.25.3", "1.25.5", "2 patch releases apart"},
		{"1.25.3", "1.25.3", ""},
		{"1.25.3", "2.0.0", ""},
		{"x", "1.0", ""},
	}
	for _, r := range rows {
		if got := Apart(r[0], r[1]); got != r[2] {
			t.Errorf("Apart(%q, %q) = %q, want %q", r[0], r[1], got, r[2])
		}
	}
}

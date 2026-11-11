package output

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/plan"
	"gopkg.in/yaml.v3"
)

func put(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func svc(name, sysroot string) plan.Service {
	return plan.Service{Name: name, PackageName: name, Sysroot: sysroot, Manifest: plan.Manifest{Program: "/" + name}}
}

func composeOf(pkgs ...string) plan.ComposeFile {
	var c plan.ComposeFile
	for _, n := range pkgs {
		c.Packages = append(c.Packages, plan.ComposePackage{Pkg: n, Name: n, Local: true, Arch: "amd64", BaseVolumeSz: "1g"})
	}
	return c
}

func onePlan(sys string) *plan.Plan {
	return &plan.Plan{Arch: "amd64", Compose: composeOf("web"), Services: []plan.Service{svc("web", sys)}}
}

const webManifest = "{\n  \"Program\": \"/web\",\n  \"RebootOnExit\": false,\n  \"RunConfig\": {\n    \"Ports\": [],\n    \"UDPPorts\": []\n  }\n}\n"

const webCompose = "packages:\n  - pkg: web\n    name: web\n    local: true\n    arch: amd64\n    base_volume_sz: 1g\n"

func TestWriteExactFiles(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	composePath := filepath.Join(dir, "out", "compose.yaml")
	src1 := filepath.Join(dir, "src1")
	src2 := filepath.Join(dir, "src2")
	put(t, filepath.Join(src1, "web"), "binary", 0o755)
	put(t, filepath.Join(src1, "etc", "conf"), "x=1\n", 0o644)
	if err := os.Symlink("web", filepath.Join(src1, "link")); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(src2, "app"), "app", 0o755)
	p := &plan.Plan{Arch: "amd64", Compose: composeOf("web", "app")}
	p.Services = []plan.Service{svc("web", src1), svc("app", src2)}
	p.Services[0].Manifest = plan.Manifest{Program: "/web", Args: []string{"-p", "80"}, Env: map[string]string{"B": "2", "A": "1"}}
	p.Services[0].Manifest.RunConfig.Ports = []string{"80"}

	written, err := Write(p, Options{OpsHome: home, ComposePath: composePath})
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(home, ".ops", "local_packages", "amd64")
	want := []string{filepath.Join(base, "web"), filepath.Join(base, "app"), composePath}
	if !reflect.DeepEqual(written, want) {
		t.Fatalf("written = %v, want %v", written, want)
	}
	wantManifest := "{\n  \"Program\": \"/web\",\n  \"Args\": [\n    \"-p\",\n    \"80\"\n  ],\n  \"Env\": {\n    \"A\": \"1\",\n    \"B\": \"2\"\n  },\n  \"RebootOnExit\": false,\n  \"RunConfig\": {\n    \"Ports\": [\n      \"80\"\n    ],\n    \"UDPPorts\": []\n  }\n}\n"
	if got := read(t, filepath.Join(base, "web", "package.manifest")); got != wantManifest {
		t.Fatalf("manifest = %q, want %q", got, wantManifest)
	}
	if got := read(t, filepath.Join(base, "app", "package.manifest")); got != "{\n  \"Program\": \"/app\",\n  \"RebootOnExit\": false,\n  \"RunConfig\": {\n    \"Ports\": [],\n    \"UDPPorts\": []\n  }\n}\n" {
		t.Fatalf("app manifest = %q", got)
	}
	fi, err := os.Stat(filepath.Join(base, "web", "package.manifest"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode with an Env = %v, want 0600", fi.Mode().Perm())
	}
	fi, err = os.Stat(filepath.Join(base, "app", "package.manifest"))
	if err != nil {
		t.Fatal(err)
	}
	// Arguments can be interpolated from the host environment: always 0600.
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode without an Env = %v, want 0600", fi.Mode().Perm())
	}
	wantCompose := webCompose + "  - pkg: app\n    name: app\n    local: true\n    arch: amd64\n    base_volume_sz: 1g\n"
	if got := read(t, composePath); got != wantCompose {
		t.Fatalf("compose = %q, want %q", got, wantCompose)
	}
	if got := read(t, filepath.Join(base, "web", "sysroot", "web")); got != "binary" {
		t.Fatalf("sysroot/web = %q", got)
	}
	if got := read(t, filepath.Join(base, "web", "sysroot", "etc", "conf")); got != "x=1\n" {
		t.Fatalf("sysroot/etc/conf = %q", got)
	}
	if l, err := os.Readlink(filepath.Join(base, "web", "sysroot", "link")); err != nil || l != "web" {
		t.Fatalf("link = %q, %v", l, err)
	}
	if got := names(t, filepath.Join(base, "web")); !reflect.DeepEqual(got, []string{"package.manifest", "sysroot"}) {
		t.Fatalf("package folder holds %v", got)
	}
	if got := names(t, base); !reflect.DeepEqual(got, []string{"app", "web"}) {
		t.Fatalf("arch folder holds %v", got)
	}
	if got := names(t, filepath.Dir(composePath)); !reflect.DeepEqual(got, []string{"compose.yaml"}) {
		t.Fatalf("compose folder holds %v", got)
	}
	if exists(src1) || exists(src2) {
		t.Fatal("source sysroot still exists after a move")
	}
}

func TestWriteManifestPassesValidation(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	put(t, filepath.Join(src, "web"), "b", 0o755)
	p := onePlan(src)
	p.Services[0].Manifest = plan.Manifest{
		Program:         "/web",
		Args:            []string{"a"},
		Env:             map[string]string{"K": "V"},
		Version:         "1",
		MapDirs:         map[string]string{"/h": "/g"},
		RebootOnExit:    true,
		DisableArgsCopy: true,
		RunConfig:       plan.RunConfig{Ports: []string{"80"}, UDPPorts: []string{"53"}},
	}
	home := filepath.Join(dir, "home")
	if _, err := Write(p, Options{OpsHome: home, ComposePath: filepath.Join(dir, "c.yaml")}); err != nil {
		t.Fatal(err)
	}
	b := []byte(read(t, filepath.Join(home, ".ops", "local_packages", "amd64", "web", "package.manifest")))
	if err := ValidateManifestJSON(b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\"DisableArgsCopy\": true") {
		t.Fatalf("manifest = %s, want DisableArgsCopy", b)
	}
	if err := ValidateManifestJSON([]byte(`{"Program":"/a","Bogus":1}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
	if err := ValidateManifestJSON([]byte(`{"RunConfig":{"Nope":[]}}`)); err == nil {
		t.Fatal("unknown nested field accepted")
	}
	if err := ValidateManifestJSON([]byte(`{"Program":"/a"} {}`)); err == nil {
		t.Fatal("trailing data accepted")
	}
	if err := ValidateManifestJSON([]byte(`{"Program":"/a"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestManifestBaseVolumeSzIsWrittenAndValid(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	src := filepath.Join(dir, "src")
	put(t, filepath.Join(src, "web"), "b", 0o755)
	s := svc("web", src)
	s.Manifest.BaseVolumeSz = "1g"
	p := &plan.Plan{Arch: "amd64", Compose: composeOf("web"), Services: []plan.Service{s}}
	if _, err := Write(p, Options{OpsHome: home, ComposePath: filepath.Join(dir, "compose.yaml")}); err != nil {
		t.Fatal(err)
	}
	b := []byte(read(t, filepath.Join(home, ".ops", "local_packages", "amd64", "web", "package.manifest")))
	if !strings.Contains(string(b), "\"BaseVolumeSz\": \"1g\"") {
		t.Fatalf("manifest = %s", b)
	}
	if err := ValidateManifestJSON(b); err != nil {
		t.Fatal(err)
	}
	if err := ValidateManifestJSON([]byte(`{"BaseVolumeSz":"1g"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestManifestPassthroughIsWrittenAndValid(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	src := filepath.Join(dir, "src")
	put(t, filepath.Join(src, "web"), "b", 0o755)
	s := svc("web", src)
	s.Manifest.ManifestPassthrough = map[string]any{"cwd": "/app"}
	p := &plan.Plan{Arch: "amd64", Compose: composeOf("web"), Services: []plan.Service{s}}
	if _, err := Write(p, Options{OpsHome: home, ComposePath: filepath.Join(dir, "compose.yaml")}); err != nil {
		t.Fatal(err)
	}
	b := []byte(read(t, filepath.Join(home, ".ops", "local_packages", "amd64", "web", "package.manifest")))
	if !strings.Contains(string(b), "\"ManifestPassthrough\": {\n    \"cwd\": \"/app\"\n  }") {
		t.Fatalf("manifest = %s", b)
	}
	if err := ValidateManifestJSON(b); err != nil {
		t.Fatal(err)
	}
	if err := ValidateManifestJSON([]byte(`{"ManifestPassthrough":{"cwd":"/"}}`)); err != nil {
		t.Fatal(err)
	}
}

func TestWriteFolderExists(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	composePath := filepath.Join(dir, "compose.yaml")
	base := filepath.Join(home, ".ops", "local_packages", "amd64")
	put(t, filepath.Join(base, "app", "marker"), "old", 0o644)
	src1 := filepath.Join(dir, "src1")
	src2 := filepath.Join(dir, "src2")
	put(t, filepath.Join(src1, "web"), "b", 0o755)
	put(t, filepath.Join(src2, "app"), "b", 0o755)
	p := &plan.Plan{Arch: "amd64", Compose: composeOf("web", "app")}
	p.Services = []plan.Service{svc("web", src1), svc("app", src2)}

	written, err := Write(p, Options{OpsHome: home, ComposePath: composePath})
	if err == nil {
		t.Fatal("no error")
	}
	if !strings.Contains(err.Error(), filepath.Join(base, "app")) {
		t.Fatalf("error does not name the folder: %v", err)
	}
	if written != nil {
		t.Fatalf("written = %v", written)
	}
	if got := names(t, base); !reflect.DeepEqual(got, []string{"app"}) {
		t.Fatalf("arch folder holds %v", got)
	}
	if got := read(t, filepath.Join(base, "app", "marker")); got != "old" {
		t.Fatalf("marker = %q", got)
	}
	if exists(composePath) {
		t.Fatal("compose.yaml written")
	}
	if !exists(filepath.Join(src1, "web")) || !exists(filepath.Join(src2, "app")) {
		t.Fatal("a source sysroot was consumed")
	}
}

func TestWriteForceReplaces(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	composePath := filepath.Join(dir, "compose.yaml")
	base := filepath.Join(home, ".ops", "local_packages", "amd64")
	put(t, filepath.Join(base, "web", "marker"), "old", 0o644)
	put(t, composePath, "old\n", 0o644)
	src := filepath.Join(dir, "src")
	put(t, filepath.Join(src, "web"), "new", 0o755)

	if _, err := Write(onePlan(src), Options{OpsHome: home, ComposePath: composePath, Force: true}); err != nil {
		t.Fatal(err)
	}
	if got := names(t, filepath.Join(base, "web")); !reflect.DeepEqual(got, []string{"package.manifest", "sysroot"}) {
		t.Fatalf("package folder holds %v", got)
	}
	if got := names(t, base); !reflect.DeepEqual(got, []string{"web"}) {
		t.Fatalf("arch folder holds %v", got)
	}
	if got := read(t, filepath.Join(base, "web", "package.manifest")); got != webManifest {
		t.Fatalf("manifest = %q", got)
	}
	if got := read(t, composePath); got != webCompose {
		t.Fatalf("compose = %q", got)
	}
	if got := names(t, dir); !reflect.DeepEqual(got, []string{"compose.yaml", "home"}) {
		t.Fatalf("temp folder holds %v", got)
	}
}

func TestWriteComposeExistsBeforeAnyPackage(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	composePath := filepath.Join(dir, "compose.yaml")
	put(t, composePath, "old\n", 0o644)
	src := filepath.Join(dir, "src")
	put(t, filepath.Join(src, "web"), "b", 0o755)

	_, err := Write(onePlan(src), Options{OpsHome: home, ComposePath: composePath})
	if err == nil || !strings.Contains(err.Error(), composePath) {
		t.Fatalf("error = %v", err)
	}
	if exists(home) {
		t.Fatal("a package was written before the compose.yaml check")
	}
	if got := read(t, composePath); got != "old\n" {
		t.Fatalf("compose = %q", got)
	}
	if !exists(filepath.Join(src, "web")) {
		t.Fatal("source sysroot was consumed")
	}
}

func TestWriteRollbackWhenSecondPackageFails(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	composePath := filepath.Join(dir, "compose.yaml")
	src1 := filepath.Join(dir, "src1")
	put(t, filepath.Join(src1, "web"), "b", 0o755)
	p := &plan.Plan{Arch: "amd64", Compose: composeOf("web", "app")}
	p.Services = []plan.Service{svc("web", src1), svc("app", filepath.Join(dir, "missing"))}

	written, err := Write(p, Options{OpsHome: home, ComposePath: composePath})
	if err == nil {
		t.Fatal("no error")
	}
	if written != nil {
		t.Fatalf("written = %v", written)
	}
	if exists(home) {
		t.Fatalf("home left behind: %v", names(t, home))
	}
	if exists(composePath) {
		t.Fatal("compose.yaml left behind")
	}
	if got := read(t, filepath.Join(src1, "web")); got != "b" {
		t.Fatalf("first sysroot not given back: %q", got)
	}
	if got := names(t, dir); !reflect.DeepEqual(got, []string{"src1"}) {
		t.Fatalf("temp folder holds %v", got)
	}
}

func TestWriteRollbackKeepsWhatExisted(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	composePath := filepath.Join(dir, "compose.yaml")
	base := filepath.Join(home, ".ops", "local_packages", "amd64")
	put(t, filepath.Join(base, "web", "marker"), "old", 0o644)
	put(t, filepath.Join(base, "other", "keep"), "keep", 0o644)
	put(t, composePath, "old\n", 0o644)
	src1 := filepath.Join(dir, "src1")
	put(t, filepath.Join(src1, "web"), "b", 0o755)
	p := &plan.Plan{Arch: "amd64", Compose: composeOf("web", "app")}
	p.Services = []plan.Service{svc("web", src1), svc("app", filepath.Join(dir, "missing"))}

	if _, err := Write(p, Options{OpsHome: home, ComposePath: composePath, Force: true}); err == nil {
		t.Fatal("no error")
	}
	if got := names(t, base); !reflect.DeepEqual(got, []string{"other", "web"}) {
		t.Fatalf("arch folder holds %v", got)
	}
	if got := names(t, filepath.Join(base, "web")); !reflect.DeepEqual(got, []string{"marker"}) {
		t.Fatalf("web holds %v", got)
	}
	if got := read(t, filepath.Join(base, "other", "keep")); got != "keep" {
		t.Fatalf("keep = %q", got)
	}
	if got := read(t, composePath); got != "old\n" {
		t.Fatalf("compose = %q", got)
	}
	if got := read(t, filepath.Join(src1, "web")); got != "b" {
		t.Fatalf("sysroot not given back: %q", got)
	}
}

func TestWriteRefusesBadNames(t *testing.T) {
	bad := []string{"", ".", "..", "a/b", "../x", "a..b", "a\\b", "/abs"}
	for _, name := range bad {
		for _, field := range []string{"package", "arch"} {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			src := filepath.Join(dir, "src")
			put(t, filepath.Join(src, "f"), "b", 0o644)
			p := onePlan(src)
			if field == "package" {
				p.Services[0].PackageName = name
			} else {
				p.Arch = name
			}
			written, err := Write(p, Options{OpsHome: home, ComposePath: filepath.Join(dir, "compose.yaml")})
			if err == nil {
				t.Fatalf("%s %q accepted", field, name)
			}
			if written != nil {
				t.Fatalf("%s %q: written = %v", field, name, written)
			}
			if got := names(t, dir); !reflect.DeepEqual(got, []string{"src"}) {
				t.Fatalf("%s %q: temp folder holds %v", field, name, got)
			}
		}
	}
}

func TestWriteRefusesDuplicatePackage(t *testing.T) {
	dir := t.TempDir()
	src1 := filepath.Join(dir, "src1")
	src2 := filepath.Join(dir, "src2")
	put(t, filepath.Join(src1, "f"), "b", 0o644)
	put(t, filepath.Join(src2, "f"), "b", 0o644)
	p := &plan.Plan{Arch: "amd64", Services: []plan.Service{svc("web", src1), svc("web", src2)}}
	if _, err := Write(p, Options{OpsHome: filepath.Join(dir, "home"), ComposePath: filepath.Join(dir, "c.yaml")}); err == nil {
		t.Fatal("duplicate accepted")
	}
	if exists(filepath.Join(dir, "home")) {
		t.Fatal("something written")
	}
}

func TestWriteRelativeComposePath(t *testing.T) {
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)
	src := filepath.Join(dir, "src")
	put(t, filepath.Join(src, "web"), "b", 0o755)
	home := filepath.Join(dir, "home")

	written, err := Write(onePlan(src), Options{OpsHome: home, ComposePath: filepath.Join("sub", "compose.yaml")})
	if err != nil {
		t.Fatal(err)
	}
	if last := written[len(written)-1]; last != filepath.Join("sub", "compose.yaml") {
		t.Fatalf("last written = %q", last)
	}
	if got := read(t, filepath.Join(work, "sub", "compose.yaml")); got != webCompose {
		t.Fatalf("compose = %q", got)
	}

	src2 := filepath.Join(dir, "src2")
	put(t, filepath.Join(src2, "web"), "b", 0o755)
	written, err = Write(onePlan(src2), Options{OpsHome: home, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if last := written[len(written)-1]; last != "compose.yaml" {
		t.Fatalf("default compose path = %q", last)
	}
	if got := read(t, filepath.Join(work, "compose.yaml")); got != webCompose {
		t.Fatalf("default compose = %q", got)
	}
}

func TestWriteOpsHomeFromEnvironment(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "envhome")
	t.Setenv("OPS_HOME", home)
	t.Setenv("HOME", filepath.Join(dir, "otherhome"))
	src := filepath.Join(dir, "src")
	put(t, filepath.Join(src, "web"), "b", 0o755)
	if _, err := Write(onePlan(src), Options{ComposePath: filepath.Join(dir, "c.yaml")}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(home, ".ops", "local_packages", "amd64", "web", "package.manifest")); got != webManifest {
		t.Fatalf("manifest = %q", got)
	}
}

func TestWriteOpsHomeDefaultsToUserHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPS_HOME", "")
	t.Setenv("HOME", dir)
	src := filepath.Join(dir, "src")
	put(t, filepath.Join(src, "web"), "b", 0o755)
	if _, err := Write(onePlan(src), Options{ComposePath: filepath.Join(dir, "c.yaml")}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, ".ops", "local_packages", "amd64", "web", "package.manifest")); got != webManifest {
		t.Fatalf("manifest = %q", got)
	}
}

func TestWriteOpsHomeOptionWinsOverEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPS_HOME", filepath.Join(dir, "env"))
	src := filepath.Join(dir, "src")
	put(t, filepath.Join(src, "web"), "b", 0o755)
	opt := filepath.Join(dir, "opt")
	if _, err := Write(onePlan(src), Options{OpsHome: opt, ComposePath: filepath.Join(dir, "c.yaml")}); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(opt, ".ops", "local_packages", "amd64", "web")) || exists(filepath.Join(dir, "env")) {
		t.Fatal("the package is not under <option>/.ops")
	}
}

func TestWriteOpsHomeEmptyIsAnError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPS_HOME", "")
	t.Setenv("HOME", "")
	src := filepath.Join(dir, "src")
	put(t, filepath.Join(src, "web"), "b", 0o755)
	written, err := Write(onePlan(src), Options{ComposePath: filepath.Join(dir, "c.yaml")})
	if err == nil || written != nil {
		t.Fatalf("written = %v, err = %v, want an error", written, err)
	}
	if !exists(filepath.Join(src, "web")) {
		t.Fatal("the sysroot was consumed")
	}
}

func TestCopyTreeKeepsModesAndLinks(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	outside := filepath.Join(dir, "outside")
	put(t, outside, "secret", 0o644)
	put(t, filepath.Join(src, "bin", "run"), "run", 0o755)
	put(t, filepath.Join(src, "secret"), "s", 0o600)
	put(t, filepath.Join(src, "priv", "f"), "p", 0o640)
	if err := os.Chmod(filepath.Join(src, "priv"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(src, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("bin/run", filepath.Join(src, "rel")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(src, "abs")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("nowhere", filepath.Join(src, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("bin", filepath.Join(src, "dirlink")); err != nil {
		t.Fatal(err)
	}

	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	modes := map[string]os.FileMode{"bin/run": 0o755, "secret": 0o600, "priv/f": 0o640, "priv": 0o700, "empty": 0o755, "bin": 0o755}
	for rel, want := range modes {
		fi, err := os.Lstat(filepath.Join(dst, rel))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Fatalf("%s mode = %v, want %v", rel, fi.Mode().Perm(), want)
		}
	}
	if fi, err := os.Lstat(filepath.Join(dst, "empty")); err != nil || !fi.IsDir() {
		t.Fatalf("empty dir not copied: %v", err)
	}
	links := map[string]string{"rel": "bin/run", "abs": outside, "dangling": "nowhere", "dirlink": "bin"}
	for rel, want := range links {
		fi, err := os.Lstat(filepath.Join(dst, rel))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s is not a link: %v", rel, fi.Mode())
		}
		got, err := os.Readlink(filepath.Join(dst, rel))
		if err != nil || got != want {
			t.Fatalf("%s -> %q, %v; want %q", rel, got, err, want)
		}
	}
	if got := read(t, filepath.Join(dst, "bin", "run")); got != "run" {
		t.Fatalf("content = %q", got)
	}
	if !exists(filepath.Join(src, "bin", "run")) {
		t.Fatal("copyTree removed the source")
	}
}

func TestCopyTreeRefusesNonDirectory(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, "f"), "x", 0o644)
	if err := copyTree(filepath.Join(dir, "f"), filepath.Join(dir, "dst")); err == nil {
		t.Fatal("file accepted")
	}
	if exists(filepath.Join(dir, "dst")) {
		t.Fatal("dst created")
	}
}

func failRename(t *testing.T, errno syscall.Errno) {
	t.Helper()
	old := rename
	rename = func(a, b string) error {
		return &os.LinkError{Op: "rename", Old: a, New: b, Err: errno}
	}
	t.Cleanup(func() { rename = old })
}

func TestMoveTreeFallsBackToCopyOnCrossDevice(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	put(t, filepath.Join(src, "bin", "run"), "run", 0o755)
	if err := os.Symlink("bin/run", filepath.Join(src, "l")); err != nil {
		t.Fatal(err)
	}
	failRename(t, syscall.EXDEV)

	copied, err := moveTree(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if !copied {
		t.Fatal("copied = false")
	}
	if got := read(t, filepath.Join(dst, "bin", "run")); got != "run" {
		t.Fatalf("content = %q", got)
	}
	if l, err := os.Readlink(filepath.Join(dst, "l")); err != nil || l != "bin/run" {
		t.Fatalf("link = %q, %v", l, err)
	}
	if !exists(filepath.Join(src, "bin", "run")) {
		t.Fatal("moveTree removed the source; the caller does that")
	}
}

func TestMoveTreeOtherErrorsDoNotCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	put(t, filepath.Join(src, "f"), "x", 0o644)
	failRename(t, syscall.EACCES)

	if _, err := moveTree(src, dst); err == nil {
		t.Fatal("no error")
	}
	if exists(dst) {
		t.Fatal("copied after a non cross-device error")
	}
}

func TestMoveTreeRenames(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	put(t, filepath.Join(src, "f"), "x", 0o644)
	copied, err := moveTree(src, dst)
	if err != nil || copied {
		t.Fatalf("copied = %v, err = %v", copied, err)
	}
	if exists(src) || read(t, filepath.Join(dst, "f")) != "x" {
		t.Fatal("not moved")
	}
}

func TestWriteCrossDeviceRemovesSourceAfterSuccess(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	src := filepath.Join(dir, "src")
	put(t, filepath.Join(src, "web"), "b", 0o755)
	failRename(t, syscall.EXDEV)

	if _, err := Write(onePlan(src), Options{OpsHome: home, ComposePath: filepath.Join(dir, "c.yaml")}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(home, ".ops", "local_packages", "amd64", "web", "sysroot", "web")); got != "b" {
		t.Fatalf("content = %q", got)
	}
	if exists(src) {
		t.Fatal("source sysroot not removed after the copy")
	}
}

func TestWriteCrossDeviceKeepsSourceOnFailure(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	src := filepath.Join(dir, "src")
	put(t, filepath.Join(src, "web"), "b", 0o755)
	p := &plan.Plan{Arch: "amd64", Compose: composeOf("web", "app")}
	p.Services = []plan.Service{svc("web", src), svc("app", filepath.Join(dir, "missing"))}
	failRename(t, syscall.EXDEV)

	if _, err := Write(p, Options{OpsHome: home, ComposePath: filepath.Join(dir, "c.yaml")}); err == nil {
		t.Fatal("no error")
	}
	if got := read(t, filepath.Join(src, "web")); got != "b" {
		t.Fatalf("source = %q", got)
	}
	if exists(home) {
		t.Fatal("home left behind")
	}
}

// countRename wraps rename and counts the calls.
func countRename(t *testing.T) *int {
	t.Helper()
	old := rename
	n := 0
	rename = func(a, b string) error {
		n++
		return old(a, b)
	}
	t.Cleanup(func() { rename = old })
	return &n
}

func TestWritePreCheckComposeExistsTouchesNothing(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	base := filepath.Join(home, ".ops", "local_packages", "amd64")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(dir, "compose.yaml")
	put(t, composePath, "old\n", 0o644)
	src := filepath.Join(dir, "src")
	put(t, filepath.Join(src, "web"), "b", 0o755)
	renames := countRename(t)

	_, err := Write(onePlan(src), Options{OpsHome: home, ComposePath: composePath})
	if err == nil || !strings.Contains(err.Error(), composePath+" exists; use force to replace it") {
		t.Fatalf("error = %v", err)
	}
	if *renames != 0 {
		t.Errorf("rename was called %d times before the refusal, want 0", *renames)
	}
	if got := names(t, base); len(got) != 0 {
		t.Errorf("arch folder holds %v, want nothing (no package folder, no temporary folder)", got)
	}
	if got := names(t, dir); !reflect.DeepEqual(got, []string{"compose.yaml", "home", "src"}) {
		t.Errorf("folder holds %v", got)
	}
	if got := names(t, src); !reflect.DeepEqual(got, []string{"web"}) {
		t.Errorf("source sysroot holds %v", got)
	}
}

func TestWritePreCheckPackageFolderExistsTouchesNothing(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	base := filepath.Join(home, ".ops", "local_packages", "amd64")
	put(t, filepath.Join(base, "app", "marker"), "old", 0o644)
	src1 := filepath.Join(dir, "src1")
	src2 := filepath.Join(dir, "src2")
	put(t, filepath.Join(src1, "web"), "b", 0o755)
	put(t, filepath.Join(src2, "app"), "b", 0o755)
	p := &plan.Plan{Arch: "amd64", Compose: composeOf("web", "app")}
	p.Services = []plan.Service{svc("web", src1), svc("app", src2)}
	renames := countRename(t)

	_, err := Write(p, Options{OpsHome: home, ComposePath: filepath.Join(dir, "compose.yaml")})
	if err == nil || !strings.Contains(err.Error(), "package folder "+filepath.Join(base, "app")+" exists; use force to replace it") {
		t.Fatalf("error = %v", err)
	}
	if *renames != 0 {
		t.Errorf("rename was called %d times before the refusal, want 0", *renames)
	}
	if got := names(t, base); !reflect.DeepEqual(got, []string{"app"}) {
		t.Errorf("arch folder holds %v, want only app", got)
	}
	if got := names(t, dir); !reflect.DeepEqual(got, []string{"home", "src1", "src2"}) {
		t.Errorf("folder holds %v", got)
	}
}

func TestWriteDuplicatePackageNameMessage(t *testing.T) {
	dir := t.TempDir()
	src1 := filepath.Join(dir, "src1")
	src2 := filepath.Join(dir, "src2")
	put(t, filepath.Join(src1, "f"), "b", 0o644)
	put(t, filepath.Join(src2, "f"), "b", 0o644)
	p := &plan.Plan{Arch: "amd64", Services: []plan.Service{svc("web", src1), svc("web", src2)}}
	_, err := Write(p, Options{OpsHome: filepath.Join(dir, "home"), ComposePath: filepath.Join(dir, "c.yaml")})
	if want := `output: package name "web" used twice`; err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestWriteSysrootErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "afile")
	put(t, file, "x", 0o644)
	tests := []struct {
		name, sysroot, want string
	}{
		{"empty", "", `output: service "web" has no sysroot`},
		{"not a directory", file, "output: sysroot " + file + ` of "web" is not a directory`},
		{"missing", filepath.Join(dir, "missing"), `output: sysroot of "web": `},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "home")
			composePath := filepath.Join(t.TempDir(), "compose.yaml")
			_, err := Write(onePlan(tt.sysroot), Options{OpsHome: home, ComposePath: composePath})
			if err == nil || !strings.HasPrefix(err.Error(), tt.want) {
				t.Fatalf("error = %v, want one that starts with %q", err, tt.want)
			}
			if exists(home) || exists(composePath) {
				t.Error("something was left behind")
			}
			if got := read(t, file); got != "x" {
				t.Errorf("the file was changed: %q", got)
			}
		})
	}
}

func TestWriteModes(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	composePath := filepath.Join(dir, "compose.yaml")
	src := filepath.Join(dir, "src")
	put(t, filepath.Join(src, "web"), "b", 0o755)
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)

	if _, err := Write(onePlan(src), Options{OpsHome: home, ComposePath: composePath}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(home, ".ops", "local_packages", "amd64", "web"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("package folder mode = %v, want 0755", fi.Mode().Perm())
	}
	fi, err = os.Stat(composePath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("compose.yaml mode = %v, want 0644", fi.Mode().Perm())
	}
}

func TestMoveTreeFailedCopyLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	put(t, filepath.Join(src, "a"), "a", 0o644)
	put(t, filepath.Join(src, "sub", "b"), "b", 0o644)
	if err := syscall.Mkfifo(filepath.Join(src, "z-fifo"), 0o644); err != nil {
		t.Skipf("no fifo: %v", err)
	}
	failRename(t, syscall.EXDEV)

	copied, err := moveTree(src, dst)
	if err == nil || !strings.Contains(err.Error(), "unsupported file type") {
		t.Fatalf("copied = %v, error = %v, want an unsupported file type error", copied, err)
	}
	if copied {
		t.Error("copied = true after a failure")
	}
	if exists(dst) {
		t.Errorf("the half copied folder is left: %v", names(t, dst))
	}
	if got := read(t, filepath.Join(src, "a")); got != "a" {
		t.Errorf("source changed: %q", got)
	}
}

func TestCopyTreeKeepsSpecialBits(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	put(t, filepath.Join(src, "suid"), "x", 0o755)
	put(t, filepath.Join(src, "sgid"), "x", 0o755)
	put(t, filepath.Join(src, "shared", "f"), "x", 0o644)
	put(t, filepath.Join(src, "tmp", "f"), "x", 0o644)
	chmods := map[string]os.FileMode{
		"suid":   0o755 | os.ModeSetuid,
		"sgid":   0o755 | os.ModeSetgid,
		"shared": 0o775 | os.ModeSetgid,
		"tmp":    0o777 | os.ModeSticky,
	}
	for rel, m := range chmods {
		if err := os.Chmod(filepath.Join(src, rel), m); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	for rel, want := range chmods {
		fi, err := os.Lstat(filepath.Join(dst, rel))
		if err != nil {
			t.Fatal(err)
		}
		const bits = os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky
		if got := fi.Mode() & bits; got != want {
			t.Errorf("%s mode = %v, want %v", rel, got, want)
		}
	}
}

func TestComposeFileWritesDNSSearchOnlyWhenSet(t *testing.T) {
	var with, without plan.ComposeFile
	with.Packages = []plan.ComposePackage{{Pkg: "web", Name: "web", Local: true, Arch: "amd64"}}
	with.DNSSearch = []string{"service"}
	without.Packages = with.Packages
	a, err := yaml.Marshal(with)
	if err != nil {
		t.Fatal(err)
	}
	b, err := yaml.Marshal(without)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(a), "dns_search:\n    - service") {
		t.Errorf("with: %q", a)
	}
	if strings.Contains(string(b), "dns_search") {
		t.Errorf("without: %q", b)
	}
}

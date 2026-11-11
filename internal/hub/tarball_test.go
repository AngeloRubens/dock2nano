package hub

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const nginxManifest = `{
   "Program":"nginx_1.28.3/nginx",
   "Args" : ["nginx", "-q"],
   "Env": {"A": "1"},
   "RunConfig": {"Ports": ["8083"]},
   "ManifestPassthrough": {"cwd": "/"},
   "Version":"1.28.3"
}`

func nginxTarball(t *testing.T) []byte {
	return tarball(t, []entry{
		dir("nginx_1.28.3/"),
		file("nginx_1.28.3/nginx", "ELF"),
		file("nginx_1.28.3/package.manifest", nginxManifest),
		file("nginx_1.28.3/config.json", `{"Dirs":["lib"]}`),
		dir("nginx_1.28.3/sysroot/"),
		dir("nginx_1.28.3/sysroot/etc/"),
		dir("nginx_1.28.3/sysroot/etc/nginx/"),
		file("nginx_1.28.3/sysroot/etc/nginx/nginx.conf", "worker_processes 1;\n"),
		file("nginx_1.28.3/sysroot/etc/other.conf", "other"),
	})
}

func TestFetchRecipe(t *testing.T) {
	f := newFakeHub(t)
	p := f.add(pkg("eyberg", "nginx", "1.28.3", "x86_64"), nginxTarball(t))
	c := f.client(t)
	rec, err := c.FetchRecipe(context.Background(), p, []string{"/etc/nginx/nginx.conf", "/etc/missing"})
	if err != nil {
		t.Fatal(err)
	}
	m := rec.Manifest
	if m.Program != "nginx_1.28.3/nginx" || !reflect.DeepEqual(m.Args, []string{"nginx", "-q"}) || m.Env["A"] != "1" || !reflect.DeepEqual(m.RunConfig.Ports, []string{"8083"}) || m.ManifestPassthrough["cwd"] != "/" {
		t.Errorf("manifest: %+v", m)
	}
	if string(rec.Config) != `{"Dirs":["lib"]}` {
		t.Errorf("config.json: %q", rec.Config)
	}
	if len(rec.Files) != 1 || string(rec.Files["/etc/nginx/nginx.conf"]) != "worker_processes 1;\n" {
		t.Errorf("files: %v", rec.Files)
	}
	if rec.Package != p {
		t.Errorf("package: %+v", rec.Package)
	}
	for _, m := range f.methods {
		if m != "GET" {
			t.Errorf("method %s", m)
		}
	}
	left, _ := os.ReadDir(c.TmpDir)
	if len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestFetchRecipeRefusesADigestThatDoesNotMatch(t *testing.T) {
	f := newFakeHub(t)
	p := f.add(pkg("eyberg", "nginx", "1.28.3", "x86_64"), nginxTarball(t))
	f.blobs["eyberg/nginx/1.28.3.tar.gz"] = tarball(t, []entry{dir("x/"), file("x/package.manifest", `{"Program":"evil"}`)})
	c := f.client(t)
	_, err := c.FetchRecipe(context.Background(), p, nil)
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("a tarball with another digest was read: %v", err)
	}
	left, _ := os.ReadDir(c.TmpDir)
	if len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestFetchRefusesAnEmptyTarball(t *testing.T) {
	f := newFakeHub(t)
	// listed with the digest of an empty file: refused before any request
	broken := pkg("eyberg", "nginx", "1.0.0", "x86_64")
	broken.SHA256 = emptySHA256
	f.manifest = append(f.manifest, broken)
	f.blobs["eyberg/nginx/1.0.0.tar.gz"] = nil
	if _, err := f.client(t).FetchRecipe(context.Background(), broken, nil); err == nil {
		t.Fatal("a package with the digest of an empty file was fetched")
	}
	if n := f.requests(); n != 0 {
		t.Errorf("%d requests for a package that cannot be used", n)
	}
	// listed with a real digest, but the body is empty
	liar := pkg("eyberg", "nginx", "1.1.0", "x86_64")
	f.manifest = append(f.manifest, liar)
	f.blobs["eyberg/nginx/1.1.0.tar.gz"] = nil
	_, err := f.client(t).FetchRecipe(context.Background(), liar, nil)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("an empty body: %v", err)
	}
}

func TestFetchRecipeNeedsAManifestAndGzip(t *testing.T) {
	f := newFakeHub(t)
	none := f.add(pkg("eyberg", "a", "1.0", "x86_64"), tarball(t, []entry{dir("a_1.0/"), file("a_1.0/readme", "x")}))
	if _, err := f.client(t).FetchRecipe(context.Background(), none, nil); err == nil || !strings.Contains(err.Error(), "package.manifest") {
		t.Errorf("no manifest: %v", err)
	}
	notgz := f.add(pkg("eyberg", "b", "1.0", "x86_64"), []byte("plain text, not a tarball"))
	if _, err := f.client(t).FetchRecipe(context.Background(), notgz, nil); err == nil {
		t.Error("a file that is not gzip was read")
	}
	bad := f.add(pkg("eyberg", "c", "1.0", "x86_64"), tarball(t, []entry{dir("c_1.0/"), file("c_1.0/package.manifest", "{")}))
	if _, err := f.client(t).FetchRecipe(context.Background(), bad, nil); err == nil || !strings.Contains(err.Error(), "package.manifest") {
		t.Errorf("a manifest that is not JSON: %v", err)
	}
}

func TestFetchRecipeLeavesOutAHugeConfigFile(t *testing.T) {
	f := newFakeHub(t)
	huge := strings.Repeat("x", maxSmallFile+1)
	p := f.add(pkg("eyberg", "a", "1.0", "x86_64"), tarball(t, []entry{dir("a_1.0/"), file("a_1.0/package.manifest", `{"Program":"a"}`), file("a_1.0/sysroot/etc/a.conf", huge)}))
	rec, err := f.client(t).FetchRecipe(context.Background(), p, []string{"/etc/a.conf"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Files) != 0 {
		t.Errorf("a file of %d bytes was kept", len(huge))
	}
}

func wholeTarball(t *testing.T, extra ...entry) []byte {
	entries := []entry{
		dir("pg_16.2/"),
		file("pg_16.2/postgres", "ELF"),
		file("pg_16.2/package.manifest", `{"Program":"pg_16.2/postgres","Args":["./postgres","-D","db"],"Env":{"LANG":"C"}}`),
		file("pg_16.2/README.md", "readme"),
		dir("pg_16.2/sysroot/"),
		dir("pg_16.2/sysroot/usr/"),
		file("pg_16.2/sysroot/usr/lib.so", "lib"),
		symlink("pg_16.2/sysroot/lib64", "usr"),
	}
	return tarball(t, append(entries, extra...))
}

func TestFetchWhole(t *testing.T) {
	f := newFakeHub(t)
	p := f.add(pkg("francescolavra", "postgres", "16.2", "x86_64"), wholeTarball(t, entry{name: "pg_16.2/sysroot/hard", typ: tar.TypeLink, link: "pg_16.2/sysroot/usr/lib.so"}, entry{name: "pg_16.2/sysroot/dev0", typ: tar.TypeChar}))
	dest := t.TempDir()
	rec, err := f.client(t).FetchWhole(context.Background(), p, dest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rec.Manifest.Args, []string{"./postgres", "-D", "db"}) {
		t.Errorf("args: %v", rec.Manifest.Args)
	}
	for path, want := range map[string]string{"postgres": "ELF", "usr/lib.so": "lib", "hard": "lib"} {
		got, err := os.ReadFile(filepath.Join(dest, path))
		if err != nil || string(got) != want {
			t.Errorf("%s: %q %v", path, got, err)
		}
	}
	if target, err := os.Readlink(filepath.Join(dest, "lib64")); err != nil || target != "usr" {
		t.Errorf("symlink: %q %v", target, err)
	}
	for _, name := range []string{"README.md", "package.manifest", "pg_16.2"} {
		if _, err := os.Lstat(filepath.Join(dest, name)); err == nil {
			t.Errorf("%s was written", name)
		}
	}
	if rec.Skipped != 1 {
		t.Errorf("skipped %d entries, want the device only", rec.Skipped)
	}
}

func TestFetchWholeWritesNothingOutsideTheFolder(t *testing.T) {
	f := newFakeHub(t)
	p := f.add(pkg("a", "evil", "1.0", "x86_64"), wholeTarball(t, file("pg_16.2/sysroot/../../escaped", "x"), file("../escaped2", "x"), file("/abs/escaped3", "x")))
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client(t).FetchWhole(context.Background(), p, dest); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"escaped", "escaped2", "abs"} {
		if _, err := os.Lstat(filepath.Join(parent, name)); err == nil {
			t.Errorf("%s was written outside the folder", name)
		}
	}
}

func TestFetchWholeDoesNotWriteThroughASymbolicLink(t *testing.T) {
	f := newFakeHub(t)
	outside := t.TempDir()
	p := f.add(pkg("a", "evil", "1.0", "x86_64"), wholeTarball(t, symlink("pg_16.2/sysroot/escape", outside), file("pg_16.2/sysroot/escape/pwned", "x")))
	dest := t.TempDir()
	if _, err := f.client(t).FetchWhole(context.Background(), p, dest); err == nil {
		t.Error("a path through a symbolic link was accepted")
	}
	if _, err := os.Lstat(filepath.Join(outside, "pwned")); err == nil {
		t.Error("a file was written through a symbolic link")
	}
}

func TestSafeJoin(t *testing.T) {
	dest := t.TempDir()
	if got, err := safeJoin(dest, "a/b/c"); err != nil || got != filepath.Join(dest, "a", "b", "c") {
		t.Errorf("%q %v", got, err)
	}
	if got, err := safeJoin(dest, "/abs"); err != nil || got != filepath.Join(dest, "abs") {
		t.Errorf("an absolute path is read under dest: %q %v", got, err)
	}
	if _, err := safeJoin(dest, "a/../../b"); err == nil {
		t.Error("..")
	}
}

func TestFetchWholeCopiesAHardLinkAndLeavesOutOneThatPointsAtNothing(t *testing.T) {
	f := newFakeHub(t)
	p := f.add(pkg("a", "x", "1.0", "x86_64"), wholeTarball(t,
		entry{name: "pg_16.2/sysroot/ok", typ: tar.TypeLink, link: "pg_16.2/sysroot/usr/lib.so"},
		entry{name: "pg_16.2/sysroot/nowhere", typ: tar.TypeLink, link: "pg_16.2/sysroot/missing"},
		entry{name: "pg_16.2/sysroot/outside", typ: tar.TypeLink, link: "pg_16.2/README.md"}))
	dest := t.TempDir()
	rec, err := f.client(t).FetchWhole(context.Background(), p, dest)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "ok")); err != nil || string(b) != "lib" {
		t.Errorf("%q %v", b, err)
	}
	if rec.Skipped != 2 {
		t.Errorf("skipped %d, want 2", rec.Skipped)
	}
}

func TestFetchWholeKeepsTheExecuteBitsAndMakesTheFilesWritable(t *testing.T) {
	f := newFakeHub(t)
	p := f.add(pkg("a", "x", "1.0", "x86_64"), wholeTarball(t, entry{name: "pg_16.2/postgres", body: "ELF2", mode: 0o4555, typ: tar.TypeReg}))
	dest := t.TempDir()
	if _, err := f.client(t).FetchWhole(context.Background(), p, dest); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(filepath.Join(dest, "postgres"))
	if err != nil || fi.Mode() != 0o755 {
		t.Errorf("%v %v", fi, err)
	}
}

func TestFetchWholeNeedsTheProgram(t *testing.T) {
	f := newFakeHub(t)
	p := f.add(pkg("a", "x", "1.0", "x86_64"), tarball(t, []entry{
		dir("pg_16.2/"),
		file("pg_16.2/package.manifest", `{"Program":"pg_16.2/postgres"}`),
		file("pg_16.2/sysroot/etc/x", "x"),
	}))
	if _, err := f.client(t).FetchWhole(context.Background(), p, t.TempDir()); err == nil || !strings.Contains(err.Error(), "program") {
		t.Errorf("a package without its program: %v", err)
	}
	q := f.add(pkg("a", "y", "1.0", "x86_64"), tarball(t, []entry{
		dir("pg_16.2/"),
		symlink("pg_16.2/postgres", "/bin/sh"),
		file("pg_16.2/package.manifest", `{"Program":"pg_16.2/postgres"}`),
	}))
	if _, err := f.client(t).FetchWhole(context.Background(), q, t.TempDir()); err == nil {
		t.Error("a program that is a link was accepted")
	}
}

func TestEntriesThatAreNotThePackageAreIgnored(t *testing.T) {
	f := newFakeHub(t)
	p := f.add(pkg("a", "x", "1.0", "x86_64"), tarball(t, []entry{
		{name: "g/pax_global_header", typ: tar.TypeXGlobalHeader, pax: map[string]string{"comment": "abc"}},
		file("stray", "x"),
		dir("pg_16.2/"),
		file("pg_16.2/package.manifest", `{"Program":"pg_16.2/postgres"}`),
	}))
	rec, err := f.client(t).FetchRecipe(context.Background(), p, nil)
	if err != nil || rec.Manifest.Program != "pg_16.2/postgres" {
		t.Errorf("%+v %v", rec, err)
	}
}

func TestTheFolderOfAPackageCanBeNamedByItsFirstFile(t *testing.T) {
	f := newFakeHub(t)
	p := f.add(pkg("a", "x", "1.0", "x86_64"), tarball(t, []entry{file("top/package.manifest", `{"Program":"top/p"}`)}))
	if rec, err := f.client(t).FetchRecipe(context.Background(), p, nil); err != nil || rec.Manifest.Program != "top/p" {
		t.Errorf("%+v %v", rec, err)
	}
}

func TestSysrootFilesThatCannotBeReadAreNamed(t *testing.T) {
	f := newFakeHub(t)
	p := f.add(pkg("a", "x", "1.0", "x86_64"), tarball(t, []entry{
		dir("n/"),
		file("n/package.manifest", `{"Program":"n/n"}`),
		symlink("n/sysroot/etc/nginx/nginx.conf", "/elsewhere"),
	}))
	rec, err := f.client(t).FetchRecipe(context.Background(), p, []string{"/etc/nginx/nginx.conf"})
	if err != nil || len(rec.Files) != 0 || !reflect.DeepEqual(rec.NotRead, []string{"/etc/nginx/nginx.conf"}) {
		t.Errorf("%+v %v", rec, err)
	}
}

// lowered sets a limit for the length of a test.
func lowered[T int | int64](t *testing.T, v *T, to T) {
	old := *v
	*v = to
	t.Cleanup(func() { *v = old })
}

func TestATarballWithTooManyEntriesIsRefused(t *testing.T) {
	f := newFakeHub(t)
	p := f.add(pkg("a", "x", "1.0", "x86_64"), wholeTarball(t))
	lowered(t, &maxEntries, 3)
	if _, err := f.client(t).FetchRecipe(context.Background(), p, nil); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Errorf("%v", err)
	}
}

func TestATarballThatExpandsTooMuchIsRefused(t *testing.T) {
	f := newFakeHub(t)
	p := f.add(pkg("a", "x", "1.0", "x86_64"), tarball(t, []entry{
		dir("n/"),
		file("n/package.manifest", `{"Program":"n/n"}`),
		file("n/sysroot/big", strings.Repeat("0", 100000)),
	}))
	lowered(t, &maxDecompressed, 5000)
	if _, err := f.client(t).FetchRecipe(context.Background(), p, nil); err == nil || !strings.Contains(err.Error(), "expands") {
		t.Errorf("%v", err)
	}
}

func TestATarballThatIsTooLargeIsRefused(t *testing.T) {
	f := newFakeHub(t)
	p := f.add(pkg("a", "x", "1.0", "x86_64"), wholeTarball(t))
	lowered(t, &maxTarball, 50)
	if _, err := f.client(t).FetchRecipe(context.Background(), p, nil); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Errorf("%v", err)
	}
}

func TestAPackageThatWritesTooMuchIsRefused(t *testing.T) {
	f := newFakeHub(t)
	p := f.add(pkg("a", "x", "1.0", "x86_64"), wholeTarball(t))
	lowered(t, &maxExtracted, 4)
	if _, err := f.client(t).FetchWhole(context.Background(), p, t.TempDir()); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("%v", err)
	}
}

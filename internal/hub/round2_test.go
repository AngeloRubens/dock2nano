package hub

import (
	"archive/tar"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStaleCopyIsReportedWithItsDateAndTheCause(t *testing.T) {
	f := newFakeHub(t)
	f.manifest = []Package{pkg("eyberg", "nginx", "1.28.3", "x86_64")}
	cache := t.TempDir()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	mk := func() *Client {
		c := f.client(t)
		c.CacheDir = cache
		c.Now = func() time.Time { return now }
		return c
	}
	first := mk()
	if _, err := first.Manifest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if when, cause := first.Stale(); !when.IsZero() || cause != nil {
		t.Errorf("a fresh list is not stale: %v %v", when, cause)
	}
	old := now.Add(-72 * time.Hour)
	if err := os.Chtimes(first.cachePath(), old, old); err != nil {
		t.Fatal(err)
	}
	f.status = http.StatusBadGateway
	c := mk()
	if _, err := c.Manifest(context.Background()); err != nil {
		t.Fatal(err)
	}
	when, cause := c.Stale()
	if !when.Equal(old) || cause == nil || !strings.Contains(cause.Error(), "502") {
		t.Errorf("%v %v", when, cause)
	}
}

func TestACopyDatedInTheFutureIsNotFresh(t *testing.T) {
	f := newFakeHub(t)
	f.manifest = []Package{pkg("eyberg", "nginx", "1.28.3", "x86_64")}
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	c := f.client(t)
	c.CacheDir = t.TempDir()
	c.Now = func() time.Time { return now }
	if err := os.WriteFile(c.cachePath(), []byte(`{"Version":2,"Packages":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	future := now.Add(24 * time.Hour)
	if err := os.Chtimes(c.cachePath(), future, future); err != nil {
		t.Fatal(err)
	}
	pkgs, err := c.Manifest(context.Background())
	if err != nil || len(pkgs) != 1 {
		t.Errorf("%v %v", pkgs, err)
	}
	if f.requests() != 1 {
		t.Errorf("%d requests", f.requests())
	}
}

func TestFetchWholeCountsTheBytesOfHardLinks(t *testing.T) {
	f := newFakeHub(t)
	p := f.add(pkg("a", "x", "1.0", "x86_64"), tarball(t, []entry{
		dir("n/"),
		file("n/n", "E"),
		file("n/package.manifest", `{"Program":"n/n"}`),
		file("n/sysroot/a", "abc"),
		{name: "n/sysroot/l1", typ: tar.TypeLink, link: "n/sysroot/a"},
		{name: "n/sysroot/l2", typ: tar.TypeLink, link: "n/sysroot/a"},
		{name: "n/sysroot/l3", typ: tar.TypeLink, link: "n/sysroot/a"},
	}))
	lowered(t, &maxExtracted, 8)
	if _, err := f.client(t).FetchWhole(context.Background(), p, t.TempDir()); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("%v", err)
	}
}

func TestAHardLinkToAnotherFolderIsLeftOut(t *testing.T) {
	f := newFakeHub(t)
	p := f.add(pkg("a", "x", "1.0", "x86_64"), tarball(t, []entry{
		dir("n/"),
		file("n/n", "E"),
		file("n/package.manifest", `{"Program":"n/n"}`),
		file("n/sysroot/a", "abc"),
		{name: "n/sysroot/l1", typ: tar.TypeLink, link: "other/sysroot/a"},
	}))
	dest := t.TempDir()
	rec, err := f.client(t).FetchWhole(context.Background(), p, dest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "l1")); err == nil || rec.Skipped != 1 {
		t.Errorf("the link was written, or not counted: %d %v", rec.Skipped, err)
	}
}

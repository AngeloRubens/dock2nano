package hub

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManifestIsReadOnceAndOnlyWithGET(t *testing.T) {
	f := newFakeHub(t)
	f.manifest = []Package{pkg("eyberg", "nginx", "1.28.3", "x86_64"), pkg("eyberg", "nginx", "1.18.0", "x86_64")}
	c := f.client(t)
	for i := 0; i < 3; i++ {
		pkgs, err := c.Manifest(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(pkgs) != 2 || pkgs[0].Name != "nginx" || pkgs[0].Version != "1.28.3" {
			t.Fatalf("packages: %+v", pkgs)
		}
	}
	if n := f.requests(); n != 1 {
		t.Errorf("%d requests for three reads", n)
	}
	for _, m := range f.methods {
		if m != http.MethodGet {
			t.Errorf("the hub received %s", m)
		}
	}
}

func TestManifestKeptOnDisk(t *testing.T) {
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
	if _, err := mk().Manifest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(cache, "hub-manifest-v2.json"), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := mk().Manifest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.requests(); n != 1 {
		t.Errorf("a recent copy on disk was not used: %d requests", n)
	}
	now = now.Add(25 * time.Hour)
	if _, err := mk().Manifest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.requests(); n != 2 {
		t.Errorf("an old copy on disk was used: %d requests", n)
	}
}

func TestManifestCorruptCopyOnDiskIsReadAgain(t *testing.T) {
	f := newFakeHub(t)
	f.manifest = []Package{pkg("eyberg", "nginx", "1.28.3", "x86_64")}
	cache := t.TempDir()
	if err := os.WriteFile(filepath.Join(cache, "hub-manifest-v2.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := f.client(t)
	c.CacheDir = cache
	pkgs, err := c.Manifest(context.Background())
	if err != nil || len(pkgs) != 1 {
		t.Fatalf("%v %v", pkgs, err)
	}
	if f.requests() != 1 {
		t.Errorf("%d requests", f.requests())
	}
}

func TestManifestErrors(t *testing.T) {
	f := newFakeHub(t)
	f.status = http.StatusInternalServerError
	if _, err := f.client(t).Manifest(context.Background()); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("a failing hub: %v", err)
	}
	f.status = 0
	f.raw = "{not json"
	if _, err := f.client(t).Manifest(context.Background()); err == nil {
		t.Error("a list that is not JSON was accepted")
	}
	f.raw = `{"Version":3,"Packages":[]}`
	if _, err := f.client(t).Manifest(context.Background()); err == nil || !strings.Contains(err.Error(), "version 3") {
		t.Errorf("another version of the list: %v", err)
	}
}

// stubDoer answers every request from memory.
type stubDoer struct {
	got []*http.Request
}

func (s *stubDoer) Do(req *http.Request) (*http.Response, error) {
	s.got = append(s.got, req)
	body := `{"Version":2,"Packages":[{"namespace":"eyberg","name":"nginx","version":"1.28.3","arch":"x86_64","sha256":"` + goodSum + `"}]}`
	return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestTheHTTPClientIsInjectable(t *testing.T) {
	d := &stubDoer{}
	c := &Client{BaseURL: "https://hub.invalid", HTTP: d}
	pkgs, err := c.Manifest(context.Background())
	if err != nil || len(pkgs) != 1 || pkgs[0].Ref() != "eyberg/nginx" {
		t.Fatalf("%v %v", pkgs, err)
	}
	if len(d.got) != 1 || d.got[0].Method != http.MethodGet || d.got[0].URL.String() != "https://hub.invalid/v2/manifest.json" {
		t.Errorf("requests: %v", d.got)
	}
}

func TestPackageURL(t *testing.T) {
	c := &Client{BaseURL: "https://hub.invalid"}
	got, err := c.packageURL(pkg("eyberg", "nginx", "v1.2", "x86_64"))
	if err != nil || got != "https://hub.invalid/v2/packages/eyberg/nginx/v1.2.tar.gz" {
		t.Errorf("%q %v", got, err)
	}
	bad := pkg("eyberg", "../x", "1.0", "x86_64")
	if _, err := c.packageURL(bad); err == nil {
		t.Error("a name with a slash makes an address")
	}
}

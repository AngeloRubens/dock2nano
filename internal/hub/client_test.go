package hub

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
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
	// the client sends requests from one place, get, which always says GET: this
	// documents the rule, and cannot fail by reverting something
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
	if err := os.Chtimes(mk().cachePath(), now, now); err != nil {
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
	c := f.client(t)
	c.CacheDir = cache
	if err := os.WriteFile(c.cachePath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	pkgs, err := c.Manifest(context.Background())
	if err != nil || len(pkgs) != 1 {
		t.Fatalf("%v %v", pkgs, err)
	}
	if f.requests() != 1 {
		t.Errorf("%d requests", f.requests())
	}
}

func TestManifestFailureIsRemembered(t *testing.T) {
	f := newFakeHub(t)
	f.status = http.StatusInternalServerError
	c := f.client(t)
	for i := 0; i < 3; i++ {
		if _, err := c.Manifest(context.Background()); err == nil {
			t.Fatal("no error")
		}
	}
	if n := f.requests(); n != 1 {
		t.Errorf("%d requests for three reads of a hub that fails", n)
	}
}

func TestManifestCancelledIsNotRemembered(t *testing.T) {
	f := newFakeHub(t)
	f.manifest = []Package{pkg("eyberg", "nginx", "1.28.3", "x86_64")}
	c := f.client(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Manifest(ctx); err == nil {
		t.Fatal("no error")
	}
	if pkgs, err := c.Manifest(context.Background()); err != nil || len(pkgs) != 1 {
		t.Errorf("%v %v", pkgs, err)
	}
}

func TestManifestOldCopyOnDiskIsUsedWhenTheNetworkFails(t *testing.T) {
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
	if err := os.Chtimes(mk().cachePath(), now, now); err != nil {
		t.Fatal(err)
	}
	now = now.Add(48 * time.Hour)
	f.status = http.StatusBadGateway
	pkgs, err := mk().Manifest(context.Background())
	if err != nil || len(pkgs) != 1 {
		t.Errorf("%v %v", pkgs, err)
	}
	// with no copy at all it is an error
	c := f.client(t)
	c.CacheDir = t.TempDir()
	if _, err := c.Manifest(context.Background()); err == nil {
		t.Error("no copy and no network, and no error")
	}
}

// slowDoer never answers until the request is cancelled.
type slowDoer struct{}

func (slowDoer) Do(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestManifestHasATimeout(t *testing.T) {
	c := &Client{BaseURL: "https://hub.invalid", HTTP: slowDoer{}, ManifestTimeout: 20 * time.Millisecond}
	// the context ends after three seconds, so that a missing timeout shows
	// as a slow answer and not as a test that never ends
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := c.Manifest(ctx); err == nil {
		t.Fatal("no error")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("it took %v", time.Since(start))
	}
}

func TestManifestCopiesOfTwoHubsAreKeptApart(t *testing.T) {
	a := &Client{BaseURL: "https://a.invalid", CacheDir: "/x"}
	b := &Client{BaseURL: "https://b.invalid", CacheDir: "/x"}
	if a.cachePath() == b.cachePath() {
		t.Errorf("one file for two hubs: %s", a.cachePath())
	}
}

func TestManifestCopyOnDiskThatIsTooLargeIsNotRead(t *testing.T) {
	c := &Client{BaseURL: "https://a.invalid", CacheDir: t.TempDir()}
	if err := os.WriteFile(c.cachePath(), make([]byte, maxManifest+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := c.readCache(true); ok {
		t.Error("a copy larger than the list may be was read")
	}
}

func TestRedirectsMustBeHTTPS(t *testing.T) {
	if err := refuseInsecureRedirect(&http.Request{URL: mustURL(t, "http://storage.example/x")}, nil); err == nil {
		t.Error("a redirect to http was followed")
	}
	if err := refuseInsecureRedirect(&http.Request{URL: mustURL(t, "https://storage.example/x")}, nil); err != nil {
		t.Errorf("a redirect to another host over https: %v", err)
	}
	if err := refuseInsecureRedirect(&http.Request{URL: mustURL(t, "https://storage.example/x")}, make([]*http.Request, 10)); err == nil {
		t.Error("eleven redirects were followed")
	}
	// the client that NewClient makes uses it
	f := newFakeHub(t)
	f.add(pkg("eyberg", "a", "1.0", "x86_64"), nginxTarball(t))
	c := NewClient("dock2nano-test")
	c.BaseURL = f.srv.URL
	c.CacheDir = ""
	c.TmpDir = t.TempDir()
	if _, err := c.FetchRecipe(context.Background(), f.manifest[0], nil); err == nil || !strings.Contains(err.Error(), "not https") {
		t.Errorf("the redirect of the test hub is to http: %v", err)
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
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

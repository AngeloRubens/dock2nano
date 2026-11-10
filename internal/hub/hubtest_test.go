package hub

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// entry is one entry of a test tarball.
type entry struct {
	name string
	body string
	mode int64
	typ  byte
	link string
}

func file(name, body string) entry { return entry{name: name, body: body, mode: 0o644, typ: tar.TypeReg} }

func dir(name string) entry { return entry{name: name, mode: 0o755, typ: tar.TypeDir} }

func symlink(name, link string) entry {
	return entry{name: name, mode: 0o777, typ: tar.TypeSymlink, link: link}
}

// tarball makes a gzip tarball of the entries.
func tarball(t *testing.T, entries []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: e.mode, Typeflag: e.typ, Linkname: e.link, Size: int64(len(e.body))}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sumOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// fakeHub is a hub on a local address. Its package downloads answer with a
// redirect, like the real one.
type fakeHub struct {
	srv *httptest.Server

	mu       sync.Mutex
	methods  []string
	paths    []string
	manifest []Package
	blobs    map[string][]byte
	status   int
	raw      string
}

func newFakeHub(t *testing.T) *fakeHub {
	t.Helper()
	f := &fakeHub{blobs: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.methods = append(f.methods, r.Method)
	f.paths = append(f.paths, r.URL.Path)
	status, raw := f.status, f.raw
	f.mu.Unlock()
	switch {
	case status != 0:
		http.Error(w, "broken", status)
	case r.URL.Path == "/v2/manifest.json":
		if raw != "" {
			w.Write([]byte(raw))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"Version": 2, "Packages": f.manifest})
	case strings.HasPrefix(r.URL.Path, "/v2/packages/"):
		http.Redirect(w, r, "/blob"+strings.TrimPrefix(r.URL.Path, "/v2/packages"), http.StatusFound)
	case strings.HasPrefix(r.URL.Path, "/blob/"):
		if b, ok := f.blobs[strings.TrimPrefix(r.URL.Path, "/blob/")]; ok {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}

// add puts a package in the list and its tarball on the server; the digest in
// the list is that of the tarball.
func (f *fakeHub) add(p Package, tgz []byte) Package {
	p.SHA256 = sumOf(tgz)
	f.manifest = append(f.manifest, p)
	f.blobs[p.Namespace+"/"+p.Name+"/"+p.Version+".tar.gz"] = tgz
	return p
}

func (f *fakeHub) client(t *testing.T) *Client {
	t.Helper()
	c := NewClient("dock2nano-test")
	c.BaseURL = f.srv.URL
	c.CacheDir = ""
	c.TmpDir = t.TempDir()
	return c
}

func (f *fakeHub) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.methods)
}

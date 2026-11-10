// Package hub reads the official hub of ops packages (https://repo.ops.city),
// read-only: it lists the packages, picks the one that fits a program, and
// takes from a package its recipe (how the program is started on nanos). It
// never writes to the hub and never runs what it downloads.
package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the address of the hub.
const DefaultBaseURL = "https://repo.ops.city"

// emptySHA256 is the digest of no bytes at all: the hub lists some packages
// whose tarball is empty, and they carry this value.
const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// maxManifest is the largest list of packages that is read.
const maxManifest = 32 << 20

// defaultMaxAge is how long a copy of the list on disk is used.
const defaultMaxAge = 24 * time.Hour

// Package is one entry of the list of the hub.
type Package struct {
	// Namespace is who published the package (eyberg, francescolavra).
	Namespace string `json:"namespace"`

	// Name is the name of the package.
	Name string `json:"name"`

	// Version is the version, as the hub spells it (v11.5.0, 1.28.3).
	Version string `json:"version"`

	// Arch is the architecture, as the hub spells it (x86_64, arm64).
	Arch string `json:"arch"`

	// SHA256 is the digest of the tarball, in hex.
	SHA256 string `json:"sha256"`
}

// Ref is the name the package is known by: namespace/name.
func (p Package) Ref() string { return p.Namespace + "/" + p.Name }

// Usable tells whether the package can be fetched and checked: all its parts
// are there, none can change the path of the download, the version is a
// version, and the digest is a real one (the digest of an empty file marks a
// broken, empty tarball).
func (p Package) Usable() bool {
	for _, part := range []string{p.Namespace, p.Name, p.Version, p.Arch} {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "/\\?#%\x00") {
			return false
		}
	}
	if _, ok := ParseVersion(p.Version); !ok {
		return false
	}
	sum := strings.ToLower(p.SHA256)
	if len(sum) != 64 || sum == emptySHA256 {
		return false
	}
	for _, c := range sum {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Doer is what the client needs from an HTTP client; *http.Client is one.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client reads the hub. The zero value is not usable; see NewClient.
type Client struct {
	// BaseURL is the address of the hub, without a trailing slash.
	BaseURL string

	// HTTP sends the requests. Only GET is ever sent.
	HTTP Doer

	// CacheDir is where the list of packages is kept between runs. Empty
	// keeps it only in memory.
	CacheDir string

	// MaxAge is how long the copy on disk is used; zero means 24 hours.
	MaxAge time.Duration

	// Now is the clock; the default is time.Now.
	Now func() time.Time

	// ManifestTimeout bounds the read of the list; zero means 30 seconds.
	ManifestTimeout time.Duration

	// TmpDir is where a tarball is kept while it is checked; empty uses the
	// temporary directory of the machine.
	TmpDir string

	mu     sync.Mutex
	loaded bool
	pkgs   []Package
	err    error

	// staleFrom and staleErr say that the list is an old copy from disk and
	// why the network was not used.
	staleFrom time.Time
	staleErr  error
}

// NewClient is a client of the hub with the real network and the folder of
// the user for the copy of the list. The folder is optional: where the
// machine has none, the list is not kept.
func NewClient(toolName string) *Client {
	c := &Client{BaseURL: DefaultBaseURL, HTTP: &http.Client{Timeout: 10 * time.Minute, CheckRedirect: refuseInsecureRedirect}}
	if dir, err := os.UserCacheDir(); err == nil {
		c.CacheDir = filepath.Join(dir, toolName)
	}
	return c
}

// refuseInsecureRedirect lets a redirect go to another host (the tarballs are
// on a storage service) but only over https, and not more than ten times.
func refuseInsecureRedirect(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return fmt.Errorf("the hub redirects to %s, which is not https", req.URL.Redacted())
	}
	if len(via) >= 10 {
		return errors.New("the hub redirects more than 10 times")
	}
	return nil
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// get sends a GET for one address and returns the body of a 200 answer.
func (c *Client) get(ctx context.Context, address string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", address, resp.Status)
	}
	return resp.Body, nil
}

// Manifest returns the list of packages of the hub. It is read once per
// client, from the copy on disk when that is recent, else from the network. A
// failure is remembered for the life of the client, so that the hub is not
// tried again for every service; when the network fails an old copy on disk
// is used.
func (c *Client) Manifest(ctx context.Context) ([]Package, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded {
		return c.pkgs, nil
	}
	if c.err != nil {
		return nil, c.err
	}
	if data, _, ok := c.readCache(false); ok {
		if pkgs, err := parseManifest(data); err == nil {
			c.pkgs, c.loaded = pkgs, true
			return pkgs, nil
		}
	}
	data, err := c.fetchManifest(ctx)
	if err == nil {
		var pkgs []Package
		if pkgs, err = parseManifest(data); err == nil {
			c.writeCache(data)
			c.pkgs, c.loaded = pkgs, true
			return pkgs, nil
		}
	}
	if stale, when, ok := c.readCache(true); ok {
		if pkgs, perr := parseManifest(stale); perr == nil {
			c.pkgs, c.loaded = pkgs, true
			c.staleFrom, c.staleErr = when, err
			return pkgs, nil
		}
	}
	if ctx.Err() == nil {
		c.err = err
	}
	return nil, err
}

// Stale implements Source: when the list is a copy from disk that is older
// than it should be, the date of the copy and why the hub was not read.
func (c *Client) Stale() (time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.staleFrom, c.staleErr
}

// fetchManifest reads the list from the network, within ManifestTimeout.
func (c *Client) fetchManifest(ctx context.Context) ([]byte, error) {
	limit := c.ManifestTimeout
	if limit <= 0 {
		limit = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	body, err := c.get(ctx, c.BaseURL+"/v2/manifest.json")
	if err != nil {
		return nil, err
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maxManifest+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxManifest {
		return nil, fmt.Errorf("the list of the hub is larger than %d bytes", maxManifest)
	}
	return data, nil
}

// parseManifest reads the list: {"Version":2,"Packages":[...]}.
func parseManifest(data []byte) ([]Package, error) {
	var m struct {
		Version  int
		Packages []Package
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("the list of the hub is not readable: %w", err)
	}
	if m.Version != 2 {
		return nil, fmt.Errorf("the list of the hub has version %d, not 2", m.Version)
	}
	return m.Packages, nil
}

// cachePath is the copy of the list on disk; its name depends on the address
// of the hub, so that two hubs do not share one.
func (c *Client) cachePath() string {
	if c.CacheDir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(c.BaseURL))
	return filepath.Join(c.CacheDir, "hub-manifest-v2-"+hex.EncodeToString(sum[:4])+".json")
}

// readCache returns the copy on disk when it is younger than MaxAge, or at
// any age when stale is true. A copy larger than the list may be is not read.
func (c *Client) readCache(stale bool) ([]byte, time.Time, bool) {
	p := c.cachePath()
	if p == "" {
		return nil, time.Time{}, false
	}
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxManifest {
		return nil, time.Time{}, false
	}
	maxAge := c.MaxAge
	if maxAge <= 0 {
		maxAge = defaultMaxAge
	}
	// a date in the future is not fresh
	if age := c.now().Sub(fi.ModTime()); !stale && (age < 0 || age > maxAge) {
		return nil, time.Time{}, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, time.Time{}, false
	}
	return data, fi.ModTime(), true
}

// writeCache keeps a copy on disk. Failing to is not an error: the next run
// reads the network again.
func (c *Client) writeCache(data []byte) {
	p := c.cachePath()
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "hub-manifest-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), p) != nil {
		os.Remove(tmp.Name())
	}
}

// packageURL is the address of the tarball of a package.
func (c *Client) packageURL(p Package) (string, error) {
	if !p.Usable() {
		return "", fmt.Errorf("the package %s %s of the hub cannot be used: its digest is missing or is that of an empty file, or a part of its name is not valid", p.Ref(), p.Version)
	}
	return c.BaseURL + "/v2/packages/" + url.PathEscape(p.Namespace) + "/" + url.PathEscape(p.Name) + "/" + url.PathEscape(p.Version) + ".tar.gz", nil
}

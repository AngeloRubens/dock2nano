package hub

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// The limits are variables so that tests can lower them.
var (
	// maxTarball is the largest tarball that is downloaded.
	maxTarball int64 = 2 << 30

	// maxExtracted is the most bytes written by FetchWhole.
	maxExtracted int64 = 4 << 30

	// maxDecompressed is the most bytes a tarball may expand to.
	maxDecompressed int64 = 8 << 30

	// maxEntries is the most entries a tarball may hold.
	maxEntries = 200000
)

// maxSmallFile is the largest file read into memory from a tarball: the
// package manifest, config.json and the configuration files of the recipe.
const maxSmallFile = 1 << 20

// RunConfig is the part of the run configuration of a package that is read.
type RunConfig struct {
	Ports    []string
	UDPPorts []string
}

// PackageManifest is the part of package.manifest that is read. The files of
// the package are not described by it.
type PackageManifest struct {
	// Program is the path of the program inside the package folder.
	Program string

	// Args is the command line of the program, starting with its name.
	Args []string

	// Env is the environment the program needs.
	Env map[string]string

	// RunConfig holds the ports the package listens on.
	RunConfig RunConfig

	// ManifestPassthrough is what the package asks nanos to read from the
	// root of its manifest (cwd, uid).
	ManifestPassthrough map[string]any

	// Version is the version the package says it is.
	Version string
}

// Recipe is what is taken from a package of the hub.
type Recipe struct {
	// Package is the entry of the list that was fetched.
	Package Package

	// Manifest is the package.manifest of the package.
	Manifest PackageManifest

	// Config is config.json of the package, when it has one.
	Config []byte

	// Files are the files of the sysroot that were asked for and found, by
	// their path inside the image ("/etc/nginx/nginx.conf").
	Files map[string][]byte

	// Skipped counts the entries of a whole extraction that were not written
	// (devices, and links that could not be placed).
	Skipped int

	// NotRead are the sysroot files that were asked for and are in the
	// package, but could not be read: links, or too large.
	NotRead []string
}

// Source is what the conversion needs from the hub. *Client implements it.
type Source interface {
	// Manifest lists the packages.
	Manifest(ctx context.Context) ([]Package, error)

	// Stale says, once Manifest has answered, whether the list is an old copy
	// from disk: its date, and why the hub was not read. The zero time says
	// it is not.
	Stale() (time.Time, error)

	// FetchRecipe downloads a package, checks its digest and reads its
	// manifest, its config.json and the sysroot files named in files.
	FetchRecipe(ctx context.Context, p Package, files []string) (*Recipe, error)

	// FetchWhole downloads a package, checks its digest and writes its
	// files into the empty directory dest: the sysroot as it is, and the
	// program at the top.
	FetchWhole(ctx context.Context, p Package, dest string) (*Recipe, error)
}

var _ Source = (*Client)(nil)

// withTarball downloads the tarball of p into a temporary file, checks its
// digest against the list, and calls fn with the file. The file is removed
// afterwards. Nothing is read from a tarball whose digest does not match.
func (c *Client) withTarball(ctx context.Context, p Package, fn func(f *os.File) error) error {
	address, err := c.packageURL(p)
	if err != nil {
		return err
	}
	body, err := c.get(ctx, address)
	if err != nil {
		return err
	}
	defer body.Close()
	f, err := os.CreateTemp(c.TmpDir, "hub-package-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(body, maxTarball+1))
	if err != nil {
		return fmt.Errorf("downloading %s %s: %w", p.Ref(), p.Version, err)
	}
	if n > maxTarball {
		return fmt.Errorf("the tarball of %s %s is larger than %d bytes", p.Ref(), p.Version, maxTarball)
	}
	if n == 0 {
		return fmt.Errorf("the tarball of %s %s is empty", p.Ref(), p.Version)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != strings.ToLower(p.SHA256) {
		return fmt.Errorf("the tarball of %s %s has the digest %s, but the list of the hub says %s", p.Ref(), p.Version, got, strings.ToLower(p.SHA256))
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return fn(f)
}

// capReader fails when more than n bytes are read through it.
type capReader struct {
	r io.Reader
	n int64
}

func (c *capReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n -= int64(n)
	if c.n < 0 {
		return n, fmt.Errorf("the tarball expands to more than %d bytes", maxDecompressed)
	}
	return n, err
}

// eachEntry reads a gzip tarball and calls visit with every entry, giving the
// path inside the top folder of the package ("package.manifest",
// "sysroot/etc/passwd"). The top folder is the first folder, or the first
// file inside a folder, of the tarball. The entries that are the folder
// itself, or outside it, or global headers, are not passed. A tarball with
// too many entries, or that expands to too much, is an error.
func eachEntry(r io.Reader, visit func(rel string, h *tar.Header, body io.Reader) error) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("the tarball is not gzip: %w", err)
	}
	defer zr.Close()
	tr := tar.NewReader(&capReader{r: zr, n: maxDecompressed})
	top := ""
	count := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading the tarball: %w", err)
		}
		if count++; count > maxEntries {
			return fmt.Errorf("the tarball holds more than %d entries", maxEntries)
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		first, rel, _ := strings.Cut(name, "/")
		if first == "." || first == ".." || path.IsAbs(name) {
			continue
		}
		if top == "" {
			if rel == "" && h.Typeflag != tar.TypeDir {
				continue
			}
			top = first
		}
		if first != top || rel == "" {
			continue
		}
		if err := visit(rel, h, tr); err != nil {
			return err
		}
	}
}

// readSmall reads a file of a tarball when it is not larger than maxSmallFile.
func readSmall(h *tar.Header, r io.Reader) ([]byte, bool, error) {
	if h.Typeflag != tar.TypeReg || h.Size > maxSmallFile {
		return nil, false, nil
	}
	data, err := io.ReadAll(io.LimitReader(r, maxSmallFile))
	return data, err == nil, err
}

// scanRecipe reads the manifest, config.json and the asked sysroot files.
func scanRecipe(f io.Reader, p Package, files []string) (*Recipe, error) {
	rec := &Recipe{Package: p, Files: map[string][]byte{}}
	found := false
	err := eachEntry(f, func(rel string, h *tar.Header, body io.Reader) error {
		switch {
		case rel == "package.manifest":
			data, ok, err := readSmall(h, body)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("package.manifest is not a regular file of a reasonable size")
			}
			if err := json.Unmarshal(data, &rec.Manifest); err != nil {
				return fmt.Errorf("package.manifest is not readable: %w", err)
			}
			found = true
		case rel == "config.json":
			if data, ok, err := readSmall(h, body); err != nil {
				return err
			} else if ok {
				rec.Config = data
			}
		case strings.HasPrefix(rel, "sysroot/"):
			inside := "/" + strings.TrimPrefix(rel, "sysroot/")
			if !contains(files, inside) {
				return nil
			}
			data, ok, err := readSmall(h, body)
			if err != nil {
				return err
			}
			if ok {
				rec.Files[inside] = data
			} else if h.Typeflag != tar.TypeDir {
				rec.NotRead = append(rec.NotRead, inside)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("the tarball of %s %s holds no package.manifest", p.Ref(), p.Version)
	}
	return rec, nil
}

// FetchRecipe implements Source.
func (c *Client) FetchRecipe(ctx context.Context, p Package, files []string) (*Recipe, error) {
	var rec *Recipe
	err := c.withTarball(ctx, p, func(f *os.File) error {
		var err error
		rec, err = scanRecipe(f, p, files)
		return err
	})
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// FetchWhole implements Source.
func (c *Client) FetchWhole(ctx context.Context, p Package, dest string) (*Recipe, error) {
	var rec *Recipe
	err := c.withTarball(ctx, p, func(f *os.File) error {
		var err error
		if rec, err = scanRecipe(f, p, nil); err != nil {
			return err
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		rec.Skipped, err = extract(f, dest, rec.Manifest.Program)
		return err
	})
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// extract writes the sysroot of a package into dest, and its program at the
// top of dest under the name it has. Directories, regular files, symbolic
// links and hard links (written as copies) are written; nothing is written
// outside dest, nor through a symbolic link. It returns how many entries it
// left out (devices, and links that point at nothing written).
func extract(r io.Reader, dest, program string) (int, error) {
	_, programRel, _ := strings.Cut(path.Clean(program), "/")
	if programRel == "" {
		programRel = path.Clean(program)
	}
	// where an entry of the package goes in dest; "" for one that is left out
	place := func(rel string) string {
		switch {
		case strings.HasPrefix(rel, "sysroot/"):
			return strings.TrimPrefix(rel, "sysroot/")
		case rel == programRel:
			return path.Base(rel)
		}
		return ""
	}
	skipped := 0
	var total int64
	err := eachEntry(r, func(rel string, h *tar.Header, body io.Reader) error {
		target := place(rel)
		if target == "" {
			return nil
		}
		full, err := safeJoin(dest, target)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			return os.MkdirAll(full, 0o755)
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return err
			}
			os.Remove(full)
			return os.Symlink(h.Linkname, full)
		case tar.TypeReg:
			total += h.Size
			if total > maxExtracted {
				return fmt.Errorf("the package holds more than %d bytes", maxExtracted)
			}
			return writeFile(full, os.FileMode(h.Mode&0o777)|0o600, body)
		case tar.TypeLink:
			linkTop, linkRel, _ := strings.Cut(path.Clean(strings.TrimPrefix(h.Linkname, "./")), "/")
			entryTop, _, _ := strings.Cut(path.Clean(strings.TrimPrefix(h.Name, "./")), "/")
			from := ""
			if linkTop == entryTop {
				from = place(linkRel)
			}
			if from == "" {
				skipped++
				return nil
			}
			src, err := safeJoin(dest, from)
			if err != nil {
				return err
			}
			fi, err := os.Lstat(src)
			if err != nil || !fi.Mode().IsRegular() {
				skipped++
				return nil
			}
			total += fi.Size()
			if total > maxExtracted {
				return fmt.Errorf("the package holds more than %d bytes", maxExtracted)
			}
			return copyInto(src, full, fi.Mode()&0o777|0o600)
		}
		skipped++
		return nil
	})
	if err != nil {
		return skipped, err
	}
	// a package whose program is not there cannot run
	if fi, err := os.Lstat(filepath.Join(dest, path.Base(programRel))); err != nil || !fi.Mode().IsRegular() {
		return skipped, fmt.Errorf("the program %s of the package is not a regular file in the package", program)
	}
	return skipped, nil
}

// copyInto copies the file src to dst.
func copyInto(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeFile(dst, mode, in)
}

// safeJoin joins a path of a tarball to dest. It refuses a path that leaves
// dest, and a path that goes through a symbolic link already written.
func safeJoin(dest, rel string) (string, error) {
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return "", fmt.Errorf("the path %q of the package leaves the folder it is written to", rel)
		}
	}
	rel = strings.TrimLeft(path.Clean("/"+rel), "/")
	if rel == "" {
		return dest, nil
	}
	cur := dest
	parts := strings.Split(rel, "/")
	for _, part := range parts[:len(parts)-1] {
		cur = cur + string(os.PathSeparator) + part
		if fi, err := os.Lstat(cur); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("the path %q of the package goes through a symbolic link", rel)
		}
	}
	return dest + string(os.PathSeparator) + strings.ReplaceAll(rel, "/", string(os.PathSeparator)), nil
}

// writeFile creates a file, replacing a file of the same name (never writing
// through a link), and copies the body into it.
func writeFile(full string, mode os.FileMode, body io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	os.Remove(full)
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

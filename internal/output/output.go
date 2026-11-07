// Package output writes a plan.Plan to disk where "ops compose up" finds it:
// one package folder per service under <ops home>/local_packages/<arch>/ and a
// compose.yaml that lists them.
package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/AngeloRubens/dock2nano/internal/plan"
	"gopkg.in/yaml.v3"
)

// Options says where Write puts things.
type Options struct {
	// OpsHome is the ops home folder. Empty means $OPS_HOME, then ~/.ops.
	OpsHome string
	// ComposePath is where compose.yaml goes. Empty means ./compose.yaml.
	ComposePath string
	// Force replaces package folders and a compose.yaml that already exist.
	Force bool
}

// rename is os.Rename; tests replace it to simulate a cross-device move.
var rename = os.Rename

// journal records how to undo (after a failure) and what to clean up (after
// success) the steps of one Write call.
type journal struct {
	undo   []func()
	commit []func()
}

func (j *journal) onFail(f func()) { j.undo = append(j.undo, f) }

func (j *journal) onOK(f func()) { j.commit = append(j.commit, f) }

func (j *journal) rollback() {
	for i := len(j.undo) - 1; i >= 0; i-- {
		j.undo[i]()
	}
}

func (j *journal) finish() {
	for _, f := range j.commit {
		f()
	}
}

// mkdirAll creates dir and its missing parents; on failure of the call, the
// folders it created are removed.
func (j *journal) mkdirAll(dir string) error {
	top := ""
	for d := filepath.Clean(dir); ; {
		if _, err := os.Lstat(d); err == nil {
			break
		}
		top = d
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		if top != "" {
			os.RemoveAll(top)
		}
		return err
	}
	if top != "" {
		j.onFail(func() { os.RemoveAll(top) })
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func validName(what, s string) error {
	if s == "" {
		return fmt.Errorf("output: %s is empty", what)
	}
	if s == "." || strings.Contains(s, "..") || strings.ContainsAny(s, "/\\\x00") || strings.ContainsRune(s, os.PathSeparator) {
		return fmt.Errorf("output: %s %q must not contain a path separator or \"..\"", what, s)
	}
	return nil
}

func opsHome(o Options) (string, error) {
	if o.OpsHome != "" {
		return o.OpsHome, nil
	}
	if h := os.Getenv("OPS_HOME"); h != "" {
		return h, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("output: no ops home: %w", err)
	}
	return filepath.Join(h, ".ops"), nil
}

// Write writes the packages of p and then its compose.yaml. It returns the
// paths it created, in order: each package folder, then the compose file.
// If it fails, what this call created is removed and what it replaced is put
// back; a sysroot that was moved is moved back, or was never removed.
func Write(p *plan.Plan, o Options) (written []string, err error) {
	if p == nil {
		return nil, errors.New("output: nil plan")
	}
	if err := validName("arch", p.Arch); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, s := range p.Services {
		if err := validName("package name", s.PackageName); err != nil {
			return nil, err
		}
		if seen[s.PackageName] {
			return nil, fmt.Errorf("output: package name %q used twice", s.PackageName)
		}
		seen[s.PackageName] = true
	}
	home, err := opsHome(o)
	if err != nil {
		return nil, err
	}
	composePath := o.ComposePath
	if composePath == "" {
		composePath = "compose.yaml"
	}
	base := filepath.Join(home, "local_packages", p.Arch)
	if !o.Force {
		if exists(composePath) {
			return nil, fmt.Errorf("output: %s exists; use force to replace it", composePath)
		}
		for _, s := range p.Services {
			final := filepath.Join(base, s.PackageName)
			if exists(final) {
				return nil, fmt.Errorf("output: package folder %s exists; use force to replace it", final)
			}
		}
	}

	var j journal
	defer func() {
		if err != nil {
			j.rollback()
			written = nil
		} else {
			j.finish()
		}
	}()

	if len(p.Services) > 0 {
		if err = j.mkdirAll(base); err != nil {
			return nil, err
		}
	}
	for _, s := range p.Services {
		var final string
		final, err = writePackage(&j, base, s, o.Force)
		if err != nil {
			return nil, err
		}
		written = append(written, final)
	}
	if err = writeCompose(&j, composePath, p.Compose, o.Force); err != nil {
		return nil, err
	}
	written = append(written, composePath)
	return written, nil
}

// writePackage builds the folder in a temporary sibling and renames it into
// place. It returns the final path.
func writePackage(j *journal, base string, s plan.Service, force bool) (string, error) {
	final := filepath.Join(base, s.PackageName)
	if s.Sysroot == "" {
		return "", fmt.Errorf("output: service %q has no sysroot", s.Name)
	}
	fi, err := os.Lstat(s.Sysroot)
	if err != nil {
		return "", fmt.Errorf("output: sysroot of %q: %w", s.PackageName, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("output: sysroot %s of %q is not a directory", s.Sysroot, s.PackageName)
	}
	tmp, err := os.MkdirTemp(base, "."+s.PackageName+".tmp-")
	if err != nil {
		return "", err
	}
	placed := false
	backup := ""
	// Registered in this order so that, on failure, the sysroot goes back
	// first, then the new folder is removed, then the temporary one.
	j.onFail(func() { os.RemoveAll(tmp) })
	j.onFail(func() {
		if placed {
			os.RemoveAll(final)
		}
		if backup != "" {
			os.Rename(backup, final)
		}
	})
	sysDst := filepath.Join(tmp, "sysroot")
	moved := false
	copied := false
	j.onFail(func() {
		if !moved || copied {
			return
		}
		if os.Rename(sysDst, s.Sysroot) != nil {
			os.Rename(filepath.Join(final, "sysroot"), s.Sysroot)
		}
	})

	copied, err = moveTree(s.Sysroot, sysDst)
	if err != nil {
		return "", fmt.Errorf("output: sysroot of %q: %w", s.PackageName, err)
	}
	moved = true
	if copied {
		src := s.Sysroot
		j.onOK(func() { os.RemoveAll(src) })
	}

	manifest, err := json.MarshalIndent(s.Manifest, "", "  ")
	if err != nil {
		return "", err
	}
	manifest = append(manifest, '\n')
	mpath := filepath.Join(tmp, "package.manifest")
	if err := os.WriteFile(mpath, manifest, 0o644); err != nil {
		return "", err
	}
	if err := os.Chmod(mpath, 0o644); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", err
	}

	if exists(final) {
		if !force {
			return "", fmt.Errorf("output: package folder %s exists; use force to replace it", final)
		}
		old := tmp + ".old"
		if err := os.Rename(final, old); err != nil {
			return "", err
		}
		backup = old
		j.onOK(func() { os.RemoveAll(old) })
	}
	if err := os.Rename(tmp, final); err != nil {
		return "", err
	}
	placed = true
	return final, nil
}

// writeCompose writes compose.yaml through a temporary file in the same folder.
func writeCompose(j *journal, path string, c plan.ComposeFile, force bool) error {
	dir := filepath.Dir(path)
	if err := j.mkdirAll(dir); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	placed := false
	backup := ""
	j.onFail(func() {
		os.Remove(tmp)
		if placed {
			os.RemoveAll(path)
		}
		if backup != "" {
			os.Rename(backup, path)
		}
	})
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	if exists(path) {
		if !force {
			return fmt.Errorf("output: %s exists; use force to replace it", path)
		}
		old := tmp + ".old"
		if err := os.Rename(path, old); err != nil {
			return err
		}
		backup = old
		j.onOK(func() { os.RemoveAll(old) })
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	placed = true
	return nil
}

// moveTree moves the directory src to dst (which must not exist). It renames;
// when that fails because src and dst are on different file systems it copies
// the tree and reports copied true. In that case src is left in place and the
// caller removes it once everything else succeeded.
func moveTree(src, dst string) (copied bool, err error) {
	err = rename(src, dst)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return false, err
	}
	if err := copyTree(src, dst); err != nil {
		os.RemoveAll(dst)
		return false, err
	}
	return true, nil
}

func keepBits(m fs.FileMode) fs.FileMode {
	return m & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
}

// copyTree copies the directory src to the new path dst, keeping permission
// bits. Symbolic links are recreated as links and never followed. Anything
// that is not a directory, a regular file or a link is refused.
func copyTree(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}
	return copyDir(src, dst, fi.Mode())
}

func copyDir(src, dst string, mode fs.FileMode) error {
	if err := os.Mkdir(dst, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		info, err := os.Lstat(s)
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			err = copyDir(s, d, info.Mode())
		case info.Mode()&fs.ModeSymlink != 0:
			var target string
			target, err = os.Readlink(s)
			if err == nil {
				err = os.Symlink(target, d)
			}
		case info.Mode().IsRegular():
			err = copyFile(s, d, info.Mode())
		default:
			err = fmt.Errorf("%s: unsupported file type %v", s, info.Mode().Type())
		}
		if err != nil {
			return err
		}
	}
	return os.Chmod(dst, keepBits(mode))
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, keepBits(mode))
}

// manifestDoc mirrors plan.Manifest. It is kept separate so that a field added
// to plan.Manifest without being added here makes ValidateManifestJSON fail on
// the manifest Write produces.
type manifestDoc struct {
	Program string

	Args []string

	Env map[string]string

	Version string

	MapDirs map[string]string

	RebootOnExit bool

	RunConfig runConfigDoc
}

type runConfigDoc struct {
	Ports []string

	UDPPorts []string
}

// ValidateManifestJSON returns an error if b is not a single JSON object that
// holds only fields ops knows.
func ValidateManifestJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var m manifestDoc
	if err := dec.Decode(&m); err != nil {
		return fmt.Errorf("output: manifest: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("output: manifest: data after the JSON object")
	}
	return nil
}

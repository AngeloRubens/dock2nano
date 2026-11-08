package cmd

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// bindCopyMaxBytes is the most bytes of files that are copied. A bigger bind
// is mounted read-only instead. The limits are variables so that tests can
// lower them.
var bindCopyMaxBytes int64 = 256 << 20

// bindCopyMaxEntries is the most files, folders and links that are copied.
var bindCopyMaxEntries = 20000

// errTooLarge ends the measure of a tree that is over a limit.
var errTooLarge = errors.New("too large to copy")

// bindMount is how one bind source reaches the containers of a run.
type bindMount struct {
	// path is what is mounted: the copy, or the original when it is read-only.
	// It is empty when the bind is not passed at all.
	path string
	// readOnly is true when path is the original, mounted :ro.
	readOnly bool
	// notes are the warnings about this bind, for the report.
	notes []string
}

// mountBinds maps the binds of a service ("host:container[:ro]") to what is
// mounted: a copy in a private temporary directory, so that the observation
// and the dependencies never write into the data of the user. A bind that is
// read-only is mounted as it is. The copy of one source is made once per run
// and shared by every service that mounts it. It returns the binds to pass
// and the notes for the report.
func (rs *runState) mountBinds(a *App, service string, binds []string) ([]string, []string) {
	var out, notes []string
	for _, b := range binds {
		rest, ro := strings.CutSuffix(b, ":ro")
		host, target, _ := strings.Cut(rest, ":")
		m := rs.bindMountOf(a, service, host, ro)
		notes = append(notes, m.notes...)
		if m.path == "" {
			continue
		}
		s := m.path + ":" + target
		if ro || m.readOnly {
			s += ":ro"
		}
		out = append(out, s)
	}
	return out, notes
}

// bindMountOf returns the mount of one source, making the copy the first time.
func (rs *runState) bindMountOf(a *App, service, host string, ro bool) *bindMount {
	if ro {
		// the copy that a service writes is what the others must read
		if m := rs.binds[host]; m != nil && m.path != "" {
			return &bindMount{path: m.path, readOnly: true}
		}
		return &bindMount{path: host, readOnly: true}
	}
	if m := rs.binds[host]; m != nil {
		return m
	}
	m := rs.copyBind(host)
	if rs.binds == nil {
		rs.binds = map[string]*bindMount{}
	}
	rs.binds[host] = m
	for _, n := range m.notes {
		fmt.Fprintf(a.Stderr, "%s: warning: %s\n", service, n)
	}
	return m
}

// copyBind makes the copy of the source, or decides that the original is
// mounted read-only.
func (rs *runState) copyBind(host string) *bindMount {
	readOnly := func(why string) *bindMount {
		return &bindMount{path: host, readOnly: true, notes: []string{fmt.Sprintf("the bind %s is mounted read-only: %s; the program cannot write there as it would in a real run", host, why)}}
	}
	src, err := filepath.EvalSymlinks(host)
	if err != nil {
		return readOnly("it cannot be read for a copy (" + err.Error() + ")")
	}
	fi, err := os.Stat(src)
	if err != nil {
		return readOnly("it cannot be read for a copy (" + err.Error() + ")")
	}
	if !fi.IsDir() && !fi.Mode().IsRegular() {
		return &bindMount{notes: []string{fmt.Sprintf("the bind %s is a special file (socket, fifo or device); it is not passed to the observation", host)}}
	}
	if err := measureTree(src); err != nil {
		if errors.Is(err, errTooLarge) {
			return readOnly("too large to copy")
		}
		return readOnly("it cannot be read for a copy (" + err.Error() + ")")
	}
	if rs.tmpRoot == "" {
		root, err := os.MkdirTemp("", ToolName+"-binds-")
		if err != nil {
			return readOnly("a private directory for the copy cannot be made (" + err.Error() + ")")
		}
		rs.tmpRoot = root
	}
	rs.copies++
	dst := filepath.Join(rs.tmpRoot, strconv.Itoa(rs.copies))
	mount := dst
	if !fi.IsDir() {
		if err := os.Mkdir(dst, 0o700); err != nil {
			return readOnly("it cannot be copied (" + err.Error() + ")")
		}
		mount = filepath.Join(dst, filepath.Base(src))
	}
	skipped, err := copyTree(src, mount)
	if err != nil {
		os.RemoveAll(dst)
		return readOnly("it cannot be copied (" + err.Error() + ")")
	}
	m := &bindMount{path: mount}
	for i, s := range skipped {
		if i == 5 {
			m.notes = append(m.notes, fmt.Sprintf("the bind %s: %d more special files were not copied", host, len(skipped)-5))
			break
		}
		m.notes = append(m.notes, fmt.Sprintf("the bind %s: the special file %s (socket, fifo or device) is not copied", host, s))
	}
	return m
}

// removeCopies removes the private directory with every copy. A failure is a
// warning: files made by a container as another user may not be removable.
func (rs *runState) removeCopies(a *App) {
	if rs.tmpRoot == "" {
		return
	}
	// a directory copied with a mode like 0555 cannot be emptied until it is writable
	filepath.WalkDir(rs.tmpRoot, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			os.Chmod(p, 0o700)
		}
		return nil
	})
	if err := os.RemoveAll(rs.tmpRoot); err != nil {
		fmt.Fprintf(a.Stderr, "%s: warning: the copies of the binds in %s are not removed: %v\n", ToolName, rs.tmpRoot, err)
	}
	rs.tmpRoot = ""
}

// measureTree returns errTooLarge when the tree at src has more entries or
// bytes than the limits. Symbolic links are not followed.
func measureTree(src string) error {
	var bytes int64
	entries := 0
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		entries++
		if entries > bindCopyMaxEntries {
			return errTooLarge
		}
		if d.Type().IsRegular() {
			fi, err := d.Info()
			if err != nil {
				return err
			}
			bytes += fi.Size()
			if bytes > bindCopyMaxBytes {
				return errTooLarge
			}
		}
		return nil
	})
}

type dirFix struct {
	path string
	info fs.FileInfo
}

// copyTree copies src to dst. Symbolic links are copied as links, never
// followed; file modes are kept; sockets, fifos and devices are skipped and
// returned as paths relative to src. src may be a file.
func copyTree(src, dst string) ([]string, error) {
	var skipped []string
	var dirs []dirFix
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := dst
		if rel != "." {
			target = filepath.Join(dst, rel)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			// writable while it is filled; the real mode is set at the end
			if err := os.Mkdir(target, fi.Mode().Perm()|0o700); err != nil {
				return err
			}
			dirs = append(dirs, dirFix{target, fi})
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if err := os.Symlink(link, target); err != nil {
				return err
			}
			keepOwner(target, fi, true)
		case d.Type().IsRegular():
			if err := copyFile(p, target, fi); err != nil {
				return err
			}
		default:
			skipped = append(skipped, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		keepOwner(dirs[i].path, dirs[i].info, false)
		if err := os.Chmod(dirs[i].path, dirs[i].info.Mode().Perm()|(dirs[i].info.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky))); err != nil {
			return nil, err
		}
	}
	return skipped, nil
}

func copyFile(src, dst string, fi fs.FileInfo) error {
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
	keepOwner(dst, fi, false)
	return os.Chmod(dst, fi.Mode().Perm()|(fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky)))
}

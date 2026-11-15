package observe

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// maxEmptyDirs is the most empty directories one package carries. A directory is carried empty
// (the content of an image directory is not copied, only the directory), so its size is nothing;
// the cap keeps a program that makes thousands of directories from filling the report.
const maxEmptyDirs = 50

// keepFile is the file that makes an empty directory a thing a package has: ops loads the files of
// a directory, and a directory with none is not in the image. It is the file the hand-made package
// that started nginx on nanos had in each of its directories.
const keepFile = ".keep"

// keepEmptyDirs puts a .keep file into each of dirs that is an empty directory of sysroot (not a
// link, and not one a file was carried into), up to limit of them. It returns the directories it
// kept, how many empty ones were left out for the limit, and a warning for each directory it could
// not read or write (the observation goes on without it). A directory that is not in sysroot
// (docker could not give it, or it is not one) is left out without a word: the caller says what it
// carried. Every access goes through an os.Root on sysroot, and a link of the image is read as the
// image would: an absolute link (/var/run -> /run) leads from the root of the sysroot, and a ".."
// at the root stays at the root, so nothing is ever reached outside the sysroot.
func keepEmptyDirs(sysroot string, dirs []string, limit int) (kept []string, over int, warnings []string, err error) {
	root, err := os.OpenRoot(sysroot)
	if err != nil {
		return nil, 0, nil, err
	}
	defer root.Close()
	for _, d := range dirs {
		name, warn := resolveIn(root, d, false, false)
		if warn != "" {
			warnings = append(warnings, fmt.Sprintf("could not carry %s as an empty directory: %s", d, warn))
			continue
		}
		if name == "" {
			continue
		}
		info, lerr := root.Lstat(name)
		if lerr != nil || !info.IsDir() {
			// absent, or a link (what it leads to is carried under its own name, if at all)
			continue
		}
		empty, rerr := isEmptyDir(root, name)
		if rerr != nil {
			warnings = append(warnings, fmt.Sprintf("could not read the directory %s of the package, so it is not carried as an empty directory: %v", d, rerr))
			continue
		}
		if !empty {
			continue
		}
		if len(kept) >= limit {
			over++
			continue
		}
		f, werr := root.OpenFile(filepath.Join(name, keepFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if werr != nil {
			warnings = append(warnings, fmt.Sprintf("could not carry %s as an empty directory (a %s file could not be written in it): %v", d, keepFile, werr))
			continue
		}
		if cerr := f.Close(); cerr != nil {
			warnings = append(warnings, fmt.Sprintf("could not carry %s as an empty directory (a %s file could not be written in it): %v", d, keepFile, cerr))
			continue
		}
		kept = append(kept, d)
	}
	return kept, over, warnings, nil
}

// isEmptyDir says whether the directory name of root has no entry.
func isEmptyDir(root *os.Root, name string) (bool, error) {
	f, err := root.Open(name)
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = f.ReadDir(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err
}

// maxLinks is how many links one path may lead through before it is taken for a loop (the limit
// of Linux is 40).
const maxLinks = 40

// resolveIn reads the path d (absolute, as the image has it) inside root as the image would: a link
// on the way is followed, an absolute one from the root of the sysroot, and ".." at the root stays
// there. It returns the path relative to the root, with no link in it. With followLast false the
// last component is not followed (it is looked at as it is, and a link there is for the caller to
// refuse); with create true a missing directory on the way, and the last one, is made. It returns
// "" and no warning when the path is not there (or is not a directory, without create), and a
// warning for what it could not read, a loop of links, or a directory it could not make.
func resolveIn(root *os.Root, d string, followLast, create bool) (rel, warn string) {
	comps := strings.Split(filepath.ToSlash(d), "/")
	var last string
	if !followLast {
		for len(comps) > 0 && (comps[len(comps)-1] == "" || comps[len(comps)-1] == ".") {
			comps = comps[:len(comps)-1]
		}
		if len(comps) == 0 || comps[len(comps)-1] == ".." {
			return "", ""
		}
		last = comps[len(comps)-1]
		comps = comps[:len(comps)-1]
	}
	var cur []string
	links := 0
	for len(comps) > 0 {
		c := comps[0]
		comps = comps[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			if len(cur) > 0 {
				cur = cur[:len(cur)-1]
			}
			continue
		}
		next := append(slices.Clone(cur), c)
		name := filepath.Join(next...)
		info, err := root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			if !create {
				return "", ""
			}
			if err := root.Mkdir(name, 0o755); err != nil {
				return "", fmt.Sprintf("making %s in the package: %v", name, err)
			}
			cur = next
			continue
		}
		if err != nil {
			return "", fmt.Sprintf("looking at %s in the package: %v", name, err)
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			links++
			if links > maxLinks {
				return "", fmt.Sprintf("more than %d links on the way to %s (a loop?)", maxLinks, d)
			}
			target, err := root.Readlink(name)
			if err != nil {
				return "", fmt.Sprintf("reading the link %s in the package: %v", name, err)
			}
			if strings.HasPrefix(target, "/") {
				cur = nil
			}
			comps = append(strings.Split(filepath.ToSlash(target), "/"), comps...)
		case info.IsDir():
			cur = next
		default:
			if create {
				return "", fmt.Sprintf("%s in the package is not a directory", name)
			}
			return "", ""
		}
	}
	if !followLast {
		cur = append(cur, last)
	}
	return filepath.Join(cur...), ""
}

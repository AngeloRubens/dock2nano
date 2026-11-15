package observe

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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
// carried. Every access goes through an os.Root on sysroot, so a link in the sysroot that climbs
// out of it (a relative link of the image) is never followed outside.
func keepEmptyDirs(sysroot string, dirs []string, limit int) (kept []string, over int, warnings []string, err error) {
	root, err := os.OpenRoot(sysroot)
	if err != nil {
		return nil, 0, nil, err
	}
	defer root.Close()
	for _, d := range dirs {
		name := filepath.FromSlash(strings.TrimPrefix(d, "/"))
		if name == "" {
			continue
		}
		info, lerr := root.Lstat(name)
		if lerr != nil || !info.IsDir() {
			// absent, a link, or a path that leaves the sysroot
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

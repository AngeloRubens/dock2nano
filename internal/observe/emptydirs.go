package observe

import (
	"os"
	"path/filepath"
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
// kept and how many empty ones were left out for the limit. A directory that is not in sysroot (docker
// could not give it, or it is not one) is left out without a word: the caller says what it carried.
func keepEmptyDirs(sysroot string, dirs []string, limit int) (kept []string, over int, err error) {
	for _, d := range dirs[:0] {
		target := filepath.Join(sysroot, filepath.FromSlash(d))
		info, lerr := os.Lstat(target)
		if lerr != nil || !info.IsDir() {
			continue
		}
		entries, rerr := os.ReadDir(target)
		if rerr != nil {
			return kept, over, rerr
		}
		if len(entries) > 0 {
			continue
		}
		if len(kept) >= limit {
			over++
			continue
		}
		if werr := os.WriteFile(filepath.Join(target, keepFile), nil, 0o644); werr != nil {
			return kept, over, werr
		}
		kept = append(kept, d)
	}
	return kept, over, nil
}

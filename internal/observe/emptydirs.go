package observe

import (
	"os"
	"path/filepath"
)

const maxEmptyDirs = 50

const keepFile = ".keep"

func keepEmptyDirs(sysroot string, dirs []string, limit int) (kept []string, over int, warnings []string, err error) {
	for _, d := range dirs {
		target := filepath.Join(sysroot, filepath.FromSlash(d))
		info, lerr := os.Lstat(target)
		if lerr != nil || !info.IsDir() {
			continue
		}
		entries, rerr := os.ReadDir(target)
		if rerr != nil || len(entries) > 0 {
			continue
		}
		if len(kept) >= limit {
			over++
			continue
		}
		if os.WriteFile(filepath.Join(target, keepFile), nil, 0o644) != nil {
			continue
		}
		kept = append(kept, d)
	}
	return kept, over, nil, nil
}

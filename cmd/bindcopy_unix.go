//go:build !windows

package cmd

import (
	"io/fs"
	"os"
	"syscall"
)

// keepOwner gives the copy the owner of the original, as far as the user is
// allowed to: a failure is ignored. A program that runs as another user
// needs its files to belong to it.
func keepOwner(path string, fi fs.FileInfo, link bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	if link {
		os.Lchown(path, int(st.Uid), int(st.Gid))
		return
	}
	os.Chown(path, int(st.Uid), int(st.Gid))
}

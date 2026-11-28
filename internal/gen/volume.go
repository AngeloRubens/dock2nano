package gen

import (
	"fmt"
	"io/fs"
	"path/filepath"
)

// volumeHeadroom is the room, in megabytes, that a package gets beyond its files.
const volumeHeadroom = 256

// volumeSize is the size of the writable volume of a package: its files, a quarter more, and
// volumeHeadroom megabytes, in the form "ops compose" reads ("266m"). A program that writes while
// it runs (a database, a server that logs) needs room that the files alone do not give; without
// it the image is as big as its content and the first write fails with "No space left on device".
// A directory that cannot be read counts as empty.
func volumeSize(sysroot string) string {
	var total int64
	filepath.WalkDir(sysroot, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	mb := total / (1024 * 1024)
	return fmt.Sprintf("%dm", mb+mb/4+volumeHeadroom)
}

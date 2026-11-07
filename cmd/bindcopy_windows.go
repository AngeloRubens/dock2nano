//go:build windows

package cmd

import "io/fs"

// keepOwner does nothing on Windows.
func keepOwner(path string, fi fs.FileInfo, link bool) {}

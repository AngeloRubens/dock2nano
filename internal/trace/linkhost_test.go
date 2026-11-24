package trace

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, p, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLinkHostFileReplacesADirectoryOfFilesOpenedThroughTheLink(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "pkg/x/dist/index.js"), "index")
	write(t, filepath.Join(root, "pkg/x/dist/own.js"), "from the link")
	write(t, filepath.Join(root, "store/x/dist/index.js"), "real index")
	write(t, filepath.Join(root, "store/x/dist/standard.js"), "standard")

	if err := LinkHostFile(root, "../store/x", "/pkg/x"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(filepath.Join(root, "pkg/x")); err != nil || got != "../store/x" {
		t.Fatalf("readlink = %q, %v; want ../store/x", got, err)
	}
	for _, c := range [][2]string{
		{"store/x/dist/index.js", "real index"},
		{"store/x/dist/standard.js", "standard"},
		{"store/x/dist/own.js", "from the link"},
		{"pkg/x/dist/standard.js", "standard"},
	} {
		f, want := c[0], c[1]
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil || string(data) != want {
			t.Errorf("%s = %q, %v; want %q", f, data, err, want)
		}
	}
}

func TestLinkHostFileLeavesAFileAndALinkOutsideTheSysrootAlone(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "pkg/f"), "file")
	if err := LinkHostFile(root, "../elsewhere", "/pkg/f"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(root, "pkg/f")); err != nil || !info.Mode().IsRegular() {
		t.Errorf("a file that is there is replaced: %v, %v", info, err)
	}
	// a directory and a link that leads out of the sysroot: nothing is moved
	write(t, filepath.Join(root, "pkg/d/a"), "a")
	if err := LinkHostFile(root, "../../../outside", "/pkg/d"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(root, "pkg/d")); err != nil || !info.IsDir() {
		t.Errorf("the directory is replaced: %v, %v", info, err)
	}
}

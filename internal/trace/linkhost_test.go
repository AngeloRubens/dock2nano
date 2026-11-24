package trace

import (
	"errors"
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

func TestLinkHostFileLeavesAFileAlone(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "pkg/f"), "file")
	if err := LinkHostFile(root, "../elsewhere", "/pkg/f"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(root, "pkg/f")); err != nil || !info.Mode().IsRegular() {
		t.Errorf("a file that is there is replaced: %v, %v", info, err)
	}
}

// the link leads out of the sysroot to a directory that exists: nothing is moved or removed
func TestLinkHostFileDoesNotLeadOutOfTheSysroot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "sysroot")
	write(t, filepath.Join(root, "pkg/d/a"), "a")
	write(t, filepath.Join(parent, "outside/keep"), "keep")
	if err := LinkHostFile(root, "../../outside", "/pkg/d"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(root, "pkg/d")); err != nil || !info.IsDir() {
		t.Errorf("the directory is replaced: %v, %v", info, err)
	}
	for _, f := range []string{"outside/keep", "sysroot/pkg/d/a"} {
		if _, err := os.Stat(filepath.Join(parent, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(parent, "outside/a")); err == nil {
		t.Errorf("a file of the sysroot was moved out of it")
	}
}

// a link that is in the sysroot already and goes out of it (legal in an image: /.. is /) must not
// make the destination a folder of this machine
func TestLinkHostFileDoesNotFollowAnExistingLinkOutOfTheSysroot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "sysroot")
	host := filepath.Join(parent, "host")
	write(t, filepath.Join(host, "x/precious"), "precious")
	write(t, filepath.Join(root, "a/y/file"), "y")
	// /a/b leads to the host folder, as ../../../ goes out of the sysroot
	if err := os.Symlink("../../host", filepath.Join(root, "a/b")); err != nil {
		t.Fatal(err)
	}
	if err := LinkHostFile(root, "../y", "/a/b/x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(host, "x/precious")); err != nil {
		t.Errorf("a file of this machine was touched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(host, "y")); err == nil {
		t.Errorf("a link or a file was made in the folder of this machine")
	}
}

func TestLinkHostFileKeepsTheDirectoryWhenAFileMeetsADirectory(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "pkg/x/dist/a.js"), "a")
	write(t, filepath.Join(root, "pkg/x/dist/sub/b.js"), "b")
	write(t, filepath.Join(root, "store/x/dist/sub"), "a file where the other has a directory")
	err := LinkHostFile(root, "../store/x", "/pkg/x")
	var clash *LinkConflict
	if !errors.As(err, &clash) || clash.Path != "dist/sub" {
		t.Fatalf("err = %v, want a *LinkConflict at dist/sub", err)
	}
	for _, f := range []string{"pkg/x/dist/a.js", "pkg/x/dist/sub/b.js"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("%s was lost: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "store/x/dist/a.js")); err == nil {
		t.Errorf("something was moved before the conflict was known")
	}
}

func TestLinkHostFileKeepsTheModeOfTheDirectoriesItMoves(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "pkg/x/private/a"), "a")
	if err := os.Chmod(filepath.Join(root, "pkg/x/private"), 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "store/x/other"), "o")
	if err := LinkHostFile(root, "../store/x", "/pkg/x"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "store/x/private"))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Errorf("mode = %v, %v; want 0700", info, err)
	}
}

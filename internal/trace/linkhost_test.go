package trace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	// where "../y" leads from the folder of this machine, so that the merge would find a target
	write(t, filepath.Join(parent, "y/other"), "other")
	// /a/b leads to the host folder, as ../../../ goes out of the sysroot
	if err := os.Symlink("../../host", filepath.Join(root, "a/b")); err != nil {
		t.Fatal(err)
	}
	if err := LinkHostFile(root, "../y", "/a/b/x"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(host, "x")); err != nil || !info.IsDir() {
		t.Errorf("the folder of this machine was replaced: %v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(host, "x/precious")); err != nil {
		t.Errorf("a file of this machine was touched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "y/precious")); err == nil {
		t.Errorf("a file of this machine was moved")
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
	var left *LinkSkipped
	if !errors.As(err, &left) || !strings.Contains(left.Why, "dist/sub") {
		t.Fatalf("err = %v, want a *LinkSkipped that names dist/sub", err)
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

func wantSkipped(t *testing.T, err error, why string) {
	t.Helper()
	var left *LinkSkipped
	if !errors.As(err, &left) || !strings.Contains(left.Why, why) {
		t.Fatalf("err = %v, want a *LinkSkipped with %q", err, why)
	}
}

func mustExist(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Stat(p); err != nil {
		t.Errorf("%v", err)
	}
}

// a link to the directory itself: the merge would move it into itself and remove it
func TestLinkHostFileSkipsALinkToItself(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a/x/file"), "f")
	wantSkipped(t, LinkHostFile(root, "x", "/a/x"), "into itself")
	mustExist(t, filepath.Join(root, "a/x/file"))
}

// a link into a subdirectory of the directory: removing the directory would remove the target
func TestLinkHostFileSkipsALinkIntoItsOwnSubdirectory(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a/x/sub/f"), "f")
	wantSkipped(t, LinkHostFile(root, "x/sub", "/a/x"), "into itself")
	mustExist(t, filepath.Join(root, "a/x/sub/f"))
}

// a link to a directory that holds the directory
func TestLinkHostFileSkipsALinkToADirectoryThatHoldsIt(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "p/q/x/file"), "f")
	write(t, filepath.Join(root, "p/other"), "o")
	wantSkipped(t, LinkHostFile(root, "..", "/p/q/x"), "into itself")
	mustExist(t, filepath.Join(root, "p/q/x/file"))
}

// the directory is, through a link of the image, a folder of this machine, and the link leads inside
func TestLinkHostFileSkipsADirectoryThatIsOutsideTheSysroot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "sysroot")
	host := filepath.Join(parent, "host")
	write(t, filepath.Join(host, "x/precious"), "p")
	write(t, filepath.Join(root, "y/file"), "y")
	if err := os.MkdirAll(filepath.Join(root, "a"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../host", filepath.Join(root, "a/b")); err != nil {
		t.Fatal(err)
	}
	// from the folder of this machine, ../sysroot/y is inside the sysroot
	wantSkipped(t, LinkHostFile(root, "../sysroot/y", "/a/b/x"), "outside the package")
	mustExist(t, filepath.Join(host, "x/precious"))
	if _, err := os.Stat(filepath.Join(root, "y/precious")); err == nil {
		t.Errorf("a file of this machine was moved into the sysroot")
	}
}

// what the link leads to does not exist: the reason is given, nothing is moved
func TestLinkHostFileSaysWhenTheTargetIsNotThere(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a/x/file"), "f")
	wantSkipped(t, LinkHostFile(root, "../gone", "/a/x"), "not in the package")
	mustExist(t, filepath.Join(root, "a/x/file"))
}

// a directory that cannot be written (mode 0555) is moved, with its mode
func TestLinkHostFileMovesAReadOnlyDirectory(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "pkg/x/ro/a"), "a")
	if err := os.Chmod(filepath.Join(root, "pkg/x/ro"), 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(root, "store/x/ro"), 0755) })
	write(t, filepath.Join(root, "store/x/other"), "o")
	if err := LinkHostFile(root, "../store/x", "/pkg/x"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "store/x/ro"))
	if err != nil || info.Mode().Perm() != 0555 {
		t.Errorf("mode = %v, %v; want 0555", info, err)
	}
	mustExist(t, filepath.Join(root, "store/x/ro/a"))
}

// a new link or a copy whose directory is, through a link of the image, a folder of this machine
func TestLinkHostFileAndCopyHostFileDoNotWriteThroughALinkOutOfTheSysroot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "sysroot")
	host := filepath.Join(parent, "host")
	if err := os.MkdirAll(host, 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "src/file"), "data")
	// root/a leads to parent/host
	if err := os.Symlink("../host", filepath.Join(root, "a")); err != nil {
		t.Fatal(err)
	}
	wantSkipped(t, LinkHostFile(root, "../src/file", "/a/new"), "outside the package")
	wantSkipped(t, CopyHostFile(root, "/src/file", "/a/copy"), "outside the package")
	entries, _ := os.ReadDir(host)
	if len(entries) != 0 {
		t.Errorf("the folder of this machine got %v", entries)
	}
}

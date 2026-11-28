//go:build !windows

package output

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The manifest may hold secrets, so its mode is set with a call of its own: the mode given to
// WriteFile is cut by the umask of the process, and a umask that removes the owner's bits would
// leave a file the owner cannot read back.
func TestManifestModeIsSetWhateverTheUmask(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	composePath := filepath.Join(dir, "out", "compose.yaml")
	src := filepath.Join(dir, "src")
	put(t, filepath.Join(src, "web"), "b", 0o755)
	// what Write only finds, so that it makes no folder under the umask below
	base := filepath.Join(home, ".ops", "local_packages", "amd64")
	for _, d := range []string{base, filepath.Dir(composePath)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := syscall.Umask(0o400)
	defer syscall.Umask(old)

	_, err := Write(onePlan(src), Options{OpsHome: home, ComposePath: composePath})
	syscall.Umask(old)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(base, "web", "package.manifest"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("manifest mode = %v, want 0600", fi.Mode().Perm())
	}
}

//go:build !windows

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/observe"
)

func TestSpecialFilesAreSkippedWithAWarning(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", dataCompose)
	h.haveAll("w")
	data := h.dataTree()
	if err := syscall.Mkfifo(filepath.Join(data, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.onObserve = func(o observe.Options) error {
		p := h.wantCopy(o.Binds[0], data, "/var/lib/data")
		if _, err := os.Lstat(filepath.Join(p, "pipe")); !os.IsNotExist(err) {
			t.Errorf("the fifo was copied: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(p, "a.txt")); err != nil {
			t.Errorf("a regular file is missing from the copy: %v", err)
		}
		return nil
	}
	h.mustRun(0, "convert")
	if !strings.Contains(h.stderr.String(), "the special file pipe") {
		t.Errorf("no warning: %q", h.stderr)
	}
	if _, err := os.Lstat(filepath.Join(data, "pipe")); err != nil {
		t.Errorf("the original fifo is gone: %v", err)
	}
}

func TestSpecialFileBindIsNotPassed(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n    volumes:\n      - ./pipe:/run/pipe\n")
	h.haveAll("w")
	if err := syscall.Mkfifo(filepath.Join(h.dir, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.mustRun(0, "convert")
	if len(h.observed[0].Binds) != 0 || !strings.Contains(h.stderr.String(), "is a special file") {
		t.Errorf("binds %v, stderr %q", h.observed[0].Binds, h.stderr)
	}
}

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/gen"
	"github.com/AngeloRubens/dock2nano/internal/observe"
	"github.com/AngeloRubens/dock2nano/internal/plan"
)

// wantCopy checks that bind is "<copy>:<target>" with the copy in the
// temporary directory of the test, and not the original.
func (h *harness) wantCopy(bind, orig, target string) string {
	h.t.Helper()
	path, ok := strings.CutSuffix(bind, ":"+target)
	if !ok {
		h.t.Fatalf("bind %q does not end with :%s", bind, target)
	}
	if path == orig || !strings.HasPrefix(path, h.tmp+string(filepath.Separator)) {
		h.t.Fatalf("bind %q: %q is not a copy of %q in %s", bind, path, orig, h.tmp)
	}
	return path
}

// bindPath is the host side of a bind.
func bindPath(bind string) string {
	p, _, _ := strings.Cut(bind, ":")
	return p
}

// snapshot describes a tree: the mode, the content or link of each entry.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			l, _ := os.Readlink(p)
			out[rel] = "link " + l
		case fi.IsDir():
			out[rel] = fmt.Sprintf("dir %v", fi.Mode())
		default:
			b, _ := os.ReadFile(p)
			out[rel] = fmt.Sprintf("file %v %q", fi.Mode(), b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (h *harness) dataTree() string {
	h.t.Helper()
	data := filepath.Join(h.dir, "data")
	h.write("data/a.txt", "alpha")
	h.write("data/sub/b.txt", "beta")
	if err := os.Chmod(filepath.Join(h.dir, "data/sub/b.txt"), 0o640); err != nil {
		h.t.Fatal(err)
	}
	h.write("data/run.sh", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(h.dir, "data/run.sh"), 0o755); err != nil {
		h.t.Fatal(err)
	}
	return data
}

const dataCompose = "services:\n  web:\n    image: w\n    volumes:\n      - ./data:/var/lib/data\n"

func TestBindIsObservedOnACopyAndTheOriginalIsUntouched(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", dataCompose)
	h.haveAll("w")
	data := h.dataTree()
	// links are copied as links; one points out of the tree and must not be followed
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(data, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(data, "rel")); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, data)
	var copyPath string
	var inCopy map[string]string
	h.onObserve = func(o observe.Options) error {
		copyPath = h.wantCopy(o.Binds[0], data, "/var/lib/data")
		inCopy = snapshot(t, copyPath)
		// the program writes, rewrites and deletes
		if err := os.WriteFile(filepath.Join(copyPath, "written"), []byte("w"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(copyPath, "a.txt"), []byte("changed"), 0o644); err != nil {
			return err
		}
		return os.RemoveAll(filepath.Join(copyPath, "sub"))
	}
	h.mustRun(0, "convert")
	if !reflect.DeepEqual(before, inCopy) {
		t.Errorf("the copy differs from the original:\ncopy     %v\noriginal %v", inCopy, before)
	}
	if after := snapshot(t, data); !reflect.DeepEqual(before, after) {
		t.Errorf("the original changed:\nbefore %v\nafter  %v", before, after)
	}
	if got := inCopy["out"]; got != "link "+outside {
		t.Errorf("the link to the outside was not kept as a link: %q", got)
	}
	if got := inCopy["rel"]; got != "link a.txt" {
		t.Errorf("relative link: %q", got)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Errorf("the copy is not removed: %v", err)
	}
}

func TestSingleFileBindIsCopied(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n    volumes:\n      - ./app.conf:/etc/app.conf\n")
	h.haveAll("w")
	orig := h.write("app.conf", "k=v\n")
	h.onObserve = func(o observe.Options) error {
		p := h.wantCopy(o.Binds[0], orig, "/etc/app.conf")
		if b, err := os.ReadFile(p); err != nil || string(b) != "k=v\n" {
			t.Errorf("copy of the file: %q, %v", b, err)
		}
		return os.WriteFile(p, []byte("changed"), 0o644)
	}
	h.mustRun(0, "convert")
	if b, _ := os.ReadFile(orig); string(b) != "k=v\n" {
		t.Errorf("the original file changed: %q", b)
	}
}

func TestBindCopyIsSharedBetweenObservationAndDependency(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", `services:
  db:
    image: d
    volumes:
      - ./data:/data
  web:
    image: w
    depends_on: [db]
    volumes:
      - ./data:/data
`)
	h.haveAll("d", "w")
	data := h.dataTree()
	before := snapshot(t, data)
	h.onObserve = func(o observe.Options) error {
		p := bindPath(o.Binds[0])
		switch o.Image {
		case "d":
			return os.WriteFile(filepath.Join(p, "from-db"), []byte("db"), 0o644)
		case "w":
			// the dependency was started on the copy that the db observation wrote to
			if b, err := os.ReadFile(filepath.Join(p, "from-db")); err != nil || string(b) != "db" {
				t.Errorf("the observation of web does not see the data of db: %q, %v", b, err)
			}
			if left, _ := os.ReadDir(h.tmp); len(left) != 1 {
				t.Errorf("one private directory expected: %v", left)
			}
			entries, _ := os.ReadDir(filepath.Join(h.tmp, mustOnly(t, h.tmp)))
			if len(entries) != 1 {
				t.Errorf("one copy expected: %v", entries)
			}
		}
		return nil
	}
	h.mustRun(0, "convert")
	if len(h.started) != 1 || len(h.observed) != 2 {
		t.Fatalf("started %d, observed %v", len(h.started), h.names())
	}
	a, b, c := h.observed[0].Binds, h.started[0].Binds, h.observed[1].Binds
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(a, c) {
		t.Errorf("binds differ: %v %v %v", a, b, c)
	}
	h.wantCopy(a[0], data, "/data")
	if after := snapshot(t, data); !reflect.DeepEqual(before, after) {
		t.Errorf("the original changed: %v", after)
	}
}

// mustOnly returns the name of the only entry of dir.
func mustOnly(t *testing.T, dir string) string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil || len(es) != 1 {
		t.Fatalf("%s: %v, %v", dir, es, err)
	}
	return es[0].Name()
}

func TestBindCopyIsRemovedOnError(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", dataCompose)
	h.haveAll("w")
	h.dataTree()
	var copyPath string
	h.onObserve = func(o observe.Options) error {
		copyPath = bindPath(o.Binds[0])
		return errors.New("boom")
	}
	h.mustRun(1, "convert")
	if copyPath == "" {
		t.Fatal("no copy was mounted")
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Errorf("the copy is not removed after an error: %v", err)
	}
}

func TestBindCopyIsRemovedOnCancel(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", dataCompose)
	h.haveAll("w")
	h.dataTree()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var copyPath string
	h.onObserve = func(o observe.Options) error {
		copyPath = bindPath(o.Binds[0])
		cancel()
		return ctx.Err()
	}
	if code := h.app.Run(ctx, []string{"convert"}); code != 1 {
		t.Fatalf("exit status %d", code)
	}
	if _, err := os.Stat(copyPath); copyPath == "" || !os.IsNotExist(err) {
		t.Errorf("the copy %q is not removed after a cancel: %v", copyPath, err)
	}
}

func TestLargeBindIsMountedReadOnlyWithAWarning(t *testing.T) {
	tests := []struct {
		name         string
		bytes        int64
		entries      int
		wantReadOnly bool
	}{
		{"within the limits", 1000, 1000, false},
		{"too many entries", 1000, 3, true},
		{"too many bytes", 4, 1000, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldB, oldE := bindCopyMaxBytes, bindCopyMaxEntries
			bindCopyMaxBytes, bindCopyMaxEntries = tt.bytes, tt.entries
			t.Cleanup(func() { bindCopyMaxBytes, bindCopyMaxEntries = oldB, oldE })
			h := newHarness(t)
			h.write("compose.yaml", dataCompose)
			h.haveAll("w")
			data := h.dataTree()
			before := snapshot(t, data)
			h.mustRun(0, "convert")
			got := h.observed[0].Binds
			if len(got) != 1 {
				t.Fatalf("binds %v", got)
			}
			warned := strings.Contains(h.stderr.String(), "web: warning: the bind "+data+" is mounted read-only: too large to copy")
			if tt.wantReadOnly {
				if got[0] != data+":/var/lib/data:ro" || !warned {
					t.Errorf("binds %v, warned %v, stderr %q", got, warned, h.stderr)
				}
				if left, _ := os.ReadDir(h.tmp); len(left) != 0 {
					t.Errorf("a copy was made: %v", left)
				}
			} else {
				h.wantCopy(got[0], data, "/var/lib/data")
				if warned {
					t.Errorf("a warning for a bind that is copied: %q", h.stderr)
				}
			}
			if after := snapshot(t, data); !reflect.DeepEqual(before, after) {
				t.Errorf("the original changed")
			}
		})
	}
}

func TestLargeBindWarningReachesTheReport(t *testing.T) {
	oldE := bindCopyMaxEntries
	bindCopyMaxEntries = 1
	t.Cleanup(func() { bindCopyMaxEntries = oldE })
	h := newHarness(t)
	h.write("compose.yaml", dataCompose)
	h.haveAll("w")
	h.dataTree()
	// the harness observer returns no warnings of its own; the one of the bind is added
	var got []string
	h.app.Generate = func(in gen.Input) (*plan.Plan, error) {
		got = in.Services["web"].Observed.Warnings
		return gen.Generate(in)
	}
	h.mustRun(0, "convert")
	if len(got) != 1 || !strings.Contains(got[0], "mounted read-only: too large to copy") {
		t.Errorf("warnings %v", got)
	}
}

func TestReadOnlyBindIsMountedWithoutACopy(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    image: w\n    volumes:\n      - ./data:/d:ro\n")
	h.haveAll("w")
	data := h.dataTree()
	h.mustRun(0, "convert")
	if want := []string{data + ":/d:ro"}; !reflect.DeepEqual(h.observed[0].Binds, want) {
		t.Errorf("binds %v, want %v", h.observed[0].Binds, want)
	}
	if left, _ := os.ReadDir(h.tmp); len(left) != 0 {
		t.Errorf("a copy was made: %v", left)
	}
}

func TestNetworkIsLabelled(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  one:\n    image: a\n")
	h.haveAll("a")
	h.mustRun(0, "convert")
	if want := [][]string{{"dock2nano=1"}}; !reflect.DeepEqual(h.dock.netLabels, want) {
		t.Errorf("labels %v, want %v", h.dock.netLabels, want)
	}
}

func TestNetworkIsRemovedWhenCreateIsCancelled(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  one:\n    image: a\n")
	h.haveAll("a")
	h.dock.netErr = context.Canceled
	h.mustRun(1, "convert")
	if len(h.dock.netCreated) != 1 || !reflect.DeepEqual(h.dock.netRemoved, h.dock.netCreated) {
		t.Errorf("created %v, removed %v", h.dock.netCreated, h.dock.netRemoved)
	}
	// with a context that is not the cancelled one
	if len(h.dock.removeCtxErr) != 1 || h.dock.removeCtxErr[0] != nil {
		t.Errorf("context of the removal: %v", h.dock.removeCtxErr)
	}
}

// A service that mounts a source read-only reads the copy that another service writes, not the
// original that no one writes to.
func TestReadOnlyBindOfASourceThatIsCopiedReadsTheCopy(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", `services:
  db:
    image: d
    volumes:
      - ./data:/var/lib/data
  app:
    image: a
    volumes:
      - ./data:/data:ro
`)
	h.haveAll("d", "a")
	data := h.dataTree()
	h.mustRun(0, "convert")
	if len(h.observed) != 2 {
		t.Fatalf("observed %v", h.names())
	}
	rw := h.wantCopy(h.observed[0].Binds[0], data, "/var/lib/data")
	ro, ok := strings.CutSuffix(h.observed[1].Binds[0], ":/data:ro")
	if !ok || ro != rw {
		t.Errorf("read-only bind %q, want the copy %q", h.observed[1].Binds[0], rw)
	}
}

// A directory copied with a mode that does not let the owner write cannot be emptied until it is
// writable: the copies are still removed.
func TestBindCopyOfAReadOnlyDirectoryIsRemoved(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", dataCompose)
	h.haveAll("w")
	data := h.dataTree()
	if err := os.Chmod(filepath.Join(data, "sub"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(data, "sub"), 0o755) })
	h.mustRun(0, "convert")
	if left, _ := os.ReadDir(h.tmp); len(left) != 0 {
		t.Errorf("the copies were left: %v", left)
	}
	if strings.Contains(h.stderr.String(), "are not removed") {
		t.Errorf("stderr: %s", h.stderr.String())
	}
}

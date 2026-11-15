package observe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func mustMkdir(t *testing.T, p string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(p, mode); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, data string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestKeepEmptyDirs(t *testing.T) {
	sys := t.TempDir()
	mk := func(p string) { mustMkdir(t, filepath.Join(sys, p), 0o755) }
	mk("var/cache/nginx/client_temp")
	mk("var/cache/nginx/proxy_temp")
	mk("full")
	mustWrite(t, filepath.Join(sys, "full", "f"), "x")
	if err := os.Symlink("full", filepath.Join(sys, "link")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(sys, "file"), "x")
	kept, over, warns, err := keepEmptyDirs(sys, []string{"/var/cache/nginx/client_temp", "/var/cache/nginx/proxy_temp", "/full", "/link", "/file", "/absent"}, 10)
	if err != nil || over != 0 || len(warns) != 0 {
		t.Fatalf("err %v over %d warnings %q", err, over, warns)
	}
	equal(t, "kept", kept, []string{"/var/cache/nginx/client_temp", "/var/cache/nginx/proxy_temp"})
	for _, d := range kept {
		if _, err := os.Stat(filepath.Join(sys, d, ".keep")); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}
	if _, err := os.Stat(filepath.Join(sys, "full", ".keep")); err == nil {
		t.Error("a directory with a file got a .keep")
	}
}

func TestKeepEmptyDirsCap(t *testing.T) {
	sys := t.TempDir()
	var dirs []string
	for i := 0; i < 5; i++ {
		d := fmt.Sprintf("/d%d", i)
		dirs = append(dirs, d)
		mustMkdir(t, filepath.Join(sys, d), 0o755)
	}
	kept, over, _, err := keepEmptyDirs(sys, dirs, 3)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "kept", kept, dirs[:3])
	equal(t, "over", over, 2)
	if _, err := os.Stat(filepath.Join(sys, "d4", ".keep")); err == nil {
		t.Error("a directory over the cap got a .keep")
	}
}

// A relative link of the image that climbs out of the sysroot must not take the .keep outside it.
func TestKeepEmptyDirsStaysInsideTheSysroot(t *testing.T) {
	sys := t.TempDir()
	outside := t.TempDir()
	mustMkdir(t, filepath.Join(outside, "victim"), 0o755)
	rel, err := filepath.Rel(sys, outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, filepath.Join(sys, "esc")); err != nil {
		t.Fatal(err)
	}
	kept, _, _, err := keepEmptyDirs(sys, []string{"/esc/victim"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 0 {
		t.Errorf("kept %q through a link that leaves the sysroot", kept)
	}
	if _, err := os.Stat(filepath.Join(outside, "victim", ".keep")); err == nil {
		t.Error("a .keep was written outside the sysroot")
	}
}

func TestKeepEmptyDirsWarnsAndGoesOn(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads and writes any directory")
	}
	sys := t.TempDir()
	mustMkdir(t, filepath.Join(sys, "a-unreadable"), 0o755)
	mustMkdir(t, filepath.Join(sys, "b-readonly"), 0o755)
	mustMkdir(t, filepath.Join(sys, "c-fine"), 0o755)
	if err := os.Chmod(filepath.Join(sys, "a-unreadable"), 0o000); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(sys, "b-readonly"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Chmod(filepath.Join(sys, "a-unreadable"), 0o755)
		os.Chmod(filepath.Join(sys, "b-readonly"), 0o755)
	})
	kept, _, warns, err := keepEmptyDirs(sys, []string{"/a-unreadable", "/b-readonly", "/c-fine"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "kept", kept, []string{"/c-fine"})
	if len(warns) != 2 || !strings.Contains(warns[0], "/a-unreadable") || !strings.Contains(warns[1], "/b-readonly") {
		t.Errorf("warnings %q", warns)
	}
}

func TestObserveCarriesTheDirectoriesTheProgramMakes(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt += `11 mkdir("/var/cache/app/made", 0700) = 0` + "\n" +
		`11 mkdir("/var/cache/app/there", 0700) = -1 EEXIST (File exists)` + "\n" +
		`11 mkdirat(AT_FDCWD, "/var/cache/app/full", 0700) = -1 EEXIST (File exists)` + "\n" +
		`11 mkdir("/var/cache/app/gone", 0700) = -1 EEXIST (File exists)` + "\n" +
		`11 mkdir("/etc/app.d", 0700) = 0` + "\n" +
		`11 mkdir("/proc/x", 0700) = 0` + "\n"
	s.d.files["/var/cache/app/made"] = entry{dir: true}
	s.d.files["/var/cache/app/there"] = entry{dir: true}
	s.d.files["/var/cache/app/full"] = entry{dir: true}
	s.d.files["/var/cache/app/full/f"] = entry{data: "x"}
	s.d.files["/etc/app.d"] = entry{dir: true}
	s.d.traceTxt += `11 openat(AT_FDCWD, "/var/cache/app/full/f", O_RDONLY) = 8` + "\n"
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "empty dirs", res.EmptyDirs, []string{"/etc/app.d", "/var/cache/app/made", "/var/cache/app/there"})
	files := tree(t, res.Sysroot)
	for _, want := range []string{"var/cache/app/made/.keep", "var/cache/app/there/.keep", "etc/app.d/.keep"} {
		if !slices.Contains(files, want) {
			t.Errorf("%s is not in the package: %v", want, files)
		}
	}
	if slices.Contains(files, "var/cache/app/full/.keep") {
		t.Error("a directory with a carried file got a .keep")
	}
	if slices.Contains(res.ConfigurationFiles, "/etc/app.d/.keep") {
		t.Errorf("our .keep is listed as a configuration file: %q", res.ConfigurationFiles)
	}
}

func TestObserveEmptyDirsCapIsReported(t *testing.T) {
	s := newScene(t)
	for i := 0; i < maxEmptyDirs+3; i++ {
		p := fmt.Sprintf("/d/e%03d", i)
		s.d.traceTxt += fmt.Sprintf(`11 mkdir("%s", 0700) = 0`+"\n", p)
		s.d.files[p] = entry{dir: true}
	}
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "carried", len(res.EmptyDirs), maxEmptyDirs)
	want := fmt.Sprintf("3 more empty directories the program tried to create were not carried (at most %d are)", maxEmptyDirs)
	if !slices.Contains(res.Warnings, want) {
		t.Errorf("warnings %q lack %q", res.Warnings, want)
	}
}

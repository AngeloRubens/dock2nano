package observe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestKeepEmptyDirs(t *testing.T) {
	sys := t.TempDir()
	mk := func(p string) { os.MkdirAll(filepath.Join(sys, p), 0o755) }
	mk("var/cache/nginx/client_temp")
	mk("var/cache/nginx/proxy_temp")
	mk("full")
	os.WriteFile(filepath.Join(sys, "full", "f"), []byte("x"), 0o644)
	os.Symlink("full", filepath.Join(sys, "link"))
	os.WriteFile(filepath.Join(sys, "file"), []byte("x"), 0o644)
	kept, over, err := keepEmptyDirs(sys, []string{"/var/cache/nginx/client_temp", "/var/cache/nginx/proxy_temp", "/full", "/link", "/file", "/absent"}, 10)
	if err != nil || over != 0 {
		t.Fatalf("err %v over %d", err, over)
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
		os.MkdirAll(filepath.Join(sys, d), 0o755)
	}
	kept, over, err := keepEmptyDirs(sys, dirs, 3)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "kept", kept, dirs[:3])
	equal(t, "over", over, 2)
	if _, err := os.Stat(filepath.Join(sys, "d4", ".keep")); err == nil {
		t.Error("a directory over the cap got a .keep")
	}
}

func TestObserveCarriesTheDirectoriesTheProgramMakes(t *testing.T) {
	s := newScene(t)
	s.d.traceTxt += `11 mkdir("/var/cache/app/made", 0700) = 0` + "\n" +
		`11 mkdir("/var/cache/app/there", 0700) = -1 EEXIST (File exists)` + "\n" +
		`11 mkdirat(AT_FDCWD, "/var/cache/app/full", 0700) = -1 EEXIST (File exists)` + "\n" +
		`11 mkdir("/var/cache/app/gone", 0700) = -1 EEXIST (File exists)` + "\n" +
		`11 mkdir("/proc/x", 0700) = 0` + "\n"
	s.d.files["/var/cache/app/made"] = entry{dir: true}
	s.d.files["/var/cache/app/there"] = entry{dir: true}
	s.d.files["/var/cache/app/full"] = entry{dir: true}
	s.d.files["/var/cache/app/full/f"] = entry{data: "x"}
	s.d.traceTxt += `11 openat(AT_FDCWD, "/var/cache/app/full/f", O_RDONLY) = 8` + "\n"
	res, err := s.observe(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "empty dirs", res.EmptyDirs, []string{"/var/cache/app/made", "/var/cache/app/there"})
	files := tree(t, res.Sysroot)
	for _, want := range []string{"var/cache/app/made/.keep", "var/cache/app/there/.keep"} {
		if !slices.Contains(files, want) {
			t.Errorf("%s is not in the package: %v", want, files)
		}
	}
	if slices.Contains(files, "var/cache/app/full/.keep") {
		t.Error("a directory with a carried file got a .keep")
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

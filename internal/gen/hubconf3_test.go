package gen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceInRootDoesNotTripOverFilesWithAFixedTempName(t *testing.T) {
	sys := hubSysroot(t, map[string]string{
		"/etc/n.conf":                 "original\n",
		"/etc/.n.conf.dock2nano-":     "decoy",
		"/etc/.n.conf.dock2nano-tmp":  "decoy",
		"/etc/.n.conf.dock2nano-temp": "decoy",
	})
	root, err := os.OpenRoot(sys)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := replaceInRoot(root, "etc/n.conf", []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b := hubConfOf(t, sys, "etc/n.conf"); b != "new\n" {
		t.Errorf("%q", b)
	}
	if left, _ := os.ReadDir(filepath.Join(sys, "etc")); len(left) != 4 {
		t.Errorf("the decoys must stay and no temporary file be left: %v", left)
	}
}

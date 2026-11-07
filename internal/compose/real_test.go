package compose

import (
	"path/filepath"
	"testing"
)

func TestRealComposes(t *testing.T) {
	files, err := filepath.Glob("testdata/real/*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no files: %v", err)
	}
	ok := 0
	for _, f := range files {
		p, err := ParseFile(f)
		if err != nil {
			t.Logf("ROW %s FAIL %v", filepath.Base(f), err)
			continue
		}
		ok++
		for _, s := range p.Services {
			t.Logf("ROW %s OK service=%s unknown=%v", filepath.Base(f), s.Name, s.Unknown)
		}
		if len(p.Unknown) > 0 {
			t.Logf("ROW %s OK toplevel-unknown=%v", filepath.Base(f), p.Unknown)
		}
	}
	t.Logf("ROW SUMMARY parsed %d of %d", ok, len(files))
}

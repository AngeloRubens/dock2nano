package trace

import (
	"slices"
	"testing"
)

func TestCreatedPathsLeavesOutWhatIsNeverCarried(t *testing.T) {
	raw := []byte("1     openat(AT_FDCWD, \"/dev/stderr\", O_WRONLY|O_CREAT|O_APPEND, 0666) = 3\n" +
		"1     openat(AT_FDCWD, \"/proc/self/oom_score_adj\", O_WRONLY|O_CREAT, 0666) = 4\n" +
		"1     openat(AT_FDCWD, \"/tmp/app.pid\", O_WRONLY|O_CREAT|O_TRUNC, 0644) = 5\n")
	got := CreatedPaths(raw, "/", 0)
	if !slices.Equal(got, []string{"/tmp/app.pid"}) {
		t.Errorf("created %q", got)
	}
}

func TestCreatedPathsOpenat2FlagPositions(t *testing.T) {
	raw := []byte("1     openat2(AT_FDCWD, \"/tmp/first\", {flags=O_CREAT|O_WRONLY, mode=0644, resolve=0}, 24) = 3\n" +
		"1     openat2(AT_FDCWD, \"/tmp/alone\", {flags=O_CREAT}, 24) = 4\n" +
		"1     openat2(AT_FDCWD, \"/tmp/last\", {flags=O_WRONLY|O_CREAT, mode=0644}, 24) = 5\n" +
		"1     openat2(AT_FDCWD, \"/tmp/read\", {flags=O_RDONLY, resolve=0}, 24) = 6\n" +
		"1     openat2(AT_FDCWD, \"/tmp/noflags\", {mode=0644}, 24) = 7\n")
	got := CreatedPaths(raw, "/", 0)
	want := []string{"/tmp/alone", "/tmp/first", "/tmp/last"}
	if !slices.Equal(got, want) {
		t.Errorf("created %q, want %q", got, want)
	}
}

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

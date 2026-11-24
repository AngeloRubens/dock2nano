package cmd

import (
	"errors"
	"strings"
	"testing"
)

const missingFileOut = `docker build --file /tmp/w/echo-server/Dockerfile --tag t --quiet -- /tmp/w/echo-server: exit status 1: Dockerfile:3
--------------------
   1 |     FROM scratch
   2 |
   3 | >>> COPY artifacts/build/release/linux/amd64/echo-server /bin/echo-server
--------------------
ERROR: failed to build: failed to solve: failed to compute cache key: failed to calculate checksum of ref abc::def: "/artifacts/build/release/linux/amd64/echo-server": not found`

const runFailOut = `docker build --file /d/Dockerfile --tag t --quiet -- /d: exit status 1: Dockerfile:7
>>> RUN curl -L https://example.com/x.tar.gz | tar xz
ERROR: failed to build: failed to solve: process "/bin/sh -c curl -L https://example.com/x.tar.gz | tar xz" did not complete successfully: exit code: 1`

func TestBuildHintMissingFile(t *testing.T) {
	h := buildHint(missingFileOut)
	for _, w := range []string{"/artifacts/build/release/linux/amd64/echo-server", "does not exist", "make, goreleaser, npm build", "convert again"} {
		if !strings.Contains(h, w) {
			t.Errorf("hint %q lacks %q", h, w)
		}
	}
}

func TestBuildHintFailedRun(t *testing.T) {
	h := buildHint(runFailOut)
	if !strings.Contains(h, "run docker build yourself to see the same error") {
		t.Errorf("hint %q", h)
	}
}

// the command of a RUN step can hold escaped double quotes (real output of the redlib build, run 45439008280)
const runFailEscapedQuotesOut = `ERROR: failed to build: failed to solve: process "/bin/sh -c curl -L \"https://example.com/redlib.tar.gz\" |     tar xz -C /usr/local/bin/" did not complete successfully: exit code: 1`

func TestBuildHintFailedRunWithEscapedQuotes(t *testing.T) {
	if h := buildHint(runFailEscapedQuotesOut); !strings.Contains(h, "run docker build yourself to see the same error") {
		t.Errorf("hint %q", h)
	}
}

func TestBuildHintOther(t *testing.T) {
	for _, s := range []string{"", "ERROR: failed to solve: no such file", `sh: foo: not found`, `process "/bin/sh -c x" exited`} {
		if h := buildHint(s); h != "" {
			t.Errorf("%q: unexpected hint %q", s, h)
		}
	}
}

func TestBuildErrorKeepsDockerTextAndAddsHint(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    build: .\n")
	h.dock.buildErr = errors.New(missingFileOut)
	h.mustRun(1, "convert")
	out := h.stderr.String()
	i := strings.Index(out, `"/artifacts/build/release/linux/amd64/echo-server": not found`)
	j := strings.Index(out, "hint: the Dockerfile copies")
	if i < 0 || j < 0 || j < i || !strings.Contains(out, "service web: ") {
		t.Errorf("docker text must stay and the hint come after it:\n%s", out)
	}
}

func TestBuildErrorWithoutHintIsUnchanged(t *testing.T) {
	h := newHarness(t)
	h.write("compose.yaml", "services:\n  web:\n    build: .\n")
	h.dock.buildErr = errors.New("no space")
	h.mustRun(1, "convert")
	if strings.Contains(h.stderr.String(), "hint:") {
		t.Errorf("stderr %q", h.stderr)
	}
}

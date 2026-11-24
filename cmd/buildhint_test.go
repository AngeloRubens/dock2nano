package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/AngeloRubens/dock2nano/internal/docker"
)

const missingFileOut = `docker build --file /tmp/w/echo-server/Dockerfile --tag t --quiet -- /tmp/w/echo-server: exit status 1: Dockerfile:3
--------------------
   1 |     FROM scratch
   2 |
   3 | >>> COPY artifacts/build/release/linux/amd64/echo-server /bin/echo-server
--------------------
ERROR: failed to build: failed to solve: failed to compute cache key: failed to calculate checksum of ref abc::def: "/artifacts/build/release/linux/amd64/echo-server": not found`

const runFailOut = `docker build --file /d/Dockerfile --tag t --quiet -- /d: exit status 1: Dockerfile:7
--------------------
   6 |     WORKDIR /w
   7 | >>> RUN curl -L https://example.com/x.tar.gz | tar xz
--------------------
ERROR: failed to build: failed to solve: process "/bin/sh -c curl -L https://example.com/x.tar.gz | tar xz" did not complete successfully: exit code: 1`

func TestBuildHintMissingFile(t *testing.T) {
	h := buildHint(missingFileOut)
	for _, w := range []string{"/artifacts/build/release/linux/amd64/echo-server", "is not in the build context (it does not exist, or .dockerignore excludes it)", "make, goreleaser, npm build", "convert again", `a build argument used in the path is empty (a "--" or "//" in the path is a sign)`} {
		if !strings.Contains(h, w) {
			t.Errorf("hint %q lacks %q", h, w)
		}
	}
}

func TestBuildHintFailedRun(t *testing.T) {
	h := buildHint(runFailOut)
	if !strings.Contains(h, "an ARG without a default that is not given") {
		t.Errorf("hint %q", h)
	}
	if !strings.Contains(h, "run docker build without --quiet to see the output of the step, which is not shown here") {
		t.Errorf("hint %q", h)
	}
}

// the command of a RUN step can hold escaped double quotes (real output of the redlib build, run 45439008280)
const runFailEscapedQuotesOut = `ERROR: failed to build: failed to solve: process "/bin/sh -c curl -L \"https://example.com/redlib.tar.gz\" |     tar xz -C /usr/local/bin/" did not complete successfully: exit code: 1`

func TestBuildHintFailedRunWithEscapedQuotes(t *testing.T) {
	if h := buildHint(runFailEscapedQuotesOut); !strings.Contains(h, "run docker build without --quiet to see the output of the step, which is not shown here") {
		t.Errorf("hint %q", h)
	}
}

// A COPY --from that fails prints the same checksum line as a COPY from the
// context, so the hint must not blame the build context. On the old code the
// hint blames the context: "has no /app/x" and the "earlier stage" text fail.
const copyFromOut = `docker build --file /tmp/w/echo-server/Dockerfile --tag t --quiet -- /tmp/w/echo-server: exit status 1: Dockerfile:3
--------------------
   1 |     FROM scratch
   2 |
   3 | >>> COPY --from=builder /app/x /x
--------------------
ERROR: failed to build: failed to solve: failed to compute cache key: failed to calculate checksum of ref abc::def: "/app/x": not found`

func TestBuildHintCopyFrom(t *testing.T) {
	h := buildHint(copyFromOut)
	for _, w := range []string{"/app/x", "COPY --from", "has no /app/x", "check the path in that stage or image"} {
		if !strings.Contains(h, w) {
			t.Errorf("hint %q lacks %q", h, w)
		}
	}
	if strings.Contains(h, "build context") || strings.Contains(h, "goreleaser") {
		t.Errorf("hint %q blames the build context", h)
	}
}

// the older BuildKit form of the missing source line; on the old code no hint
// is returned, so the first assertion fails
const missingOldOut = `ERROR: failed to solve: failed to compute cache key: "/dist/app" not found: not found`

func TestBuildHintMissingFileOldForm(t *testing.T) {
	if h := buildHint(missingOldOut); !strings.Contains(h, `"/dist/app"`) || !strings.Contains(h, "is not in the build context") {
		t.Errorf("hint %q", h)
	}
}

// TestBuildHintOther and TestBuildErrorWithoutHintIsUnchanged are negative
// guards: they pass on the base by design (no hint existed there). The
// coordinator runs finer revert checks on the positive tests.
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

const errTail = `ERROR: failed to build: failed to solve: failed to compute cache key: failed to calculate checksum of ref abc::def: "/app/x": not found`

func hintFor(instr string) string {
	return buildHint("Dockerfile:8\n--------------------\n" + instr + "\n--------------------\n" + errTail)
}

// docker marks every line of a multi-line instruction; --from on a
// continuation line must still give the --from hint (fails on old code).
func TestBuildHintCopyFromContinuationLine(t *testing.T) {
	h := hintFor("   7 | >>> COPY --chown=a:b \\\n   8 | >>>     --from=builder /app/x /x")
	if !strings.Contains(h, "COPY --from") || strings.Contains(h, "build context") {
		t.Errorf("hint %q", h)
	}
}

func TestBuildHintCopyFromAfterOtherOption(t *testing.T) {
	h := hintFor("   3 | >>> COPY --chmod=755 --from=builder /a /a")
	if !strings.Contains(h, "COPY --from") || strings.Contains(h, "build context") {
		t.Errorf("hint %q", h)
	}
}

// a context line (not marked) with --from must not decide the hint
func TestBuildHintCopyFromOnContextLineOnly(t *testing.T) {
	h := hintFor("   2 |     COPY --from=builder /a /a\n   3 | >>> COPY dist/app /app")
	if !strings.Contains(h, "build context") || strings.Contains(h, "COPY --from") {
		t.Errorf("hint %q", h)
	}
}

// ExitError shows only the last 1 KiB of stderr; the hint must still see the
// ">>>" line that the tail cuts (fails on old code: blames the context).
func TestBuildErrorHintReadsFullStderr(t *testing.T) {
	stderr := "Dockerfile:3\n   3 | >>> COPY --from=builder /app/x /x\n" + strings.Repeat("x\n", 1500) + errTail
	err := &docker.ExitError{Args: []string{"build"}, Code: 1, Stderr: stderr}
	if strings.Contains(err.Error(), ">>>") {
		t.Fatalf("test setup: the tail should cut the >>> line")
	}
	out := withBuildHint(err).Error()
	if !strings.Contains(out, "COPY --from") || strings.Contains(out, "build context") {
		t.Errorf("out %q", out)
	}
}

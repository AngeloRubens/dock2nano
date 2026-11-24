package cmd

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/AngeloRubens/dock2nano/internal/docker"
)

// missingSource matches the line docker prints when a COPY or ADD source is
// not in the build context: the checksum or cache key step names the path
// and says it was not found.
var missingSource = regexp.MustCompile(`(?:failed to calculate checksum|failed to compute cache key)[^\n]*"([^"\n]+)": not found`)

// missingSourceOld matches the older BuildKit form of the same line, where the
// quoted path is followed by "not found: not found".
var missingSourceOld = regexp.MustCompile(`failed to compute cache key: "([^"\n]+)" not found: not found`)

// markedLine matches a line that docker marks as part of the failing
// instruction: an optional "NN |" and then ">>> ". Every line of a
// multi-line instruction is marked.
var markedLine = regexp.MustCompile(`^\s*(?:\d+\s*\|\s*)?>>>\s?(.*)$`)

// copyFrom matches the failing instruction, joined on one line, when it is a
// COPY that takes its files from a stage or image.
var copyFrom = regexp.MustCompile(`(?i)^COPY\b.*--from=`)

// failingCopyFrom reports whether the failing instruction is a COPY with
// --from. It joins the consecutive ">>>" lines of a block (a continuation
// backslash at the end of a line is dropped) and matches each joined block.
// Lines that are not marked, such as context lines, are never read.
func failingCopyFrom(text string) bool {
	var cur []string
	flush := func() bool {
		if len(cur) == 0 {
			return false
		}
		joined := strings.Join(cur, " ")
		cur = nil
		return copyFrom.MatchString(strings.TrimSpace(joined))
	}
	for _, line := range strings.Split(text, "\n") {
		m := markedLine.FindStringSubmatch(line)
		if m == nil {
			if flush() {
				return true
			}
			continue
		}
		cur = append(cur, strings.TrimSpace(strings.TrimSuffix(strings.TrimRight(m[1], " \t\r"), "\\")))
	}
	return flush()
}

// failedRun matches the line docker prints when a RUN step ends with an error.
var failedRun = regexp.MustCompile(`process ".*" did not complete successfully`)

// Only the text of the error is read. docker.ExitError shows the last 1 KiB of
// stderr in its message, which can cut the ">>>" lines; withBuildHint therefore
// reads the whole stderr kept by the runner (64 KiB) as well.
//
// buildHint reads the text of a failed docker build and returns a short hint
// for the two causes that are common and that the user can act on, or "" for
// any other text. It never replaces docker's text: the caller prints it below.
func buildHint(text string) string {
	m := missingSource.FindStringSubmatch(text)
	if m == nil {
		m = missingSourceOld.FindStringSubmatch(text)
	}
	if m != nil {
		if failingCopyFrom(text) {
			return fmt.Sprintf("hint: the Dockerfile copies %q with COPY --from: the stage or image named in --from has no %s: "+
				"check the path in that stage or image.", m[1], m[1])
		}
		return fmt.Sprintf("hint: the Dockerfile copies %q and it is not in the build context "+
			"(it does not exist, or .dockerignore excludes it). "+
			"It is probably made by a step that runs before docker build (make, goreleaser, npm build): "+
			"run that step first, then convert again. "+
			"Another cause: a build argument used in the path is empty (a \"--\" or \"//\" in the path is a sign).", m[1])
	}
	if failedRun.MatchString(text) {
		return "hint: a RUN step of the Dockerfile fails, so the build itself fails: " +
			"run docker build without --quiet to see the output of the step, which is not shown here; " +
			"a common cause is an ARG without a default that is not given."
	}
	return ""
}

// withBuildHint adds the hint, when there is one, on a line below the error.
func withBuildHint(err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	var ee *docker.ExitError
	if errors.As(err, &ee) {
		text += "\n" + ee.Stderr
	}
	h := buildHint(text)
	if h == "" {
		return err
	}
	return fmt.Errorf("%w\n%s", err, h)
}

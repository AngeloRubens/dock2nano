package cmd

import (
	"fmt"
	"regexp"
	"strings"
)

// missingSource matches the line docker prints when a COPY or ADD source is
// not in the build context: the checksum or cache key step names the path
// and says it was not found.
var missingSource = regexp.MustCompile(`(?:failed to calculate checksum|failed to compute cache key)[^\n]*"([^"\n]+)": not found`)

// failedRun matches the line docker prints when a RUN step ends with an error.
var failedRun = regexp.MustCompile(`process ".*" did not complete successfully`)

// buildHint reads the text of a failed docker build and returns a short hint
// for the two causes that are common and that the user can act on, or "" for
// any other text. It never replaces docker's text: the caller prints it below.
func buildHint(text string) string {
	if m := missingSource.FindStringSubmatch(text); m != nil {
		return fmt.Sprintf("hint: the Dockerfile copies %q from the build context and that file does not exist. "+
			"It is probably made by a step that runs before docker build (make, goreleaser, npm build): "+
			"run that step first, then convert again.", strings.TrimSpace(m[1]))
	}
	if failedRun.MatchString(text) {
		return "hint: a RUN step of the Dockerfile fails, so the build itself fails: " +
			"run docker build yourself to see the same error."
	}
	return ""
}

// withBuildHint adds the hint, when there is one, on a line below the error.
func withBuildHint(err error) error {
	if err == nil {
		return nil
	}
	h := buildHint(err.Error())
	if h == "" {
		return err
	}
	return fmt.Errorf("%w\n%s", err, h)
}

package cmd

import (
	"fmt"
	"regexp"
)

// missingSource matches the line docker prints when a COPY or ADD source is
// not in the build context: the checksum or cache key step names the path
// and says it was not found.
var missingSource = regexp.MustCompile(`(?:failed to calculate checksum|failed to compute cache key)[^\n]*"([^"\n]+)": not found`)

// missingSourceOld matches the older BuildKit form of the same line, where the
// quoted path is followed by "not found: not found".
var missingSourceOld = regexp.MustCompile(`failed to compute cache key: "([^"\n]+)" not found: not found`)

// copyFrom matches the failing instruction that docker prints on a ">>> "
// line when it is a COPY or ADD that takes its files from a stage or image.
var copyFrom = regexp.MustCompile(`(?mi)^\s*>>>\s+(?:COPY|ADD)\s[^\n]*--from=`)

// failedRun matches the line docker prints when a RUN step ends with an error.
var failedRun = regexp.MustCompile(`process ".*" did not complete successfully`)

// buildHint reads the text of a failed docker build and returns a short hint
// for the two causes that are common and that the user can act on, or "" for
// any other text. It never replaces docker's text: the caller prints it below.
func buildHint(text string) string {
	m := missingSource.FindStringSubmatch(text)
	if m == nil {
		m = missingSourceOld.FindStringSubmatch(text)
	}
	if m != nil {
		if copyFrom.MatchString(text) {
			return fmt.Sprintf("hint: the Dockerfile copies %q with COPY --from: the stage or image named in --from has no %s: "+
				"check the path the earlier stage writes.", m[1], m[1])
		}
		return fmt.Sprintf("hint: the Dockerfile copies %q and it is not in the build context "+
			"(it does not exist, or .dockerignore excludes it). "+
			"It is probably made by a step that runs before docker build (make, goreleaser, npm build): "+
			"run that step first, then convert again.", m[1])
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
	h := buildHint(err.Error())
	if h == "" {
		return err
	}
	return fmt.Errorf("%w\n%s", err, h)
}

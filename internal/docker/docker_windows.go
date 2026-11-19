//go:build windows

package docker

import "os/exec"

// setupCancel makes the command stoppable on Windows, where there are no
// process groups and no SIGTERM: when the context is done, the process of the
// command is killed (its children are not). Windows is not a supported host;
// this file only lets the package build.
func setupCancel(cmd *exec.Cmd) (stop func()) {
	cmd.WaitDelay = waitDelay
	cmd.Cancel = func() error { return cmd.Process.Kill() }
	return func() {}
}

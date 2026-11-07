//go:build !windows

package docker

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// setupCancel makes the command stoppable. The command gets its own process
// group; when the context is done the group gets SIGTERM (docker forwards it
// to its children), and the group is killed if the command or a child that
// holds the pipes is still there waitDelay later. The returned function
// cancels the pending kill.
func setupCancel(cmd *exec.Cmd) (stop func()) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = waitDelay
	var timer *time.Timer
	cmd.Cancel = func() error {
		pid := cmd.Process.Pid
		timer = time.AfterFunc(waitDelay, func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
		if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	return func() {
		if timer != nil {
			timer.Stop()
		}
	}
}

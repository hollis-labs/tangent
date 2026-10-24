//go:build unix

package artifacts

import (
	"os/exec"
	"syscall"
	"time"
)

// confine puts the child in its own process group and, on timeout, kills the whole group, so a
// renderer that spawned helpers cannot outlive the request.
func confine(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = time.Second
}

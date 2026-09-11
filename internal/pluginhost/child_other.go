//go:build !unix

package pluginhost

import (
	"os"
	"os/exec"
)

// configureProcessGroup is a no-op where process groups are not available.
// The child is still spawned and still reaped on its own stdin EOF; what is
// lost is the ability to reach a grandchild with a signal.
func configureProcessGroup(*exec.Cmd) {}

// killProcessGroup falls back to killing the one process. A plugin runtime that
// forks leaves grandchildren behind here, which is a real limitation of this
// platform rather than of the design.
func killProcessGroup(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}

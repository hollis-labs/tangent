//go:build unix

package pluginhost

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup puts a spawned plugin in a fresh process group so
// killProcessGroup can reach anything it forks.
//
// A plugin runtime that spawns helpers — node, python, a shell wrapper — leaves
// grandchildren a SIGKILL to the child's own pid does not touch, and a
// grandchild holding the stdout pipe open is a child this host will wait on and
// never see exit. Nanite found that as its audit finding 07; copying the answer
// is cheaper than rediscovering it.
func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup SIGKILLs the group led by pid. The negative pid is what
// makes it the group rather than the one process.
func killProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}

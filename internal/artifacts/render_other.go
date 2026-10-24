//go:build !unix

package artifacts

import (
	"os/exec"
	"time"
)

// confine bounds how long Run waits for the child's pipes after a timeout.
func confine(cmd *exec.Cmd) { cmd.WaitDelay = time.Second }

//go:build !windows

package server

import (
	"os/exec"
	"syscall"
)

// terminateProcess is the Unix version: SIGTERM gives the process a
// chance to flush logs / clean up. The caller falls back to Kill if
// the process doesn't exit within the grace period.
func terminateProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(syscall.SIGTERM)
}

//go:build windows

package server

import "os/exec"

// terminateProcess is the Windows version: TaskKill is the only thing
// that can gracefully terminate a child tree (the dedicated server may
// spawn its own subprocesses).
func terminateProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// /T = terminate child tree, /F = force. There's no clean "polite
	// stop" on Windows the way SIGTERM works on Unix.
	return exec.Command("taskkill", "/T", "/F", "/PID", itoa(cmd.Process.Pid)).Run()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

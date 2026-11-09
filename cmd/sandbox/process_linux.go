package main

import (
	"os"
	"os/exec"
	"syscall"
)

// dumpable=0 prevents the same-UID contestant from reading supervisor environ,
// memory or /proc/1/fd and stealing its per-container signing key.
func protectSupervisor() error {
	_, _, err := syscall.RawSyscall6(syscall.SYS_PRCTL, 4, 0, 0, 0, 0, 0)
	if err != 0 {
		return err
	}
	return nil
}
func prepareProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
func peakMemory(state *os.ProcessState) (int64, bool) {
	usage, ok := state.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0, false
	}
	return usage.Maxrss, true
}
func processCode(state *os.ProcessState) int {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return state.ExitCode()
}

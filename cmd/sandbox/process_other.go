//go:build !linux

package main

import (
	"errors"
	"os"
	"os/exec"
)

func protectSupervisor() error                  { return errors.New("sandbox requires Linux") }
func prepareProcess(*exec.Cmd)                  {}
func peakMemory(*os.ProcessState) (int64, bool) { return 0, false }
func processCode(state *os.ProcessState) int    { return state.ExitCode() }

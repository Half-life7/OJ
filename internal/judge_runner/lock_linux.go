package judge_runner

import (
	"os"
	"syscall"
)

func lockJournal(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
func syncJournal(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return ErrSandbox
	}
	defer f.Close()
	if f.Sync() != nil {
		return ErrSandbox
	}
	return nil
}

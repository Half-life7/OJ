//go:build !linux

package judge_runner

import "os"

// Production Runner requires Linux; other platforms can exercise journal tests.
func lockJournal(*os.File) error { return nil }
func syncJournal(*os.Root) error { return nil }

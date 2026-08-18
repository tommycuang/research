//go:build !windows

package sink

import (
	"os"
	"syscall"
)

func dedupeLockSupported() bool {
	return true
}

func withDedupeLock(path string, operation func() error) error {
	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	return operation()
}

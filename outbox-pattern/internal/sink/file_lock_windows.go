//go:build windows

package sink

import "errors"

func dedupeLockSupported() bool {
	return false
}

func withDedupeLock(_ string, _ func() error) error {
	return errors.New("dedupe ledger locking is unsupported on Windows")
}

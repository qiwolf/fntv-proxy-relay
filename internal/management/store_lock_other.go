//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package management

import (
	"errors"
	"os"
)

func openStoreLock(string) (*os.File, error) {
	return nil, errors.New("secure document storage unsupported on this platform")
}
func openStoreRead(string) (*os.File, error) {
	return nil, errors.New("secure document storage unsupported on this platform")
}
func lockStore(*os.File) error {
	return errors.New("secure document storage unsupported on this platform")
}
func unlockStore(*os.File) error { return nil }
func tryLockStore(*os.File) error {
	return errors.New("secure document storage unsupported on this platform")
}

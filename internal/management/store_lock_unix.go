//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package management

import (
	"golang.org/x/sys/unix"
	"os"
)

func openStoreLock(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	i, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !i.Mode().IsRegular() {
		f.Close()
		return nil, os.ErrPermission
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
func openStoreRead(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
func lockStore(f *os.File) error    { return unix.Flock(int(f.Fd()), unix.LOCK_EX) }
func tryLockStore(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func unlockStore(f *os.File) error  { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }

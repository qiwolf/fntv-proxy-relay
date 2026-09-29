package management

import (
	"errors"
	"os"
	"path/filepath"
)

// AcquireManagerLease prevents two supervisors from controlling the same state.
// Keep the returned file open for the entire process lifetime.
func AcquireManagerLease(dir string) (*os.File, error) {
	if err := privateDirectory(dir); err != nil {
		return nil, err
	}
	f, err := openStoreLock(filepath.Join(dir, ".manager.lock"))
	if err != nil {
		return nil, err
	}
	if err = tryLockStore(f); err != nil {
		f.Close()
		return nil, errors.New("another manager owns this data directory")
	}
	return f, nil
}

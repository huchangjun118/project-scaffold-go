package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockFileName is created in the workspace root for the duration of a
// generation. The kernel releases the flock automatically if the process
// dies, so stale locks never block recovery.
const lockFileName = ".scaffold.lock"

// workspaceLock guards a target workspace against concurrent generations.
type workspaceLock struct {
	file *os.File
	path string
}

// acquireLock obtains an exclusive, non-blocking lock on the workspace.
// It fails immediately (rather than waiting) when another generation is
// running.
func acquireLock(workspace string) (*workspaceLock, error) {
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return nil, fmt.Errorf("%w: cannot create workspace %s: %v", ErrPermission, workspace, err)
	}
	path := filepath.Join(workspace, lockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot open lock file %s: %v", ErrPermission, path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("%w: %s", ErrWorkspaceLocked, path)
		}
		return nil, fmt.Errorf("scaffold: cannot lock workspace %s: %w", workspace, err)
	}
	return &workspaceLock{file: f, path: path}, nil
}

// Release releases the lock and removes the lock file.
func (l *workspaceLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

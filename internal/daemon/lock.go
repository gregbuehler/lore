package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/gregbuehler/lore/internal/store"
)

// indexLock is an exclusive OS-level lock held for the daemon's lifetime.
type indexLock struct {
	file *os.File
	path string
}

// LockPathForVault returns the lock file guarding a vault's index.
func LockPathForVault(vaultPath string) string {
	return store.DefaultPathForVault(vaultPath) + ".lock"
}

// acquireIndexLock takes an exclusive flock on the vault's index before any
// indexing starts.
//
// The socket check in prepareSocketPath is not enough: the listener is only
// bound *after* BuildIndex, so a second daemon started during the first one's
// initial index (seconds to tens of seconds on a large vault) sees no socket,
// passes the check, and then writes the same SQLite index concurrently. That is
// how an FTS5 index ends up with segments that fail ranked queries.
//
// flock is tied to the open file description, so the lock disappears when the
// holder exits — including a SIGKILL — which means no stale-lock cleanup.
func acquireIndexLock(vaultPath string) (*indexLock, error) {
	path := LockPathForVault(vaultPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating index dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening index lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder := lockHolder(f)
		f.Close()
		return nil, fmt.Errorf("another lore daemon is already indexing this vault%s; "+
			"stop it before starting a new one (lock: %s)", holder, path)
	}
	if err := f.Truncate(0); err == nil {
		if _, err := f.WriteAt([]byte(fmt.Sprintf("%d\n%s\n", os.Getpid(), vaultPath)), 0); err != nil {
			// Diagnostics only — the lock itself is what matters.
			fmt.Printf("lore daemon: warning: could not record lock owner: %v\n", err)
		}
	}
	return &indexLock{file: f, path: path}, nil
}

// Release drops the lock. The lock file is left in place: removing it would let
// a racing daemon lock a file nobody else can see.
func (l *indexLock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
}

// lockHolder reads the pid recorded by the daemon holding the lock, for a more
// useful error message. Best effort: an empty string when unavailable.
func lockHolder(f *os.File) string {
	buf := make([]byte, 64)
	n, err := f.ReadAt(buf, 0)
	if n == 0 || (err != nil && n == 0) {
		return ""
	}
	pid := ""
	for _, b := range buf[:n] {
		if b < '0' || b > '9' {
			break
		}
		pid += string(b)
	}
	if pid == "" {
		return ""
	}
	return " (pid " + pid + ")"
}

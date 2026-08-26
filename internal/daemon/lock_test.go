package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireIndexLockIsExclusive(t *testing.T) {
	t.Setenv("LORE_DB", filepath.Join(t.TempDir(), "db"))
	vault := t.TempDir()

	first, err := acquireIndexLock(vault)
	if err != nil {
		t.Fatalf("first acquireIndexLock: %v", err)
	}

	if _, err := acquireIndexLock(vault); err == nil {
		t.Fatal("second acquireIndexLock succeeded, want exclusion")
	} else if !strings.Contains(err.Error(), "already indexing") {
		t.Fatalf("second acquireIndexLock error = %v, want 'already indexing'", err)
	}

	first.Release()

	second, err := acquireIndexLock(vault)
	if err != nil {
		t.Fatalf("acquireIndexLock after release: %v", err)
	}
	second.Release()
}

func TestAcquireIndexLockRecordsHolderPID(t *testing.T) {
	t.Setenv("LORE_DB", filepath.Join(t.TempDir(), "db"))
	vault := t.TempDir()

	lock, err := acquireIndexLock(vault)
	if err != nil {
		t.Fatalf("acquireIndexLock: %v", err)
	}
	defer lock.Release()

	data, err := os.ReadFile(LockPathForVault(vault))
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("lock file empty, want pid and vault path")
	}
	if !strings.Contains(string(data), vault) {
		t.Fatalf("lock file = %q, want vault path recorded", data)
	}

	// The exclusion error names the holder so operators can find it.
	_, err = acquireIndexLock(vault)
	if err == nil {
		t.Fatal("second acquireIndexLock succeeded, want exclusion")
	}
	if !strings.Contains(err.Error(), "pid ") {
		t.Fatalf("exclusion error = %v, want holder pid", err)
	}
}

// Vaults with separate indexes must not block each other.
func TestAcquireIndexLockIsPerVault(t *testing.T) {
	t.Setenv("LORE_DB", filepath.Join(t.TempDir(), "db"))

	a, err := acquireIndexLock(t.TempDir())
	if err != nil {
		t.Fatalf("lock vault a: %v", err)
	}
	defer a.Release()

	b, err := acquireIndexLock(t.TempDir())
	if err != nil {
		t.Fatalf("lock vault b: %v", err)
	}
	defer b.Release()
}

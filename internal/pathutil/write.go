package pathutil

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// AtomicWriteFile writes data to path by first writing a temporary file in the
// destination directory, then renaming it into place.
func AtomicWriteFile(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming temporary file: %w", err)
	}
	if err := syncParentDir(dir); err != nil {
		return fmt.Errorf("syncing parent directory: %w", err)
	}
	removeTmp = false
	return nil
}

var syncParentDir = func(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

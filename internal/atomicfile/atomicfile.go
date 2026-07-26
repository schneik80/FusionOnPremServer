// Package atomicfile is the one write path for every data file the server
// owns: temp file in the target directory, then rename. A crash mid-write
// can never leave a torn file — readers see the old bytes or the new bytes,
// nothing in between. This is the invariant the local stores (and the
// backup engine reading them) are built on.
package atomicfile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFile atomically replaces path with data. The temp file is created in
// path's directory (rename is only atomic within a filesystem) with the
// given permissions.
func WriteFile(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("atomicfile: creating temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return fmt.Errorf("atomicfile: chmod: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("atomicfile: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("atomicfile: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("atomicfile: close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("atomicfile: rename: %w", err)
	}
	return nil
}

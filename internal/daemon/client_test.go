package daemon

import "testing"

func TestSameVaultPathNormalizesAbsolutePaths(t *testing.T) {
	dir := t.TempDir()
	if !sameVaultPath(dir, dir+"/.") {
		t.Fatalf("expected equivalent paths to match")
	}
}

func TestSameVaultPathRejectsEmptyPath(t *testing.T) {
	if sameVaultPath("", t.TempDir()) {
		t.Fatalf("expected empty path to reject")
	}
}

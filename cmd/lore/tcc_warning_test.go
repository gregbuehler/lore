package lore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWarnIfTCCProtectedFlagsVaultsInProtectedDirs(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	cases := []struct {
		vault    string
		wantWarn bool
	}{
		{filepath.Join(home, "Documents", "lore", "me"), true},
		{filepath.Join(home, "Desktop", "vault"), true},
		{filepath.Join(home, "Downloads", "vault"), true},
		{filepath.Join(home, "lore", "me"), false},
		{filepath.Join(home, "DocumentsArchive", "vault"), false},
		{"/tmp/vault", false},
	}
	for _, tc := range cases {
		out := captureStdout(t, func() { warnIfTCCProtected(tc.vault, "/usr/local/bin/lore") })
		warned := strings.Contains(out, "privacy consent")
		if warned != tc.wantWarn {
			t.Errorf("warnIfTCCProtected(%q) warned = %v, want %v (output: %q)", tc.vault, warned, tc.wantWarn, out)
		}
	}
}

package lore

import "testing"

func TestParseDaemonPIDFileAcceptsVaultPathLine(t *testing.T) {
	pid, err := parseDaemonPIDFile([]byte("12345\n/home/me/vault\n"))
	if err != nil {
		t.Fatalf("parseDaemonPIDFile returned error: %v", err)
	}
	if pid != 12345 {
		t.Fatalf("pid = %d, want 12345", pid)
	}
}

func TestParseDaemonPIDFileRejectsEmptyFile(t *testing.T) {
	if _, err := parseDaemonPIDFile([]byte("\n")); err == nil {
		t.Fatal("expected empty pid file to be rejected")
	}
}

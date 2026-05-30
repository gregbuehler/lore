package lore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateDailyLogIfMissingDoesNotReplaceExistingFile(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "2026-05-30.md")
	existing := "---\ntags:\n  - daily-log\n---\n# 2026-05-30\n\n- already there\n"
	if err := os.WriteFile(logFile, []byte(existing), 0o644); err != nil {
		t.Fatalf("write existing log: %v", err)
	}

	if err := createDailyLogIfMissing(logFile, "2026-05-30"); err != nil {
		t.Fatalf("create daily log if missing: %v", err)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if string(data) != existing {
		t.Fatalf("daily log content changed to %q, want existing content preserved", data)
	}
}

func TestCreateDailyLogIfMissingCreatesScaffold(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "2026-05-30.md")

	if err := createDailyLogIfMissing(logFile, "2026-05-30"); err != nil {
		t.Fatalf("create daily log if missing: %v", err)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "# 2026-05-30") {
		t.Fatalf("daily log scaffold = %q, want date heading", data)
	}
}

func TestCreateDailyLogIfMissingReturnsNonExistenceErrors(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "missing-parent", "2026-05-30.md")

	err := createDailyLogIfMissing(logFile, "2026-05-30")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want os.ErrNotExist", err)
	}
}

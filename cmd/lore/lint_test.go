package lore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLintFormatCountsWriteErrorWhenFixingSectionName(t *testing.T) {
	oldLintFix := lintFix
	oldWriteLintFile := writeLintFile
	lintFix = true
	writeLintFile = func(string, []byte, os.FileMode) error {
		return errors.New("permission denied")
	}
	defer func() {
		lintFix = oldLintFix
		writeLintFile = oldWriteLintFile
	}()

	path := filepath.Join(t.TempDir(), "service.md")
	content := `---
entity_type: service
---
# Service

## What It Does

## Known Issues and Quirks

## Change Log
`

	if got := lintFormat(path, "Wiki/Services/service.md", content, "service"); got != 1 {
		t.Fatalf("lintFormat() issues = %d, want 1", got)
	}
}

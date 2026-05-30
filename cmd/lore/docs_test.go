package lore

import (
	"strings"
	"testing"
)

func TestGenerateCommandReferenceUsesActualCobraUseLines(t *testing.T) {
	doc := generateCommandReference(rootCmd)

	if !strings.Contains(doc, "### `lore entity create <path>`") {
		t.Fatalf("generated command reference missing entity create use line:\n%s", doc)
	}
	if strings.Contains(doc, "lore entity create <type> <name>") {
		t.Fatalf("generated command reference contains stale entity create syntax:\n%s", doc)
	}
	if !strings.Contains(doc, "### `lore docs commands [output-path]`") {
		t.Fatalf("generated command reference should include docs generation command:\n%s", doc)
	}
	if strings.Contains(doc, "### `lore library publish") {
		t.Fatalf("generated command reference should not include duplicate library publish command:\n%s", doc)
	}
	if strings.Contains(doc, "### `lore completion") {
		t.Fatalf("generated command reference should not include Cobra completion commands:\n%s", doc)
	}
}

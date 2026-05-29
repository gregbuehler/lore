package pathutil

import (
	"path/filepath"
	"testing"
)

func TestResolveMarkdownUnderRootAcceptsRelativeNode(t *testing.T) {
	root := t.TempDir()

	got, rel, err := ResolveMarkdownUnderRoot(root, "Wiki/Services/gateway")
	if err != nil {
		t.Fatalf("ResolveMarkdownUnderRoot returned error: %v", err)
	}

	want := filepath.Join(root, "Wiki", "Services", "gateway.md")
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if rel != filepath.Join("Wiki", "Services", "gateway") {
		t.Fatalf("rel = %q", rel)
	}
}

func TestResolveMarkdownUnderRootRejectsTraversal(t *testing.T) {
	root := t.TempDir()

	if _, _, err := ResolveMarkdownUnderRoot(root, "../outside"); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
}

func TestResolveMarkdownUnderRootRejectsAbsolutePath(t *testing.T) {
	root := t.TempDir()

	if _, _, err := ResolveMarkdownUnderRoot(root, filepath.Join(root, "Wiki", "Services", "gateway")); err == nil {
		t.Fatal("expected absolute path to be rejected")
	}
}

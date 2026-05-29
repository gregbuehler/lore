package pathutil

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ResolveMarkdownUnderRoot converts a user-facing markdown node path into an
// absolute file path below root. The returned rel value has no .md suffix.
func ResolveMarkdownUnderRoot(root, node string) (absPath, rel string, err error) {
	if strings.TrimSpace(node) == "" {
		return "", "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(node) {
		return "", "", fmt.Errorf("absolute paths are not allowed: %s", node)
	}

	rel = filepath.Clean(filepath.FromSlash(strings.TrimSuffix(node, ".md")))
	if rel == "." || rel == "" {
		return "", "", fmt.Errorf("path is required")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path escapes root: %s", node)
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("resolving root: %w", err)
	}
	candidate := filepath.Join(absRoot, rel+".md")
	checkRel, err := filepath.Rel(absRoot, candidate)
	if err != nil {
		return "", "", fmt.Errorf("checking path: %w", err)
	}
	if checkRel == ".." || strings.HasPrefix(checkRel, ".."+string(filepath.Separator)) || filepath.IsAbs(checkRel) {
		return "", "", fmt.Errorf("path escapes root: %s", node)
	}

	return candidate, rel, nil
}

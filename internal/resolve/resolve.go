// Package resolve provides Obsidian-style wikilink resolution.
//
// It maps bare filenames (without .md) to their full relative paths,
// enabling shortest-path and folder-expansion resolution strategies.
package resolve

import (
	"os"
	"path/filepath"
	"strings"
)

// SkipIndexFile returns true for files that are infrastructure, not content.
// Mirrors the same set used in the indexer.
func SkipIndexFile(name string) bool {
	switch strings.ToLower(name) {
	case "claude.md", "log.md", "readme.md", "changelog.md", "license.md":
		return true
	}
	return false
}

// Resolver maps bare filenames (lowercase, no .md) to their full relative
// paths, enabling Obsidian-style "shortest path" wikilink resolution.
//
// When a wikilink like [[FAQ]] appears in a file at Threads/Deployment/Alpha/Spec.md,
// Obsidian resolves it to the closest file named FAQ.md. This resolver replicates
// that behavior: prefer a file in the same directory, then the closest ancestor,
// then the shortest overall path.
type Resolver struct {
	// basename (lowercase, no .md) → list of relPaths (without .md)
	index map[string][]string
}

// New returns a new, empty Resolver.
func New() *Resolver {
	return &Resolver{
		index: make(map[string][]string),
	}
}

// NewWithIndex returns a Resolver pre-populated with the given index.
// Intended for use in tests.
func NewWithIndex(index map[string][]string) *Resolver {
	return &Resolver{index: index}
}

// Scan walks a root directory and adds all .md files to the index.
func (r *Resolver) Scan(root string) {
	filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() {
			if strings.HasPrefix(fi.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(fi.Name(), ".md") {
			return nil
		}
		if SkipIndexFile(fi.Name()) {
			return nil
		}

		rel, _ := filepath.Rel(root, path)
		relKey := strings.TrimSuffix(rel, ".md")
		basename := strings.ToLower(strings.TrimSuffix(fi.Name(), ".md"))

		r.index[basename] = append(r.index[basename], relKey)
		return nil
	})
}

// Resolve takes a target and the source file's relPath (without .md),
// and returns the best matching full relPath. Returns "" if no resolution
// is needed (i.e., the target already exists as-is in the index).
//
// Handles four cases:
//  1. Target already exists as-is → no resolution needed
//  2. Folder expansion: target → target/target (common when a file grows into a folder)
//  3. Short names (no /): look up by basename, prefer closest to source
//  4. Relative paths (has /): try prefixing with source's ancestor directories
func (r *Resolver) Resolve(target, sourceRelPath string) string {
	// Check if target already exists as a full path — no resolution needed
	if r.Exists(target) {
		return ""
	}

	// Try folder expansion first: Threads/Foo → Threads/Foo/Foo
	// This is the most common rename pattern (file grows into folder with same-named main page)
	expanded := target + "/" + filepath.Base(target)
	if r.Exists(expanded) {
		return expanded
	}

	if !strings.Contains(target, "/") {
		return r.resolveShortName(target, sourceRelPath)
	}

	// Try relative resolution first
	if resolved := r.resolveRelative(target, sourceRelPath); resolved != "" {
		return resolved
	}

	// Fall back to short-name resolution using just the basename.
	// Handles cases like "Threads/DB Migration to V2" where the folder was
	// renamed but the file inside kept its original name.
	basename := filepath.Base(target)
	return r.resolveShortName(basename, sourceRelPath)
}

// Exists checks whether a relPath (without .md) exists in the index.
func (r *Resolver) Exists(relPath string) bool {
	basename := strings.ToLower(filepath.Base(relPath))
	for _, c := range r.index[basename] {
		if c == relPath {
			return true
		}
	}
	return false
}

// resolveShortName resolves bare filenames like "FAQ" to their full path.
//
// Resolution priority (matching Obsidian behavior):
//  1. File in the same directory as source
//  2. File in a parent directory of source
//  3. File in a sibling/child directory of source (longest common prefix)
//  4. Shortest path overall
func (r *Resolver) resolveShortName(shortName, sourceRelPath string) string {
	candidates := r.index[strings.ToLower(shortName)]
	if len(candidates) == 0 {
		return ""
	}
	if len(candidates) == 1 {
		return candidates[0]
	}

	sourceDir := filepath.Dir(sourceRelPath)

	// Priority 1: same directory
	for _, c := range candidates {
		if filepath.Dir(c) == sourceDir {
			return c
		}
	}

	// Priority 2: closest ancestor directory
	best := ""
	bestPrefixLen := -1
	for _, c := range candidates {
		cDir := filepath.Dir(c)
		if strings.HasPrefix(sourceDir, cDir) {
			if len(cDir) > bestPrefixLen {
				bestPrefixLen = len(cDir)
				best = c
			}
		}
	}
	if best != "" {
		return best
	}

	// Priority 3: longest common prefix with source path
	bestPrefixLen = -1
	for _, c := range candidates {
		prefix := commonPrefix(sourceDir, filepath.Dir(c))
		if len(prefix) > bestPrefixLen {
			bestPrefixLen = len(prefix)
			best = c
		}
	}
	if best != "" {
		return best
	}

	// Priority 4: shortest path
	shortest := candidates[0]
	for _, c := range candidates[1:] {
		if len(c) < len(shortest) {
			shortest = c
		}
	}
	return shortest
}

// resolveRelative handles targets like "Alpha/Roadmap" from a source at
// "Threads/Deployment/Overview" by trying parent directory prefixes.
// Walks up from the source's directory trying: sourceDir/target, parentDir/target, etc.
func (r *Resolver) resolveRelative(target, sourceRelPath string) string {
	sourceDir := filepath.Dir(sourceRelPath)

	// Walk up from source directory
	dir := sourceDir
	for dir != "." && dir != "" {
		candidate := filepath.Join(dir, target)
		if r.Exists(candidate) {
			return candidate
		}
		dir = filepath.Dir(dir)
	}

	// Try from root
	if r.Exists(target) {
		return target
	}

	// Try folder expansion: target → target/basename(target)
	// Common pattern: Threads/Foo.md expands to Threads/Foo/Foo.md
	expanded := target + "/" + filepath.Base(target)
	if r.Exists(expanded) {
		return expanded
	}
	// Also try folder expansion with parent directory walk
	dir = sourceDir
	for dir != "." && dir != "" {
		candidate := filepath.Join(dir, expanded)
		if r.Exists(candidate) {
			return candidate
		}
		dir = filepath.Dir(dir)
	}

	return ""
}

// commonPrefix returns the longest shared path prefix between two paths.
func commonPrefix(a, b string) string {
	aParts := strings.Split(a, string(filepath.Separator))
	bParts := strings.Split(b, string(filepath.Separator))

	n := len(aParts)
	if len(bParts) < n {
		n = len(bParts)
	}

	var shared []string
	for i := 0; i < n; i++ {
		if aParts[i] == bParts[i] {
			shared = append(shared, aParts[i])
		} else {
			break
		}
	}
	return strings.Join(shared, string(filepath.Separator))
}

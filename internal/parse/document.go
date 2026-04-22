package parse

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Document is the fully-parsed representation of a vault markdown file.
type Document struct {
	Path        string     // absolute path
	RelPath     string     // relative to vault/library root
	Title       string     // first H1 or filename sans .md
	EntityType  string     // from frontmatter entity_type:
	Aliases     []string   // from frontmatter aliases:
	Tags        []string   // from frontmatter tags:
	LastUpdated string     // from frontmatter last_updated: (YYYY-MM-DD)
	Status      string     // from frontmatter status:
	Frontmatter map[string]string   // all raw frontmatter k/v (for typed edges)
	Wikilinks   []Wikilink // all extracted wikilinks with edge types
	Sections    []Section  // parsed sections
	Body        string     // full text after frontmatter (for BM25 tokenization)
}

// ParseDocument reads a markdown file and returns a fully parsed Document.
func ParseDocument(path, root string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	rel, _ := filepath.Rel(root, path)
	content := string(data)

	doc := &Document{
		Path:        path,
		RelPath:     rel,
		Title:       strings.TrimSuffix(filepath.Base(path), ".md"),
		Frontmatter: make(map[string]string),
	}

	// Parse frontmatter + body
	body := parseFrontmatter(content, doc)
	doc.Body = body

	// Extract title from first H1 if present
	for _, line := range strings.SplitN(body, "\n", 20) {
		if strings.HasPrefix(line, "# ") {
			doc.Title = strings.TrimPrefix(line, "# ")
			break
		}
	}

	// Parse sections
	doc.Sections = parseSections(body)

	// Extract wikilinks with context-sensitive typing
	doc.Wikilinks = extractAllWikilinks(doc)

	return doc, nil
}

// parseFrontmatter extracts YAML frontmatter into the Document fields
// and returns the body (content after frontmatter).
func parseFrontmatter(content string, doc *Document) string {
	if !strings.HasPrefix(content, "---\n") {
		return content
	}

	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return content
	}

	fmBlock := content[4 : 4+end]
	body := content[4+end+4:] // skip past closing ---\n

	scanner := bufio.NewScanner(strings.NewReader(fmBlock))
	var currentListKey string
	var currentList []string

	flushList := func() {
		if currentListKey != "" && len(currentList) > 0 {
			switch currentListKey {
			case "aliases":
				doc.Aliases = currentList
			case "tags":
				doc.Tags = currentList
			}
			doc.Frontmatter[currentListKey] = strings.Join(currentList, ", ")
			currentListKey = ""
			currentList = nil
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// YAML list continuation: "  - item"
		if currentListKey != "" && strings.HasPrefix(line, "  - ") {
			item := strings.TrimSpace(strings.TrimPrefix(line, "  - "))
			item = stripQuotes(item)
			currentList = append(currentList, item)
			continue
		} else if currentListKey != "" {
			flushList()
		}

		if !strings.Contains(trimmed, ":") {
			continue
		}

		parts := strings.SplitN(trimmed, ":", 2)
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		// Check if this starts a YAML list (value is empty or [])
		if val == "" {
			currentListKey = key
			currentList = nil
			continue
		}

		// Inline list: [a, b, c]
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			inner := val[1 : len(val)-1]
			var items []string
			for _, item := range strings.Split(inner, ",") {
				item = strings.TrimSpace(item)
				item = stripQuotes(item)
				if item != "" {
					items = append(items, item)
				}
			}
			switch key {
			case "aliases":
				doc.Aliases = items
			case "tags":
				doc.Tags = items
			}
			doc.Frontmatter[key] = strings.Join(items, ", ")
			continue
		}

		val = stripQuotes(val)
		doc.Frontmatter[key] = val

		switch key {
		case "entity_type":
			doc.EntityType = val
		case "type":
			if doc.EntityType == "" {
				doc.EntityType = val
			}
		case "last_updated":
			doc.LastUpdated = val
		case "status":
			doc.Status = val
		case "title":
			doc.Title = val
		}
	}
	flushList()

	return body
}

func stripQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

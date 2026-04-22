package parse

import (
	"regexp"
	"strings"
)

// codeBlockRe matches fenced code blocks (``` or ~~~).
var codeBlockRe = regexp.MustCompile("(?s)```.*?```|~~~.*?~~~")

// inlineCodeRe matches inline code spans (`...`).
var inlineCodeRe = regexp.MustCompile("`[^`]+`")

// Wikilink represents a cross-reference extracted from a document.
type Wikilink struct {
	Target   string // e.g. "Wiki/People/jsmith" (without .md)
	EdgeType string // "owner", "depends_on", "mentions", etc.
	Raw      string // original matched text e.g. "[[Wiki/People/jsmith]]"
	Source   string // "frontmatter:<key>" or "section:<heading>" or "prose"
}

var wikilinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// frontmatterEdgeTypes maps frontmatter keys to edge types.
var frontmatterEdgeTypes = map[string]string{
	"owner":       "owner",
	"owned_by":    "owner",
	"depends_on":  "depends_on",
	"deployed_in": "deployed_in",
	"escalates_to": "escalates_to",
	"related":     "relates",
	"blocks":      "blocks",
	"blocked_by":  "blocked_by",
}

// sectionEdgeTypes maps section heading keywords to edge types.
var sectionEdgeTypes = map[string]string{
	"dependencies":  "depends_on",
	"dependency":    "depends_on",
	"depends on":    "depends_on",
	"escalation":    "escalates_to",
	"owner":         "owner",
	"team":          "owner",
	"deployed in":   "deployed_in",
	"environments":  "deployed_in",
	"cross-references": "relates",
	"related":       "relates",
	"see also":      "relates",
}

// stripCode removes fenced code blocks and inline code spans from text
// so that example wikilinks inside code are not indexed.
func stripCode(text string) string {
	text = codeBlockRe.ReplaceAllString(text, "")
	text = inlineCodeRe.ReplaceAllString(text, "")
	return text
}

// extractAllWikilinks pulls wikilinks from both frontmatter and body,
// assigning edge types based on context.
func extractAllWikilinks(doc *Document) []Wikilink {
	var links []Wikilink

	// 1. Typed links from frontmatter
	for key, val := range doc.Frontmatter {
		edgeType, isTyped := frontmatterEdgeTypes[key]
		if !isTyped {
			continue
		}
		for _, match := range wikilinkRe.FindAllStringSubmatch(val, -1) {
			target := normalizeTarget(match[1])
			if target == "" || strings.HasPrefix(target, "@") || strings.Contains(target, ".png") || strings.Contains(target, ".jpg") {
				continue
			}
			links = append(links, Wikilink{
				Target:   target,
				EdgeType: edgeType,
				Raw:      match[0],
				Source:   "frontmatter:" + key,
			})
		}
	}

	// 2. Section-scoped and prose links from body
	for _, section := range doc.Sections {
		edgeType := inferEdgeType(section.Heading)
		cleanBody := stripCode(section.Body)
		for _, match := range wikilinkRe.FindAllStringSubmatch(cleanBody, -1) {
			target := normalizeTarget(match[1])
			if target == "" || strings.HasPrefix(target, "@") || strings.Contains(target, ".png") || strings.Contains(target, ".jpg") {
				continue
			}
			links = append(links, Wikilink{
				Target:   target,
				EdgeType: edgeType,
				Raw:      match[0],
				Source:   "section:" + section.Heading,
			})
		}
	}

	return links
}

// inferEdgeType determines the edge type from a section heading.
func inferEdgeType(heading string) string {
	lower := strings.ToLower(heading)
	for keyword, edgeType := range sectionEdgeTypes {
		if strings.Contains(lower, keyword) {
			return edgeType
		}
	}
	return "mentions"
}

// normalizeTarget cleans a wikilink target for use as a graph key.
// Handles: display text (|), .md suffix, leading/trailing space, anchors.
// Returns "" for targets that should be skipped (templates, directory-only refs).
func normalizeTarget(raw string) string {
	// Handle display text: [[target|display]] or [[target\|display]] (escaped in tables)
	if idx := strings.Index(raw, "|"); idx >= 0 {
		raw = raw[:idx]
	}
	// Handle anchors: [[target#section]]
	if idx := strings.Index(raw, "#"); idx >= 0 {
		raw = raw[:idx]
	}
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, ".md")
	raw = strings.TrimSuffix(raw, "\\") // strip escaped pipe remnant from tables
	raw = strings.TrimSuffix(raw, "/")  // strip directory-only refs

	// Skip template placeholders like <slug>, YYYY-MM-DD, etc.
	if strings.Contains(raw, "<") || strings.Contains(raw, "YYYY") {
		return ""
	}
	// Skip emoji shortcodes (:space:, :rocket:, etc.)
	if strings.HasPrefix(raw, ":") && strings.HasSuffix(raw, ":") {
		return ""
	}
	return raw
}

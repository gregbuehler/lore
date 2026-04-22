package parse

import (
	"strings"
)

// BuildAbstract generates a 1-2 sentence summary of a document from its
// structured content. No LLM needed — exploits the predictable page format.
func BuildAbstract(doc *Document) string {
	var parts []string

	// Type + title
	if doc.EntityType != "" {
		parts = append(parts, doc.Title+" ["+doc.EntityType+"]")
	} else {
		parts = append(parts, doc.Title)
	}

	// First meaningful prose line (skip headings, bold labels, empty lines)
	firstLine := extractFirstProse(doc.Body)
	if firstLine != "" {
		parts = append(parts, firstLine)
	}

	// Owner line (common pattern: **Owner / DRI:** ...)
	owner := extractOwner(doc.Body)
	if owner != "" {
		parts = append(parts, "Owner: "+owner)
	}

	// Status if active thread
	if doc.Status != "" && doc.Status != "reference" {
		parts = append(parts, "Status: "+doc.Status)
	}

	// Last updated
	if doc.LastUpdated != "" {
		parts = append(parts, "Updated: "+doc.LastUpdated)
	}

	return strings.Join(parts, " — ")
}

// extractFirstProse finds the first line of actual descriptive text in the body,
// skipping headings, bold key-value lines, empty lines, and frontmatter artifacts.
func extractFirstProse(body string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Skip headings
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Skip bold key-value lines: **Key:** value
		if strings.HasPrefix(trimmed, "**") && strings.Contains(trimmed, ":**") {
			continue
		}
		// Skip list items that are just links or short metadata
		if (strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ")) && len(trimmed) < 40 {
			continue
		}
		// Skip table rows
		if strings.HasPrefix(trimmed, "|") {
			continue
		}
		// Skip horizontal rules
		if trimmed == "---" {
			continue
		}

		// Found a prose line — truncate to first sentence
		sentence := firstSentence(trimmed)
		if len(sentence) > 150 {
			sentence = sentence[:147] + "..."
		}
		return sentence
	}
	return ""
}

// extractOwner looks for the **Owner / DRI:** pattern.
func extractOwner(body string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "owner") && strings.Contains(trimmed, ":**") {
			// Extract value after the bold marker
			idx := strings.Index(trimmed, ":**")
			if idx >= 0 {
				val := strings.TrimSpace(trimmed[idx+3:])
				// Remove trailing bold marker if any
				val = strings.TrimSuffix(val, "**")
				if len(val) > 80 {
					val = val[:77] + "..."
				}
				return val
			}
		}
	}
	return ""
}

// firstSentence returns text up to the first sentence-ending punctuation.
func firstSentence(text string) string {
	// Look for ". " or ".\n" as sentence boundary
	for i := 0; i < len(text)-1; i++ {
		if text[i] == '.' && (text[i+1] == ' ' || text[i+1] == '\n') {
			return text[:i+1]
		}
	}
	// No sentence boundary found, return full text
	return text
}

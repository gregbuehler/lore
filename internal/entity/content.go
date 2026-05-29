package entity

import (
	"fmt"
	"strings"
)

// ValidTypes lists the accepted entity_type values.
var ValidTypes = map[string]bool{
	"service":        true,
	"environment":    true,
	"person":         true,
	"tool":           true,
	"infrastructure": true,
	"organization":   true,
	"customer":       true,
	"vendor":         true,
	"concept":        true,
}

func BuildContent(entityType, title, today string) string {
	var sb strings.Builder

	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("entity_type: %s\n", entityType))
	sb.WriteString(fmt.Sprintf("title: \"%s\"\n", title))
	sb.WriteString(fmt.Sprintf("last_updated: %s\n", today))
	sb.WriteString("tags:\n")
	sb.WriteString(fmt.Sprintf("  - %s\n", entityType))
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("# %s\n", title))
	sb.WriteString("\n")

	switch entityType {
	case "person":
		sb.WriteString("## What They Do\n\n")
	default:
		sb.WriteString("## What It Does\n\n")
	}

	sb.WriteString("## Known Issues\n\n")
	sb.WriteString("## Change Log\n\n")

	return sb.String()
}

func AppendToSection(content, sectionName, text string) (string, error) {
	target := "## " + sectionName
	lines := strings.Split(content, "\n")

	sectionIdx := -1
	for i, line := range lines {
		if strings.TrimRight(line, " \t") == target {
			sectionIdx = i
			break
		}
	}

	if sectionIdx < 0 {
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += fmt.Sprintf("\n%s\n\n%s\n", target, text)
		return content, nil
	}

	insertAt := len(lines)
	for i := sectionIdx + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") || strings.HasPrefix(lines[i], "# ") {
			insertAt = i
			break
		}
	}

	insertBefore := insertAt
	for insertBefore > sectionIdx+1 && strings.TrimSpace(lines[insertBefore-1]) == "" {
		insertBefore--
	}

	newLines := make([]string, 0, len(lines)+2)
	newLines = append(newLines, lines[:insertBefore]...)
	newLines = append(newLines, text)
	newLines = append(newLines, "")
	newLines = append(newLines, lines[insertBefore:]...)

	return strings.Join(newLines, "\n"), nil
}

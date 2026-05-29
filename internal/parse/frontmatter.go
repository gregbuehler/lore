package parse

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseFrontmatterMap extracts the YAML frontmatter block into a generic map.
func ParseFrontmatterMap(content string) map[string]any {
	fmBlock, _, ok := splitFrontmatter(content)
	if !ok {
		return map[string]any{}
	}
	var out map[string]any
	if err := yaml.Unmarshal([]byte(fmBlock), &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}

// SetFrontmatterField sets or adds a top-level key in the YAML frontmatter block.
func SetFrontmatterField(content, key, value string) (string, error) {
	fmBlock, body, ok := splitFrontmatter(content)
	if !ok {
		return content, fmt.Errorf("file does not begin with YAML frontmatter (---)")
	}

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(fmBlock), &doc); err != nil {
		return content, fmt.Errorf("parsing frontmatter: %w", err)
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	}
	mapping := doc.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return content, fmt.Errorf("frontmatter must be a YAML mapping")
	}

	valueNode := parseYAMLValueNode(value)
	replaced := false
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = valueNode
			replaced = true
			break
		}
	}
	if !replaced {
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			valueNode,
		)
	}

	data, err := yaml.Marshal(&doc)
	if err != nil {
		return content, fmt.Errorf("marshaling frontmatter: %w", err)
	}
	return "---\n" + string(data) + "---\n" + body, nil
}

func splitFrontmatter(content string) (frontmatter, body string, ok bool) {
	if !strings.HasPrefix(content, "---\n") {
		return "", "", false
	}
	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", false
	}
	after := rest[end+len("\n---"):]
	if strings.HasPrefix(after, "\n") {
		after = after[1:]
	}
	return rest[:end], after, true
}

func parseYAMLValueNode(value string) *yaml.Node {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(value), &doc); err == nil && len(doc.Content) > 0 {
		return doc.Content[0]
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

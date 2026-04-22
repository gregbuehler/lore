package parse

import "strings"

// Section represents a markdown section (heading + body until next heading).
type Section struct {
	Heading string
	Level   int    // 1 for #, 2 for ##, etc.
	Body    string // text between this heading and the next
}

// parseSections splits markdown body into sections by headings.
// Content before the first heading goes into a section with Heading="".
func parseSections(body string) []Section {
	var sections []Section
	var current *Section

	for _, line := range strings.Split(body, "\n") {
		level := headingLevel(line)
		if level > 0 {
			if current != nil {
				sections = append(sections, *current)
			}
			heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
			current = &Section{
				Heading: heading,
				Level:   level,
			}
		} else if current != nil {
			current.Body += line + "\n"
		} else {
			// Content before first heading
			if current == nil {
				current = &Section{Heading: "", Level: 0}
			}
			current.Body += line + "\n"
		}
	}
	if current != nil {
		sections = append(sections, *current)
	}
	return sections
}

func headingLevel(line string) int {
	if !strings.HasPrefix(line, "#") {
		return 0
	}
	level := 0
	for _, c := range line {
		if c == '#' {
			level++
		} else {
			break
		}
	}
	// Must be followed by a space
	if level > 0 && level < len(line) && line[level] == ' ' {
		return level
	}
	return 0
}

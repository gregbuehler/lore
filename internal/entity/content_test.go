package entity

import (
	"strings"
	"testing"
)

func TestBuildContentIncludesExpectedSections(t *testing.T) {
	content := BuildContent("service", "Gateway", "2026-05-29")

	for _, want := range []string{
		"entity_type: service",
		"title: \"Gateway\"",
		"last_updated: 2026-05-29",
		"## What It Does",
		"## Known Issues",
		"## Change Log",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("content missing %q:\n%s", want, content)
		}
	}
}

func TestAppendToSectionInsertsBeforeNextSection(t *testing.T) {
	content := "# Page\n\n## Known Issues\n\nOld\n\n## Change Log\n\n"

	got, err := AppendToSection(content, "Known Issues", "New")
	if err != nil {
		t.Fatalf("AppendToSection: %v", err)
	}
	want := "# Page\n\n## Known Issues\n\nOld\nNew\n\n\n## Change Log\n\n"
	if got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

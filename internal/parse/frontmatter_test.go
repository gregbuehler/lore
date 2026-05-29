package parse

import "testing"

func TestParseFrontmatterMapPreservesLists(t *testing.T) {
	fm := ParseFrontmatterMap("---\ntitle: Service\ntags:\n  - service\n  - critical\n---\n# Service\n")

	tags, ok := fm["tags"].([]any)
	if !ok {
		t.Fatalf("tags = %#v, want []any", fm["tags"])
	}
	if len(tags) != 2 || tags[0] != "service" || tags[1] != "critical" {
		t.Fatalf("tags = %#v", tags)
	}
}

func TestSetFrontmatterFieldUpdatesExistingKey(t *testing.T) {
	content := "---\ntitle: Old\ntags:\n  - service\n---\n# Old\n"

	got, err := SetFrontmatterField(content, "title", "New")
	if err != nil {
		t.Fatalf("SetFrontmatterField: %v", err)
	}
	want := "---\ntitle: New\ntags:\n    - service\n---\n# Old\n"
	if got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestSetFrontmatterFieldAddsSequenceValue(t *testing.T) {
	content := "---\ntitle: Service\n---\n# Service\n"

	got, err := SetFrontmatterField(content, "tags", "[service, critical]")
	if err != nil {
		t.Fatalf("SetFrontmatterField: %v", err)
	}
	want := "---\ntitle: Service\ntags: [service, critical]\n---\n# Service\n"
	if got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

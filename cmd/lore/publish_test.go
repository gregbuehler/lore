package lore

import (
	"bytes"
	"strings"
	"testing"
)

func TestFormatPublishStatusesIncludesEachTargetPorcelain(t *testing.T) {
	output := formatPublishStatuses([]publishChange{
		{
			name:   "services",
			path:   "/tmp/services",
			status: " M README.md\n?? runbook.md\n",
		},
		{
			name:   "infra",
			path:   "/tmp/infra",
			status: "A  deploy.yaml\n",
		},
	})

	for _, want := range []string{
		"Pending changes to publish:",
		"services (/tmp/services)",
		"   M README.md",
		"  ?? runbook.md",
		"infra (/tmp/infra)",
		"  A  deploy.yaml",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("formatPublishStatuses() missing %q in:\n%s", want, output)
		}
	}
}

func TestConfirmPublishAcceptsYesResponses(t *testing.T) {
	for _, input := range []string{"y\n", "yes\n", "Y\n", "YES\n"} {
		var out bytes.Buffer
		ok, err := confirmPublish(strings.NewReader(input), &out)
		if err != nil {
			t.Fatalf("confirmPublish(%q) returned error: %v", input, err)
		}
		if !ok {
			t.Fatalf("confirmPublish(%q) = false, want true", input)
		}
		if !strings.Contains(out.String(), "Publish these changes?") {
			t.Fatalf("prompt output = %q, want confirmation prompt", out.String())
		}
	}
}

func TestConfirmPublishRejectsNonYesResponses(t *testing.T) {
	for _, input := range []string{"n\n", "\n", "no\n"} {
		var out bytes.Buffer
		ok, err := confirmPublish(strings.NewReader(input), &out)
		if err != nil {
			t.Fatalf("confirmPublish(%q) returned error: %v", input, err)
		}
		if ok {
			t.Fatalf("confirmPublish(%q) = true, want false", input)
		}
	}
}

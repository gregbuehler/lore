package parse

import "testing"

func TestStripCode(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			"inline code",
			"See `[[Threads/foo]]` for details",
			"See  for details",
		},
		{
			"fenced code block",
			"Before\n```\n[[Threads/bar]]\n```\nAfter",
			"Before\n\nAfter",
		},
		{
			"no code",
			"See [[Threads/baz]] for details",
			"See [[Threads/baz]] for details",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripCode(tt.input)
			if got != tt.expect {
				t.Errorf("stripCode(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

func TestNormalizeTargetSkips(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"Threads/<slug>", ""},
		{"Daily Log/YYYY-MM/YYYY-MM-DD", ""},
		{"Research/", "Research"},
		{"Research/Pipeline\\", "Research/Pipeline"},
		{"Wiki/Services/gateway", "Wiki/Services/gateway"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeTarget(tt.input)
		if got != tt.expect {
			t.Errorf("normalizeTarget(%q) = %q, want %q", tt.input, got, tt.expect)
		}
	}
}

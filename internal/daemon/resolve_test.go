package daemon

import (
	"testing"

	"github.com/gregbuehler/lore/internal/resolve"
)

// makeResolver builds a shortNameResolver directly from a pre-populated index
// for testing purposes.
func makeResolver(index map[string][]string) *shortNameResolver {
	r := resolve.NewWithIndex(index)
	return &shortNameResolver{r: r}
}

func TestResolveShortName(t *testing.T) {
	r := makeResolver(map[string][]string{
		"faq":         {"Threads/Deployment/Alpha/FAQ", "Wiki/FAQ"},
		"roadmap":     {"Threads/Deployment/Alpha/Roadmap"},
		"gateway":     {"Wiki/Services/gateway"},
		"unique-page": {"Threads/Unique Page"},
	})

	tests := []struct {
		name     string
		target   string
		source   string
		expected string
	}{
		{
			"same dir preferred",
			"FAQ",
			"Threads/Deployment/Alpha/Spec",
			"Threads/Deployment/Alpha/FAQ",
		},
		{
			"only one match",
			"Roadmap",
			"Daily Log/2026-05/2026-05-01",
			"Threads/Deployment/Alpha/Roadmap",
		},
		{
			"already exists returns empty",
			"Wiki/Services/gateway",
			"Daily Log/2026-05/2026-05-01",
			"",
		},
		{
			"no match returns empty",
			"nonexistent",
			"Threads/Foo",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.resolve(tt.target, tt.source)
			if got != tt.expected {
				t.Errorf("resolve(%q, %q) = %q, want %q", tt.target, tt.source, got, tt.expected)
			}
		})
	}
}

func TestResolveRelativePath(t *testing.T) {
	r := makeResolver(map[string][]string{
		"roadmap":  {"Threads/Deployment/Alpha/Roadmap"},
		"pipeline": {"Threads/Deployment/Research/Pipeline"},
		"design":   {"Threads/LTS/Design", "Wiki/Design"},
	})

	tests := []struct {
		name     string
		target   string
		source   string
		expected string
	}{
		{
			"relative from sibling dir",
			"Alpha/Roadmap",
			"Threads/Deployment/Overview",
			"Threads/Deployment/Alpha/Roadmap",
		},
		{
			"relative from same parent",
			"Research/Pipeline",
			"Threads/Deployment/Overview",
			"Threads/Deployment/Research/Pipeline",
		},
		{
			"no match",
			"Nonexistent/Path",
			"Threads/Foo",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.resolve(tt.target, tt.source)
			if got != tt.expected {
				t.Errorf("resolve(%q, %q) = %q, want %q", tt.target, tt.source, got, tt.expected)
			}
		})
	}
}

func TestResolveFolderExpansion(t *testing.T) {
	r := makeResolver(map[string][]string{
		// DB Migration lives inside a renamed folder
		"db migration to v2": {"Threads/DB V2 Migration/DB Migration to V2"},
		// Service Stability has a same-named file inside a folder
		"service stability remediation": {"Threads/Service Stability Remediation/Service Stability Remediation"},
		// Platform Upgrade expanded into a folder with same-named main page
		"platform upgrade": {"Threads/Platform Upgrade/Platform Upgrade"},
	})

	tests := []struct {
		name     string
		target   string
		source   string
		expected string
	}{
		{
			"folder expansion: target/target",
			"Threads/Platform Upgrade",
			"Daily Log/2026-04/2026-04-17",
			"Threads/Platform Upgrade/Platform Upgrade",
		},
		{
			"basename fallback when folder renamed",
			"Threads/DB Migration to V2",
			"Daily Log/2026-04/2026-04-08",
			"Threads/DB V2 Migration/DB Migration to V2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.resolve(tt.target, tt.source)
			if got != tt.expected {
				t.Errorf("resolve(%q, %q) = %q, want %q", tt.target, tt.source, got, tt.expected)
			}
		})
	}
}

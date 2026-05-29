package lore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSourcesParsesYAML(t *testing.T) {
	libPath := t.TempDir()
	content := `name: services
sources:
  - repo: git.example.com/org/deployments
    local: ~/src/deployments
    watch:
      - path: "deployments/{entity}/**"
        maps_to: environment
      - path: "services/{entity}/**"
        maps_to: service
`
	if err := os.WriteFile(filepath.Join(libPath, "library.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write library.yaml: %v", err)
	}

	sources := loadSources(libPath)
	if len(sources) != 1 {
		t.Fatalf("sources len = %d, want 1", len(sources))
	}
	if sources[0].Repo != "git.example.com/org/deployments" {
		t.Fatalf("repo = %q", sources[0].Repo)
	}
	if len(sources[0].Watch) != 2 {
		t.Fatalf("watch len = %d, want 2", len(sources[0].Watch))
	}
	if sources[0].Watch[0].Path != "deployments/{entity}/**" || sources[0].Watch[0].MapsTo != "environment" {
		t.Fatalf("first watch = %#v", sources[0].Watch[0])
	}
}

func TestBuildGitHubCommitsURLEscapesQueryValues(t *testing.T) {
	got, err := buildGitHubCommitsURL("https://git.example.com/api/v3", "org name", "repo/name", "deployments/prod env/**", "2026-05-29")
	if err != nil {
		t.Fatalf("buildGitHubCommitsURL: %v", err)
	}
	want := "https://git.example.com/api/v3/repos/org%20name/repo%2Fname/commits?path=deployments%2Fprod+env%2F%2A%2A&per_page=100&since=2026-05-29T00%3A00%3A00Z"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

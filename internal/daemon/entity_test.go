package daemon

import (
	"path/filepath"
	"testing"

	"github.com/gregbuehler/lore/internal/store"
)

func TestDispatchEntityListIncludesWikiDocumentsWithoutWikiText(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	docs := []*store.Document{
		{
			Path:       "/vault/Wiki/Services/cinder.md",
			RelPath:    "Wiki/Services/cinder.md",
			Title:      "Cinder",
			EntityType: "service",
			Body:       "Handles ash processing and retention.",
			Root:       "/vault",
			Abstract:   "Handles ash processing and retention.",
		},
		{
			Path:       "/vault/Wiki/People/marlow.md",
			RelPath:    "Wiki/People/marlow.md",
			Title:      "Marlow",
			EntityType: "person",
			Body:       "Coordinates launch readiness.",
			Root:       "/vault",
			Abstract:   "Coordinates launch readiness.",
		},
		{
			Path:       "/vault/Threads/cinder.md",
			RelPath:    "Threads/cinder.md",
			Title:      "Cinder Thread",
			EntityType: "service",
			Body:       "Contains the token wiki but is not an entity page.",
			Root:       "/vault",
		},
	}
	for _, doc := range docs {
		if err := db.UpsertDocument(doc); err != nil {
			t.Fatalf("upsert %s: %v", doc.RelPath, err)
		}
	}

	d := &Daemon{state: &State{Store: db}}

	resp := d.dispatchEntityList(&Request{Type: "entity_list"})
	if !resp.OK {
		t.Fatalf("entity_list failed: %s", resp.Error)
	}
	got := resultRelPaths(resp.Results)
	want := []string{"Wiki/People/marlow", "Wiki/Services/cinder"}
	if !sameStrings(got, want) {
		t.Fatalf("entity_list rel_paths = %v, want %v", got, want)
	}

	resp = d.dispatchEntityList(&Request{Type: "entity_list", EntityType: "service"})
	if !resp.OK {
		t.Fatalf("entity_list service failed: %s", resp.Error)
	}
	got = resultRelPaths(resp.Results)
	want = []string{"Wiki/Services/cinder"}
	if !sameStrings(got, want) {
		t.Fatalf("entity_list service rel_paths = %v, want %v", got, want)
	}
}

func resultRelPaths(results []Result) []string {
	relPaths := make([]string, 0, len(results))
	for _, r := range results {
		relPaths = append(relPaths, r.RelPath)
	}
	return relPaths
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

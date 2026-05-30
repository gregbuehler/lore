package lore

import (
	"strings"
	"testing"
)

func TestBuildEntityListRequestUsesEntityListEndpoint(t *testing.T) {
	req := buildEntityListRequest("service")

	if req.Type != "entity_list" {
		t.Fatalf("Type = %q, want entity_list", req.Type)
	}
	if req.Query != "" || req.Filter != nil {
		t.Fatalf("request should not use search fields: query=%q filter=%v", req.Query, req.Filter)
	}
	if req.EntityType != "service" {
		t.Fatalf("EntityType = %q, want service", req.EntityType)
	}
}

func TestCheckEntityDeleteBacklinksRequiresIndexUnlessForced(t *testing.T) {
	t.Setenv("LORE_DB", t.TempDir())
	vault := t.TempDir()

	err := checkEntityDeleteBacklinks(vault, "Wiki/Services/gateway", false)
	if err == nil {
		t.Fatal("expected missing backlink index to reject delete")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %q, want force guidance", err)
	}

	if err := checkEntityDeleteBacklinks(vault, "Wiki/Services/gateway", true); err != nil {
		t.Fatalf("forced backlink check returned error: %v", err)
	}
}

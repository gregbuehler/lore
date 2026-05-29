package lore

import "testing"

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

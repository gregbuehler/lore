package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoreHTTPRequiresBearerTokenAndReturnsScopedSources(t *testing.T) {
	d, vault, _ := documentTestDaemon(t)
	handler := d.handleHTTP("test-token")
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/sources", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated sources status = %d", unauthorized.Code)
	}
	d.state.initialized = false
	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK || strings.Contains(health.Body.String(), d.state.VaultPath) {
		t.Fatalf("health response = %d %s", health.Code, health.Body.String())
	}
	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("uninitialized ready status = %d", ready.Code)
	}
	d.state.initialized = true
	ready = httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("initialized ready status = %d", ready.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/sources", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("sources status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"source_id":"vault"`) || !strings.Contains(body, `"source_id":"library:notes"`) {
		t.Fatalf("sources response = %s", body)
	}
	if strings.Contains(body, d.state.VaultPath) || strings.Contains(body, d.state.Paths[1]) {
		t.Fatalf("sources exposed a filesystem root: %s", body)
	}
	if err := os.WriteFile(filepath.Join(vault, "target.md"), []byte("# HTTP Target"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolveRequest := httptest.NewRequest(http.MethodGet, "/api/v1/resolve?source=vault&from=same.md&target=target", nil)
	resolveRequest.Header.Set("Authorization", "Bearer test-token")
	resolveResponse := httptest.NewRecorder()
	handler.ServeHTTP(resolveResponse, resolveRequest)
	if resolveResponse.Code != http.StatusOK ||
		!strings.Contains(resolveResponse.Body.String(), `"resolved":true`) ||
		!strings.Contains(resolveResponse.Body.String(), `"source_id":"vault"`) ||
		!strings.Contains(resolveResponse.Body.String(), `"rel_path":"target.md"`) {
		t.Fatalf("resolve response = %d %s", resolveResponse.Code, resolveResponse.Body.String())
	}
}

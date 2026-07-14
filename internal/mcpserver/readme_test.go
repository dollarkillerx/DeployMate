package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadmeHandlerReturnsVersionAndTools(t *testing.T) {
	srv := httptest.NewServer(ReadmeHandler("v1.2.3"))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
	var got Readme
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Version != "v1.2.3" {
		t.Fatalf("version = %q, want v1.2.3", got.Version)
	}
	if len(got.Tools) != 8 {
		t.Fatalf("tools = %d, want 8", len(got.Tools))
	}
	if got.MCPEndpoint != "/mcp" {
		t.Fatalf("mcp_endpoint = %q", got.MCPEndpoint)
	}
}

func TestReadmeHandlerRejectsNonGet(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/readme", nil)
	ReadmeHandler("dev").ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

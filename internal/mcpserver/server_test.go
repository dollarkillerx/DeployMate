package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jianping5/DeployMate/internal/auth"
	commandpkg "github.com/jianping5/DeployMate/internal/command"
	filespkg "github.com/jianping5/DeployMate/internal/files"
	jobspkg "github.com/jianping5/DeployMate/internal/jobs"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestHTTPServerRequiresBearerAndExposesTools(t *testing.T) {
	dir := t.TempDir()
	token, _, err := auth.EnsureToken(filepath.Join(dir, "token.sha256"), filepath.Join(dir, "initial-token"))
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := auth.LoadVerifier(filepath.Join(dir, "token.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	runner := commandpkg.NewRunner(2, 4096, time.Minute)
	jobs, err := jobspkg.NewManager(filepath.Join(dir, "jobs"), runner)
	if err != nil {
		t.Fatal(err)
	}
	fileManager := filespkg.NewManager(1024, time.Minute)
	handler := NewHandler(Dependencies{Runner: runner, Jobs: jobs, Files: fileManager, PublicBaseURL: "https://example.test:9443", Version: "test"})
	mux := http.NewServeMux()
	mux.Handle("/mcp", auth.Middleware(verifier, handler))
	mux.Handle("/files/", fileManager.Handler())
	ts := httptest.NewServer(mux)
	defer ts.Close()

	unauthorized, err := http.Post(ts.URL+"/mcp", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.StatusCode)
	}
	unauthorized.Body.Close()

	httpClient := &http.Client{Transport: bearerTransport{token: token, base: http.DefaultTransport}}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: httpClient}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"get_server_info": false, "exec_command": false, "start_command": false, "get_job": false, "cancel_job": false, "list_jobs": false, "create_upload": false, "create_download": false}
	for _, tool := range tools.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing tool %s", name)
		}
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "exec_command", Arguments: map[string]any{"command": "printf mcp-ok"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %+v", result)
	}
}

func TestCreateUploadReturnsPublicOneTimeURL(t *testing.T) {
	dir := t.TempDir()
	runner := commandpkg.NewRunner(1, 1024, time.Minute)
	jobs, _ := jobspkg.NewManager(filepath.Join(dir, "jobs"), runner)
	files := filespkg.NewManager(1024, time.Minute)
	server := NewServer(Dependencies{Runner: runner, Jobs: jobs, Files: files, PublicBaseURL: "https://host.test:9443", Version: "test"})
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	go server.Run(ctx, st)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	path := filepath.Join(dir, "artifact")
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "create_upload", Arguments: map[string]any{"remote_path": path, "size": 0, "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || result.StructuredContent == nil {
		t.Fatalf("result=%+v", result)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("ticket unexpectedly created destination")
	}
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

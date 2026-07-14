package mcpserver

import (
	"encoding/json"
	"net/http"
)

// Tool describes a single MCP tool exposed by the agent.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Readme is the payload returned by the /readme endpoint.
type Readme struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	MCPEndpoint string   `json:"mcp_endpoint"`
	Auth        string   `json:"auth"`
	Tools       []Tool   `json:"tools"`
	Usage       []string `json:"usage"`
}

// ReadmeHandler serves an unauthenticated JSON description of the agent so
// operators can confirm the version and learn how to connect a client.
func ReadmeHandler(version string) http.Handler {
	readme := Readme{
		Name:        "deploymate-agent",
		Version:     version,
		Description: "Remote MCP agent for AI-driven deployment. Exposes an MCP Streamable HTTP endpoint that lets an AI client run shell commands, inspect the host, and transfer files on this server.",
		MCPEndpoint: "/mcp",
		Auth:        "Send 'Authorization: Bearer <token>' on every /mcp request. The token is created on first start at /etc/deploymate/initial-token.",
		Tools: []Tool{
			{Name: "get_server_info", Description: "Get OS, CPU, memory, disk, network, uptime, systemd, and agent information."},
			{Name: "exec_command", Description: "Execute a shell command synchronously through /bin/sh -lc."},
			{Name: "start_command", Description: "Start a long-running shell command and return a job ID."},
			{Name: "get_job", Description: "Get job status and incremental stdout/stderr from a cursor."},
			{Name: "cancel_job", Description: "Cancel a running job and its process group."},
			{Name: "list_jobs", Description: "List recent asynchronous jobs, optionally filtered by status."},
			{Name: "create_upload", Description: "Create a short-lived, single-use HTTP PUT URL for uploading a local file."},
			{Name: "create_download", Description: "Create a short-lived, single-use HTTP GET URL for downloading a remote file."},
		},
		Usage: []string{
			"Register with Claude Code: claude mcp add --transport http --scope user --header \"Authorization: Bearer <token>\" <name> https://<host>:9443/mcp",
			"Register with Codex: codex mcp add <name> --url https://<host>:9443/mcp --bearer-token-env-var <ENV_VAR>",
			"File uploads and downloads use short-lived single-use tickets returned by create_upload / create_download; transfer with curl to the returned URL.",
		},
	}
	body, err := json.MarshalIndent(readme, "", "  ")
	if err != nil {
		body = []byte(`{"name":"deploymate-agent"}`)
	}
	body = append(body, '\n')
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}

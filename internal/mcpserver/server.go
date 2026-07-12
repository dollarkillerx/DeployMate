package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	commandpkg "github.com/jianping5/DeployMate/internal/command"
	filespkg "github.com/jianping5/DeployMate/internal/files"
	jobspkg "github.com/jianping5/DeployMate/internal/jobs"
	"github.com/jianping5/DeployMate/internal/systeminfo"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Dependencies struct {
	Runner        *commandpkg.Runner
	Jobs          *jobspkg.Manager
	Files         *filespkg.Manager
	PublicBaseURL string
	Version       string
	Logger        *slog.Logger
}

type emptyInput struct{}
type execInput struct {
	Command        string            `json:"command" jsonschema:"Shell command executed through /bin/sh -lc"`
	Cwd            string            `json:"cwd,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int64             `json:"timeout_seconds,omitempty"`
	MaxOutputBytes int64             `json:"max_output_bytes,omitempty"`
}
type execOutput struct {
	ExitCode             int    `json:"exit_code"`
	Stdout               string `json:"stdout"`
	Stderr               string `json:"stderr"`
	DurationMilliseconds int64  `json:"duration_milliseconds"`
	StdoutTruncated      bool   `json:"stdout_truncated"`
	StderrTruncated      bool   `json:"stderr_truncated"`
	TimedOut             bool   `json:"timed_out"`
	Cancelled            bool   `json:"cancelled"`
}
type startOutput struct {
	JobID  string         `json:"job_id"`
	Status jobspkg.Status `json:"status"`
}
type jobInput struct {
	JobID  string `json:"job_id"`
	Cursor int64  `json:"cursor,omitempty"`
}
type cancelInput struct {
	JobID string `json:"job_id"`
}
type cancelOutput struct {
	Cancelled bool `json:"cancelled"`
}
type listInput struct {
	Status jobspkg.Status `json:"status,omitempty"`
	Limit  int            `json:"limit,omitempty"`
}
type listOutput struct {
	Jobs []jobspkg.Job `json:"jobs"`
}
type uploadInput struct {
	RemotePath string `json:"remote_path"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Mode       uint32 `json:"mode,omitempty"`
	Overwrite  bool   `json:"overwrite,omitempty"`
}
type transferOutput struct {
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	ExpiresAt time.Time         `json:"expires_at"`
	Size      int64             `json:"size"`
	SHA256    string            `json:"sha256"`
	Headers   map[string]string `json:"headers,omitempty"`
}
type downloadInput struct {
	RemotePath string `json:"remote_path"`
}

func NewServer(d Dependencies) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "deploymate-agent", Version: d.Version}, nil)
	if d.Logger != nil {
		s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				started := time.Now()
				tool := ""
				if call, ok := req.(*mcp.CallToolRequest); ok {
					tool = call.Params.Name
				}
				result, err := next(ctx, method, req)
				d.Logger.Info("mcp operation", "method", method, "tool", tool, "duration_ms", time.Since(started).Milliseconds(), "error", errString(err))
				return result, err
			}
		})
	}
	mcp.AddTool(s, &mcp.Tool{Name: "get_server_info", Description: "Get operating system, CPU, memory, disk, network, uptime, systemd, and agent information."}, func(ctx context.Context, req *mcp.CallToolRequest, in emptyInput) (*mcp.CallToolResult, systeminfo.Info, error) {
		out, err := systeminfo.Collect(d.Version)
		return nil, out, err
	})
	mcp.AddTool(s, &mcp.Tool{Name: "exec_command", Description: "Execute a shell command synchronously as the agent user (root in the systemd installation)."}, func(ctx context.Context, req *mcp.CallToolRequest, in execInput) (*mcp.CallToolResult, execOutput, error) {
		if strings.TrimSpace(in.Command) == "" {
			return nil, execOutput{}, fmt.Errorf("command is required")
		}
		result, err := d.Runner.Run(ctx, commandRequest(in))
		return nil, mapExec(result), codedError(err)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "start_command", Description: "Start a long-running shell command and return a job ID."}, func(ctx context.Context, req *mcp.CallToolRequest, in execInput) (*mcp.CallToolResult, startOutput, error) {
		if strings.TrimSpace(in.Command) == "" {
			return nil, startOutput{}, fmt.Errorf("command is required")
		}
		job, err := d.Jobs.Start(commandRequest(in))
		return nil, startOutput{JobID: job.ID, Status: job.Status}, codedError(err)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "get_job", Description: "Get job status and incremental stdout/stderr from a cursor."}, func(ctx context.Context, req *mcp.CallToolRequest, in jobInput) (*mcp.CallToolResult, jobspkg.View, error) {
		out, err := d.Jobs.Get(in.JobID, in.Cursor)
		return nil, out, codedError(err)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "cancel_job", Description: "Cancel a running job and its process group."}, func(ctx context.Context, req *mcp.CallToolRequest, in cancelInput) (*mcp.CallToolResult, cancelOutput, error) {
		err := d.Jobs.Cancel(in.JobID)
		return nil, cancelOutput{Cancelled: err == nil}, codedError(err)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "list_jobs", Description: "List recent asynchronous jobs, optionally filtered by status."}, func(ctx context.Context, req *mcp.CallToolRequest, in listInput) (*mcp.CallToolResult, listOutput, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		return nil, listOutput{Jobs: d.Jobs.List(in.Status, limit)}, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "create_upload", Description: "Create a short-lived, single-use HTTP PUT URL for uploading a local file."}, func(ctx context.Context, req *mcp.CallToolRequest, in uploadInput) (*mcp.CallToolResult, transferOutput, error) {
		ticket, err := d.Files.CreateUpload(in.RemotePath, in.Size, in.SHA256, os.FileMode(in.Mode), in.Overwrite)
		if err != nil {
			return nil, transferOutput{}, codedError(err)
		}
		return nil, transferOutput{Method: http.MethodPut, URL: strings.TrimRight(d.PublicBaseURL, "/") + "/files/upload/" + ticket.ID, ExpiresAt: ticket.ExpiresAt, Size: ticket.Size, SHA256: ticket.SHA256, Headers: map[string]string{"Content-Type": "application/octet-stream"}}, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "create_download", Description: "Create a short-lived, single-use HTTP GET URL for downloading a remote file."}, func(ctx context.Context, req *mcp.CallToolRequest, in downloadInput) (*mcp.CallToolResult, transferOutput, error) {
		ticket, err := d.Files.CreateDownload(in.RemotePath)
		if err != nil {
			return nil, transferOutput{}, codedError(err)
		}
		return nil, transferOutput{Method: http.MethodGet, URL: strings.TrimRight(d.PublicBaseURL, "/") + "/files/download/" + ticket.ID, ExpiresAt: ticket.ExpiresAt, Size: ticket.Size, SHA256: ticket.SHA256}, nil
	})
	return s
}

func NewHandler(d Dependencies) http.Handler {
	s := NewServer(d)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{JSONResponse: true, Stateless: true})
}

func commandRequest(in execInput) commandpkg.Request {
	return commandpkg.Request{Command: in.Command, Cwd: in.Cwd, Env: in.Env, Timeout: time.Duration(in.TimeoutSeconds) * time.Second, MaxOutputBytes: in.MaxOutputBytes}
}
func mapExec(r commandpkg.Result) execOutput {
	return execOutput{ExitCode: r.ExitCode, Stdout: r.Stdout, Stderr: r.Stderr, DurationMilliseconds: r.Duration.Milliseconds(), StdoutTruncated: r.StdoutTruncated, StderrTruncated: r.StderrTruncated, TimedOut: r.TimedOut, Cancelled: r.Cancelled}
}

func codedError(err error) error {
	if err == nil {
		return nil
	}
	code := "INTERNAL_ERROR"
	switch {
	case errors.Is(err, commandpkg.ErrLimitReached):
		code = "COMMAND_LIMIT_REACHED"
	case errors.Is(err, jobspkg.ErrNotFound):
		code = "JOB_NOT_FOUND"
	case errors.Is(err, filespkg.ErrFileTooLarge):
		code = "FILE_TOO_LARGE"
	case errors.Is(err, filespkg.ErrDestinationExists):
		code = "INVALID_ARGUMENT"
	}
	return fmt.Errorf("%s: %w", code, err)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

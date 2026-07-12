package command

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

var ErrLimitReached = errors.New("command concurrency limit reached")

type Request struct {
	Command        string
	Cwd            string
	Env            map[string]string
	Timeout        time.Duration
	MaxOutputBytes int64
	OnStdout       func([]byte) `json:"-"`
	OnStderr       func([]byte) `json:"-"`
}

type Result struct {
	ExitCode        int           `json:"exit_code"`
	Stdout          string        `json:"stdout"`
	Stderr          string        `json:"stderr"`
	Duration        time.Duration `json:"duration"`
	StdoutTruncated bool          `json:"stdout_truncated"`
	StderrTruncated bool          `json:"stderr_truncated"`
	TimedOut        bool          `json:"timed_out"`
	Cancelled       bool          `json:"cancelled"`
}

type Runner struct {
	sem              chan struct{}
	defaultOutputMax int64
	defaultTimeout   time.Duration
	maxTimeout       time.Duration
}

func NewRunner(maxConcurrent int, defaultOutputMax int64, maxTimeout time.Duration) *Runner {
	return NewRunnerWithTimeouts(maxConcurrent, defaultOutputMax, maxTimeout, maxTimeout)
}

func NewRunnerWithTimeouts(maxConcurrent int, defaultOutputMax int64, defaultTimeout, maxTimeout time.Duration) *Runner {
	return &Runner{sem: make(chan struct{}, maxConcurrent), defaultOutputMax: defaultOutputMax, defaultTimeout: defaultTimeout, maxTimeout: maxTimeout}
}

func (r *Runner) Run(parent context.Context, req Request) (Result, error) {
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	default:
		return Result{}, ErrLimitReached
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = r.defaultTimeout
	}
	if timeout > r.maxTimeout {
		timeout = r.maxTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	limit := req.MaxOutputBytes
	if limit <= 0 || limit > r.defaultOutputMax {
		limit = r.defaultOutputMax
	}
	stdout := &limitedBuffer{limit: limit, onWrite: req.OnStdout}
	stderr := &limitedBuffer{limit: limit, onWrite: req.OnStderr}
	cmd := exec.Command("/bin/sh", "-lc", req.Command)
	cmd.Dir = req.Cwd
	cmd.Env = os.Environ()
	for key, value := range req.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	started := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{}, err
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-waited:
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		waitErr = <-waited
	}
	result := Result{
		ExitCode: exitCode(waitErr), Stdout: stdout.String(), Stderr: stderr.String(),
		Duration: time.Since(started), StdoutTruncated: stdout.truncated, StderrTruncated: stderr.truncated,
		TimedOut:  errors.Is(ctx.Err(), context.DeadlineExceeded),
		Cancelled: errors.Is(ctx.Err(), context.Canceled),
	}
	return result, nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int64
	truncated bool
	onWrite   func([]byte)
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := b.limit - int64(b.buf.Len())
	if remaining <= 0 {
		b.truncated = b.truncated || original > 0
		return original, nil
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, _ = b.buf.Write(p)
	if b.onWrite != nil && len(p) > 0 {
		chunk := append([]byte(nil), p...)
		b.onWrite(chunk)
	}
	return original, nil
}

func (b *limitedBuffer) String() string { return b.buf.String() }

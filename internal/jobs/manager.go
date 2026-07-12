package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	commandpkg "github.com/jianping5/DeployMate/internal/command"
)

var ErrNotFound = errors.New("job not found")

type Status string

const (
	Queued    Status = "queued"
	Running   Status = "running"
	Succeeded Status = "succeeded"
	Failed    Status = "failed"
	Cancelled Status = "cancelled"
	TimedOut  Status = "timed_out"
)

func (s Status) terminal() bool {
	return s == Succeeded || s == Failed || s == Cancelled || s == TimedOut
}

type Job struct {
	ID         string             `json:"id"`
	Status     Status             `json:"status"`
	Request    commandpkg.Request `json:"request"`
	Result     commandpkg.Result  `json:"result"`
	Error      string             `json:"error,omitempty"`
	CreatedAt  time.Time          `json:"created_at"`
	StartedAt  *time.Time         `json:"started_at,omitempty"`
	FinishedAt *time.Time         `json:"finished_at,omitempty"`
}

type View struct {
	Job
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	NextCursor int64  `json:"next_cursor"`
}

type Manager struct {
	mu      sync.RWMutex
	dir     string
	runner  *commandpkg.Runner
	jobs    map[string]*Job
	cancels map[string]context.CancelFunc
}

func NewManager(dir string, runner *commandpkg.Runner) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	m := &Manager{dir: dir, runner: runner, jobs: map[string]*Job{}, cancels: map[string]context.CancelFunc{}}
	entries, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	for _, path := range entries {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var job Job
		if err := json.Unmarshal(b, &job); err != nil {
			return nil, err
		}
		if !job.Status.terminal() {
			now := time.Now()
			job.Status, job.Error, job.FinishedAt = Failed, "agent restarted before job completed", &now
			_ = m.persist(&job)
		}
		m.jobs[job.ID] = &job
	}
	return m, nil
}

func (m *Manager) Start(req commandpkg.Request) (Job, error) {
	id, err := randomID("job_")
	if err != nil {
		return Job{}, err
	}
	job := &Job{ID: id, Status: Queued, Request: req, CreatedAt: time.Now()}
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.jobs[id], m.cancels[id] = job, cancel
	if err := m.persist(job); err != nil {
		delete(m.jobs, id)
		delete(m.cancels, id)
		m.mu.Unlock()
		cancel()
		return Job{}, err
	}
	snapshot := *job
	m.mu.Unlock()
	go m.run(ctx, id)
	return snapshot, nil
}

func (m *Manager) run(ctx context.Context, id string) {
	m.mu.Lock()
	job := m.jobs[id]
	now := time.Now()
	job.Status, job.StartedAt = Running, &now
	_ = m.persist(job)
	m.mu.Unlock()
	runRequest := job.Request
	runRequest.OnStdout = func(chunk []byte) { m.appendLog(id, true, chunk) }
	runRequest.OnStderr = func(chunk []byte) { m.appendLog(id, false, chunk) }
	result, err := m.runner.Run(ctx, runRequest)
	m.mu.Lock()
	defer m.mu.Unlock()
	result.Stdout, result.Stderr = "", ""
	job.Result = result
	finished := time.Now()
	job.FinishedAt = &finished
	switch {
	case result.Cancelled:
		job.Status = Cancelled
	case result.TimedOut:
		job.Status = TimedOut
	case err != nil:
		job.Status, job.Error = Failed, err.Error()
	case result.ExitCode == 0:
		job.Status = Succeeded
	default:
		job.Status = Failed
	}
	delete(m.cancels, id)
	_ = m.persist(job)
}

func (m *Manager) Get(id string, cursor int64) (View, error) {
	m.mu.RLock()
	job, ok := m.jobs[id]
	if !ok {
		m.mu.RUnlock()
		return View{}, ErrNotFound
	}
	snapshot := *job
	m.mu.RUnlock()
	if cursor < 0 {
		cursor = 0
	}
	stdoutCursor, stderrCursor := unpackCursor(cursor)
	out, stdoutNext := m.readLog(id, "stdout", stdoutCursor)
	errOut, stderrNext := m.readLog(id, "stderr", stderrCursor)
	view := View{Job: snapshot, Stdout: out, Stderr: errOut, NextCursor: packCursor(stdoutNext, stderrNext)}
	return view, nil
}

func (m *Manager) appendLog(id string, stdout bool, chunk []byte) {
	stream := "stderr"
	if stdout {
		stream = "stdout"
	}
	path := filepath.Join(m.dir, id+"."+stream)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(chunk)
	_ = f.Close()
}

func (m *Manager) readLog(id, stream string, offset int) (string, int) {
	path := filepath.Join(m.dir, id+"."+stream)
	f, err := os.Open(path)
	if err != nil {
		return "", offset
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", offset
	}
	size := int(info.Size())
	if offset < 0 || offset > size {
		offset = size
	}
	if _, err := f.Seek(int64(offset), 0); err != nil {
		return "", offset
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", offset
	}
	return string(b), offset + len(b)
}

func packCursor(stdout, stderr int) int64 {
	return int64(uint64(uint32(stdout))<<32 | uint64(uint32(stderr)))
}
func unpackCursor(cursor int64) (int, int) {
	value := uint64(cursor)
	return int(uint32(value >> 32)), int(uint32(value))
}

func (m *Manager) Cancel(id string) error {
	m.mu.RLock()
	job, ok := m.jobs[id]
	cancel := m.cancels[id]
	terminal := ok && job.Status.terminal()
	m.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}
	if terminal {
		return nil
	}
	if cancel != nil {
		cancel()
	}
	return nil
}

func (m *Manager) List(status Status, limit int) []Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]Job, 0, len(m.jobs))
	for _, job := range m.jobs {
		if status == "" || job.Status == status {
			items = append(items, *job)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

func (m *Manager) persist(job *Job) error {
	b, err := json.Marshal(job)
	if err != nil {
		return err
	}
	path := filepath.Join(m.dir, job.ID+".json")
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

func randomID(prefix string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

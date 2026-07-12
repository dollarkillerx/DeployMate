package jobs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	commandpkg "github.com/jianping5/DeployMate/internal/command"
)

func TestManagerRunsJobAndReadsLogsByCursor(t *testing.T) {
	runner := commandpkg.NewRunner(2, 1024, 2*time.Second)
	dir := t.TempDir()
	m, err := NewManager(dir, runner)
	if err != nil {
		t.Fatal(err)
	}
	job, err := m.Start(commandpkg.Request{Command: "printf hello; printf problem >&2"})
	if err != nil {
		t.Fatal(err)
	}
	job = waitForTerminal(t, m, job.ID)
	if job.Status != Succeeded {
		t.Fatalf("job status = %s, error=%s", job.Status, job.Error)
	}
	view, err := m.Get(job.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if view.Stdout != "hello" || view.Stderr != "problem" || view.NextCursor == 0 {
		t.Fatalf("unexpected view: %+v", view)
	}
	empty, err := m.Get(job.ID, view.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Stdout != "" || empty.Stderr != "" {
		t.Fatalf("cursor returned duplicate logs: %+v", empty)
	}
	metadata, err := os.ReadFile(filepath.Join(dir, job.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(metadata), `"stdout":"hello"`) || strings.Contains(string(metadata), `"stdout_log"`) {
		t.Fatalf("job metadata duplicates command logs: %s", metadata)
	}
	stdoutFile, err := os.ReadFile(filepath.Join(dir, job.ID+".stdout"))
	if err != nil || string(stdoutFile) != "hello" {
		t.Fatalf("stdout file=%q err=%v", stdoutFile, err)
	}
}

func TestManagerCancelsJob(t *testing.T) {
	runner := commandpkg.NewRunner(1, 1024, 5*time.Second)
	m, err := NewManager(t.TempDir(), runner)
	if err != nil {
		t.Fatal(err)
	}
	job, err := m.Start(commandpkg.Request{Command: "sleep 10"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := m.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	job = waitForTerminal(t, m, job.ID)
	if job.Status != Cancelled {
		t.Fatalf("job status = %s", job.Status)
	}
}

func TestManagerExposesLogsWhileJobIsRunning(t *testing.T) {
	runner := commandpkg.NewRunner(1, 1024, 3*time.Second)
	m, err := NewManager(t.TempDir(), runner)
	if err != nil {
		t.Fatal(err)
	}
	job, err := m.Start(commandpkg.Request{Command: "printf first; sleep 1; printf second"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(deadline) {
		view, err := m.Get(job.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == Running && view.Stdout == "first" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("running job did not expose its first output chunk")
}

func TestManagerRejectsUnknownJob(t *testing.T) {
	m, err := NewManager(t.TempDir(), commandpkg.NewRunner(1, 1024, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get("missing", 0); err != ErrNotFound {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestCancelSynchronizesWithStatusChanges(t *testing.T) {
	m, err := NewManager(t.TempDir(), commandpkg.NewRunner(1, 1024, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	job := &Job{ID: "job_race", Status: Running, CreatedAt: time.Now()}
	m.jobs[job.ID] = job
	m.cancels[job.ID] = func() {}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			m.mu.Lock()
			if i%2 == 0 {
				job.Status = Running
			} else {
				job.Status = Succeeded
			}
			m.mu.Unlock()
		}
	}()
	for i := 0; i < 10000; i++ {
		_ = m.Cancel(job.ID)
	}
	<-done
}

func waitForTerminal(t *testing.T, m *Manager, id string) Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view, err := m.Get(id, 0)
		if err != nil {
			t.Fatal(err)
		}
		if view.Job.Status.terminal() {
			return view.Job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return Job{}
}

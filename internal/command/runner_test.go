package command

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunnerCapturesExitAndTruncatesOutput(t *testing.T) {
	r := NewRunner(1, 5, time.Second)
	result, err := r.Run(context.Background(), Request{Command: "printf 123456789; printf err >&2; exit 7", MaxOutputBytes: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 7 || result.Stdout != "12345" || result.Stderr != "err" || !result.StdoutTruncated {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestRunnerDoesNotAllowRequestToExceedServerOutputLimit(t *testing.T) {
	r := NewRunner(1, 5, time.Second)
	result, err := r.Run(context.Background(), Request{Command: "printf 123456789", MaxOutputBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "12345" || !result.StdoutTruncated {
		t.Fatalf("server output cap was bypassed: %+v", result)
	}
}

func TestRunnerTimesOutProcessGroup(t *testing.T) {
	r := NewRunner(1, 1024, 100*time.Millisecond)
	result, err := r.Run(context.Background(), Request{Command: "sleep 10", Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if !result.TimedOut || result.Duration > 2*time.Second {
		t.Fatalf("command did not time out promptly: %+v", result)
	}
}

func TestRunnerUsesConfiguredDefaultTimeout(t *testing.T) {
	r := NewRunnerWithTimeouts(1, 1024, 50*time.Millisecond, time.Second)
	result, err := r.Run(context.Background(), Request{Command: "sleep 10"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.TimedOut || result.Duration > 2*time.Second {
		t.Fatalf("default timeout not applied: %+v", result)
	}
}

func TestRunnerPassesWorkingDirectoryAndEnvironment(t *testing.T) {
	r := NewRunner(1, 1024, time.Second)
	result, err := r.Run(context.Background(), Request{Command: "printf '%s:%s' \"$PWD\" \"$DEPLOYMATE_TEST\"", Cwd: t.TempDir(), Env: map[string]string{"DEPLOYMATE_TEST": "ok"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(result.Stdout, ":ok") {
		t.Fatalf("environment not applied: %+v", result)
	}
}

func TestRunnerRejectsWhenConcurrencyIsExhausted(t *testing.T) {
	r := NewRunner(1, 1024, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(started)
		_, _ = r.Run(ctx, Request{Command: "sleep 10"})
		close(done)
	}()
	<-started
	time.Sleep(30 * time.Millisecond)
	_, err := r.Run(context.Background(), Request{Command: "true"})
	if err != ErrLimitReached {
		t.Fatalf("error = %v, want ErrLimitReached", err)
	}
	cancel()
	<-done
}

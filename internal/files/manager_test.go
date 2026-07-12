package files

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUploadTicketWritesVerifiedFileAndIsSingleUse(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "release.bin")
	content := []byte("deploymate")
	sum := sha256.Sum256(content)
	m := NewManager(1024, time.Minute)
	ticket, err := m.CreateUpload(destination, int64(len(content)), hex.EncodeToString(sum[:]), 0o640, false)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPut, "/files/upload/"+ticket.ID, bytes.NewReader(content))
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	got, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("file=%q err=%v", got, err)
	}
	info, _ := os.Stat(destination)
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}

	rr = httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/files/upload/"+ticket.ID, bytes.NewReader(content)))
	if rr.Code != http.StatusGone {
		t.Fatalf("reused ticket status=%d", rr.Code)
	}
}

func TestUploadRejectsChecksumMismatchAndCleansTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "release.bin")
	m := NewManager(1024, time.Minute)
	ticket, err := m.CreateUpload(destination, 3, strings.Repeat("0", 64), 0o600, false)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/files/upload/"+ticket.ID, bytes.NewBufferString("abc")))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, ".deploymate-upload-*"))
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}

func TestDownloadTicketReturnsFileOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(1024, time.Minute)
	ticket, err := m.CreateDownload(path)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Size != 5 || ticket.SHA256 == "" {
		t.Fatalf("ticket=%+v", ticket)
	}
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/files/download/"+ticket.ID, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	body, _ := io.ReadAll(rr.Result().Body)
	if string(body) != "hello" {
		t.Fatalf("body=%q", body)
	}
	rr = httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/files/download/"+ticket.ID, nil))
	if rr.Code != http.StatusGone {
		t.Fatalf("reused ticket status=%d", rr.Code)
	}
}

func TestDownloadRejectsFileChangedAfterTicketCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(1024, time.Minute)
	ticket, err := m.CreateDownload(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/files/download/"+ticket.ID, nil))
	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateUploadRejectsTooLargeAndExistingDestination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(4, time.Minute)
	if _, err := m.CreateUpload(path, 5, strings.Repeat("0", 64), 0o600, true); err != ErrFileTooLarge {
		t.Fatalf("large error=%v", err)
	}
	if _, err := m.CreateUpload(path, 1, strings.Repeat("0", 64), 0o600, false); err != ErrDestinationExists {
		t.Fatalf("exists error=%v", err)
	}
}

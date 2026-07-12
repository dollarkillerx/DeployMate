package files

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrFileTooLarge      = errors.New("file too large")
	ErrDestinationExists = errors.New("destination exists")
)

type Direction string

const (
	Upload   Direction = "upload"
	Download Direction = "download"
)

type Ticket struct {
	ID        string      `json:"id"`
	Direction Direction   `json:"direction"`
	Path      string      `json:"-"`
	Size      int64       `json:"size"`
	SHA256    string      `json:"sha256"`
	Mode      os.FileMode `json:"-"`
	Overwrite bool        `json:"-"`
	ExpiresAt time.Time   `json:"expires_at"`
}

type Manager struct {
	mu      sync.Mutex
	maxSize int64
	ttl     time.Duration
	tickets map[string]Ticket
}

func NewManager(maxSize int64, ttl time.Duration) *Manager {
	return &Manager{maxSize: maxSize, ttl: ttl, tickets: make(map[string]Ticket)}
}

func (m *Manager) CreateUpload(path string, size int64, checksum string, mode os.FileMode, overwrite bool) (Ticket, error) {
	if size < 0 || size > m.maxSize {
		return Ticket{}, ErrFileTooLarge
	}
	if decoded, err := hex.DecodeString(checksum); err != nil || len(decoded) != sha256.Size {
		return Ticket{}, fmt.Errorf("invalid sha256")
	}
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return Ticket{}, ErrDestinationExists
		} else if !os.IsNotExist(err) {
			return Ticket{}, err
		}
	}
	if mode == 0 {
		mode = 0o600
	}
	return m.create(Ticket{Direction: Upload, Path: path, Size: size, SHA256: strings.ToLower(checksum), Mode: mode.Perm(), Overwrite: overwrite})
}

func (m *Manager) CreateDownload(path string) (Ticket, error) {
	file, err := os.Open(path)
	if err != nil {
		return Ticket{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Ticket{}, err
	}
	if !info.Mode().IsRegular() {
		return Ticket{}, fmt.Errorf("path is not a regular file")
	}
	if info.Size() > m.maxSize {
		return Ticket{}, ErrFileTooLarge
	}
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return Ticket{}, err
	}
	return m.create(Ticket{Direction: Download, Path: path, Size: info.Size(), SHA256: hex.EncodeToString(h.Sum(nil))})
}

func (m *Manager) create(ticket Ticket) (Ticket, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return Ticket{}, err
	}
	prefix := "up_"
	if ticket.Direction == Download {
		prefix = "down_"
	}
	ticket.ID = prefix + hex.EncodeToString(b)
	ticket.ExpiresAt = time.Now().Add(m.ttl)
	m.mu.Lock()
	m.tickets[ticket.ID] = ticket
	m.mu.Unlock()
	return ticket, nil
}

func (m *Manager) consume(id string, direction Direction) (Ticket, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ticket, ok := m.tickets[id]
	if ok {
		delete(m.tickets, id)
	}
	if !ok || ticket.Direction != direction || time.Now().After(ticket.ExpiresAt) {
		return Ticket{}, false
	}
	return ticket, true
}

func (m *Manager) Handler() http.Handler { return http.HandlerFunc(m.serveHTTP) }

func (m *Manager) serveHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "files" {
		http.NotFound(w, r)
		return
	}
	switch {
	case parts[1] == "upload" && r.Method == http.MethodPut:
		ticket, ok := m.consume(parts[2], Upload)
		if !ok {
			writeError(w, http.StatusGone, "TRANSFER_EXPIRED", "transfer ticket is invalid, expired, or already used")
			return
		}
		m.handleUpload(w, r, ticket)
	case parts[1] == "download" && r.Method == http.MethodGet:
		ticket, ok := m.consume(parts[2], Download)
		if !ok {
			writeError(w, http.StatusGone, "TRANSFER_EXPIRED", "transfer ticket is invalid, expired, or already used")
			return
		}
		m.handleDownload(w, ticket)
	default:
		http.NotFound(w, r)
	}
}

func (m *Manager) handleUpload(w http.ResponseWriter, r *http.Request, ticket Ticket) {
	if err := os.MkdirAll(filepath.Dir(ticket.Path), 0o755); err != nil {
		writeError(w, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(ticket.Path), ".deploymate-upload-*")
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	h := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r.Body, ticket.Size+1))
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to write upload")
		return
	}
	if written != ticket.Size {
		writeError(w, http.StatusUnprocessableEntity, "INVALID_ARGUMENT", "uploaded size does not match ticket")
		return
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != ticket.SHA256 {
		writeError(w, http.StatusUnprocessableEntity, "CHECKSUM_MISMATCH", "uploaded checksum does not match ticket")
		return
	}
	if err := os.Chmod(tmpPath, ticket.Mode); err != nil {
		writeError(w, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	if ticket.Overwrite {
		err = os.Rename(tmpPath, ticket.Path)
	} else {
		err = os.Link(tmpPath, ticket.Path)
	}
	if err != nil {
		writeError(w, http.StatusConflict, "INVALID_ARGUMENT", err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (m *Manager) handleDownload(w http.ResponseWriter, ticket Ticket) {
	file, err := os.Open(ticket.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "INVALID_ARGUMENT", err.Error())
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != ticket.Size {
		writeError(w, http.StatusConflict, "CHECKSUM_MISMATCH", "file changed after download ticket was created")
		return
	}
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil || hex.EncodeToString(h.Sum(nil)) != ticket.SHA256 {
		writeError(w, http.StatusConflict, "CHECKSUM_MISMATCH", "file changed after download ticket was created")
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprint(ticket.Size))
	w.Header().Set("X-DeployMate-SHA256", ticket.SHA256)
	_, _ = io.Copy(w, file)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": message, "retryable": false})
}

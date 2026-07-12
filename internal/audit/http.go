package audit

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

func Middleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := newRequestID()
		w.Header().Set("X-Request-ID", requestID)
		wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		started := time.Now()
		next.ServeHTTP(wrapped, r)
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		logger.Info("request",
			"request_id", requestID, "client_ip", host, "method", r.Method,
			"operation", operation(r.URL.Path), "status", wrapped.status,
			"response_bytes", wrapped.bytes, "duration_ms", time.Since(started).Milliseconds())
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *responseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}

func operation(path string) string {
	if strings.HasPrefix(path, "/files/upload/") {
		return "file_upload"
	}
	if strings.HasPrefix(path, "/files/download/") {
		return "file_download"
	}
	if path == "/mcp" {
		return "mcp"
	}
	return "http"
}
func newRequestID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "req_unknown"
	}
	return "req_" + hex.EncodeToString(b)
}

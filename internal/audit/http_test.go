package audit

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddlewareAddsRequestIDAndRedactsTransferTicket(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h := Middleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) }))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/files/upload/up_secret", nil))
	if rr.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing request ID")
	}
	if strings.Contains(logs.String(), "up_secret") {
		t.Fatalf("ticket leaked in log: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"status":201`) {
		t.Fatalf("status missing from log: %s", logs.String())
	}
}

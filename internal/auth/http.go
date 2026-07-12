package auth

import (
	"encoding/json"
	"net/http"
	"strings"
)

func Middleware(verifier *Verifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || !verifier.Verify(token) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "UNAUTHORIZED", "message": "invalid bearer token", "retryable": false})
			return
		}
		next.ServeHTTP(w, r)
	})
}

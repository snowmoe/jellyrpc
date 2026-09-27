package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/snowmoe/jellyrpc/internal/jellyfin"
)

// newFakeJellyfin starts a httptest server with the routes given (using
// ServeMux patterns e.g "GET /Users/Me") and returns a client for it,
// anything not in routes 404s
func newFakeJellyfin(t *testing.T, key string, routes map[string]http.HandlerFunc) *jellyfin.Client {
	t.Helper()

	mux := http.NewServeMux()
	for pattern, h := range routes {
		mux.HandleFunc(pattern, h)
	}

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return jellyfin.NewClient(srv.URL, key, "test")
}

// respondJSON returns a handler that writes v as json
func respondJSON(t *testing.T, v any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := json.NewEncoder(w).Encode(v)
		if err != nil {
			t.Errorf("failed to write json: %v", err)
		}
	}
}

// respondStatus returns a handler that only writes a status code
func respondStatus(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
	}
}

// hasToken checks the requests auth header for the token
func hasToken(r *http.Request, token string) bool {
	return strings.Contains(r.Header.Get("Authorization"), `Token="`+token+`"`)
}

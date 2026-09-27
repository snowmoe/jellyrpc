package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient makes a httptest server with the handler and returns a
// client pointed at it, server gets closed on cleanup
func newTestClient(t *testing.T, key string, h http.HandlerFunc) *Client {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return NewClient(srv.URL, key, "test")
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()

	err := json.NewEncoder(w).Encode(v)
	if err != nil {
		t.Errorf("failed to write json: %v", err)
	}
}

func TestDoStatusError(t *testing.T) {
	c := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := c.PublicSystemInfo(context.Background())

	statErr, ok := errors.AsType[*StatusError](err)
	if !ok {
		t.Fatalf("expected *StatusError, got: %v", err)
	}
	if statErr.Code != 500 {
		t.Errorf("expected code 500, got %d", statErr.Code)
	}
}

func TestDoDecodeError(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected string
	}{
		// a reverse proxy page or just not jellyfin at all
		{"html body", "<html>salmon</html>", "unexpected response, is this a jellyfin server?"},
		{"wrong type", `{"ServerName": 5}`, "failed to decode jellyfin response"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, tc.body)
			})

			_, err := c.PublicSystemInfo(context.Background())

			_, ok := errors.AsType[*JSONDecodeError](err)
			if !ok {
				t.Fatalf("expected *JSONDecodeError, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.expected) {
				t.Errorf("expected error containing %q, got %q", tc.expected, err.Error())
			}
		})
	}
}

func TestDoContentType(t *testing.T) {
	var gotGet, gotPost string

	c := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			gotGet = r.Header.Get("Content-Type")
		case http.MethodPost:
			gotPost = r.Header.Get("Content-Type")
		}
		writeJSON(t, w, map[string]any{})
	})

	c.PublicSystemInfo(context.Background())
	c.AuthenticateQC(context.Background(), "salmon")

	if gotGet != "" {
		t.Errorf("expected no content type without a body, got %q", gotGet)
	}
	if gotPost != "application/json" {
		t.Errorf("expected application/json with a body, got %q", gotPost)
	}
}

func TestAuthHeader(t *testing.T) {
	c := NewClient("http://localhost", "", "v1.2.3")

	h := c.authHeader()
	for _, want := range []string{`MediaBrowser Client="jellyrpc"`, `Version="v1.2.3"`, `DeviceId="` + c.deviceID + `"`} {
		if !strings.Contains(h, want) {
			t.Errorf("expected header to contain %q, got: %s", want, h)
		}
	}
	if strings.Contains(h, "Token=") {
		t.Errorf("expected no token without a key, got: %s", h)
	}

	c.APIKey = "salmon"
	h = c.authHeader()
	if !strings.Contains(h, `Token="salmon"`) {
		t.Errorf("expected token in header, got: %s", h)
	}
}

func TestActiveSession(t *testing.T) {
	sessions := []Session{
		{UserName: "trout", NowPlayingItem: NowPlayingItem{Name: "not ours"}},
		// our user but idle, should be skipped over
		{UserName: "SNOW"},
		{UserName: "Snow", NowPlayingItem: NowPlayingItem{Name: "Salmon"}},
	}

	c := newTestClient(t, "salmon", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Sessions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		writeJSON(t, w, sessions)
	})

	t.Run("matches user case insensitive", func(t *testing.T) {
		c.UserName = "snow"

		s, err := c.ActiveSession(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.NowPlayingItem.Name != "Salmon" {
			t.Errorf("expected Salmon, got %q", s.NowPlayingItem.Name)
		}
	})

	t.Run("no session gives empty not nil", func(t *testing.T) {
		c.UserName = "pike"

		s, err := c.ActiveSession(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s == nil {
			t.Fatal("expected empty session, got nil")
		}
		if s.NowPlayingItem.Name != "" {
			t.Errorf("expected empty session, got %+v", *s)
		}
	})
}

func TestActiveSessionError(t *testing.T) {
	c := newTestClient(t, "salmon", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	s, err := c.ActiveSession(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if s != nil {
		t.Errorf("expected nil session on error, got %+v", *s)
	}
}

// setup and check both rely on /Users/Me giving a 400 for api keys
func TestCurrentUserAPIKey(t *testing.T) {
	c := newTestClient(t, "salmon", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	_, err := c.CurrentUser(context.Background())

	statErr, ok := errors.AsType[*StatusError](err)
	if !ok || statErr.Code != 400 {
		t.Errorf("expected 400 *StatusError, got: %v", err)
	}
}

func TestQCEmptySecret(t *testing.T) {
	c := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no request should be made, got: %s %s", r.Method, r.URL)
	})

	_, err := c.ConnectQC(context.Background(), "")
	if err == nil {
		t.Error("ConnectQC: expected error for empty secret")
	}

	_, err = c.AuthenticateQC(context.Background(), "")
	if err == nil {
		t.Error("AuthenticateQC: expected error for empty secret")
	}
}

func TestConnectQC(t *testing.T) {
	// make sure it actually gets encoded
	secret := "salmon & chips=yes"

	for _, authed := range []bool{true, false} {
		c := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/QuickConnect/Connect" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			if got := r.URL.Query().Get("secret"); got != secret {
				t.Errorf("expected secret %q, got %q", secret, got)
			}
			writeJSON(t, w, QuickConnect{Authenticated: authed})
		})

		ok, err := c.ConnectQC(context.Background(), secret)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok != authed {
			t.Errorf("expected %v, got %v", authed, ok)
		}
	}
}

func TestAuthenticateQC(t *testing.T) {
	c := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/Users/AuthenticateWithQuickConnect" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}

		var body struct{ Secret string }
		err := json.NewDecoder(r.Body).Decode(&body)
		if err != nil {
			t.Errorf("failed to decode body: %v", err)
		}
		if body.Secret != "salmon" {
			t.Errorf("expected secret salmon, got %q", body.Secret)
		}

		writeJSON(t, w, map[string]any{
			"User":        map[string]any{"Name": "snow"},
			"AccessToken": "trout",
		})
	})

	auth, err := c.AuthenticateQC(context.Background(), "salmon")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth.User.Name != "snow" || auth.Token != "trout" {
		t.Errorf("expected snow/trout, got %s/%s", auth.User.Name, auth.Token)
	}
}

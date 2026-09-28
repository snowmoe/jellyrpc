package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/snowmoe/jellyrpc/internal/jellyfin"
)

func assertResult(t *testing.T, got result, status status, msg string) {
	t.Helper()

	if got.status != status || got.msg != msg {
		t.Errorf("\nexpected: %s %q\ngot:      %s %q", status, msg, got.status, got.msg)
	}
}

func TestCheckServer(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		c := newFakeJellyfin(t, "", map[string]http.HandlerFunc{
			"GET /System/Info/Public": respondJSON(t, jellyfin.SystemInfo{ServerName: "salmon", Version: "10.11.0"}),
		})

		assertResult(t, checkServer(context.Background(), c), statusOK, "salmon (v10.11.0)")
	})

	t.Run("server error", func(t *testing.T) {
		c := newFakeJellyfin(t, "", map[string]http.HandlerFunc{
			"GET /System/Info/Public": respondStatus(http.StatusBadGateway),
		})

		assertResult(t, checkServer(context.Background(), c), statusFail, "jellyfin returned 502 Bad Gateway")
	})
}

func TestCheckToken(t *testing.T) {
	tests := []struct {
		name     string
		handler  func(t *testing.T) http.HandlerFunc
		status   status
		msg      string
		wantUser bool
	}{
		{
			name: "user token",
			handler: func(t *testing.T) http.HandlerFunc {
				return respondJSON(t, jellyfin.User{Name: "snow"})
			},
			status:   statusOK,
			msg:      "user: snow",
			wantUser: true,
		},
		{
			name:    "api key",
			handler: func(*testing.T) http.HandlerFunc { return respondStatus(http.StatusBadRequest) },
			status:  statusSkip,
			msg:     "using api key",
		},
		{
			name:    "rejected",
			handler: func(*testing.T) http.HandlerFunc { return respondStatus(http.StatusUnauthorized) },
			status:  statusFail,
			msg:     "token rejected",
		},
		{
			name:    "server error",
			handler: func(*testing.T) http.HandlerFunc { return respondStatus(http.StatusInternalServerError) },
			status:  statusFail,
			msg:     "jellyfin returned 500 Internal Server Error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newFakeJellyfin(t, "salmon", map[string]http.HandlerFunc{
				"GET /Users/Me": tc.handler(t),
			})

			r, user := checkToken(context.Background(), c)
			assertResult(t, r, tc.status, tc.msg)

			if tc.wantUser && (user == nil || user.Name != "snow") {
				t.Errorf("expected user snow, got %+v", user)
			}
			if !tc.wantUser && user != nil {
				t.Errorf("expected nil user, got %+v", *user)
			}
		})
	}
}

func TestCheckUser(t *testing.T) {
	users := []jellyfin.User{{Name: "trout"}, {Name: "Snow"}}

	t.Run("token user matches", func(t *testing.T) {
		c := newFakeJellyfin(t, "salmon", nil)
		c.UserName = "snow"

		r := checkUser(context.Background(), c, &jellyfin.User{Name: "SNOW"})
		assertResult(t, r, statusSkip, "using token")
	})

	t.Run("token user mismatch", func(t *testing.T) {
		c := newFakeJellyfin(t, "salmon", nil)
		c.UserName = "trout"

		r := checkUser(context.Background(), c, &jellyfin.User{Name: "snow"})
		assertResult(t, r, statusWarn, "mismatched usernames (token: snow, config: trout)")
	})

	t.Run("api key user exists", func(t *testing.T) {
		c := newFakeJellyfin(t, "salmon", map[string]http.HandlerFunc{
			"GET /Users": respondJSON(t, users),
		})
		c.UserName = "snow"

		// reports jellyfin's spelling of the name, not the configs
		r := checkUser(context.Background(), c, nil)
		assertResult(t, r, statusOK, "user 'Snow' exists")
	})

	t.Run("api key user missing", func(t *testing.T) {
		c := newFakeJellyfin(t, "salmon", map[string]http.HandlerFunc{
			"GET /Users": respondJSON(t, users),
		})
		c.UserName = "pike"

		r := checkUser(context.Background(), c, nil)
		assertResult(t, r, statusFail, "user 'pike' doesn't exist")
	})

	t.Run("api key users error", func(t *testing.T) {
		c := newFakeJellyfin(t, "salmon", map[string]http.HandlerFunc{
			"GET /Users": respondStatus(http.StatusForbidden),
		})
		c.UserName = "snow"

		r := checkUser(context.Background(), c, nil)
		assertResult(t, r, statusFail, "jellyfin returned 403 Forbidden")
	})
}

func TestCheckPlaying(t *testing.T) {
	tests := []struct {
		name     string
		sessions []jellyfin.Session
		msg      string
	}{
		{"nothing playing", nil, "nothing playing"},
		{
			name: "episode shows series",
			sessions: []jellyfin.Session{{
				UserName:       "snow",
				NowPlayingItem: jellyfin.NowPlayingItem{Name: "Pilot", SeriesName: "Salmon"},
			}},
			msg: "currently playing: Salmon",
		},
		{
			name: "movie shows name",
			sessions: []jellyfin.Session{{
				UserName:       "snow",
				NowPlayingItem: jellyfin.NowPlayingItem{Name: "Trout"},
			}},
			msg: "currently playing: Trout",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newFakeJellyfin(t, "salmon", map[string]http.HandlerFunc{
				"GET /Sessions": respondJSON(t, tc.sessions),
			})
			c.UserName = "snow"

			assertResult(t, checkPlaying(context.Background(), c), statusOK, tc.msg)
		})
	}

	t.Run("error", func(t *testing.T) {
		c := newFakeJellyfin(t, "salmon", map[string]http.HandlerFunc{
			"GET /Sessions": respondStatus(http.StatusUnauthorized),
		})

		r := checkPlaying(context.Background(), c)
		assertResult(t, r, statusFail, "jellyfin returned unauthorized, check your api key")
	})
}

func TestCheckConfigFile(t *testing.T) {
	dir := t.TempDir()

	write := func(t *testing.T, name, contents string) string {
		t.Helper()

		path := filepath.Join(dir, name)
		err := os.WriteFile(path, []byte(contents), 0o600)
		if err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("missing file", func(t *testing.T) {
		r, cfg := checkConfigFile(filepath.Join(dir, "nope"))
		assertResult(t, r, statusFail, "config file doesn't exist, try 'jellyrpc setup'")
		if cfg != nil {
			t.Errorf("expected nil config, got %+v", *cfg)
		}
	})

	t.Run("unknown keys", func(t *testing.T) {
		path := write(t, "unknown", "JELLYFIN_USER=snow\nSALMON=1\nTROUT=2\n")

		r, cfg := checkConfigFile(path)
		assertResult(t, r, statusWarn, "unknown key(s): SALMON, TROUT")
		// a warn still needs to hand the config on to the next checks
		if cfg == nil || cfg.JellyfinUser != "snow" {
			t.Errorf("expected config with user snow, got %+v", cfg)
		}
	})

	t.Run("multiple warnings", func(t *testing.T) {
		path := write(t, "warnings", "JELLYFIN_USER=snow\nARTWORK_SOURCE=salmon\nTROUT=1\n")

		r, cfg := checkConfigFile(path)
		assertResult(t, r, statusWarn, `invalid ARTWORK_SOURCE "salmon", using auto; unknown key(s): TROUT`)
		if cfg == nil || cfg.JellyfinUser != "snow" {
			t.Errorf("expected config with user snow, got %+v", cfg)
		}
	})

	t.Run("clean", func(t *testing.T) {
		path := write(t, "clean", "JELLYFIN_USER=snow\n")

		r, cfg := checkConfigFile(path)
		assertResult(t, r, statusOK, "")
		if cfg == nil {
			t.Error("expected config, got nil")
		}
	})

	t.Run("unreadable", func(t *testing.T) {
		r, cfg := checkConfigFile(dir)
		if r.status != statusFail {
			t.Errorf("expected fail, got %s %q", r.status, r.msg)
		}
		if cfg != nil {
			t.Errorf("expected nil config, got %+v", *cfg)
		}
	})
}

func TestCheckConfigValues(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		cfg := &Config{JellyfinURL: "https://jelly.example.com", PauseTimeout: -1}

		r := checkConfigValues(cfg)
		assertResult(t, r, statusFail, "config file missing required values: JELLYFIN_KEY, JELLYFIN_USER")
	})

	t.Run("valid", func(t *testing.T) {
		cfg := &Config{
			JellyfinURL:  "https://jelly.example.com",
			JellyfinKey:  "salmon",
			JellyfinUser: "snow",
			PauseTimeout: -1,
		}

		assertResult(t, checkConfigValues(cfg), statusOK, "")

		// defaults get applied here so later checks (handshake) see the app id
		if cfg.AppID != defaultAppID {
			t.Errorf("expected default app id, got %q", cfg.AppID)
		}
	})
}

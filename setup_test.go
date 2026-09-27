package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/snowmoe/jellyrpc/internal/jellyfin"
)

func TestUpdateConfig(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		values   map[string]string
		expected string
	}{
		{
			name:     "replace existing key in place",
			src:      "A=1\n# comment\n\nB=2\nC=3\n",
			values:   map[string]string{"B": "x"},
			expected: "A=1\n# comment\n\nB=x\nC=3\n",
		},
		{
			name:     "untouched lines kept byte for byte",
			src:      "  # indented comment  \n\t\nnot a key\nUNKNOWN = spaced  \nB=2\n",
			values:   map[string]string{"B": "x"},
			expected: "  # indented comment  \n\t\nnot a key\nUNKNOWN = spaced  \nB=x\n",
		},
		{
			name:     "missing keys appended sorted",
			src:      "A=1\n",
			values:   map[string]string{"Z": "z", "M": "m"},
			expected: "A=1\nM=m\nZ=z\n",
		},
		{
			name:     "no trailing newline before append",
			src:      "A=1",
			values:   map[string]string{"B": "b"},
			expected: "A=1\nB=b\n",
		},
		{
			name:     "no trailing newline on replaced last line",
			src:      "A=1",
			values:   map[string]string{"A": "2"},
			expected: "A=2\n",
		},
		{
			name:     "empty src",
			src:      "",
			values:   map[string]string{"A": "1"},
			expected: "A=1\n",
		},
		{
			name:     "empty src and values",
			src:      "",
			values:   nil,
			expected: "",
		},
		{
			name:     "spaced key still matches",
			src:      "  KEY = old  \n",
			values:   map[string]string{"KEY": "new"},
			expected: "KEY=new\n",
		},
		{
			name:     "value containing equals kept whole",
			src:      "URL=http://x?a=b\n",
			values:   map[string]string{"KEY": "abc=="},
			expected: "URL=http://x?a=b\nKEY=abc==\n",
		},
		{
			name:     "commented key not uncommented",
			src:      "# KEY=\n",
			values:   map[string]string{"KEY": "v"},
			expected: "# KEY=\nKEY=v\n",
		},
		{
			name:     "duplicate keys both replaced",
			src:      "A=1\nA=2\n",
			values:   map[string]string{"A": "3"},
			expected: "A=3\nA=3\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := updateConfig(tc.src, tc.values)
			if got != tc.expected {
				t.Errorf("\nexpected: %q\ngot:      %q", tc.expected, got)
			}
		})
	}
}

func TestUpdateConfigIdempotent(t *testing.T) {
	values := map[string]string{
		"JELLYFIN_URL":  "https://jelly.example.com",
		"JELLYFIN_KEY":  "abc123",
		"JELLYFIN_USER": "snow",
		"NEW_KEY":       "appended",
	}

	once := updateConfig(exampleCfg, values)
	twice := updateConfig(once, values)

	if once != twice {
		t.Errorf("second update changed output\nonce:  %q\ntwice: %q", once, twice)
	}
}

func TestUpdateConfigExample(t *testing.T) {
	values := map[string]string{
		"JELLYFIN_URL":  "https://jelly.example.com",
		"JELLYFIN_KEY":  "abc123",
		"JELLYFIN_USER": "snow",
	}

	expected := `# required values
JELLYFIN_URL=https://jelly.example.com
JELLYFIN_KEY=abc123
JELLYFIN_USER=snow

# optional values, uncomment to use
# !! see readme for more info
# POLL_RATE=
# PAUSE_TIMEOUT=
# APP_ID=
# DB_LINK=
# USE_EPISODE_ART=

`

	got := updateConfig(exampleCfg, values)
	if got != expected {
		t.Errorf("\nexpected:\n%s\ngot:\n%s", expected, got)
	}
}

// whatever setup writes needs to actually load in run
func TestUpdateConfigRoundTrip(t *testing.T) {
	values := map[string]string{
		"JELLYFIN_URL":  "https://jelly.example.com",
		"JELLYFIN_KEY":  "abc123",
		"JELLYFIN_USER": "snow",
	}

	src := updateConfig(exampleCfg, values)

	cfg, unknown, err := parseConfig(strings.NewReader(src))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(unknown) != 0 {
		t.Errorf("expected no unknown keys, got: %v", unknown)
	}

	cfg.applyDefaults(defaultAppID)

	missing, err := cfg.validate()
	if err != nil {
		t.Fatalf("expected valid config, got: %v (%v)", err, missing)
	}

	if cfg.JellyfinURL != values["JELLYFIN_URL"] ||
		cfg.JellyfinKey != values["JELLYFIN_KEY"] ||
		cfg.JellyfinUser != values["JELLYFIN_USER"] {
		t.Errorf("values didn't round trip, got: %+v", *cfg)
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	t.Run("creates file", func(t *testing.T) {
		err := writeFileAtomic(path, []byte("first\n"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		assertFile(t, path, "first\n", 0o600)
	})

	t.Run("overwrites existing file", func(t *testing.T) {
		// old file's looser perms shouldn't carry over
		err := os.WriteFile(path, []byte("old\n"), 0o644)
		if err != nil {
			t.Fatal(err)
		}

		err = writeFileAtomic(path, []byte("second\n"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		assertFile(t, path, "second\n", 0o600)
	})

	t.Run("no temp files left behind", func(t *testing.T) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}

		if len(entries) != 1 || entries[0].Name() != "config" {
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("expected only config in dir, got: %v", names)
		}
	})

	t.Run("missing dir errors", func(t *testing.T) {
		missing := filepath.Join(dir, "nope", "config")

		err := writeFileAtomic(missing, []byte("data"))
		if err == nil {
			t.Fatal("expected error for missing dir, got nil")
		}

		_, err = os.Stat(missing)
		if !os.IsNotExist(err) {
			t.Errorf("expected no file written, stat err: %v", err)
		}
	})
}

func TestSaveConfig(t *testing.T) {
	// nested dir that doesn't exist yet, like a fresh ~/.config/jellyrpc
	cfgDir := filepath.Join(t.TempDir(), "config", "jellyrpc")
	path := filepath.Join(cfgDir, "config")

	c := jellyfin.NewClient("https://jelly.example.com", "abc123", "test")
	c.UserName = "snow"

	err := saveConfig(path, exampleCfg, c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(cfgDir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("expected dir perms 0700, got %#o", perm)
	}

	cfg, err := loadValidConfig(path)
	if err != nil {
		t.Fatalf("saved config didn't load: %v", err)
	}
	if cfg.JellyfinURL != c.BaseURL || cfg.JellyfinKey != c.APIKey || cfg.JellyfinUser != c.UserName {
		t.Errorf("saved values mismatch, got: %+v", *cfg)
	}
}

func TestConfigSource(t *testing.T) {
	dir := t.TempDir()

	t.Run("no file uses example", func(t *testing.T) {
		src, url, err := configSource(filepath.Join(dir, "missing"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if src != exampleCfg {
			t.Errorf("expected example config, got: %q", src)
		}
		if url != "" {
			t.Errorf("expected empty url, got: %q", url)
		}
	})

	t.Run("existing file", func(t *testing.T) {
		path := filepath.Join(dir, "config")
		contents := "# mine\nJELLYFIN_URL=jelly.example.com/web/\nPOLL_RATE=3\n"

		err := os.WriteFile(path, []byte(contents), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		src, url, err := configSource(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if src != contents {
			t.Errorf("expected existing contents, got: %q", src)
		}
		if url != "https://jelly.example.com" {
			t.Errorf("expected sanitised url, got: %q", url)
		}
	})

	t.Run("existing file without url", func(t *testing.T) {
		path := filepath.Join(dir, "nourl")

		err := os.WriteFile(path, []byte("POLL_RATE=3\n"), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		_, url, err := configSource(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "" {
			t.Errorf("expected empty url, got: %q", url)
		}
	})

	t.Run("unreadable path errors", func(t *testing.T) {
		// reading a dir gives an error that isn't ErrNotExist
		_, _, err := configSource(dir)
		if err == nil {
			t.Fatal("expected error reading a directory, got nil")
		}
	})
}

func assertFile(t *testing.T, path, contents string, perm os.FileMode) {
	t.Helper()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != contents {
		t.Errorf("expected contents %q, got %q", contents, got)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != perm {
		t.Errorf("expected perms %#o, got %#o", perm, info.Mode().Perm())
	}
}

// newTestPrompt returns a prompt reading from input, and the buffer it writes to
func newTestPrompt(input string) (*Prompt, *bytes.Buffer) {
	var out bytes.Buffer
	return NewPrompt(strings.NewReader(input), &out), &out
}

func TestPromptString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		def      string
		expected string
		wantErr  bool
		prompt   string
	}{
		{name: "empty uses default", input: "\n", def: "salmon", expected: "salmon", prompt: "fish [salmon]: "},
		{name: "input over default", input: "trout\n", def: "salmon", expected: "trout"},
		{name: "input trimmed", input: "  trout \t\n", def: "salmon", expected: "trout"},
		{name: "required loops until value", input: "\n\ntrout\n", expected: "trout", prompt: "fish (required): "},
		{name: "eof after text", input: "trout", expected: "trout"},
		{name: "eof with nothing", input: "", wantErr: true},
		{name: "eof while required", input: "\n", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, out := newTestPrompt(tc.input)

			got, err := p.String("fish", tc.def)
			if tc.wantErr {
				if !errors.Is(err, io.EOF) {
					t.Errorf("expected EOF error, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, got)
			}
			if tc.prompt != "" && !strings.HasPrefix(out.String(), tc.prompt) {
				t.Errorf("expected prompt %q, got output %q", tc.prompt, out.String())
			}
		})
	}
}

func TestPromptStringRequiredMsg(t *testing.T) {
	p, out := newTestPrompt("\n\ntrout\n")

	_, err := p.String("fish", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if n := strings.Count(out.String(), "fish cannot be empty"); n != 2 {
		t.Errorf("expected 2 empty msgs, got %d in %q", n, out.String())
	}
}

func TestPromptBool(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		def       bool
		trueChar  string
		falseChar string
		expected  bool
		prompt    string
	}{
		{name: "empty default true", input: "\n", def: true, trueChar: "y", falseChar: "n", expected: true, prompt: "fish? [Y/n] "},
		{name: "empty default false", input: "\n", def: false, trueChar: "y", falseChar: "n", expected: false, prompt: "fish? [y/N] "},
		{name: "upper input", input: "Y\n", trueChar: "y", falseChar: "n", expected: true},
		{name: "false char", input: "n\n", def: true, trueChar: "y", falseChar: "n", expected: false},
		{name: "bad input retries", input: "salmon\ny\n", trueChar: "y", falseChar: "n", expected: true},
		{name: "custom chars", input: "K\n", def: true, trueChar: "q", falseChar: "k", expected: false, prompt: "fish? [Q/k] "},
		// chars passed in upper should still be lowered
		{name: "upper chars", input: "q\n", trueChar: "Q", falseChar: "K", expected: true, prompt: "fish? [q/K] "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, out := newTestPrompt(tc.input)

			got, err := p.BoolWithChars("fish?", tc.def, tc.trueChar, tc.falseChar)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
			if tc.prompt != "" && !strings.HasPrefix(out.String(), tc.prompt) {
				t.Errorf("expected prompt %q, got output %q", tc.prompt, out.String())
			}
		})
	}
}

func TestPromptBoolRetryMsg(t *testing.T) {
	p, out := newTestPrompt("salmon\ny\n")

	_, err := p.Bool("fish?", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out.String(), "enter y or n") {
		t.Errorf("expected retry msg, got %q", out.String())
	}
}

func TestPromptBoolEOF(t *testing.T) {
	p, _ := newTestPrompt("")

	_, err := p.Bool("fish?", true)
	if !errors.Is(err, io.EOF) {
		t.Errorf("expected EOF error, got: %v", err)
	}
}

// meHandler acts like /Users/Me, salmon is a user token for a user (snow),
// trout is an api key (400), anything else is rejected
func meHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case hasToken(r, "salmon"):
			respondJSON(t, jellyfin.User{Name: "snow"})(w, r)
		case hasToken(r, "trout"):
			w.WriteHeader(http.StatusBadRequest)
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}
}

func TestAskKey(t *testing.T) {
	users := []jellyfin.User{{Name: "pike"}, {Name: "Snow"}}

	tests := []struct {
		name     string
		input    string
		wantName string
		wantKey  string
		outMsg   string
	}{
		{name: "user token", input: "salmon\n", wantName: "snow", wantKey: "salmon"},
		{name: "rejected then token", input: "cod\nsalmon\n", wantName: "snow", wantKey: "salmon", outMsg: "key or token was rejected"},
		// api key isn't tied to a user so it has to ask, and uses jellyfin's spelling
		{name: "api key", input: "trout\nsnow\n", wantName: "Snow", wantKey: "trout", outMsg: "enter the jellyfin user"},
		{name: "api key unknown user retries", input: "trout\ncod\nsnow\n", wantName: "Snow", wantKey: "trout", outMsg: `user "cod" doesn't exist`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newFakeJellyfin(t, "", map[string]http.HandlerFunc{
				"GET /Users/Me": meHandler(t),
				"GET /Users":    respondJSON(t, users),
			})
			p, out := newTestPrompt(tc.input)

			name, key, err := askKey(context.Background(), p, c)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if name != tc.wantName || key != tc.wantKey {
				t.Errorf("expected %s/%s, got %s/%s", tc.wantName, tc.wantKey, name, key)
			}
			if tc.outMsg != "" && !strings.Contains(out.String(), tc.outMsg) {
				t.Errorf("expected output to contain %q, got %q", tc.outMsg, out.String())
			}
		})
	}
}

func TestAskKeyErrors(t *testing.T) {
	t.Run("server error", func(t *testing.T) {
		c := newFakeJellyfin(t, "", map[string]http.HandlerFunc{
			"GET /Users/Me": respondStatus(http.StatusInternalServerError),
		})
		p, _ := newTestPrompt("salmon\n")

		_, _, err := askKey(context.Background(), p, c)

		statErr, ok := errors.AsType[*jellyfin.StatusError](err)
		if !ok || statErr.Code != 500 {
			t.Errorf("expected 500 *StatusError, got: %v", err)
		}
	})

	t.Run("users error", func(t *testing.T) {
		c := newFakeJellyfin(t, "", map[string]http.HandlerFunc{
			"GET /Users/Me": meHandler(t),
			"GET /Users":    respondStatus(http.StatusForbidden),
		})
		p, _ := newTestPrompt("trout\nsnow\n")

		_, _, err := askKey(context.Background(), p, c)
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("eof", func(t *testing.T) {
		c := newFakeJellyfin(t, "", map[string]http.HandlerFunc{
			"GET /Users/Me": meHandler(t),
		})
		p, _ := newTestPrompt("cod\n")

		_, _, err := askKey(context.Background(), p, c)
		if !errors.Is(err, io.EOF) {
			t.Errorf("expected EOF error, got: %v", err)
		}
	})
}

func TestAuthenticate(t *testing.T) {
	tests := []struct {
		name    string
		qc      bool
		input   string
		askedQC bool
	}{
		// no qc on the server means we shouldn't even ask
		{name: "qc disabled", qc: false, input: "salmon\n", askedQC: false},
		{name: "qc enabled pick key", qc: true, input: "k\nsalmon\n", askedQC: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newFakeJellyfin(t, "", map[string]http.HandlerFunc{
				"GET /QuickConnect/Enabled": respondJSON(t, tc.qc),
				"GET /Users/Me":             meHandler(t),
			})
			p, out := newTestPrompt(tc.input)

			err := authenticate(context.Background(), p, c)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if c.UserName != "snow" || c.APIKey != "salmon" {
				t.Errorf("expected client snow/salmon, got %s/%s", c.UserName, c.APIKey)
			}

			asked := strings.Contains(out.String(), "(Q)uick Connect")
			if asked != tc.askedQC {
				t.Errorf("expected qc asked: %v, got output %q", tc.askedQC, out.String())
			}
		})
	}
}

func TestAskServer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /System/Info/Public", respondJSON(t, jellyfin.SystemInfo{ServerName: "salmon"}))

	live := httptest.NewServer(mux)
	t.Cleanup(live.Close)

	// grab a url then close it so nothing's listening there
	dead := httptest.NewServer(mux)
	deadURL := dead.URL
	dead.Close()

	t.Run("unreachable then sanitised url", func(t *testing.T) {
		// no protocol + web ui path, should get sanitised back to the real url
		messy := strings.TrimPrefix(live.URL, "http://") + "/web/"
		p, out := newTestPrompt(deadURL + "\n" + messy + "\n")

		c, err := askServer(context.Background(), p, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if c.BaseURL != live.URL {
			t.Errorf("expected %s, got %s", live.URL, c.BaseURL)
		}
		for _, msg := range []string{"can't reach server", "using " + live.URL + " (salmon)"} {
			if !strings.Contains(out.String(), msg) {
				t.Errorf("expected output to contain %q, got %q", msg, out.String())
			}
		}
	})

	t.Run("accepts default", func(t *testing.T) {
		p, _ := newTestPrompt("\n")

		c, err := askServer(context.Background(), p, live.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.BaseURL != live.URL {
			t.Errorf("expected %s, got %s", live.URL, c.BaseURL)
		}
	})
}

func TestWaitForQC(t *testing.T) {
	t.Run("polls until authenticated", func(t *testing.T) {
		var calls atomic.Int32

		c := newFakeJellyfin(t, "", map[string]http.HandlerFunc{
			"GET /QuickConnect/Connect": func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("secret") != "salmon" {
					t.Errorf("expected secret salmon, got %q", r.URL.Query().Get("secret"))
				}
				n := calls.Add(1)
				respondJSON(t, jellyfin.QuickConnect{Authenticated: n >= 3})(w, r)
			},
		})

		err := waitForQC(context.Background(), c, time.Millisecond, "salmon")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n := calls.Load(); n != 3 {
			t.Errorf("expected 3 polls, got %d", n)
		}
	})

	t.Run("server error", func(t *testing.T) {
		c := newFakeJellyfin(t, "", map[string]http.HandlerFunc{
			"GET /QuickConnect/Connect": respondStatus(http.StatusUnauthorized),
		})

		err := waitForQC(context.Background(), c, time.Millisecond, "salmon")

		_, ok := errors.AsType[*jellyfin.StatusError](err)
		if !ok {
			t.Errorf("expected *StatusError, got: %v", err)
		}
	})

	t.Run("times out", func(t *testing.T) {
		c := newFakeJellyfin(t, "", map[string]http.HandlerFunc{
			"GET /QuickConnect/Connect": respondJSON(t, jellyfin.QuickConnect{}),
		})

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		err := waitForQC(ctx, c, time.Millisecond, "salmon")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected deadline exceeded, got: %v", err)
		}
	})
}

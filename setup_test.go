package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

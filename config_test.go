package main

import (
	"bufio"
	"bytes"
	"errors"
	"log"
	"os"
	"slices"
	"strings"
	"testing"
)

// captureLog makes the log output a buffer during tests, so we can
// check Warn msgs and they dont spam the test output
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stdout) })

	return &buf
}

func TestMissingRequiredValues(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		expected []string
	}{
		{
			name: "missing jellyfin url",
			config: Config{
				JellyfinKey:  "bar",
				JellyfinURL:  "",
				JellyfinUser: "foo",
			},
			expected: []string{"JELLYFIN_URL"},
		},
		{
			name: "missing jellyfin url and key",
			config: Config{
				JellyfinKey:  "",
				JellyfinURL:  "",
				JellyfinUser: "foo",
			},
			expected: []string{"JELLYFIN_KEY", "JELLYFIN_URL"},
		},
		{
			name: "missing jellyfin key and user",
			config: Config{
				JellyfinKey:  "",
				JellyfinURL:  "foo",
				JellyfinUser: "",
			},
			expected: []string{"JELLYFIN_KEY", "JELLYFIN_USER"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			missing, _ := tc.config.validate()

			if !slices.Equal(missing, tc.expected) {
				t.Errorf("expected: %v, got: %v", tc.expected, missing)
			}
		})
	}
}

func TestParseConfig(t *testing.T) {
	src := `# comment
JELLYFIN_URL=jelly.example.com/web/
JELLYFIN_KEY=abc123==
JELLYFIN_USER=snow
POLL_RATE=7
PAUSE_TIMEOUT=0
APP_ID=42
DB_LINK=yes
USE_EPISODE_ART=1
`
	cfg, unknown, err := parseConfig(strings.NewReader(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(unknown) != 0 {
		t.Errorf("expected no unknown keys, got: %v", unknown)
	}

	want := Config{
		JellyfinURL:   "https://jelly.example.com",
		JellyfinKey:   "abc123==",
		JellyfinUser:  "snow",
		PollRate:      7,
		PauseTimeout:  0,
		AppID:         "42",
		UseDBLink:     true,
		UseEpisodeArt: true,
	}
	if *cfg != want {
		t.Errorf("\nexpected: %+v\ngot:      %+v", want, *cfg)
	}
}

func TestParseConfigSkipsAndUnknown(t *testing.T) {
	src := `
# JELLYFIN_USER=commented
   # indented comment
no equals sign lol
  JELLYFIN_USER  =  snow  
FOO=1
BAR=2
`
	cfg, unknown, err := parseConfig(strings.NewReader(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.JellyfinUser != "snow" {
		t.Errorf("expected user %q, got %q", "snow", cfg.JellyfinUser)
	}

	want := []string{"FOO", "BAR"}
	if !slices.Equal(unknown, want) {
		t.Errorf("expected unknown: %v, got: %v", want, unknown)
	}
}

func TestParseConfigPauseTimeout(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		expected int
	}{
		// -1 = unset, needed so applyDefaults can tell apart from 0
		{"unset", "", -1},
		{"explicit zero", "PAUSE_TIMEOUT=0\n", 0},
		{"set", "PAUSE_TIMEOUT=15\n", 15},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _, err := parseConfig(strings.NewReader(tc.src))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.PauseTimeout != tc.expected {
				t.Errorf("expected %d, got %d", tc.expected, cfg.PauseTimeout)
			}
		})
	}
}

func TestParseConfigBadInts(t *testing.T) {
	logs := captureLog(t)

	cfg, _, err := parseConfig(strings.NewReader("POLL_RATE=fast\nPAUSE_TIMEOUT=ten\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// bad values shouldn't touch the predefault values
	if cfg.PollRate != 0 {
		t.Errorf("expected poll rate 0, got %d", cfg.PollRate)
	}
	if cfg.PauseTimeout != -1 {
		t.Errorf("expected pause timeout -1, got %d", cfg.PauseTimeout)
	}

	out := logs.String()
	for _, msg := range []string{"failed to set poll rate", "failed to set pause timeout"} {
		if !strings.Contains(out, msg) {
			t.Errorf("expected warning containing %q, got:\n%s", msg, out)
		}
	}
}

func TestParseConfigLineTooLong(t *testing.T) {
	src := "JELLYFIN_KEY=" + strings.Repeat("a", bufio.MaxScanTokenSize) + "\n"

	cfg, _, err := parseConfig(strings.NewReader(src))
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Errorf("expected bufio.ErrTooLong, got: %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil config on error, got: %+v", cfg)
	}
}

func TestParseBool(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"yes", true},
		{"Yes", true},
		{"on", true},
		{"false", false},
		{"0", false},
		{"no", false},
		{"", false},
		{"y", false},
		{"salmon", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := parseBool(tc.input)
			if got != tc.expected {
				t.Errorf("parseBool(%q): expected %v, got %v", tc.input, tc.expected, got)
			}
		})
	}
}

func TestApplyDefaults(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		expected Config
	}{
		{
			name:     "all unset",
			config:   Config{PauseTimeout: -1},
			expected: Config{PollRate: 5, PauseTimeout: 10, AppID: "default"},
		},
		{
			name:     "negative poll rate",
			config:   Config{PollRate: -3, PauseTimeout: -1},
			expected: Config{PollRate: 5, PauseTimeout: 10, AppID: "default"},
		},
		{
			name:     "explicit zero pause timeout kept",
			config:   Config{PauseTimeout: 0},
			expected: Config{PollRate: 5, PauseTimeout: 0, AppID: "default"},
		},
		{
			name:     "custom values kept",
			config:   Config{PollRate: 2, PauseTimeout: 30, AppID: "custom"},
			expected: Config{PollRate: 2, PauseTimeout: 30, AppID: "custom"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.config.applyDefaults("default")

			if tc.config != tc.expected {
				t.Errorf("\nexpected: %+v\ngot:      %+v", tc.expected, tc.config)
			}
		})
	}
}

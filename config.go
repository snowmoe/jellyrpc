package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/snowmoe/jellyrpc/internal/jellyfin"
)

type Config struct {
	JellyfinURL   string
	JellyfinKey   string
	JellyfinUser  string
	PollRate      int
	PauseTimeout  int
	AppID         string
	ArtworkSource string
	UseDBLink     bool
	UseEpisodeArt bool
}

const (
	artAuto     string = "auto"
	artJellyfin string = "jellyfin"
	artBridge   string = "bridge"
)

// accepts the usual truthy spellings so a config isnt silently false on "1" or "yes"
func parseBool(val string) bool {
	switch strings.ToLower(val) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

func configPath() (string, error) {
	configDir, err := configDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(configDir, "config"), nil
}

func configDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("unable to get config dir: %w", err)
	}

	return filepath.Join(configDir, "jellyrpc"), nil
}

func (cfg *Config) applyDefaults(defaultAppID string) {
	if cfg.PollRate <= 0 {
		cfg.PollRate = 5
	}
	// minutes paused before we drop the presence, unset (-1) gets a default,
	// an explicit 0 disables the timeout
	if cfg.PauseTimeout < 0 {
		cfg.PauseTimeout = 10
	}
	if cfg.AppID == "" {
		cfg.AppID = defaultAppID
	}
	if cfg.ArtworkSource == "" {
		cfg.ArtworkSource = artAuto
	}
}

func (cfg *Config) validate() ([]string, error) {
	var missing []string

	// check all explicity so we can present ALL missing values
	if cfg.JellyfinKey == "" {
		missing = append(missing, "JELLYFIN_KEY")
	}

	if cfg.JellyfinURL == "" {
		missing = append(missing, "JELLYFIN_URL")
	}

	if cfg.JellyfinUser == "" {
		missing = append(missing, "JELLYFIN_USER")
	}

	if len(missing) > 0 {
		return missing, errors.New("config file missing required values")
	}

	return nil, nil
}

func loadValidConfig(cfgPath string) (*Config, error) {
	cfg, warnings, err := loadConfig(cfgPath)
	if errors.Is(err, os.ErrNotExist) {
		// TODO prompt to run "jellyrpc setup" as a fix?
		return nil, fmt.Errorf("file doesn't exist: %s", cfgPath)
	} else if err != nil {
		// TODO catch other error types explicity, e.g. perm issues
		return nil, err
	}

	for _, w := range warnings {
		Warn("%s", w)
	}

	cfg.applyDefaults(defaultAppID)

	missing, err := cfg.validate()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.Join(missing, ", "))
	}

	return cfg, nil
}

func loadConfig(path string) (*Config, []string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	return parseConfig(file)
}

func parseConfig(r io.Reader) (*Config, []string, error) {
	var (
		unknown  []string
		warnings []string
	)

	addWarning := func(format string, v ...any) {
		warnings = append(warnings, fmt.Sprintf(format, v...))
	}

	// -1 marks pause timeout as unset so applyDefaults
	// can tell it apart from 0 which disables the timeout
	cfg := &Config{PauseTimeout: -1}
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "JELLYFIN_URL":
			cfg.JellyfinURL = jellyfin.SanitiseURL(val)
		case "JELLYFIN_KEY":
			cfg.JellyfinKey = val
		case "JELLYFIN_USER":
			cfg.JellyfinUser = val
		case "POLL_RATE":
			i, err := strconv.Atoi(val)
			if err != nil {
				addWarning("invalid POLL_RATE: %q", val)
				continue
			}
			cfg.PollRate = i
		case "PAUSE_TIMEOUT":
			i, err := strconv.Atoi(val)
			if err != nil {
				addWarning("invalid PAUSE_TIMEOUT: %q", val)
				continue
			}
			cfg.PauseTimeout = i
		case "APP_ID":
			cfg.AppID = val
		case "ARTWORK_SOURCE":
			val = strings.ToLower(val)
			switch val {
			case artAuto, artJellyfin, artBridge:
				cfg.ArtworkSource = val
			default:
				addWarning("invalid ARTWORK_SOURCE %q, using auto", val)
			}
		case "DB_LINK":
			cfg.UseDBLink = parseBool(val)
		case "USE_EPISODE_ART":
			cfg.UseEpisodeArt = parseBool(val)
		default:
			unknown = append(unknown, key)
		}
	}

	err := scanner.Err()
	if err != nil {
		return nil, nil, err
	}

	if len(unknown) > 0 {
		addWarning("unknown key(s): %s", strings.Join(unknown, ", "))
	}

	return cfg, warnings, nil
}

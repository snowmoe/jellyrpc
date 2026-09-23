package main

import (
	"bufio"
	"errors"
	"os"
	"strconv"
	"strings"
)

// simple config thingy
// since I wanted no deps I just split key and val with =, trim spaces, and pray

type Config struct {
	JellyfinURL   string
	JellyfinKey   string
	JellyfinUser  string
	PollRate      int
	PauseTimeout  int
	AppID         string
	UseDBLink     bool
	UseEpisodeArt bool
}

// accepts the usual truthy spellings so a config isnt silently false on "1" or "yes"
func parseBool(val string) bool {
	switch strings.ToLower(val) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

func (cfg *Config) ApplyDefaults(defaultAppID string) {
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
}

func (cfg *Config) Validate() (error, []string) {
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
		return errors.New("config file missing required values"), missing
	}

	return nil, missing
}

func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// -1 marks pause timeout as unset so ApplyDefaults can tell it apart from
	// an explicit 0 which disables the timeout
	cfg := &Config{PauseTimeout: -1}
	scanner := bufio.NewScanner(file)

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
			cfg.JellyfinURL = SanitiseURL(val)
		case "JELLYFIN_KEY":
			cfg.JellyfinKey = val
		case "JELLYFIN_USER":
			cfg.JellyfinUser = val
		case "POLL_RATE":
			i, err := strconv.Atoi(val)
			if err != nil {
				Warn("failed to set poll rate from config: %v\n", err)
				continue
			}
			cfg.PollRate = i
		case "PAUSE_TIMEOUT":
			i, err := strconv.Atoi(val)
			if err != nil {
				Warn("failed to set pause timeout from config: %v\n", err)
				continue
			}
			cfg.PauseTimeout = i
		case "APP_ID":
			cfg.AppID = val
		case "DB_LINK":
			cfg.UseDBLink = parseBool(val)
		case "USE_EPISODE_ART":
			cfg.UseEpisodeArt = parseBool(val)
		default:
			Warn("unknown config key: %s", key)
		}
	}

	err = scanner.Err()
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

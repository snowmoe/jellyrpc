package main

import (
	"bufio"
	"os"
	"strings"
)

// simple config thingy
// since I wanted no deps I just split key and val with =, trim spaces, and pray

type Config struct {
	JellyfinURL  string
	JellyfinKey  string
	JellyfinUser string
}

func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	cfg := &Config{}
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
			cfg.JellyfinURL = val
		case "JELLYFIN_KEY":
			cfg.JellyfinKey = val
		case "JELLYFIN_USER":
			cfg.JellyfinUser = val
		}
	}

	err = scanner.Err()
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

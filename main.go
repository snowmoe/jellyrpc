package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

const defaultAppID = "1517892834907394229"

var (
	gitHash    = "dev"
	gitVersion = "dev"
)

func main() {
	if err := run(); err != nil {
		Fatal("%v", err)
	}
}

func run() error {
	Info("starting jellyfin rpc daemon")
	if gitVersion != "dev" {
		Info("running jellyrpc %s", gitVersion)
	} else if gitHash != "dev" {
		Info("running jellyrpc from commit: %s", gitHash)
	} else {
		Info("running dev build")
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}

	cfgPath := filepath.Join(configDir, "jellyrpc", "config")

	cfg, err := LoadConfig(cfgPath)
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("couldn't find config file, does it exist?")
	} else if err != nil {
		return err
	}

	cfg.ApplyDefaults(defaultAppID)

	err, missing := cfg.Validate()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.Join(missing, ", "))
	}
	Info("loaded config file")

	if cfg.AppID != defaultAppID {
		Info("using custom discord app id: %s", cfg.AppID)
	}

	if cfg.UseEpisodeArt {
		Info("preferring episode art instead of series")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app := &App{
		Config:   cfg,
		Sessions: NewJellyfinClient(cfg),
		Connect: func(clientID string) (PresenceClient, error) {
			return NewDiscordConn(clientID)
		},
	}

	return app.Run(ctx)
}

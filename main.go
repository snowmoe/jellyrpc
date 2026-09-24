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
	var (
		err    error
		subCmd string
	)

	if len(os.Args) <= 1 {
		subCmd = "run"
	} else {
		subCmd = os.Args[1]
	}

	switch subCmd {
	case "run":
		err = run()
	case "setup", "check":
		err = fmt.Errorf("%s: not implemented yet", subCmd)
	case "version", "-v", "--version":
		fmt.Println(version())
	case "help", "-h", "--help":
		printHelp()
	default:
		fmt.Fprintf(os.Stderr, "jellyrpc: unknown command: %s\n  run 'jellyrpc help' for a full list of commands\n", subCmd)
		os.Exit(2)
	}

	if err != nil {
		if subCmd == "run" {
			Fatal("%v", err)
		}

		Die("%v", err)
	}
}

func version() string {
	if gitVersion != "dev" {
		return fmt.Sprintf("jellyrpc %s", gitVersion)
	} else if gitHash != "dev" {
		return fmt.Sprintf("jellyrpc dev build: commit %s", gitHash)
	} else {
		return "jellyrpc dev build"
	}
}

func printHelp() {
	fmt.Print(`simple jellyfin discord rpc daemon.

USAGE
  jellyrpc <subcommand>

SUBCOMMANDS
  run      start the daemon (default)
  setup    run the setup wizard
  check    test the config and jellyfin connection
  version  print jellyrpc build version or hash
  help     show this message
`)
}

func run() error {
	Info("starting jellyfin rpc daemon")
	Info("running %s", version())

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

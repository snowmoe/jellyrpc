package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
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

func waitForConfig(ctx context.Context, cfgPath string, interval time.Duration) (*Config, error) {
	var lastMsg string

	for {
		cfg, err := LoadValidConfig(cfgPath)
		if err == nil {
			return cfg, nil
		}

		msg := err.Error()
		if msg != lastMsg {
			Warn("waiting for valid config: %v", err)
			lastMsg = msg
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func run() error {
	Info("starting jellyfin rpc daemon")
	Info("running %s", version())

	cfgPath, err := GetConfigPath()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := waitForConfig(ctx, cfgPath, 3*time.Second)
	if err != nil {
		if ctx.Err() != nil {
			// return nil because we were told to stop
			return nil
		}
		return err
	}
	Info("config file loaded")

	if cfg.AppID != defaultAppID {
		Info("using custom discord app id: %s", cfg.AppID)
	}

	if cfg.UseEpisodeArt {
		Info("preferring episode art instead of series")
	}

	app := &App{
		Config:   cfg,
		Sessions: NewJellyfinClient(cfg),
		Connect: func(clientID string) (PresenceClient, error) {
			return NewDiscordConn(clientID)
		},
	}

	return app.Run(ctx)
}

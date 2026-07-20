package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const defaultAppID = "1517892834907394229"

var (
	gitHash    = "dev"
	gitVersion = "dev"
)

func main() {
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
		Fatal("error finding config dir: %v", err)
		return
	}

	cfgPath := filepath.Join(configDir, "jellyrpc", "config")

	cfg, err := LoadConfig(cfgPath)
	if errors.Is(err, os.ErrNotExist) {
		Fatal("couldn't find config file, does it exist?")
	} else if err != nil {
		Fatal("error loading config file: %v", err)
		return
	}

	cfg.ApplyDefaults(defaultAppID)

	if err := cfg.Validate(); err != nil {
		Fatal("%v", err)
	}
	Info("loaded config file")

	if cfg.AppID != defaultAppID {
		Info("using custom discord app id: %s", cfg.AppID)
	}

	if cfg.UseEpisodeArt {
		Info("preferring episode art instead of series")
	}

	ticker := time.NewTicker(time.Duration(cfg.PollRate) * time.Second)
	defer ticker.Stop()

	jf := NewJellyfinClient(cfg)

	var dc *DiscordConn
	var lastWatching = ""

	for range ticker.C {
		sess, err := jf.GetActiveSession(context.Background())
		if err != nil || !isSessionActive(sess) {
			if err != nil {
				Fatal("jellyfin api err: %v", err)
			}

			if dc != nil {
				Info("no active jellyfin sessions, closing ipc socket")
				dc.Close()
				dc = nil
			}
			continue
		}

		if dc == nil {
			Info("active jellyfin session detected, opening ipc socket")
			dc, err = NewDiscordConn(cfg.AppID)
			if err != nil {
				Fatal("failed to connect: %v", err)
				dc = nil
				continue
			}
		}

		// only logging when id changes, keeps shit tidy
		if lastWatching != sess.NowPlayingItem.Id {
			lastWatching = sess.NowPlayingItem.Id
			Info("active playing: %s, id: %s", sess.NowPlayingItem.Name, sess.NowPlayingItem.Id)
		}

		presence := BuildPresence(cfg, sess, time.Now().UnixMilli())

		if presence.Paused {
			err = dc.SetPaused(presence.Title, presence.TitleURL, presence.ArtworkURL)
		} else {
			err = dc.SetWatching(
				presence.Title,
				presence.State,
				presence.TitleURL,
				presence.ArtworkURL,
				presence.StartEpoch,
				presence.EndEpoch,
			)
		}
		if err != nil {
			Fatal("failed to update discord status: %v", err)
		}
	}
}

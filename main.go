package main

import (
	"errors"
	"fmt"
	"io"
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

	// check if required config options have values
	// TODO use another jellyfin endpoint to verify values before polling?
	if cfg.JellyfinKey == "" || cfg.JellyfinURL == "" || cfg.JellyfinUser == "" {
		Fatal("config file missing required values")
	} else {
		Info("loaded config file")
	}

	if cfg.PollRate <= 0 {
		Info("no poll rate, set using default (5s)")
		cfg.PollRate = 5
	}

	if cfg.AppID != "" {
		Info("using custom discord app id: %s", cfg.AppID)
	} else {
		cfg.AppID = defaultAppID
	}

	if cfg.useEpisodeArt {
		Info("preferring episode art instead of series")
	}

	ticker := time.NewTicker(time.Duration(cfg.PollRate) * time.Second)
	defer ticker.Stop()

	var dc *DiscordConn
	var lastWatching = ""

	for range ticker.C {
		sess, err := getJellyfinSessions(cfg)
		if err != nil || !isSessionActive(sess) {
			if err != nil && errors.Is(err, io.EOF) {
				Fatal("jellyfin api returned EOF: probably unauthorized api key or invalid instance url")
			} else if err != nil {
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

		var rpcTitle, targetImageID, rpcState, artworkURL, rpcTitleURL string

		// only logging when id changes, keeps shit tidy
		if lastWatching != sess.NowPlayingItem.Id {
			lastWatching = sess.NowPlayingItem.Id
			Info("active playing: %s, id: %s", sess.NowPlayingItem.Name, sess.NowPlayingItem.Id)
		}

		// if current session is an episode and therefore a series
		// we use the series name as the title, and state as season, ep and ep name
		if sess.NowPlayingItem.Type == "Episode" {
			rpcTitle = sess.NowPlayingItem.SeriesName
			rpcState = fmt.Sprintf("S%02d:E%02d - %s",
				sess.NowPlayingItem.ParentIndexNumber,
				sess.NowPlayingItem.IndexNumber,
				sess.NowPlayingItem.Name,
			)

			// if use episode cover if use ep art is true or if no series id was found
			if cfg.useEpisodeArt || sess.NowPlayingItem.SeriesId == "" {
				targetImageID = sess.NowPlayingItem.Id
			} else {
				// otherwise fallback to using series art
				targetImageID = sess.NowPlayingItem.SeriesId
			}
		} else {
			// else = movie (probably) so no state
			rpcTitle = sess.NowPlayingItem.Name
			rpcState = ""
			targetImageID = sess.NowPlayingItem.Id
		}

		// api "bridge" that lets me use an imdb or tvdb id
		// fetches the imdb image link and 302's to that, with caching !!
		// should let people with non pub instances still have rpc cover art
		// without any need for them to provide api key + keeping mine secret hehehe
		bridgeApi := "https://rot.sh/poster"

		if IsLocalInstance(cfg.JellyfinURL) {
			// did check and couldn't see if tmdb has per episode id's, so this might just be redundant but oh well
			if sess.NowPlayingItem.ProviderIds.Tmdb != "" {
				artworkURL = fmt.Sprintf("%s?tmdb=%s", bridgeApi, sess.NowPlayingItem.ProviderIds.Tmdb)
			} else if sess.NowPlayingItem.ProviderIds.Imdb != "" {
				artworkURL = fmt.Sprintf("%s?imdb=%s", bridgeApi, sess.NowPlayingItem.ProviderIds.Imdb)
			} else if sess.NowPlayingItem.ProviderIds.Tvdb != "" {
				artworkURL = fmt.Sprintf("%s?tvdb=%s", bridgeApi, sess.NowPlayingItem.ProviderIds.Tvdb)
			} else {
				// should just fallback to using the large image key
				artworkURL = "jellyfin"
			}
		} else {
			artworkURL = fmt.Sprintf("%s/Items/%s/Images/Primary?fillWidth=400&quality=85",
				cfg.JellyfinURL,
				targetImageID,
			)
		}

		if cfg.useDBLink {
			if sess.NowPlayingItem.ProviderIds.Imdb != "" {
				rpcTitleURL = fmt.Sprintf("https://www.imdb.com/title/%s", sess.NowPlayingItem.ProviderIds.Imdb)
			} else if sess.NowPlayingItem.ProviderIds.Tvdb != "" {
				// was kinda lazy and couldn't find a way to link straight to tvdb page from id
				// fuck tvdb anyway shits ass
				// TODO helper func to resolve direct tvdb link from id
				rpcTitleURL = fmt.Sprintf("https://thetvdb.com/search?query=%s", sess.NowPlayingItem.ProviderIds.Tvdb)
			} else {
				Warn("unable to find db link for media")
			}
		}

		if sess.PlayState.IsPaused {
			err := dc.SetPaused(rpcTitle, rpcTitleURL, artworkURL)
			if err != nil {
				Fatal("failed to update discord status: %v", err)
			}
		} else {
			// to have a time bar in our rpc activity we need a start and end epoch
			// and the current time along that is calculated from our now time epoch (by discord)
			// so in order for our time bar to be correct we need to
			// subtract the current position from our current time

			currentPosSec := sess.PlayState.PositionTicks / 10000000
			totalRunSec := sess.NowPlayingItem.RunTimeTicks / 10000000

			now := time.Now().UnixMilli()
			remainingSec := totalRunSec - currentPosSec

			startEpoch := now - (currentPosSec * 1000)
			endEpoch := now + (remainingSec * 1000)

			// then we set our activity status using the current playing item + epochs we calculated
			err = dc.SetWatching(rpcTitle, rpcState, rpcTitleURL, artworkURL, startEpoch, endEpoch)
			if err != nil {
				Fatal("failed to update discord status: %v", err)
			}
		}
	}
}

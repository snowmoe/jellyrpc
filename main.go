package main

import (
	"fmt"
	"log"
	"os"
	"time"
)

// TODO add optional config opt to override the app id
const applicationID = "1517892834907394229"

func init() {
	log.SetFlags(0)
	log.SetOutput(os.Stdout)
}

func main() {
	log.Println("starting jellyfin rpc daemon")

	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Fatalf("error finding config dir: %v\n", err)
		return
	}

	cfgPath := configDir + "/jellyrpc/config"
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("error loading config file: %v\n", err)
		return
	}

	// check if required config options have values
	// TODO use another jellyfin endpoint to verify values before polling?
	if cfg.JellyfinKey == "" || cfg.JellyfinURL == "" || cfg.JellyfinUser == "" {
		log.Fatalln("config file missing values")
	} else {
		log.Println("loaded config file")
	}

	// TODO config option to change polling rate
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var dc *DiscordConn
	var lastWatching = ""

	for range ticker.C {
		sess, err := getJellyfinSessions(cfg)
		if err != nil || !isSessionActive(sess) {
			if err != nil {
				log.Fatalf("jellyfin api err: %v\n", err)
			}

			if dc != nil {
				log.Println("no active jellyfin sessions, closing ipc socket")
				dc.Close()
				dc = nil
			}
			continue
		}

		if dc == nil {
			log.Println("active jellyfin session detected, opening ipc socket")
			dc, err = NewDiscordConn(applicationID)
			if err != nil {
				log.Fatalf("failed to connect: %v\n", err)
				dc = nil
				continue
			}
		}

		var rpcTitle, rpcState, targetImageID string

		// only logging when id changes, keeps shit tidy
		if lastWatching != sess.NowPlayingItem.Id {
			lastWatching = sess.NowPlayingItem.Id
			log.Printf("active playing: %s, id: %s\n", sess.NowPlayingItem.Name, sess.NowPlayingItem.Id)
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

			// prefer series' main cover art, and fallback to per ep art
			if sess.NowPlayingItem.SeriesId != "" {
				targetImageID = sess.NowPlayingItem.SeriesId
			} else {
				targetImageID = sess.NowPlayingItem.Id
			}
		} else {
			// else = movie (probably) so no state
			rpcTitle = sess.NowPlayingItem.Name
			rpcState = ""
			targetImageID = sess.NowPlayingItem.Id
		}

		// after testing this will only work with a public jellyfin instance (duh)
		// so may need a fallback to movie db, using a flag to avoid rechecking a
		// jellyfin instances publicity every poll
		artworkURL := fmt.Sprintf("%s/Items/%s/Images/Primary?fillWidth=400&quality=85",
			cfg.JellyfinURL,
			targetImageID,
		)

		if sess.PlayState.IsPaused {
			err := dc.SetPaused(rpcTitle, artworkURL)
			if err != nil {
				log.Fatalf("failed to update discord status: %v\n", err)
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
			err = dc.SetWatching(rpcTitle, rpcState, artworkURL, startEpoch, endEpoch)
			if err != nil {
				log.Fatalf("failed to update discord status: %v\n", err)
			}
		}
	}
}

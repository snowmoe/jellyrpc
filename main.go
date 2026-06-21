package main

import (
	"fmt"
	"os"
	"time"
)

// TODO add optional config opt to override the app id
const applicationID = "1517892834907394229"

func main() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		fmt.Printf("error finding config dir: %v\n", err)
		return
	}

	cfgPath := configDir + "/jellyrpc/config"
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		fmt.Printf("error loading config file: %v\n", err)
		return
	}

	fmt.Println("starting jellyfin rpc daemon")

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var dc *DiscordConn

	for range ticker.C {
		sess, err := getJellyfinSessions(cfg)
		if err != nil || !isSessionActive(sess) {
			if err != nil {
				fmt.Printf("jellyfin api err: %v\n", err)
			}

			if dc != nil {
				fmt.Println("no active jf sessions, closing ipc socket")
				dc.Close()
				dc = nil
			}
			continue
		}

		if dc == nil {
			fmt.Println("active jf session detected, reopening ipc socket")
			dc, err = NewDiscordConn(applicationID)
			if err != nil {
				fmt.Printf("failed to connect: %v\n", err)
				dc = nil
				continue
			}
		}

		var rpcTitle, rpcState, targetImageID string

		if sess.NowPlayingItem.Type == "Episode" {
			rpcTitle = sess.NowPlayingItem.SeriesName
			rpcState = fmt.Sprintf("S%02d:E%02d - %s",
				sess.NowPlayingItem.ParentIndexNumber,
				sess.NowPlayingItem.IndexNumber,
				sess.NowPlayingItem.Name,
			)

			if sess.NowPlayingItem.SeriesId != "" {
				targetImageID = sess.NowPlayingItem.SeriesId
			} else {
				targetImageID = sess.NowPlayingItem.Id
			}
		} else {
			rpcTitle = sess.NowPlayingItem.Name
			rpcState = ""
			targetImageID = sess.NowPlayingItem.Id
		}

		artworkURL := fmt.Sprintf("%s/Items/%s/Images/Primary?fillWidth=400&quality=85",
			cfg.JellyfinURL,
			targetImageID,
		)

		if sess.PlayState.IsPaused {
			err := dc.SetPaused(rpcTitle, artworkURL)
			if err != nil {
				fmt.Printf("failed updating discord status: %v\n", err)
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
				fmt.Println(err)
				return
			}
		}
	}
}

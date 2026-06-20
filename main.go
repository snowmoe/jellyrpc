package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// TODO add config to set consts
// TODO add optional config opt to override the app id
const (
	jellyfinURL   = ""
	apiKey        = ""
	username      = ""
	applicationID = "1517892834907394229"
)

// /Session endpoint json structure
// https://api.jellyfin.org/#tag/Session/operation/GetSessions
type Session struct {
	UserName       string `json:"UserName"`
	NowPlayingItem struct {
		Name         string `json:"Name"`
		Id           string `json:"Id"`
		Type         string `json:"Type"`
		RunTimeTicks int64  `json:"RunTimeTicks"`
	} `json:"NowPlayingItem"`
	PlayState struct {
		IsPaused      bool  `json:"IsPaused"`
		PositionTicks int64 `json:"PositionTicks"`
	} `json:"PlayState"`
}

func main() {
	fmt.Println("starting jellyfin rpc daemon")

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var dc *DiscordConn

	for range ticker.C {
		sess, err := getJellyfinSessions()
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

		if sess.PlayState.IsPaused {
			err := dc.SetPaused(sess.NowPlayingItem.Name)
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
			// TODO fetch the status correctly (e.g. EP no + ep name for series, and/or something adhoc for movies)
			// TODO fetch and add cover art for media
			err = dc.SetWatching(sess.NowPlayingItem.Name, "test", startEpoch, endEpoch)
			if err != nil {
				fmt.Println(err)
				return
			}
		}
	}
}

// very simple functon compared to the other ipc shit
// just call the GET /Sessions endpoint and unmarshal into our structs
// making sure we only get the session for the specified user
func getJellyfinSessions() (*Session, error) {
	req, _ := http.NewRequest("GET", jellyfinURL+"/Sessions", nil)
	req.Header.Set("X-MediaBrowser-Token", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var sessions []Session
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return nil, err
	}

	for _, s := range sessions {
		if s.UserName == username && s.NowPlayingItem.Name != "" {
			return &s, nil
		}
	}
	return &Session{}, nil
}

func isSessionActive(sess *Session) bool {
	if sess == nil {
		return false
	}

	if sess.NowPlayingItem.Name == "" || sess.NowPlayingItem.Id == "" {
		return false
	}

	return true
}

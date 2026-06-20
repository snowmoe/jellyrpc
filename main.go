package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// TODO add config to set consts
// later probably a way to replace the adhoc app id
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
		PositionTicks int64 `json:"PositionTicks"`
	} `json:"PlayState"`
}

func main() {
	// initalise our rpc over ipc socket
	dc, err := NewDiscordConn(applicationID)
	if err != nil {
		fmt.Println(err)
	}
	// defer with the close method which should stop activities cleanly
	defer dc.Close()

	sess, err := getJellyfinSessions()
	if err != nil {
		panic(err)
	}

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
	// 2 big todos here are handling a paused session, and actually implementing and update
	// loop, as rn discord will just display that we are watching through X movie or series, even if paused
	// I also need to fetch the status correctly (e.g. EP no + ep name for series, and/or something adhoc for movies)
	err = dc.SetWatching(sess.NowPlayingItem.Name, "test", startEpoch, endEpoch)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(sess)
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

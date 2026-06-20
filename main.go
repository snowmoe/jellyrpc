package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

const (
	jellyfinURL   = ""
	apiKey        = ""
	username      = ""
	applicationID = "1517892834907394229" // yes it has to be hardcoded desu
)

// https://api.jellyfin.org/#tag/Session/operation/GetSessions
type Session struct {
	UserName       string `json:"UserName"`
	NowPlayingItem struct {
		Name string `json:"Name"`
		Id   string `json:"Id"`
		Type string `json:"Type"`
	} `json:"NowPlayingItem"`
}

func main() {
	sess, err := getJellyfinSessions()
	if err != nil {
		panic(err)
	}

	fmt.Println(sess)
}

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

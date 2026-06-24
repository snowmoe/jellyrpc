package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// /Session endpoint json structure
// https://api.jellyfin.org/#tag/Session/operation/GetSessions
type Session struct {
	UserName       string `json:"UserName"`
	NowPlayingItem `json:"NowPlayingItem"`
	PlayState      struct {
		IsPaused      bool  `json:"IsPaused"`
		PositionTicks int64 `json:"PositionTicks"`
	} `json:"PlayState"`
}

type NowPlayingItem struct {
	Name              string `json:"Name"`
	Id                string `json:"Id"`
	Type              string `json:"Type"`
	RunTimeTicks      int64  `json:"RunTimeTicks"`
	SeriesName        string `json:"SeriesName,omitempty"`
	SeriesId          string `json:"SeriesId,omitempty"`
	ParentIndexNumber int    `json:"ParentIndexNumber,omitempty"`
	IndexNumber       int    `json:"IndexNumber,omitempty"`
	ProviderIds       `json:"ProviderIds"`
}

type ProviderIds struct {
	Imdb string `json:"Imdb"`
	Tmdb string `json:"Tmdb"`
	Tvdb string `json:"Tvdb"`
}

// very simple functon compared to the other ipc shit
// just call the GET /Sessions endpoint and unmarshal into our structs
// making sure we only get the session for the specified user
func getJellyfinSessions(cfg *Config) (*Session, error) {
	req, _ := http.NewRequest("GET", cfg.JellyfinURL+"/Sessions", nil)
	authScheme := fmt.Sprintf("MediaBrowser Token=%s", cfg.JellyfinKey)
	req.Header.Set("Authorization", authScheme)

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
		if s.UserName == cfg.JellyfinUser && s.NowPlayingItem.Name != "" {
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

// determines if the jellyfin instance url provided is local
// checks if localhost or a .local domain
// checks if ip (if parseable) is rfc1918 or loopback
// still kept 127 and ::1 in the host check anyway but can possibly be removed
func IsLocalInstance(hostURL string) bool {
	u, err := url.Parse(hostURL)
	if err != nil {
		return true
	}

	host := u.Hostname()

	if host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".local") {
		return true
	}

	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsPrivate() || ip.IsLoopback()
	}

	return false
}

// cleans url's AND guesses protocol if it's missing (which isn't an issue if U READ DA README UGH)
func SanitiseURL(rawURL string) string {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return ""
	}

	// should catch if a user didn't READ THE README(!!!!) and missed the protocol
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		// then so the local instance func doesn't err from url.Parse with a missing protocol
		// just append http:// temporarily so that can parse n do it's thang
		tempURL := "http://" + u

		// then if it's local we just guess that it'll be http://
		if IsLocalInstance(tempURL) {
			u = "http://" + u
		} else {
			// if not local (ie almost 100% likely a domain being used) then we guess it'll be https://
			u = "https://" + u
		}
	}

	// if we fail to parse then just give the raw url back and pray
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}

	hostURL := parsed.Scheme + "://" + parsed.Host

	// prolly not needed but fuckit we schizo
	hostURL = strings.TrimSuffix(hostURL, "/")

	return hostURL
}

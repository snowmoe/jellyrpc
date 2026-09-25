package jellyfin

import "time"

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
	ID                string `json:"Id"`
	Type              string `json:"Type"`
	RunTimeTicks      int64  `json:"RunTimeTicks"`
	SeriesName        string `json:"SeriesName,omitempty"`
	SeriesId          string `json:"SeriesId,omitempty"`
	ParentIndexNumber int    `json:"ParentIndexNumber,omitempty"`
	IndexNumber       int    `json:"IndexNumber,omitempty"`
	ProviderIDs       `json:"ProviderIds"`
}

type ProviderIDs struct {
	Imdb string `json:"Imdb"`
	Tmdb string `json:"Tmdb"`
	Tvdb string `json:"Tvdb"`
}

// /System/Info/Public
// https://api.jellyfin.org/#tag/System/operation/GetPublicSystemInfo
type SystemInfo struct {
	LocalAddress           string `json:"LocalAddress"`
	ServerName             string `json:"ServerName"`
	Version                string `json:"Version"`
	ProductName            string `json:"ProductName"`
	ID                     string `json:"Id"`
	StartupWizardCompleted bool   `json:"StartupWizardCompleted"`
}

type User struct {
	Name   string `json:"Name"`
	Policy struct {
		IsAdministrator bool `json:"IsAdministrator"`
	} `json:"Policy"`
}

type QuickConnect struct {
	Authenticated bool      `json:"Authenticated"`
	Secret        string    `json:"Secret"`
	Code          string    `json:"Code"`
	DeviceID      string    `json:"DeviceId"`
	DeviceName    string    `json:"DeviceName"`
	AppName       string    `json:"AppName"`
	AppVersion    string    `json:"AppVersion"`
	DateAdded     time.Time `json:"DateAdded"`
}

package main

import "fmt"

const bridgeAPI = "https://rot.sh/poster"

// jellyfin reports time in 100ns ticks so 1e7 of them make a second
const ticksPerSecond = 10000000

type Presence struct {
	Title      string
	State      string
	TitleURL   string
	ArtworkURL string
	StartEpoch int64
	EndEpoch   int64
	Paused     bool
}

func BuildPresence(cfg *Config, sess *Session, nowMillis int64) Presence {
	item := sess.NowPlayingItem

	title, state, imageID := mediaDisplay(item, cfg.UseEpisodeArt)
	artworkURL := artworkURLFor(cfg.JellyfinURL, imageID, item.ProviderIds)

	p := Presence{
		Title:      title,
		State:      state,
		TitleURL:   titleURLFor(item.ProviderIds, cfg.UseDBLink),
		ArtworkURL: artworkURL,
		Paused:     sess.PlayState.IsPaused,
	}

	if !p.Paused {
		currentPosSec := sess.PlayState.PositionTicks / ticksPerSecond
		totalRunSec := sess.NowPlayingItem.RunTimeTicks / ticksPerSecond
		remainingSec := totalRunSec - currentPosSec

		p.StartEpoch = nowMillis - (currentPosSec * 1000)
		p.EndEpoch = nowMillis + (remainingSec * 1000)
	}

	return p
}

func mediaDisplay(item NowPlayingItem, useEpisodeArt bool) (title, state, imageID string) {
	if item.Type != "Episode" {
		return item.Name, "", item.Id
	}

	title = item.SeriesName
	state = fmt.Sprintf("S%02d:E%02d - %s", item.ParentIndexNumber, item.IndexNumber, item.Name)
	if useEpisodeArt || item.SeriesId == "" {
		imageID = item.Id
	} else {
		imageID = item.SeriesId
	}
	return title, state, imageID
}

func artworkURLFor(jellyfinURL, imageID string, ids ProviderIds) string {
	if IsLocalInstance(jellyfinURL) {
		if ids.Tmdb != "" {
			return fmt.Sprintf("%s?tmdb=%s", bridgeAPI, ids.Tmdb)
		}
		if ids.Imdb != "" {
			return fmt.Sprintf("%s?imdb=%s", bridgeAPI, ids.Imdb)
		}
		if ids.Tvdb != "" {
			return fmt.Sprintf("%s?tvdb=%s", bridgeAPI, ids.Tvdb)
		}
		return "jellyfin"
	}

	return fmt.Sprintf("%s/Items/%s/Images/Primary?fillWidth=400&quality=85", jellyfinURL, imageID)
}

func titleURLFor(ids ProviderIds, enabled bool) string {
	if !enabled {
		return ""
	}
	if ids.Imdb != "" {
		return fmt.Sprintf("https://www.imdb.com/title/%s", ids.Imdb)
	}
	if ids.Tvdb != "" {
		return fmt.Sprintf("https://thetvdb.com/search?query=%s", ids.Tvdb)
	}

	Warn("unable to find db link for media")
	return ""
}

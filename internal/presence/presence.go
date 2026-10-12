package presence

import (
	"fmt"
	"net/url"

	"github.com/snowmoe/jellyrpc/internal/jellyfin"
)

// will probably make the bridge open source and self hostable, making this configurable in future
const bridgeAPI = "https://rot.sh/poster"

// jellyfin reports time in 100ns ticks so 1e7 of them make a second
const ticksPerSecond = 10000000

type Activity struct {
	Title      string
	State      string
	TitleURL   string
	ArtworkURL string
	StartEpoch int64
	EndEpoch   int64
	Paused     bool
}

type Options struct {
	JellyfinURL   string
	Local         bool
	UseEpisodeArt bool
	UseDBLink     bool
}

// Build takes a set of options (derived from config), a jellyfin Session,
// the provider ids of the episode's series (empty if not an episode or
// unknown), and the current unix time. building and returning an rpc Activity
// based on the set options, the current jellyfin session, and the progress bar
// calculated from the jellyfin session progress.
func Build(opts Options, sess *jellyfin.Session, seriesIDs jellyfin.ProviderIDs, nowMillis int64) Activity {
	item := sess.NowPlayingItem

	title, state, imageID := mediaDisplay(item, opts.UseEpisodeArt)
	artworkURL := artworkURLFor(opts, imageID, item, seriesIDs)

	p := Activity{
		Title:      title,
		State:      state,
		TitleURL:   titleURLFor(item.ProviderIDs, opts.UseDBLink),
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

func mediaDisplay(item jellyfin.NowPlayingItem, useEpisodeArt bool) (title, state, imageID string) {
	if item.Type != "Episode" {
		return item.Name, "", item.ID
	}

	title = item.SeriesName
	state = fmt.Sprintf("S%02d:E%02d - %s", item.ParentIndexNumber, item.IndexNumber, item.Name)
	if useEpisodeArt || item.SeriesId == "" {
		imageID = item.ID
	} else {
		imageID = item.SeriesId
	}
	return title, state, imageID
}

func artworkURLFor(opts Options, imageID string, item jellyfin.NowPlayingItem, seriesIDs jellyfin.ProviderIDs) string {
	if opts.Local {
		// older bridges only read tmdb/imdb/tvdb and pick one themselves
		// (tmdb > imdb > tvdb), so the extra params are safe to send them
		ids := item.ProviderIDs
		q := url.Values{}
		setIfNotEmpty(q, "tmdb", ids.Tmdb)
		setIfNotEmpty(q, "imdb", ids.Imdb)
		setIfNotEmpty(q, "tvdb", ids.Tvdb)

		if item.Type == "Episode" {
			// episode ids are often missing on tmdb, the series ids let
			// the bridge fall back to the series poster
			setIfNotEmpty(q, "series_tmdb", seriesIDs.Tmdb)
			setIfNotEmpty(q, "series_imdb", seriesIDs.Imdb)
			setIfNotEmpty(q, "series_tvdb", seriesIDs.Tvdb)
		}

		if len(q) == 0 {
			return "jellyfin"
		}

		if item.Type == "Episode" {
			q.Set("type", "episode")
			if opts.UseEpisodeArt {
				q.Set("art", "episode")
			}
		} else if item.Type == "Movie" {
			q.Set("type", "movie")
		}

		return fmt.Sprintf("%s?%s", bridgeAPI, q.Encode())
	}

	return fmt.Sprintf("%s/Items/%s/Images/Primary?fillWidth=400&quality=85", opts.JellyfinURL, imageID)
}

func setIfNotEmpty(q url.Values, key, val string) {
	if val != "" {
		q.Set(key, val)
	}
}

func titleURLFor(ids jellyfin.ProviderIDs, enabled bool) string {
	if !enabled {
		return ""
	}
	if ids.Imdb != "" {
		return fmt.Sprintf("https://www.imdb.com/title/%s", ids.Imdb)
	}
	if ids.Tvdb != "" {
		return fmt.Sprintf("https://thetvdb.com/search?query=%s", ids.Tvdb)
	}

	return ""
}

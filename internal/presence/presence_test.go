package presence

import (
	"testing"

	"github.com/snowmoe/jellyrpc/internal/jellyfin"
)

func TestBuildPresenceEpisode(t *testing.T) {
	opts := Options{
		JellyfinURL:   "https://jelly.example.com",
		UseDBLink:     true,
		UseEpisodeArt: false,
	}
	sess := &jellyfin.Session{
		NowPlayingItem: jellyfin.NowPlayingItem{
			Name:              "Pilot",
			ID:                "episode-id",
			Type:              "Episode",
			RunTimeTicks:      180 * 10000000,
			SeriesName:        "Example Show",
			SeriesId:          "series-id",
			ParentIndexNumber: 1,
			IndexNumber:       2,
			ProviderIDs:       jellyfin.ProviderIDs{Imdb: "tt123"},
		},
	}
	sess.PlayState.PositionTicks = 30 * 10000000

	got := Build(opts, sess, 100000)

	if got.Title != "Example Show" {
		t.Fatalf("Title = %q, want %q", got.Title, "Example Show")
	}
	if got.State != "S01:E02 - Pilot" {
		t.Fatalf("State = %q, want %q", got.State, "S01:E02 - Pilot")
	}
	if got.ArtworkURL != "https://jelly.example.com/Items/series-id/Images/Primary?fillWidth=400&quality=85" {
		t.Fatalf("ArtworkURL = %q", got.ArtworkURL)
	}
	if got.TitleURL != "https://www.imdb.com/title/tt123" {
		t.Fatalf("TitleURL = %q", got.TitleURL)
	}
	if got.StartEpoch != 70000 || got.EndEpoch != 250000 {
		t.Fatalf("timestamps = %d/%d, want 70000/250000", got.StartEpoch, got.EndEpoch)
	}
}

func TestBuildPresenceLocalArtworkFallback(t *testing.T) {
	opts := Options{JellyfinURL: "http://192.168.1.10:8096"}
	sess := &jellyfin.Session{
		NowPlayingItem: jellyfin.NowPlayingItem{
			Name:        "Movie",
			ID:          "movie-id",
			Type:        "Movie",
			ProviderIDs: jellyfin.ProviderIDs{Tmdb: "42"},
		},
	}

	got := Build(opts, sess, 0)

	if got.ArtworkURL != "https://rot.sh/poster?tmdb=42" {
		t.Fatalf("ArtworkURL = %q", got.ArtworkURL)
	}
}

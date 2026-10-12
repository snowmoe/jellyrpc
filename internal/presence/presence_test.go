package presence

import (
	"testing"

	"github.com/snowmoe/jellyrpc/internal/jellyfin"
)

func TestBuildPresenceEpisode(t *testing.T) {
	opts := Options{
		JellyfinURL:   "https://jelly.example.com",
		Local:         false,
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

	got := Build(opts, sess, jellyfin.ProviderIDs{}, 100000)

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

func TestBuildPresenceArtwork(t *testing.T) {
	const jfURL = "https://jelly.example.com"

	tests := []struct {
		name     string
		local    bool
		ids      jellyfin.ProviderIDs
		expected string
	}{
		{"public uses jellyfin", false, jellyfin.ProviderIDs{Tmdb: "42"}, jfURL + "/Items/movie-id/Images/Primary?fillWidth=400&quality=85"},
		{"local sends all ids", true, jellyfin.ProviderIDs{Tmdb: "42", Imdb: "tt123", Tvdb: "7"}, "https://rot.sh/poster?imdb=tt123&tmdb=42&tvdb=7&type=movie"},
		{"local imdb and tvdb", true, jellyfin.ProviderIDs{Imdb: "tt123", Tvdb: "7"}, "https://rot.sh/poster?imdb=tt123&tvdb=7&type=movie"},
		{"local tvdb only", true, jellyfin.ProviderIDs{Tvdb: "7"}, "https://rot.sh/poster?tvdb=7&type=movie"},
		{"local with no ids", true, jellyfin.ProviderIDs{}, "jellyfin"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// the url alone shouldn't decide anything anymore, only Local does
			opts := Options{JellyfinURL: jfURL, Local: tc.local}
			sess := &jellyfin.Session{
				NowPlayingItem: jellyfin.NowPlayingItem{
					Name:        "Movie",
					ID:          "movie-id",
					Type:        "Movie",
					ProviderIDs: tc.ids,
				},
			}

			got := Build(opts, sess, jellyfin.ProviderIDs{}, 0)

			if got.ArtworkURL != tc.expected {
				t.Errorf("\nexpected: %s\ngot:      %s", tc.expected, got.ArtworkURL)
			}
		})
	}
}

func TestBuildPresenceEpisodeArtwork(t *testing.T) {
	tests := []struct {
		name       string
		episodeArt bool
		ids        jellyfin.ProviderIDs
		seriesIDs  jellyfin.ProviderIDs
		expected   string
	}{
		{"series ids only", false, jellyfin.ProviderIDs{}, jellyfin.ProviderIDs{Tvdb: "79414"}, "https://rot.sh/poster?series_tvdb=79414&type=episode"},
		{"episode and series ids", false, jellyfin.ProviderIDs{Tvdb: "555"}, jellyfin.ProviderIDs{Tmdb: "42511", Imdb: "tt0816407"}, "https://rot.sh/poster?series_imdb=tt0816407&series_tmdb=42511&tvdb=555&type=episode"},
		{"episode art", true, jellyfin.ProviderIDs{Tvdb: "555"}, jellyfin.ProviderIDs{Tmdb: "42511"}, "https://rot.sh/poster?art=episode&series_tmdb=42511&tvdb=555&type=episode"},
		{"no ids at all", true, jellyfin.ProviderIDs{}, jellyfin.ProviderIDs{}, "jellyfin"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := Options{Local: true, UseEpisodeArt: tc.episodeArt}
			sess := &jellyfin.Session{
				NowPlayingItem: jellyfin.NowPlayingItem{
					Name:        "Episode",
					ID:          "episode-id",
					Type:        "Episode",
					SeriesId:    "series-id",
					ProviderIDs: tc.ids,
				},
			}

			got := Build(opts, sess, tc.seriesIDs, 0)

			if got.ArtworkURL != tc.expected {
				t.Errorf("\nexpected: %s\ngot:      %s", tc.expected, got.ArtworkURL)
			}
		})
	}
}

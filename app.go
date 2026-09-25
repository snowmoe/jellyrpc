package main

import (
	"context"
	"time"

	"github.com/snowmoe/jellyrpc/internal/jellyfin"
	"github.com/snowmoe/jellyrpc/internal/presence"
)

type SessionProvider interface {
	ActiveSession(ctx context.Context) (*jellyfin.Session, error)
}

type PresenceClient interface {
	SetWatching(title, status, titleURL, arturl string, startEpoch, endEpoch int64) error
	SetPaused(title, titleURL, arturl string) error
	Close()
}

type PresenceConnector func(clientID string) (PresenceClient, error)

type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type realTicker struct {
	*time.Ticker
}

func (t realTicker) C() <-chan time.Time {
	return t.Ticker.C
}

type App struct {
	Config      *Config
	Sessions    SessionProvider
	Connect     PresenceConnector
	NewTicker   func(time.Duration) Ticker
	Now         func() time.Time
	lastPlaying string
	pausedSince time.Time
	sessionDown bool // whether the last jellyfin fetch failed, gates repeat warns
	discordDown bool // whether the last discord connect failed, gates repeat warns
}

func (a *App) Run(ctx context.Context) error {
	newTicker := a.NewTicker
	if newTicker == nil {
		newTicker = func(d time.Duration) Ticker {
			return realTicker{Ticker: time.NewTicker(d)}
		}
	}
	now := a.Now
	if now == nil {
		now = time.Now
	}

	ticker := newTicker(time.Duration(a.Config.PollRate) * time.Second)
	defer ticker.Stop()

	var dc PresenceClient
	defer func() {
		if dc != nil {
			dc.Close()
		}
	}()

	// poll once up front so we dont sit idle for a full poll rate on startup
	a.poll(ctx, &dc, now)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
			a.poll(ctx, &dc, now)
		}
	}
}

// poll runs a single tick, transient errors are logged and swallowed so the
// daemon keeps running across jellyfin/discord/network blips instead of dying
func (a *App) poll(ctx context.Context, dc *PresenceClient, now func() time.Time) {
	sess, err := a.Sessions.ActiveSession(ctx)
	if err != nil {
		// warn once on the way down, stay quiet until it recovers
		if !a.sessionDown {
			Warn("failed to fetch jellyfin session: %v", err)
			a.sessionDown = true
		}
		a.reset(dc)
		return
	}
	if a.sessionDown {
		Info("jellyfin session fetch recovered")
		a.sessionDown = false
	}

	if !isSessionActive(sess) {
		a.pausedSince = time.Time{}
		if *dc != nil {
			Info("no active jellyfin sessions, closing ipc socket")
			a.reset(dc)
		}
		return
	}

	// a new item restarts the pause window so a freshly opened but paused
	// item still gets shown and its own idle timeout
	if sess.NowPlayingItem.ID != a.lastPlaying {
		a.pausedSince = time.Time{}
	}

	// idle timeout, once paused past the configured window drop the socket and
	// stop pushing until playback resumes or the item changes
	if sess.PlayState.IsPaused {
		if a.pausedSince.IsZero() {
			a.pausedSince = now()
		}
		timeout := time.Duration(a.Config.PauseTimeout) * time.Minute
		if timeout > 0 && now().Sub(a.pausedSince) >= timeout {
			if *dc != nil {
				Info("paused for %d min, closing ipc socket", a.Config.PauseTimeout)
				a.reset(dc)
			}
			return
		}
	} else {
		a.pausedSince = time.Time{}
	}

	if *dc == nil {
		Info("active jellyfin session detected, opening ipc socket")
		conn, err := a.Connect(a.Config.AppID)
		if err != nil {
			if !a.discordDown {
				Warn("failed to connect to discord: %v", err)
				a.discordDown = true
			}
			return
		}
		a.discordDown = false
		*dc = conn
	}

	p := presence.Build(
		presence.Options{
			JellyfinURL:   a.Config.JellyfinURL,
			UseEpisodeArt: a.Config.UseEpisodeArt,
			UseDBLink:     a.Config.UseDBLink,
		},
		sess,
		now().UnixMilli(),
	)

	if a.lastPlaying != sess.NowPlayingItem.ID {
		a.lastPlaying = sess.NowPlayingItem.ID
		Info("active playing: %s, id: %s", sess.NowPlayingItem.Name, sess.NowPlayingItem.ID)

		if a.Config.UseDBLink && p.TitleURL == "" {
			Warn("unable to find db link for active media")
		}
	}

	var setErr error
	if p.Paused {
		setErr = (*dc).SetPaused(p.Title, p.TitleURL, p.ArtworkURL)
	} else {
		setErr = (*dc).SetWatching(
			p.Title,
			p.State,
			p.TitleURL,
			p.ArtworkURL,
			p.StartEpoch,
			p.EndEpoch,
		)
	}

	// drop the connection on write failure so the next tick reconnects
	if setErr != nil {
		Warn("failed to update discord status: %v", setErr)
		a.reset(dc)
	}
}

func (a *App) reset(dc *PresenceClient) {
	if *dc != nil {
		(*dc).Close()
		*dc = nil
	}
}

func isSessionActive(sess *jellyfin.Session) bool {
	if sess == nil {
		return false
	}

	if sess.NowPlayingItem.Name == "" || sess.NowPlayingItem.ID == "" {
		return false
	}

	return true
}

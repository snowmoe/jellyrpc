package main

import (
	"context"
	"time"
)

type SessionProvider interface {
	GetActiveSession(ctx context.Context) (*Session, error)
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
	sess, err := a.Sessions.GetActiveSession(ctx)
	if err != nil {
		Warn("failed to fetch jellyfin session: %v", err)
		a.reset(dc)
		return
	}

	if !isSessionActive(sess) {
		if *dc != nil {
			Info("no active jellyfin sessions, closing ipc socket")
			a.reset(dc)
		}
		return
	}

	if *dc == nil {
		Info("active jellyfin session detected, opening ipc socket")
		conn, err := a.Connect(a.Config.AppID)
		if err != nil {
			Warn("failed to connect to discord: %v", err)
			return
		}
		*dc = conn
	}

	if a.lastPlaying != sess.NowPlayingItem.Id {
		a.lastPlaying = sess.NowPlayingItem.Id
		Info("active playing: %s, id: %s", sess.NowPlayingItem.Name, sess.NowPlayingItem.Id)
	}

	presence := BuildPresence(a.Config, sess, now().UnixMilli())

	var setErr error
	if presence.Paused {
		setErr = (*dc).SetPaused(presence.Title, presence.TitleURL, presence.ArtworkURL)
	} else {
		setErr = (*dc).SetWatching(
			presence.Title,
			presence.State,
			presence.TitleURL,
			presence.ArtworkURL,
			presence.StartEpoch,
			presence.EndEpoch,
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

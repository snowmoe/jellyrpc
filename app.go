package main

import (
	"context"
	"errors"
	"time"

	"github.com/snowmoe/jellyrpc/internal/discord"
	"github.com/snowmoe/jellyrpc/internal/jellyfin"
	"github.com/snowmoe/jellyrpc/internal/presence"
)

// ms, jf epochs are unix millis
const startTolerance int64 = 5_000

// interval to resend rpc even if activity hasn't changed
// so we notice if discord restarted
const resendInterval = 60 * time.Second

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

	// so we can gate presence updates
	lastSent   presence.Activity
	lastSentAt time.Time

	// states to gate repeated warns
	sessionDown bool // last jellyfin fetch failed
	discordDown bool // last discord connect failed
	rpcRejected bool // last rpc set activity was rejected
}

func (a *App) run(ctx context.Context) error {
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

	local, msg := artworkSource(a.Config.ArtworkSource, a.Config.JellyfinURL)
	Info("%s", msg)

	opts := presence.Options{
		JellyfinURL:   a.Config.JellyfinURL,
		Local:         local,
		UseEpisodeArt: a.Config.UseEpisodeArt,
		UseDBLink:     a.Config.UseDBLink,
	}

	// poll once up front so we dont sit idle for a full poll rate on startup
	a.poll(ctx, &dc, opts, now)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
			a.poll(ctx, &dc, opts, now)
		}
	}
}

// poll runs a single tick, transient errors are logged and swallowed so the
// daemon keeps running across jellyfin/discord/network blips instead of dying
func (a *App) poll(ctx context.Context, dc *PresenceClient, opts presence.Options, now func() time.Time) {
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
		opts,
		sess,
		now().UnixMilli(),
	)

	if a.lastPlaying != sess.NowPlayingItem.ID {
		a.lastPlaying = sess.NowPlayingItem.ID
		// log new item playing
		Info("active playing: %s, id: %s", sess.NowPlayingItem.Name, sess.NowPlayingItem.ID)

		// clear rpc rejection state
		a.rpcRejected = false

		if a.Config.UseDBLink && p.TitleURL == "" {
			Warn("unable to find db link for active media")
		}
	}

	if sameActivity(p, a.lastSent) && now().Sub(a.lastSentAt) < resendInterval {
		return
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

	rejErr, ok := errors.AsType[*discord.RejectedError](setErr)
	if ok {
		if !a.rpcRejected {
			Warn("%s", rejErr)
			a.rpcRejected = true
		}
		a.lastSent = p
		a.lastSentAt = now()
		return
	}

	if setErr != nil {
		// drop the connection on write failure so the next tick reconnects
		Warn("failed to update discord status: %v", setErr)
		a.reset(dc)
		return
	}

	// set the last send activity and rpcRejected state
	a.lastSent = p
	a.lastSentAt = now()
	a.rpcRejected = false
}

func (a *App) reset(dc *PresenceClient) {
	if *dc != nil {
		(*dc).Close()
		*dc = nil
	}

	// reset the lastSent activity
	a.lastSent = presence.Activity{}
}

func sameActivity(a, b presence.Activity) bool {
	diff := a.StartEpoch - b.StartEpoch

	// abs the diff
	if diff < 0 {
		diff = -diff
	}

	// 0 the epochs to compare the other struct fields
	a.StartEpoch, b.StartEpoch = 0, 0
	a.EndEpoch, b.EndEpoch = 0, 0

	// only considered the same if fields match and epoch difference
	// is less than the tolerance
	return a == b && diff < startTolerance
}

func artworkSource(src, jellyfinURL string) (local bool, msg string) {
	switch src {
	case artJellyfin:
		// force local to false so artwork is attempted from jellyfin
		local = false
		msg = "artwork via jellyfin (config)"
	case artBridge:
		// force local to true so artwork is fetched via bridge
		local = true
		msg = "artwork via bridge (config)"
	default: // artAuto or unset
		local = jellyfin.IsLocalInstance(jellyfinURL)
		if local {
			// this message is verbose as fuck because I could've done with it myself,
			// if someone has a public reverse proxy for jellyfin, but on lan they rewrite
			// the domain to the reverse proxy locally we guess local instance from the lookup
			msg = "artwork via bridge (auto: local instance, set ARTWORK_SOURCE=jellyfin if public)"
		} else {
			msg = "artwork via jellyfin (auto: public instance)"
		}
	}

	return
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

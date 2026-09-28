package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/snowmoe/jellyrpc/internal/discord"
	"github.com/snowmoe/jellyrpc/internal/jellyfin"
	"github.com/snowmoe/jellyrpc/internal/presence"
)

type fakeSessions struct {
	sess *jellyfin.Session
	err  error
}

func (f *fakeSessions) ActiveSession(context.Context) (*jellyfin.Session, error) {
	return f.sess, f.err
}

type fakePresenceClient struct {
	watchingCalls int
	pausedCalls   int
	closed        bool
	err           error // returned from every set, nil unless a test wants a failure
}

func (f *fakePresenceClient) SetWatching(title, status, titleURL, arturl string, startEpoch, endEpoch int64) error {
	f.watchingCalls++
	return f.err
}

func (f *fakePresenceClient) SetPaused(title, titleURL, arturl string) error {
	f.pausedCalls++
	return f.err
}

func (f *fakePresenceClient) Close() {
	f.closed = true
}

type fakeTicker struct {
	ch chan time.Time
}

func newFakeTicker() *fakeTicker {
	return &fakeTicker{ch: make(chan time.Time)}
}

func (f *fakeTicker) C() <-chan time.Time {
	return f.ch
}

func (f *fakeTicker) Stop() {}

func TestAppRunUpdatesPresenceAndClosesOnCancel(t *testing.T) {
	ticker := newFakeTicker()
	client := &fakePresenceClient{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app := &App{
		Config: &Config{
			JellyfinURL:   "https://jelly.example.com",
			PollRate:      1,
			AppID:         "app-id",
			ArtworkSource: artJellyfin, // skips the dns lookup
		},
		Sessions: &fakeSessions{sess: &jellyfin.Session{
			NowPlayingItem: jellyfin.NowPlayingItem{
				Name:         "Movie",
				ID:           "movie-id",
				Type:         "Movie",
				RunTimeTicks: 120 * 10000000,
			},
		}},
		Connect: func(string) (PresenceClient, error) {
			return client, nil
		},
		NewTicker: func(time.Duration) Ticker {
			return ticker
		},
		Now: func() time.Time {
			return time.UnixMilli(100000)
		},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.run(ctx)
	}()

	ticker.ch <- time.Now()
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not exit after context cancellation")
	}

	// one immediate poll on startup plus one from the ticker, the second
	// one is identical (clock doesn't move) so it gets skipped
	if client.watchingCalls != 1 {
		t.Fatalf("watchingCalls = %d, want 1", client.watchingCalls)
	}
	if client.closed != true {
		t.Fatal("client was not closed")
	}
}

func TestPollPauseTimeoutClosesSocket(t *testing.T) {
	client := &fakePresenceClient{}
	current := time.UnixMilli(0)
	now := func() time.Time { return current }

	sess := &jellyfin.Session{
		NowPlayingItem: jellyfin.NowPlayingItem{Name: "Movie", ID: "movie-id", Type: "Movie"},
	}
	sess.PlayState.IsPaused = true

	app := &App{
		Config: &Config{
			JellyfinURL:  "https://jelly.example.com",
			AppID:        "app-id",
			PauseTimeout: 1,
		},
		Sessions: &fakeSessions{sess: sess},
		Connect: func(string) (PresenceClient, error) {
			return client, nil
		},
	}

	var dc PresenceClient
	opts := presence.Options{JellyfinURL: "https://jelly.example.com"}

	// first poll while paused opens the socket and pushes once
	app.poll(context.Background(), &dc, opts, now)
	if dc == nil || client.pausedCalls != 1 {
		t.Fatalf("expected open socket and one paused push, got dc=%v pausedCalls=%d", dc != nil, client.pausedCalls)
	}

	// advance past the timeout, next poll should drop the socket and stop pushing
	current = current.Add(2 * time.Minute)
	app.poll(context.Background(), &dc, opts, now)
	if dc != nil {
		t.Fatal("socket was not closed after pause timeout")
	}
	if !client.closed {
		t.Fatal("client was not closed after pause timeout")
	}
	if client.pausedCalls != 1 {
		t.Fatalf("pausedCalls = %d, want 1 (no push after timeout)", client.pausedCalls)
	}
}

// playing gives an unpaused session for name, so poll goes down the SetWatching
// path. names need to differ between items or the activities look identical
func playing(name string) *jellyfin.Session {
	return &jellyfin.Session{
		NowPlayingItem: jellyfin.NowPlayingItem{Name: name, ID: name + "-id", Type: "Movie", RunTimeTicks: 120 * 10000000},
	}
}

// seconds to jellyfin ticks
func ticks(d time.Duration) int64 {
	return int64(d / time.Second * 10000000)
}

func TestPollRejectedActivity(t *testing.T) {
	logs := captureLog(t)

	rejected := &discord.RejectedError{Code: 4000, Message: "salmon"}
	client := &fakePresenceClient{err: rejected}
	sessions := &fakeSessions{sess: playing("Salmon")}

	current := time.UnixMilli(100000)
	now := func() time.Time { return current }

	app := &App{
		Config:   &Config{JellyfinURL: "https://jelly.example.com", AppID: "app-id"},
		Sessions: sessions,
		Connect: func(string) (PresenceClient, error) {
			return client, nil
		},
	}

	var dc PresenceClient
	opts := presence.Options{JellyfinURL: "https://jelly.example.com"}
	poll := func() { app.poll(context.Background(), &dc, opts, now) }
	warns := func() int { return strings.Count(logs.String(), "discord rejected activity") }

	// first rejection warns, but the socket is fine so we keep it
	poll()
	if warns() != 1 {
		t.Fatalf("expected 1 warn, got %d\n%s", warns(), logs)
	}
	if dc == nil || client.closed {
		t.Fatal("rejection shouldn't drop the connection")
	}

	// same activity again, no point resending something discord already said no to
	poll()
	poll()
	if client.watchingCalls != 1 {
		t.Errorf("expected rejected activity not to be resent, got %d calls", client.watchingCalls)
	}

	// the forced resend still retries it, but it's the same item so stay quiet
	current = current.Add(resendInterval)
	poll()
	if client.watchingCalls != 2 {
		t.Errorf("expected a forced resend after %s, got %d calls", resendInterval, client.watchingCalls)
	}
	if warns() != 1 {
		t.Fatalf("expected still 1 warn, got %d\n%s", warns(), logs)
	}

	// new item gets its own warn
	sessions.sess = playing("Trout")
	poll()
	if warns() != 2 {
		t.Fatalf("expected 2 warns after new item, got %d\n%s", warns(), logs)
	}

	// a success clears it, so the next rejection on the same item warns again
	client.err = nil
	sessions.sess = playing("Cod")
	poll()
	client.err = rejected
	sessions.sess.PlayState.PositionTicks = ticks(time.Minute) // seek so it actually sends
	poll()
	if warns() != 3 {
		t.Fatalf("expected 3 warns after success then reject, got %d\n%s", warns(), logs)
	}

	if dc == nil || client.closed {
		t.Error("connection should have survived all of that")
	}
}

func TestPollSkipsUnchangedActivity(t *testing.T) {
	client := &fakePresenceClient{}
	sess := playing("Salmon")

	current := time.UnixMilli(100000)
	now := func() time.Time { return current }

	sessions := &fakeSessions{sess: sess}

	app := &App{
		Config:   &Config{JellyfinURL: "https://jelly.example.com", AppID: "app-id"},
		Sessions: sessions,
		Connect: func(string) (PresenceClient, error) {
			return client, nil
		},
	}

	var dc PresenceClient
	opts := presence.Options{JellyfinURL: "https://jelly.example.com"}
	poll := func() { app.poll(context.Background(), &dc, opts, now) }

	// normal playback, clock and position move together so start stays put
	play := func(d time.Duration) {
		current = current.Add(d)
		sess.PlayState.PositionTicks += ticks(d)
	}

	expect := func(step string, watching, paused int) {
		t.Helper()
		if client.watchingCalls != watching || client.pausedCalls != paused {
			t.Fatalf("%s: expected %d watching + %d paused, got %d + %d",
				step, watching, paused, client.watchingCalls, client.pausedCalls)
		}
	}

	poll()
	expect("first poll", 1, 0)

	play(5 * time.Second)
	poll()
	play(5 * time.Second)
	poll()
	expect("normal playback", 1, 0)

	// jellyfin's position lagging a bit behind the clock, like the rounding wobble
	current = current.Add(time.Second)
	poll()
	expect("small wobble", 1, 0)

	// skip forward 30s
	sess.PlayState.PositionTicks += ticks(30 * time.Second)
	poll()
	expect("seek", 2, 0)

	// nothing changes for a while, still resend so a restarted discord gets it back
	play(resendInterval)
	poll()
	expect("forced resend", 3, 0)

	sess.PlayState.IsPaused = true
	poll()
	current = current.Add(10 * time.Second)
	poll()
	expect("paused", 3, 1)

	sess.PlayState.IsPaused = false
	poll()
	expect("resumed", 4, 1)

	// write fails, socket gets dropped
	client.err = errors.New("broken pipe")
	sess.PlayState.PositionTicks += ticks(time.Minute)
	poll()
	expect("failed send", 5, 1)
	if dc != nil {
		t.Fatal("expected connection dropped after failed send")
	}

	// fresh socket means discord shows nothing, so the same activity has to go out again
	client.err = nil
	poll()
	expect("after reconnect", 6, 1)

	// jellyfin blips and we drop the socket, then it comes straight back with the
	// same thing well inside the resend interval, still has to be sent
	sessions.err = errors.New("salmon")
	poll()
	sessions.err = nil
	play(5 * time.Second)
	poll()
	expect("after jellyfin blip", 7, 1)
}

func TestPollSetErrorResets(t *testing.T) {
	logs := captureLog(t)

	// any error that isn't a rejection means the socket's dead
	client := &fakePresenceClient{err: errors.New("broken pipe")}

	app := &App{
		Config:   &Config{JellyfinURL: "https://jelly.example.com", AppID: "app-id"},
		Sessions: &fakeSessions{sess: playing("Salmon")},
		Connect: func(string) (PresenceClient, error) {
			return client, nil
		},
	}

	var dc PresenceClient
	opts := presence.Options{JellyfinURL: "https://jelly.example.com"}
	app.poll(context.Background(), &dc, opts, func() time.Time { return time.UnixMilli(100000) })

	if dc != nil || !client.closed {
		t.Fatal("expected the connection to be dropped so the next tick reconnects")
	}
	if !strings.Contains(logs.String(), "failed to update discord status: broken pipe") {
		t.Errorf("expected a warn about the failed update, got:\n%s", logs)
	}
	if strings.Contains(logs.String(), "discord rejected activity") {
		t.Errorf("plain error shouldn't be reported as a rejection:\n%s", logs)
	}
}

func TestSameActivity(t *testing.T) {
	base := presence.Activity{
		Title:      "Salmon",
		State:      "S1E2",
		TitleURL:   "https://title",
		ArtworkURL: "https://art",
		StartEpoch: 100000,
		EndEpoch:   200000,
	}

	// change copies base and lets each case tweak it
	change := func(f func(*presence.Activity)) presence.Activity {
		a := base
		f(&a)
		return a
	}
	start := func(ms int64) presence.Activity {
		return change(func(a *presence.Activity) { a.StartEpoch += ms; a.EndEpoch += ms })
	}

	tests := []struct {
		name     string
		b        presence.Activity
		expected bool
	}{
		{"identical", base, true},
		{"wobble forward", start(1000), true},
		{"wobble back", start(-1000), true},
		{"just under tolerance", start(startTolerance - 1), true},
		// tolerance is exclusive
		{"at tolerance", start(startTolerance), false},
		{"seek forward", start(-30000), false},
		{"seek back", start(30000), false},
		// only start matters, end follows it anyway
		{"end moved alone", change(func(a *presence.Activity) { a.EndEpoch += 30000 }), true},
		{"paused", change(func(a *presence.Activity) { a.Paused = true }), false},
		{"different title", change(func(a *presence.Activity) { a.Title = "Trout" }), false},
		{"different state", change(func(a *presence.Activity) { a.State = "S1E3" }), false},
		{"different art", change(func(a *presence.Activity) { a.ArtworkURL = "https://trout" }), false},
		{"different link", change(func(a *presence.Activity) { a.TitleURL = "" }), false},
		// what reset leaves behind, must never match something real
		{"blank", presence.Activity{}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameActivity(base, tc.b); got != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
			// should be the same whichever way round
			if got := sameActivity(tc.b, base); got != tc.expected {
				t.Errorf("reversed: expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func TestArtworkSource(t *testing.T) {
	// only ip literals here so auto never hits dns, the lookup side
	// gets tested properly in the jellyfin package with a fake resolver
	const (
		localURL  = "http://192.168.1.10:8096"
		publicURL = "https://203.0.113.5"
	)

	tests := []struct {
		name      string
		src       string
		url       string
		wantLocal bool
		wantMsg   string
	}{
		{"forced jellyfin on local", artJellyfin, localURL, false, "artwork via jellyfin (config)"},
		{"forced bridge on public", artBridge, publicURL, true, "artwork via bridge (config)"},
		{"auto local", artAuto, localURL, true, "artwork via bridge (auto: local instance, set ARTWORK_SOURCE=jellyfin if public)"},
		{"auto public", artAuto, publicURL, false, "artwork via jellyfin (auto: public instance)"},
		// anything that skipped applyDefaults should still act like auto
		{"unset acts as auto", "", localURL, true, "artwork via bridge (auto: local instance, set ARTWORK_SOURCE=jellyfin if public)"},
		{"garbage acts as auto", "salmon", publicURL, false, "artwork via jellyfin (auto: public instance)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			local, msg := artworkSource(tc.src, tc.url)

			if local != tc.wantLocal {
				t.Errorf("expected local %v, got %v", tc.wantLocal, local)
			}
			if msg != tc.wantMsg {
				t.Errorf("\nexpected: %q\ngot:      %q", tc.wantMsg, msg)
			}
		})
	}
}

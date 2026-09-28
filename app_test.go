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

	// one immediate poll on startup plus one from the ticker
	if client.watchingCalls != 2 {
		t.Fatalf("watchingCalls = %d, want 2", client.watchingCalls)
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

// playing gives an unpaused session for id, so poll goes down the SetWatching path
func playing(id string) *jellyfin.Session {
	return &jellyfin.Session{
		NowPlayingItem: jellyfin.NowPlayingItem{Name: "Salmon", ID: id, Type: "Movie", RunTimeTicks: 120 * 10000000},
	}
}

func TestPollRejectedActivity(t *testing.T) {
	logs := captureLog(t)

	client := &fakePresenceClient{err: &discord.RejectedError{Code: 4000, Message: "salmon"}}
	sessions := &fakeSessions{sess: playing("salmon-id")}
	now := func() time.Time { return time.UnixMilli(100000) }

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

	// same item rejected again, stay quiet (this used to warn every other tick)
	poll()
	poll()
	if warns() != 1 {
		t.Fatalf("expected still 1 warn, got %d\n%s", warns(), logs)
	}
	if client.watchingCalls != 3 {
		t.Errorf("expected we still try sending every poll, got %d calls", client.watchingCalls)
	}

	// new item gets its own warn
	sessions.sess = playing("trout-id")
	poll()
	if warns() != 2 {
		t.Fatalf("expected 2 warns after new item, got %d\n%s", warns(), logs)
	}

	// a success clears it, so the next rejection on the same item warns again
	client.err = nil
	poll()
	client.err = &discord.RejectedError{Code: 4000, Message: "salmon"}
	poll()
	if warns() != 3 {
		t.Fatalf("expected 3 warns after success then reject, got %d\n%s", warns(), logs)
	}

	if dc == nil || client.closed {
		t.Error("connection should have survived all of that")
	}
}

func TestPollSetErrorResets(t *testing.T) {
	logs := captureLog(t)

	// any error that isn't a rejection means the socket's dead
	client := &fakePresenceClient{err: errors.New("broken pipe")}

	app := &App{
		Config:   &Config{JellyfinURL: "https://jelly.example.com", AppID: "app-id"},
		Sessions: &fakeSessions{sess: playing("salmon-id")},
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

package main

import (
	"context"
	"testing"
	"time"

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
}

func (f *fakePresenceClient) SetWatching(title, status, titleURL, arturl string, startEpoch, endEpoch int64) error {
	f.watchingCalls++
	return nil
}

func (f *fakePresenceClient) SetPaused(title, titleURL, arturl string) error {
	f.pausedCalls++
	return nil
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

package main

import (
	"context"
	"testing"
	"time"
)

type fakeSessions struct {
	sess *Session
	err  error
}

func (f *fakeSessions) GetActiveSession(context.Context) (*Session, error) {
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
			JellyfinURL: "https://jelly.example.com",
			PollRate:    1,
			AppID:       "app-id",
		},
		Sessions: &fakeSessions{sess: &Session{
			NowPlayingItem: NowPlayingItem{
				Name:         "Movie",
				Id:           "movie-id",
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
		errCh <- app.Run(ctx)
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

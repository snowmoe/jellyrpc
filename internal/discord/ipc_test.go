package discord

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type frame struct {
	op   uint32
	body []byte
}

// written separately from send/readFrame so the tests don't just
// agree with whatever the real ones do
func writeTestFrame(w io.Writer, op uint32, body []byte) error {
	header := make([]byte, 8)
	binary.LittleEndian.PutUint32(header[0:4], op)
	binary.LittleEndian.PutUint32(header[4:8], uint32(len(body)))

	_, err := w.Write(append(header, body...))
	return err
}

func readTestFrame(r io.Reader) (frame, error) {
	header := make([]byte, 8)
	if _, err := io.ReadFull(r, header); err != nil {
		return frame{}, err
	}

	body := make([]byte, binary.LittleEndian.Uint32(header[4:8]))
	if _, err := io.ReadFull(r, body); err != nil {
		return frame{}, err
	}

	return frame{op: binary.LittleEndian.Uint32(header[0:4]), body: body}, nil
}

// socketDir makes a temp dir and points XDG_RUNTIME_DIR at it. uses
// MkdirTemp over t.TempDir since unix socket paths max out at 108 chars
func socketDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "jrpc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)

	return dir
}

// fakeDiscord listens on discord-ipc-n in dir and handles one connection,
// every frame it gets is sent on frames. reply decides the opcode sent back,
// returning false closes the connection without replying
func fakeDiscord(t *testing.T, dir string, n int, reply func(op uint32) (uint32, bool)) <-chan frame {
	t.Helper()

	ln, err := net.Listen("unix", filepath.Join(dir, fmt.Sprintf("discord-ipc-%d", n)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	frames := make(chan frame, 10)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			f, err := readTestFrame(conn)
			if err != nil {
				return
			}
			frames <- f

			op, ok := reply(f.op)
			if !ok {
				return
			}
			if writeTestFrame(conn, op, []byte("{}")) != nil {
				return
			}
		}
	}()

	return frames
}

// acceptHandshake is the happy path discord, opcode 1 back for everything
func acceptHandshake(uint32) (uint32, bool) { return 1, true }

func nextFrame(t *testing.T, frames <-chan frame) frame {
	t.Helper()

	select {
	case f := <-frames:
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for frame")
		return frame{}
	}
}

func TestDiscoverIPCSocket(t *testing.T) {
	t.Run("finds later socket", func(t *testing.T) {
		dir := socketDir(t)

		// 0 to 2 don't exist so it has to keep going
		ln, err := net.Listen("unix", filepath.Join(dir, "discord-ipc-3"))
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()

		conn, err := DiscoverIPCSocket()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		conn.Close()
	})

	t.Run("no sockets", func(t *testing.T) {
		socketDir(t)

		conn, err := DiscoverIPCSocket()
		if err == nil {
			conn.Close()
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "could not connect to discord ipc") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestNewConn(t *testing.T) {
	t.Run("handshake accepted", func(t *testing.T) {
		frames := fakeDiscord(t, socketDir(t), 0, acceptHandshake)

		dc, err := NewConn("salmon", "v1.2.3")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer dc.Close()

		f := nextFrame(t, frames)
		if f.op != 0 {
			t.Errorf("expected handshake opcode 0, got %d", f.op)
		}

		var hs handshake
		err = json.Unmarshal(f.body, &hs)
		if err != nil {
			t.Fatalf("bad handshake json: %v", err)
		}
		if hs.V != "1" || hs.ClientID != "salmon" {
			t.Errorf("expected v1 + salmon, got %+v", hs)
		}
	})

	t.Run("handshake rejected", func(t *testing.T) {
		fakeDiscord(t, socketDir(t), 0, func(uint32) (uint32, bool) { return 2, true })

		_, err := NewConn("salmon", "v1.2.3")
		if err == nil || !strings.Contains(err.Error(), "rejected handshake") {
			t.Errorf("expected rejected handshake error, got: %v", err)
		}
	})

	t.Run("discord hangs up", func(t *testing.T) {
		fakeDiscord(t, socketDir(t), 0, func(uint32) (uint32, bool) { return 0, false })

		_, err := NewConn("salmon", "v1.2.3")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})
}

// decodeActivity pulls the activity back out of a SET_ACTIVITY frame
func decodeActivity(t *testing.T, f frame) activity {
	t.Helper()

	if f.op != 1 {
		t.Errorf("expected opcode 1, got %d", f.op)
	}

	var p payload
	err := json.Unmarshal(f.body, &p)
	if err != nil {
		t.Fatalf("bad payload json: %v", err)
	}
	if p.Cmd != "SET_ACTIVITY" {
		t.Errorf("expected SET_ACTIVITY, got %q", p.Cmd)
	}
	if p.Args.PID != os.Getpid() {
		t.Errorf("expected our pid %d, got %d", os.Getpid(), p.Args.PID)
	}

	return p.Args.Activity
}

func TestSetActivity(t *testing.T) {
	frames := fakeDiscord(t, socketDir(t), 0, acceptHandshake)

	dc, err := NewConn("salmon", "v1.2.3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer dc.Close()

	// throw away the handshake
	nextFrame(t, frames)

	t.Run("watching", func(t *testing.T) {
		err := dc.SetWatching("Salmon", "S1E2", "https://title", "https://art", 100, 200)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		a := decodeActivity(t, nextFrame(t, frames))

		if a.Type != 3 || a.Details != "Salmon" || a.State != "S1E2" || a.DetailsURL != "https://title" {
			t.Errorf("unexpected activity: %+v", a)
		}
		if a.Assets == nil || a.Assets.LargeImage != "https://art" {
			t.Fatalf("expected art asset, got %+v", a.Assets)
		}
		// version wasn't being set in Conn before 904a550
		if a.Assets.LargeText != "jellyrpc v1.2.3" {
			t.Errorf("expected 'jellyrpc v1.2.3', got %q", a.Assets.LargeText)
		}
		if a.Timestamps == nil || a.Timestamps.Start != 100 || a.Timestamps.End != 200 {
			t.Errorf("expected timestamps 100-200, got %+v", a.Timestamps)
		}
	})

	t.Run("watching no start", func(t *testing.T) {
		err := dc.SetWatching("Salmon", "S1E2", "", "", 0, 200)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		a := decodeActivity(t, nextFrame(t, frames))
		if a.Timestamps != nil {
			t.Errorf("expected no timestamps, got %+v", a.Timestamps)
		}
	})

	t.Run("empty clears", func(t *testing.T) {
		err := dc.SetWatching("", "", "https://title", "https://art", 100, 200)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		a := decodeActivity(t, nextFrame(t, frames))
		if a != (activity{}) {
			t.Errorf("expected empty activity, got %+v", a)
		}
	})

	t.Run("paused", func(t *testing.T) {
		err := dc.SetPaused("Salmon", "https://title", "https://art")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		a := decodeActivity(t, nextFrame(t, frames))
		if a.State != "Paused" || a.Details != "Salmon" {
			t.Errorf("unexpected activity: %+v", a)
		}
		if a.Assets == nil || a.Assets.LargeText != "jellyrpc v1.2.3" {
			t.Errorf("expected version in large text, got %+v", a.Assets)
		}
	})
}

func TestSendFrame(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	dc := &Conn{conn: client}
	defer dc.Close()

	go dc.send(1, []byte("salmon"))

	server.SetReadDeadline(time.Now().Add(2 * time.Second))
	f, err := readTestFrame(server)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.op != 1 || string(f.body) != "salmon" {
		t.Errorf("expected 1 salmon, got %d %q", f.op, f.body)
	}
}

func TestReadFrameSplit(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	dc := &Conn{conn: client}
	defer dc.Close()

	body := []byte(`{"salmon":"trout"}`)

	// header + half the body, then the rest a bit later, like it came
	// over in two packets
	go func() {
		header := make([]byte, 8)
		binary.LittleEndian.PutUint32(header[0:4], 1)
		binary.LittleEndian.PutUint32(header[4:8], uint32(len(body)))

		server.Write(append(header, body[:5]...))
		time.Sleep(10 * time.Millisecond)
		server.Write(body[5:])
	}()

	op, got, err := dc.readFrame()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if op != 1 || string(got) != string(body) {
		t.Errorf("expected 1 %q, got %d %q", body, op, got)
	}
}

func TestCloseTwice(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	dc := &Conn{conn: client}
	dc.Close()
	// shouldnt panic
	dc.Close()

	if dc.conn != nil {
		t.Error("expected conn to be nil after close")
	}
}

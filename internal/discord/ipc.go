package discord

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

// https://docs.discord.com/developers/topics/rpc#rpc-over-ipc

// handshake payload where we send our client id
// V = rpc version, ClientID is self explanatory
type handshake struct {
	V        string `json:"v"`
	ClientID string `json:"client_id"`
}

// main payload consisting of our cmd, args and a nonce
// https://docs.discord.com/developers/topics/rpc#payloads
type payload struct {
	Cmd   string `json:"cmd"`
	Args  args   `json:"args"`
	Nonce string `json:"nonce"`
}

// args struct contains our client pid (us, not discord client)
// and the activity payload inside
type args struct {
	PID      int      `json:"pid"`
	Activity activity `json:"activity"`
}

// the rest are pretty self explanatory
// we're just creating the payload json structure with structs

type activity struct {
	Type       int         `json:"type"`
	Details    string      `json:"details"`
	DetailsURL string      `json:"details_url,omitempty"`
	State      string      `json:"state"`
	Timestamps *timestamps `json:"timestamps,omitempty"`
	Assets     *assets     `json:"assets,omitempty"`
	Instance   bool        `json:"instance"`
}

type assets struct {
	LargeImage string `json:"large_image,omitempty"`
	LargeText  string `json:"large_text,omitempty"`
}

type timestamps struct {
	Start int64 `json:"start,omitempty"`
	End   int64 `json:"end,omitempty"`
}

// because we have functions on this struct so when used later in main.go
// we can just call <Conn var name>.SetWatching() without having to
// pass in a pointer to the socket connection every time
type Conn struct {
	conn    net.Conn
	version string
}

// initiates a connection with discords ipc, completes a handshake
// and returns a pointer to a DiscordConn type to be used for calling the other funcs
func NewConn(clientID, version string) (*Conn, error) {
	// start with the xdg runtime dir and if not found for whatever reason use /tmp fallback
	// technically there's other fallback var's (TMPDIR, TEMP, TMP) but they were all empty for me
	// seems reasonable to skip straight to /tmp

	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = "/tmp"
	}

	// discord exposes sockets as discord-ipc-0..9, extra clients (flatpak, a
	// second install) land on higher numbers so try each and take the first
	var conn net.Conn
	var err error
	for i := range 10 {
		socketPath := filepath.Join(runtimeDir, fmt.Sprintf("discord-ipc-%d", i))
		conn, err = net.Dial("unix", socketPath)
		if err == nil {
			break
		}
	}
	if conn == nil {
		return nil, fmt.Errorf("could not connect to discord ipc: %w", err)
	}

	dc := &Conn{conn: conn}

	// marhshal a json object from our handshake payload struct
	// using version 1 of rpc, and the clientID passed into this func
	hs, _ := json.Marshal(handshake{V: "1", ClientID: clientID})

	// send with opcode 0, send by the client to initiate handshake
	// we should recieve opcode 1 later if this is successful
	if err := dc.send(0, hs); err != nil {
		conn.Close()
		return nil, err
	}

	// read the handshake reply, opcode 2 is a close frame so discord rejected us
	opcode, _, err := dc.readFrame()
	if err != nil {
		conn.Close()
		return nil, err
	}
	if opcode == 2 {
		conn.Close()
		return nil, fmt.Errorf("discord rejected handshake, check your app id")
	}

	// return our dc struct to be used in main.go for calling the other funcs
	return dc, nil
}

// reads a single frame off the socket, the 8 byte header gives us the opcode
// and body length so we know exactly how many bytes to pull, ReadFull handles
// the body arriving across multiple packets
func (dc *Conn) readFrame() (uint32, []byte, error) {
	// deadline so a silent discord cant wedge us in a blocking read
	dc.conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	header := make([]byte, 8)
	if _, err := io.ReadFull(dc.conn, header); err != nil {
		return 0, nil, err
	}

	opcode := binary.LittleEndian.Uint32(header[0:4])
	length := binary.LittleEndian.Uint32(header[4:8])

	body := make([]byte, length)
	if _, err := io.ReadFull(dc.conn, body); err != nil {
		return 0, nil, err
	}

	return opcode, body, nil
}

// takes an opcode (uint32 so 4 bytes) and a payload and
// sends over the socket, following the discord rpc over ipc spec
func (dc *Conn) send(opcode uint32, payload []byte) error {
	buf := new(bytes.Buffer)

	// writes the op code into buffer (uint32 so already 4 bytes)
	binary.Write(buf, binary.LittleEndian, opcode)

	// the next 4 bytes are the size of the body/paylooad
	binary.Write(buf, binary.LittleEndian, uint32(len(payload)))

	// finally we write the rest of the payload
	buf.Write(payload)

	// and write that to the socket obvs
	_, err := dc.conn.Write(buf.Bytes())
	return err
}

// that's where the cool byte level shit ends, now it's just boring rpc shit..
// of which I haven't commented much because it's pretty simple to understand

func (dc *Conn) SetWatching(title, status, titleURL, arturl string, startEpoch, endEpoch int64) error {
	// if the title or status are emtpy just send an empty activity to clear
	if title == "" && status == "" {
		return dc.setActivity(activity{})
	}

	activity := activity{
		Type:       3,
		Details:    title,
		DetailsURL: titleURL,
		State:      status,
		Instance:   true,
		Assets: &assets{
			LargeImage: arturl,
			LargeText:  fmt.Sprintf("jellyrpc %s", dc.version),
		},
	}

	if startEpoch > 0 {
		activity.Timestamps = &timestamps{
			Start: startEpoch,
			End:   endEpoch,
		}
	}

	return dc.setActivity(activity)
}

// simeple func to set a "paused" state
// attempted to try send an empty SET_ACTIVITY but that doesn't
// clear the rpc activity, instead fallsback to something adhoc
func (dc *Conn) SetPaused(title, titleURL, arturl string) error {
	return dc.setActivity(activity{
		Type:       3,
		Details:    title,
		DetailsURL: titleURL,
		State:      "Paused",
		Instance:   true,
		Assets: &assets{
			LargeImage: arturl,
			LargeText:  fmt.Sprintf("jellyrpc %s", dc.version),
		},
	})
}

func (dc *Conn) setActivity(activity activity) error {
	p := payload{
		Cmd:   "SET_ACTIVITY",
		Nonce: "1",
		Args: args{
			PID:      os.Getpid(),
			Activity: activity,
		},
	}
	payloadJSON, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err := dc.send(1, payloadJSON); err != nil {
		return err
	}

	// discord replies to every frame, drain it so the socket buffer doesnt
	// fill up and stall writes over a long running session
	_, _, err = dc.readFrame()
	return err
}

// just close the socket connection without sending an
// empty payload or opcode 2
func (dc *Conn) Close() {
	if dc.conn != nil {
		// close the actual socket connection
		dc.conn.Close()
		dc.conn = nil
	}
}

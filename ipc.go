package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// originally an llm just spewed this out, but I was lowkey pissed I
// didn't understand it so went through along side docs and annotated
// EVERYTHING, hence the fuck ton of comments everywhere
// but hey, I understand it now and it's actually quite interesting, worth reading through

// https://docs.discord.com/developers/topics/rpc#rpc-over-ipc

// header consisting of 4+4 bytes
// 4 for the opcode
// 4 for the legnth of our payload (in bytes)
type Frame struct {
	Opcode uint32
	Length uint32
}

// handshake payload where we send our client id
// V = rpc version, ClientID is self explanatory
type Handshake struct {
	V        string `json:"v"`
	ClientID string `json:"client_id"`
}

// main payload consisting of our cmd, args and a nonce
// https://docs.discord.com/developers/topics/rpc#payloads
type Payload struct {
	Cmd   string `json:"cmd"`
	Args  Args   `json:"args"`
	Nonce string `json:"nonce"`
}

// args struct contains our client pid (us, not discord client)
// and the activity payload inside
type Args struct {
	PID      int      `json:"pid"`
	Activity Activity `json:"activity"`
}

// the rest are pretty self explanatory
// we're just creating the payload json structure with structs

type Activity struct {
	Type       int         `json:"type"`
	Details    string      `json:"details"`
	State      string      `json:"state"`
	Timestamps *Timestamps `json:"timestamps,omitempty"`
	Assets     *Assets     `json:"assets,omitempty"`
	Instance   bool        `json:"instance"`
}

type Assets struct {
	LargeImage string `json:"large_image,omitempty"`
	LargeText  string `json:"large_text,omitempty"`
}

type Timestamps struct {
	Start int64 `json:"start,omitempty"`
	End   int64 `json:"end,omitempty"`
}

// because we have functions on this struct so when used later in main.go
// we can just call <DiscordConn var name>.SetWatching() without having to
// pass in a pointer to the socket connection every time
type DiscordConn struct {
	conn net.Conn
}

// initiates a connection with discords ipc, completes a handshake
// and returns a pointer to a DiscordConn type to be used for calling the other funcs
func NewDiscordConn(clientID string) (*DiscordConn, error) {
	// start with the xdg runtime dir and if not found for whatever reason use /tmp fallback
	// technically there's other fallback var's (TMPDIR, TEMP, TMP) but they were all empty for me
	// seems reasonable to skip straight to /tmp

	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = "/tmp"
	}

	// connect to the unix socket under the dir resolved before
	// 0 for the first client found, didn't see in docs but I assume if multiple then we could use 1,2, etc.
	socketPath := filepath.Join(runtimeDir, "discord-ipc-0")
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("could not connect to discord ipc: %w", err)
	}

	dc := &DiscordConn{conn: conn}

	// marhshal a json object from our handshake payload struct
	// using version 1 of rpc, and the clientID passed into this func
	hs, _ := json.Marshal(Handshake{V: "1", ClientID: clientID})

	// send with opcode 0, send by the client to initiate handshake
	// we should recieve opcode 1 later if this is successful
	if err := dc.send(0, hs); err != nil {
		conn.Close()
		return nil, err
	}

	// create an 8 byte header to read our response into
	header := make([]byte, 8)

	// .. read the response into it
	if _, err := conn.Read(header); err != nil {
		conn.Close()
		return nil, err
	}

	// the first 4 bytes of the header is the opcode response
	// the last 4 bytes are the legnth of the body
	// this lets us determine how large the message is, which is crucial since
	// it's just pure bytes, so there is no message "borders"
	length := binary.LittleEndian.Uint32(header[4:8])

	// create a body with size of X bytes read from header
	body := make([]byte, length)

	// read from the conn into that body
	conn.Read(body)

	// return our dc struct to be used in main.go for calling the other funcs
	return dc, nil
}

// takes an opcode (uint32 so 4 bytes) and a payload and
// sends over the socket, following the discord rpc over ipc spec
func (dc *DiscordConn) send(opcode uint32, payload []byte) error {
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

func (dc *DiscordConn) SetWatching(title, status string, startEpoch, endEpoch int64) error {
	// if the title or status are emtpy just send an empty activity to clear
	if title == "" && status == "" {
		p := Payload{
			Cmd:   "SET_ACTIVITY",
			Nonce: "1",
			Args:  Args{PID: os.Getpid()},
		}
		payloadJSON, _ := json.Marshal(p)
		return dc.send(1, payloadJSON)
	}

	p := Payload{
		Cmd:   "SET_ACTIVITY",
		Nonce: "1",
		Args: Args{
			PID: os.Getpid(),
			Activity: Activity{
				Type:     3, // sets the activity type to watching
				Details:  title,
				State:    status,
				Instance: true,
				Assets: &Assets{
					LargeImage: "jellyfin",
					LargeText:  "Jellyfin",
				},
			},
		},
	}

	if startEpoch > 0 {
		p.Args.Activity.Timestamps = &Timestamps{
			Start: startEpoch,
			End:   endEpoch,
		}
	}

	payloadJSON, err := json.Marshal(p)
	if err != nil {
		return err
	}

	return dc.send(1, payloadJSON)
}

// func to clear + close our rpc activity
// we chain and empty activity to clear the rpc activity from our profiel
// then an opcode 2 payload after, should stop a ghost activity in theory
func (dc *DiscordConn) Close() {
	if dc.conn != nil {
		// send an empty SET_ACTIVITY to clear status
		// since discord doesn't have any "STOP_ACTIVITY" cmd
		// although I just found opcode 2 exists so maybe slap that after(?)
		p := Payload{
			Cmd:   "SET_ACTIVITY",
			Nonce: "1",
			Args:  Args{PID: os.Getpid()},
		}
		payloadJSON, _ := json.Marshal(p)
		// send with opcode 1
		dc.send(1, payloadJSON)

		// after sending emtpy opcode 1 to clear activity
		// send a proper opcode 2 payload with code 1000 (see lin)
		// https://docs.discord.food/topics/rpc#rpc-close-codes
		closePayload := []byte(`{"code":1000,"message":"bye bye"}`)
		dc.send(2, closePayload)

		// close the actual socket connection
		dc.conn.Close()
	}
}

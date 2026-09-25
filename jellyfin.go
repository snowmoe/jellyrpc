package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// /Session endpoint json structure
// https://api.jellyfin.org/#tag/Session/operation/GetSessions
type Session struct {
	UserName       string `json:"UserName"`
	NowPlayingItem `json:"NowPlayingItem"`
	PlayState      struct {
		IsPaused      bool  `json:"IsPaused"`
		PositionTicks int64 `json:"PositionTicks"`
	} `json:"PlayState"`
}

type NowPlayingItem struct {
	Name              string `json:"Name"`
	Id                string `json:"Id"`
	Type              string `json:"Type"`
	RunTimeTicks      int64  `json:"RunTimeTicks"`
	SeriesName        string `json:"SeriesName,omitempty"`
	SeriesId          string `json:"SeriesId,omitempty"`
	ParentIndexNumber int    `json:"ParentIndexNumber,omitempty"`
	IndexNumber       int    `json:"IndexNumber,omitempty"`
	ProviderIds       `json:"ProviderIds"`
}

type ProviderIds struct {
	Imdb string `json:"Imdb"`
	Tmdb string `json:"Tmdb"`
	Tvdb string `json:"Tvdb"`
}

type JellyfinClient struct {
	BaseURL    string
	APIKey     string
	UserName   string
	DeviceID   string
	Hostname   string
	HTTPClient *http.Client
}

type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string {
	switch e.Code {
	case 401:
		return "jellyfin returned unauthorized, check your api key"
	default:
		return fmt.Sprintf("jellyfin returned %s", e.Message)
	}
}

type JSONDecodeError struct {
	Err error
}

func (e *JSONDecodeError) Unwrap() error { return e.Err }
func (e *JSONDecodeError) Error() string {
	if _, ok := errors.AsType[*json.SyntaxError](e.Err); ok {
		return "unexpected response, is this a jellyfin server?"
	}

	return fmt.Sprintf("failed to decode jellyfin response:\n%v", e.Err)
}

func getDeviceID(hostname string) string {
	sum := sha256.Sum256([]byte("jellyrpc:" + hostname))
	return hex.EncodeToString(sum[:16])
}

func NewJellyfinClient(baseURL, apiKey string) *JellyfinClient {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	deviceID := getDeviceID(hostname)

	return &JellyfinClient{
		BaseURL:  baseURL,
		APIKey:   apiKey,
		Hostname: hostname,
		DeviceID: deviceID,
		// timeout so a hung jellyfin connection cant block polling forever
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *JellyfinClient) do(ctx context.Context, method, path string, body any, out any) error {
	var reqBody io.Reader

	// if our body isn't nil then we attempt to marshal it to json
	// then wrap that in a reader for the request
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}

		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reqBody)
	if err != nil {
		return err
	}

	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	req.Header.Set("Authorization", c.authHeader())

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &StatusError{Code: resp.StatusCode, Message: resp.Status}
	}

	// no response body wanted, so we can return nil as was success
	if out == nil {
		return nil
	}

	// attempt to unmarshal the body to the provided out interface
	err = json.NewDecoder(resp.Body).Decode(out)
	if err != nil {
		return &JSONDecodeError{Err: err}
	}

	return nil
}

func (c *JellyfinClient) authHeader() string {
	mediaBrowser := fmt.Sprintf("MediaBrowser Client=%q, Device=%q, DeviceId=%q, Version=%q",
		"jellyrpc",
		c.Hostname,
		c.DeviceID,
		gitVersion,
	)

	if c.APIKey != "" {
		mediaBrowser += fmt.Sprintf(", Token=%q", c.APIKey)
	}

	return mediaBrowser
}

func (c *JellyfinClient) GetActiveSession(ctx context.Context) (*Session, error) {
	var sessions []Session

	err := c.do(ctx, "GET", "/Sessions", nil, &sessions)
	if err != nil {
		return nil, err
	}

	for _, s := range sessions {
		if strings.EqualFold(s.UserName, c.UserName) && s.NowPlayingItem.Name != "" {
			return &s, nil
		}
	}
	return &Session{}, nil
}

func isSessionActive(sess *Session) bool {
	if sess == nil {
		return false
	}

	if sess.NowPlayingItem.Name == "" || sess.NowPlayingItem.Id == "" {
		return false
	}

	return true
}

// determines if the jellyfin instance url provided is local
// checks if localhost or a .local domain
// checks if ip (if parseable) is rfc1918 or loopback
// still kept 127 and ::1 in the host check anyway but can possibly be removed
func IsLocalInstance(hostURL string) bool {
	u, err := url.Parse(hostURL)
	if err != nil {
		return true
	}

	host := u.Hostname()

	if host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".local") {
		return true
	}

	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsPrivate() || ip.IsLoopback()
	}

	return false
}

// cleans url's AND guesses protocol if it's missing (which isn't an issue if U READ DA README UGH)
func SanitiseURL(rawURL string) string {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return ""
	}

	// should catch if a user didn't READ THE README(!!!!) and missed the protocol
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		// then so the local instance func doesn't err from url.Parse with a missing protocol
		// just append http:// temporarily so that can parse n do it's thang
		tempURL := "http://" + u

		// then if it's local we just guess that it'll be http://
		if IsLocalInstance(tempURL) {
			u = "http://" + u
		} else {
			// if not local (ie almost 100% likely a domain being used) then we guess it'll be https://
			u = "https://" + u
		}
	}

	// if we fail to parse then just give the raw url back and pray
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}

	hostURL := parsed.Scheme + "://" + parsed.Host

	// prolly not needed but fuckit we schizo
	hostURL = strings.TrimSuffix(hostURL, "/")

	return hostURL
}

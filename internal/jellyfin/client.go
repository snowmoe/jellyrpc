package jellyfin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type Client struct {
	BaseURL    string
	APIKey     string
	UserName   string
	hostname   string
	deviceID   string
	version    string
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

func NewClient(baseURL, apiKey, version string) *Client {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	deviceID := getDeviceID(hostname)

	return &Client{
		BaseURL:  baseURL,
		APIKey:   apiKey,
		hostname: hostname,
		deviceID: deviceID,
		version:  version,
		// timeout so a hung jellyfin connection cant block polling forever
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
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

func (c *Client) authHeader() string {
	mediaBrowser := fmt.Sprintf("MediaBrowser Client=%q, Device=%q, DeviceId=%q, Version=%q",
		"jellyrpc",
		c.hostname,
		c.deviceID,
		c.version,
	)

	if c.APIKey != "" {
		mediaBrowser += fmt.Sprintf(", Token=%q", c.APIKey)
	}

	return mediaBrowser
}

func (c *Client) GetActiveSession(ctx context.Context) (*Session, error) {
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

// I could handle 503 and use returned Retry-After + Message to log and delay next poll
// but that's bullshit and I'll think about it another day.
// returns Server Name, ID, and an error, sue me
func (c *Client) GetPublicSystemInfo(ctx context.Context) (string, string, error) {
	var info infoResponse

	err := c.do(ctx, "GET", "/System/Info/Public", nil, &info)
	if err != nil {
		return "", "", err
	}

	return info.ServerName, info.ID, nil
}

func (c *Client) GetQuickConnectEnabled(ctx context.Context) (bool, error) {
	var ok bool

	err := c.do(ctx, "GET", "/QuickConnect/Enabled", nil, &ok)
	if err != nil {
		return false, err
	}

	return ok, nil
}

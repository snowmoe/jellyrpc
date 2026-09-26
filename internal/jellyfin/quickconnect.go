package jellyfin

import (
	"context"
	"errors"
	"net/url"
)

func (c *Client) QuickConnectEnabled(ctx context.Context) (bool, error) {
	var ok bool

	err := c.do(ctx, "GET", "/QuickConnect/Enabled", nil, &ok)
	if err != nil {
		return false, err
	}

	return ok, nil
}

func (c *Client) InitiateQC(ctx context.Context) (QuickConnect, error) {
	var qc QuickConnect

	err := c.do(ctx, "POST", "/QuickConnect/Initiate", nil, &qc)
	if err != nil {
		return QuickConnect{}, err
	}

	return qc, nil
}

// checks the auth status of a quick connect request
func (c *Client) ConnectQC(ctx context.Context, secret string) (bool, error) {
	if secret == "" {
		return false, errors.New("missing QuickConnect secret")
	}

	var qcResp QuickConnect

	q := url.Values{}
	q.Set("secret", secret)
	query := q.Encode()

	path := "/QuickConnect/Connect?" + query

	err := c.do(ctx, "GET", path, nil, &qcResp)
	if err != nil {
		return false, err
	}

	return qcResp.Authenticated, nil
}

func (c *Client) AuthenticateQC(ctx context.Context, secret string) (Authorization, error) {
	if secret == "" {
		return Authorization{}, errors.New("missing QuickConnect secret")
	}

	var auth Authorization

	body := struct {
		Secret string
	}{
		Secret: secret,
	}

	err := c.do(ctx, "POST", "/Users/AuthenticateWithQuickConnect", body, &auth)
	if err != nil {
		return Authorization{}, err
	}

	return auth, nil
}

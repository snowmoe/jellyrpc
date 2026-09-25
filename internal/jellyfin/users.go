package jellyfin

import "context"

// CurrentUser returns the token's user.
// api keys aren't tied to a user so jellfin returns 400
func (c *Client) CurrentUser(ctx context.Context) (User, error) {
	var user User

	err := c.do(ctx, "GET", "/Users/Me", nil, &user)
	if err != nil {
		return User{}, err
	}

	return user, nil
}

// returns a slice of User types, will fail if token has no admin perms
func (c *Client) Users(ctx context.Context) ([]User, error) {
	var users []User

	err := c.do(ctx, "GET", "/Users", nil, &users)
	if err != nil {
		return nil, err
	}

	return users, nil
}

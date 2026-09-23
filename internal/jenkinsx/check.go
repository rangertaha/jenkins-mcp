// SPDX-License-Identifier: GPL-3.0-or-later

package jenkinsx

import "context"

// Identity describes the calling Jenkins principal, as reported by the
// built-in /whoAmI endpoint.
type Identity struct {
	Authenticated bool     `json:"authenticated" jsonschema:"whether the request was authenticated"`
	Anonymous     bool     `json:"anonymous" jsonschema:"whether Jenkins treated the caller as anonymous"`
	Name          string   `json:"name" jsonschema:"resolved principal name"`
	Authorities   []string `json:"authorities" jsonschema:"granted authorities/roles, if Jenkins reports any"`
}

// Check verifies credentials by calling Jenkins' /whoAmI endpoint, returning
// the resolved principal. Unlike /me/api/json, /whoAmI/api/json responds
// even for an invalid token (as an anonymous, unauthenticated principal)
// rather than a bare 401, giving Check a clean, structured way to report a
// bad credential instead of only an HTTP status.
func Check(ctx context.Context, c *Client) (*Identity, error) {
	var id Identity
	if err := c.Get(ctx, "/whoAmI/api/json", nil, &id); err != nil {
		return nil, err
	}
	return &id, nil
}

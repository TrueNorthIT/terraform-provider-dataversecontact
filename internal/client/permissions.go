package client

import "context"

// EnsureAuth0 syncs permissions to Auth0 for a scope.
func (c *Client) EnsureAuth0(ctx context.Context, scope string) (*EnsureAuth0Response, error) {
	url := c.adminURL(scope, "table-manager/ensure-auth0")
	var resp EnsureAuth0Response
	if err := c.doJSON(ctx, "POST", url, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetScopes returns all available scopes.
func (c *Client) GetScopes(ctx context.Context) (*ScopesResponse, error) {
	// Scopes endpoint uses "default" scope in the URL but returns all scopes
	url := c.adminURL("default", "table-manager/scopes")
	var resp ScopesResponse
	if err := c.doJSON(ctx, "GET", url, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

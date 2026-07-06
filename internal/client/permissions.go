package client

import (
	"context"
	"fmt"
)

// PublishDefaults publishes a scope's baseline permissions (its defaults.json)
// via PUT /api/v2/_admin/{scope}/table-manager/defaults.
//
// permissions maps each route name to the list of baseline permission tokens
// granted to every authenticated contact for that route (e.g.
// {"case": ["team", "write", "create"]}). allowSelfRegister toggles
// self-registration for citizen-facing scopes. companyModel is optional —
// nil publishes the classic parent-account (multi-contact) model.
// selfRegisterAutoLink is optional — non-nil enables domain-based company
// auto-linking for newly provisioned contacts.
func (c *Client) PublishDefaults(ctx context.Context, scope string, permissions map[string][]string, allowSelfRegister bool, companyModel *CompanyModel, selfRegisterAutoLink *SelfRegisterAutoLink) (*PublishDefaultsResponse, error) {
	url := c.adminURL(scope, "table-manager/defaults")
	if permissions == nil {
		permissions = map[string][]string{}
	}
	body := PublishDefaultsRequest{
		Permissions:          permissions,
		AllowSelfRegister:    allowSelfRegister,
		CompanyModel:         companyModel,
		SelfRegisterAutoLink: selfRegisterAutoLink,
	}
	var resp PublishDefaultsResponse
	if err := c.doJSON(ctx, "PUT", url, body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetScopes returns all available scopes.
func (c *Client) GetScopes(ctx context.Context) (*ScopesResponse, error) {
	// The scopes listing is exposed at /api/v2/_admin/scopes (no scope segment).
	url := fmt.Sprintf("%s/api/v2/_admin/scopes", c.BaseURL)
	var resp ScopesResponse
	if err := c.doJSON(ctx, "GET", url, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

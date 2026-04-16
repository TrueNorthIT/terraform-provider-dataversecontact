package client

import (
	"context"
	"encoding/json"
	"fmt"
)

// GetTableDefinitions returns all table definitions for a scope.
func (c *Client) GetTableDefinitions(ctx context.Context, scope string) (*TableDefinitionsResponse, error) {
	url := c.adminURL(scope, "table-definitions")
	var resp TableDefinitionsResponse
	if err := c.doJSON(ctx, "GET", url, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetTable reads a single table's draft or published schema.
func (c *Client) GetTable(ctx context.Context, scope, routeName string) (*TableManagerResponse, error) {
	url := c.adminURL(scope, fmt.Sprintf("table-manager/%s", routeName))
	var resp TableManagerResponse
	if err := c.doJSON(ctx, "GET", url, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SaveTableDraft saves a table schema as a draft.
func (c *Client) SaveTableDraft(ctx context.Context, scope, routeName string, schemaJSON json.RawMessage) (*SaveDraftResponse, error) {
	url := c.adminURL(scope, fmt.Sprintf("table-manager/%s", routeName))
	var resp SaveDraftResponse
	if err := c.doJSON(ctx, "PUT", url, schemaJSON, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DiscardTableDraft discards a table's draft.
func (c *Client) DiscardTableDraft(ctx context.Context, scope, routeName string) error {
	url := c.adminURL(scope, fmt.Sprintf("table-manager/%s", routeName))
	_, _, err := c.doRequest(ctx, "DELETE", url, nil)
	return err
}

// PublishTables publishes one or more table drafts.
func (c *Client) PublishTables(ctx context.Context, scope string, tables []string) (*PublishResponse, error) {
	url := c.adminURL(scope, "table-manager/publish")
	body := PublishRequest{Tables: tables}
	var resp PublishResponse
	if err := c.doJSON(ctx, "POST", url, body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UnpublishTables unpublishes one or more tables.
func (c *Client) UnpublishTables(ctx context.Context, scope string, tables []string) (*UnpublishResponse, error) {
	url := c.adminURL(scope, "table-manager/unpublish")
	body := UnpublishRequest{Tables: tables}
	var resp UnpublishResponse
	if err := c.doJSON(ctx, "POST", url, body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RemoveTables removes tables to the recycle bin.
func (c *Client) RemoveTables(ctx context.Context, scope string, tables []string) (*RemoveResponse, error) {
	url := c.adminURL(scope, "table-manager/remove")
	body := RemoveRequest{Tables: tables}
	var resp RemoveResponse
	if err := c.doJSON(ctx, "POST", url, body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// PermanentlyDeleteTable permanently deletes a table from the recycle bin.
func (c *Client) PermanentlyDeleteTable(ctx context.Context, scope, routeName string) error {
	url := c.adminURL(scope, fmt.Sprintf("table-manager/recycled/%s", routeName))
	_, _, err := c.doRequest(ctx, "DELETE", url, nil)
	return err
}

// SaveAndPublishTable atomically saves a draft and publishes a table.
// This is a convenience method that performs two API calls.
func (c *Client) SaveAndPublishTable(ctx context.Context, scope, routeName string, schemaJSON json.RawMessage) (*PublishResponse, error) {
	// Step 1: Save draft
	_, err := c.SaveTableDraft(ctx, scope, routeName, schemaJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to save draft: %w", err)
	}

	// Step 2: Publish
	publishResp, err := c.PublishTables(ctx, scope, []string{routeName})
	if err != nil {
		// Try to discard the draft on publish failure
		_ = c.DiscardTableDraft(ctx, scope, routeName)
		return nil, fmt.Errorf("failed to publish (draft discarded): %w", err)
	}

	return publishResp, nil
}

// DeleteTable performs the full delete lifecycle: unpublish → remove → permanent delete.
func (c *Client) DeleteTable(ctx context.Context, scope, routeName string) error {
	// Step 1: Unpublish (ignore errors — may already be unpublished)
	_, _ = c.UnpublishTables(ctx, scope, []string{routeName})

	// Step 2: Remove to recycle bin (ignore errors — may already be removed)
	_, _ = c.RemoveTables(ctx, scope, []string{routeName})

	// Step 3: Permanently delete
	if err := c.PermanentlyDeleteTable(ctx, scope, routeName); err != nil {
		return fmt.Errorf("failed to permanently delete table %q: %w", routeName, err)
	}

	return nil
}

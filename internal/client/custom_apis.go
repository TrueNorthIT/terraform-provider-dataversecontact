package client

import (
	"context"
	"encoding/json"
	"fmt"
)

// GetCustomApiDefinitions returns all custom API definitions for a scope.
func (c *Client) GetCustomApiDefinitions(ctx context.Context, scope string) (*CustomApiDefinitionsResponse, error) {
	url := c.adminURL(scope, "custom-api-definitions")
	var resp CustomApiDefinitionsResponse
	if err := c.doJSON(ctx, "GET", url, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetCustomApi reads a single custom API's draft or published schema.
func (c *Client) GetCustomApi(ctx context.Context, scope, routeName string) (*CustomApiManagerResponse, error) {
	url := c.adminURL(scope, fmt.Sprintf("custom-api-manager/%s", routeName))
	var resp CustomApiManagerResponse
	if err := c.doJSON(ctx, "GET", url, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SaveCustomApiDraft saves a custom API schema as a draft.
func (c *Client) SaveCustomApiDraft(ctx context.Context, scope, routeName string, schemaJSON json.RawMessage) (*CustomApiSaveDraftResponse, error) {
	url := c.adminURL(scope, fmt.Sprintf("custom-api-manager/%s", routeName))
	var resp CustomApiSaveDraftResponse
	if err := c.doJSON(ctx, "PUT", url, schemaJSON, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DiscardCustomApiDraft discards a custom API's draft.
func (c *Client) DiscardCustomApiDraft(ctx context.Context, scope, routeName string) error {
	url := c.adminURL(scope, fmt.Sprintf("custom-api-manager/%s", routeName))
	_, _, err := c.doRequest(ctx, "DELETE", url, nil)
	return err
}

// PublishCustomApis publishes one or more custom API drafts.
func (c *Client) PublishCustomApis(ctx context.Context, scope string, apis []string) (*CustomApiPublishResponse, error) {
	url := c.adminURL(scope, "custom-api-manager/publish")
	body := CustomApiPublishRequest{Apis: apis}
	var resp CustomApiPublishResponse
	if err := c.doJSON(ctx, "POST", url, body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RemoveCustomApis removes custom APIs to the recycle bin.
func (c *Client) RemoveCustomApis(ctx context.Context, scope string, apis []string) (*CustomApiRemoveResponse, error) {
	url := c.adminURL(scope, "custom-api-manager/remove")
	body := CustomApiRemoveRequest{Apis: apis}
	var resp CustomApiRemoveResponse
	if err := c.doJSON(ctx, "POST", url, body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// PermanentlyDeleteCustomApi permanently deletes a custom API from the recycle bin.
func (c *Client) PermanentlyDeleteCustomApi(ctx context.Context, scope, routeName string) error {
	url := c.adminURL(scope, fmt.Sprintf("custom-api-manager/recycled/%s", routeName))
	_, _, err := c.doRequest(ctx, "DELETE", url, nil)
	return err
}

// SaveAndPublishCustomApi atomically saves a draft and publishes a custom API.
func (c *Client) SaveAndPublishCustomApi(ctx context.Context, scope, routeName string, schemaJSON json.RawMessage) (*CustomApiPublishResponse, error) {
	// Step 1: Save draft
	_, err := c.SaveCustomApiDraft(ctx, scope, routeName, schemaJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to save custom API draft: %w", err)
	}

	// Step 2: Publish
	publishResp, err := c.PublishCustomApis(ctx, scope, []string{routeName})
	if err != nil {
		_ = c.DiscardCustomApiDraft(ctx, scope, routeName)
		return nil, fmt.Errorf("failed to publish custom API (draft discarded): %w", err)
	}

	return publishResp, nil
}

// DeleteCustomApi performs the full delete lifecycle: remove → permanent delete.
func (c *Client) DeleteCustomApi(ctx context.Context, scope, routeName string) error {
	// Step 1: Remove to recycle bin
	_, _ = c.RemoveCustomApis(ctx, scope, []string{routeName})

	// Step 2: Permanently delete
	if err := c.PermanentlyDeleteCustomApi(ctx, scope, routeName); err != nil {
		return fmt.Errorf("failed to permanently delete custom API %q: %w", routeName, err)
	}

	return nil
}

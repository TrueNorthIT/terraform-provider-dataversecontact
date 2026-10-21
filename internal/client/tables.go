package client

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
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

// SaveAndPublishTable saves a draft and publishes it, in two API calls.
//
// Both calls can answer 200 without doing what was asked. The save drops
// fields Dataverse doesn't have (see StrippedFieldsError), and the publish
// reports a route it couldn't publish in its errors list. Either way the
// draft is discarded and an error returned, so what is published is left as
// it was rather than replaced by something other than the config.
//
// The save response is returned even with an error, for the warnings it
// carries.
func (c *Client) SaveAndPublishTable(ctx context.Context, scope, routeName string, schemaJSON json.RawMessage) (*SaveDraftResponse, error) {
	save, err := c.SaveTableDraft(ctx, scope, routeName, schemaJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to save draft: %w", err)
	}
	if stripped := strippedFields(routeName, save.Validation); stripped != nil {
		_ = c.DiscardTableDraft(ctx, scope, routeName)
		return save, stripped
	}

	publishResp, err := c.PublishTables(ctx, scope, []string{routeName})
	if err != nil {
		_ = c.DiscardTableDraft(ctx, scope, routeName)
		return save, fmt.Errorf("failed to publish (draft discarded): %w", err)
	}
	if err := routeNotPublished(routeName, publishResp.Published, publishResp.Errors); err != nil {
		_ = c.DiscardTableDraft(ctx, scope, routeName)
		return save, err
	}

	return save, nil
}

// StrippedFieldsError reports fields the API dropped from a saved draft
// because the Dataverse table has no such column. The API still answers the
// save with a 200, so publishing would replace the table with one missing
// those fields.
type StrippedFieldsError struct {
	Route  string
	Fields []ValidationIssue // FIELD_NOT_FOUND errors on "fields.<name>"
}

func (e *StrippedFieldsError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "the API dropped %d field(s) from %q that its Dataverse table doesn't have, so nothing was published (draft discarded):", len(e.Fields), e.Route)
	for _, f := range e.Fields {
		fmt.Fprintf(&b, "\n  - %s: %s", e.FieldName(f), f.Message)
	}
	return b.String()
}

// FieldName is the field an issue is about, without its "fields." prefix.
func (e *StrippedFieldsError) FieldName(issue ValidationIssue) string {
	return strings.TrimPrefix(issue.Field, "fields.")
}

// IsStrippedField reports whether the API acts on a validation error by
// deleting the field from the saved schema: FIELD_NOT_FOUND on a
// "fields.<name>" path, exactly as the API's draft PUT selects them. Every
// other validation error leaves the schema as sent.
func IsStrippedField(issue ValidationIssue) bool {
	return issue.Code == "FIELD_NOT_FOUND" && strings.HasPrefix(issue.Field, "fields.")
}

func strippedFields(routeName string, v *ValidationInfo) error {
	if v == nil {
		return nil
	}
	var stripped []ValidationIssue
	for _, issue := range v.Errors {
		if IsStrippedField(issue) {
			stripped = append(stripped, issue)
		}
	}
	if len(stripped) == 0 {
		return nil
	}
	return &StrippedFieldsError{Route: routeName, Fields: stripped}
}

// routeNotPublished turns a publish that answered 200 but didn't publish
// route into an error, using the API's own reason when it gave one.
func routeNotPublished(route string, published []string, errs []RouteError) error {
	for _, e := range errs {
		if e.RouteName == route {
			return fmt.Errorf("the API saved the draft but could not publish %q: %s (draft discarded, so what is published is unchanged)", route, e.Error)
		}
	}
	if !slices.Contains(published, route) {
		return fmt.Errorf("the API saved the draft but did not report %q as published (draft discarded, so what is published is unchanged)", route)
	}
	return nil
}

// DeleteTable performs the full delete lifecycle: unpublish → remove → permanent delete.
//
// Deleting an ALREADY-DELETED table succeeds. `remove` only finds a table that
// has a published blob or a draft, so a table that is already gone never
// reaches the recycle bin and step 3 then answers 404 — the delete had nothing
// left to do, which is not a failure. Erroring there left Terraform holding
// state for a table the API no longer had, and no retry could clear it: every
// subsequent destroy took the same path and failed the same way, so the only
// way out was `terraform state rm`.
//
// A 404 is only accepted when `remove` also reported nothing removed. If
// remove DID bin the table and the purge then 404s, something raced us and the
// error is real.
func (c *Client) DeleteTable(ctx context.Context, scope, routeName string) error {
	// Step 1: Unpublish (ignore errors — may already be unpublished)
	_, _ = c.UnpublishTables(ctx, scope, []string{routeName})

	// Step 2: Remove to recycle bin (ignore errors — may already be removed)
	removeResp, _ := c.RemoveTables(ctx, scope, []string{routeName})
	binned := removeResp != nil && len(removeResp.Removed) > 0

	// Step 3: Permanently delete
	if err := c.PermanentlyDeleteTable(ctx, scope, routeName); err != nil {
		if IsNotFound(err) && !binned {
			return nil
		}
		return fmt.Errorf("failed to permanently delete table %q: %w", routeName, err)
	}

	return nil
}

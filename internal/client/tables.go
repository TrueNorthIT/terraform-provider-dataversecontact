package client

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
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

// SaveAndPublishTable saves a draft and publishes it.
//
// Both calls can answer 200 without doing what was asked. The save drops
// fields Dataverse doesn't have (see StrippedFieldsError), and the publish
// reports a route it couldn't publish in its errors list. Either way the
// draft is discarded and an error returned. A strip is caught before
// anything is published; a publish error may still have published.
//
// The save response is returned even with an error, for the warnings it
// carries.
func (c *Client) SaveAndPublishTable(ctx context.Context, scope, routeName string, schemaJSON json.RawMessage) (*SaveDraftResponse, error) {
	save, err := c.SaveTableDraft(ctx, scope, routeName, schemaJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to save draft: %w", err)
	}
	discard := func() string { return discardOutcome(routeName, c.DiscardTableDraft(ctx, scope, routeName)) }

	stripped, err := c.findStrippedFields(ctx, scope, routeName, schemaJSON, save.Validation)
	if err != nil {
		return save, fmt.Errorf("could not read back the saved draft of %q to check it, so nothing was published (%s): %w", routeName, discard(), err)
	}
	if stripped != nil {
		stripped.Draft = discard()
		return save, stripped
	}

	publishResp, err := c.PublishTables(ctx, scope, []string{routeName})
	if err != nil {
		return save, fmt.Errorf("failed to publish (%s): %w", discard(), err)
	}
	if err := routeNotPublished(routeName, publishResp.Published, publishResp.Errors); err != nil {
		return save, fmt.Errorf("%w (%s)", err, discard())
	}

	return save, nil
}

// discardOutcome says what became of a draft after a refused save. A draft
// left behind goes live with the next Table Manager "Publish all", so the
// message says so rather than claim it was discarded.
func discardOutcome(route string, err error) string {
	switch {
	case err == nil:
		return "draft discarded"
	case IsNotFound(err):
		return "no draft left"
	default:
		return fmt.Sprintf("the draft of %q could not be discarded and is still saved, so a Table Manager \"Publish all\" would publish it: %v", route, err)
	}
}

// StrippedField is a field the API dropped from a saved draft.
type StrippedField struct {
	Name    string
	Message string
}

// StrippedFieldsError reports fields the API dropped from a saved draft. It
// drops a field whose column the Dataverse table doesn't have, still answers
// the save with a 200, and publishing would replace the table with one
// missing those fields.
type StrippedFieldsError struct {
	Route  string
	Fields []StrippedField
	// Draft says what became of the draft: discarded, or left behind.
	Draft string
}

func (e *StrippedFieldsError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "the API dropped %d field(s) from %q, so nothing was published (%s):", len(e.Fields), e.Route, e.Draft)
	for _, f := range e.Fields {
		fmt.Fprintf(&b, "\n  - %s: %s", f.Name, f.Message)
	}
	return b.String()
}

// IsStrippedField reports whether a validation error is one the API acts on
// by deleting the field: FIELD_NOT_FOUND on a "fields.<name>" path, as the
// API's draft PUT selects them.
func IsStrippedField(issue ValidationIssue) bool {
	return issue.Code == "FIELD_NOT_FOUND" && strings.HasPrefix(issue.Field, "fields.")
}

// findStrippedFields reads back the draft just saved and lists the fields
// sent that aren't in it. The save response can't be relied on for this: the
// API validates again after stripping and answers with that result, which no
// longer mentions the stripped fields. The read is cheap, because the table
// GET answers from the draft before loading anything else.
//
// The API's FIELD_NOT_FOUND message is used when the response does carry one
// (it keeps the first result when the second validation fails).
func (c *Client) findStrippedFields(ctx context.Context, scope, routeName string, sent json.RawMessage, v *ValidationInfo) (*StrippedFieldsError, error) {
	var want struct {
		LogicalName string                     `json:"dataverseLogicalName"`
		Fields      map[string]json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(sent, &want); err != nil {
		return nil, fmt.Errorf("reading the schema sent: %w", err)
	}
	if len(want.Fields) == 0 {
		return nil, nil
	}

	got, err := c.GetTable(ctx, scope, routeName)
	if err != nil {
		return nil, err
	}
	if got.Source != "draft" {
		return nil, fmt.Errorf("the API answered with the %s table instead of the draft, which something else published or discarded meanwhile", got.Source)
	}
	var saved struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(got.Schema, &saved); err != nil {
		return nil, fmt.Errorf("reading the saved draft: %w", err)
	}

	messages := map[string]string{}
	if v != nil {
		for _, issue := range v.Errors {
			if IsStrippedField(issue) {
				messages[strings.TrimPrefix(issue.Field, "fields.")] = issue.Message
			}
		}
	}
	table := want.LogicalName
	if table == "" {
		table = "its Dataverse table"
	}
	var stripped []StrippedField
	for _, name := range slices.Sorted(maps.Keys(want.Fields)) {
		if _, kept := saved.Fields[name]; kept {
			continue
		}
		msg := messages[name]
		if msg == "" {
			msg = fmt.Sprintf("The API dropped %q from the saved draft, which it does when %s has no such column", name, table)
		}
		stripped = append(stripped, StrippedField{Name: name, Message: msg})
	}
	if len(stripped) == 0 {
		return nil, nil
	}
	return &StrippedFieldsError{Route: routeName, Fields: stripped}, nil
}

// routeNotPublished turns a publish that answered 200 but didn't publish
// route into an error, using the API's own reason when it gave one. The API
// can fail after writing the published table (deleting the draft), so the
// message doesn't promise nothing changed.
func routeNotPublished(route string, published []string, errs []RouteError) error {
	for _, e := range errs {
		if e.RouteName == route {
			return fmt.Errorf("the API saved the draft but reported an error publishing %q, so it may not have been published: %s", route, e.Error)
		}
	}
	if !slices.Contains(published, route) {
		return fmt.Errorf("the API saved the draft but did not report %q as published, so it may not have been", route)
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
// error is real. And `remove` must have answered: it reports a table it can't
// find as a per-route error in a 200, so a failed request (a restarting API)
// says nothing about whether the table is gone, and the delete stops there.
func (c *Client) DeleteTable(ctx context.Context, scope, routeName string) error {
	// Step 1: Unpublish (ignore errors — may already be unpublished)
	_, _ = c.UnpublishTables(ctx, scope, []string{routeName})

	// Step 2: Remove to recycle bin
	removeResp, err := c.RemoveTables(ctx, scope, []string{routeName})
	if err != nil && !IsNotFound(err) {
		return fmt.Errorf("failed to remove table %q: %w", routeName, err)
	}
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

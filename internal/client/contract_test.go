package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures are real API responses; see internal/testdata/contract/README.md.
// Each must decode into the type the client reads it with. A field the
// client types differently from the API fails here, before it fails an apply.

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", "contract", name+".json"))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

func decodeFixture(t *testing.T, name string, target any) {
	t.Helper()
	if err := json.Unmarshal(readFixture(t, name), target); err != nil {
		t.Fatalf("%s does not decode into %T: %v", name, target, err)
	}
}

func TestContractTableResponses(t *testing.T) {
	var get TableManagerResponse
	decodeFixture(t, "get-table-published", &get)
	if get.Source == "" || len(get.Schema) == 0 || get.Draft != nil {
		t.Errorf("get-table-published: source=%q draft=%v schema=%d bytes", get.Source, get.Draft, len(get.Schema))
	}

	var defs TableDefinitionsResponse
	decodeFixture(t, "get-table-definitions", &defs)
	if len(defs.Definitions) == 0 || defs.Definitions[0].RouteName == "" || defs.Definitions[0].FieldCount == 0 {
		t.Errorf("get-table-definitions: %+v", defs.Definitions)
	}

	var save SaveDraftResponse
	decodeFixture(t, "put-table-draft", &save)
	if save.Draft == nil || save.Draft.RouteName == "" || save.Validation == nil ||
		save.Validation.Errors[0].Code == "" || save.Validation.Warnings[0].Code == "" {
		t.Errorf("put-table-draft: %+v", save)
	}

	var pub PublishResponse
	decodeFixture(t, "post-table-publish", &pub)
	if len(pub.Published) == 0 || pub.Errors[0].RouteName == "" || pub.Errors[0].Error == "" {
		t.Errorf("post-table-publish: %+v", pub)
	}
	for route, v := range pub.Validation {
		if v == nil || len(v.Warnings) == 0 || v.Warnings[0].Code == "" {
			t.Errorf("post-table-publish validation[%s]: %+v", route, v)
		}
	}

	var unpub UnpublishResponse
	decodeFixture(t, "post-table-unpublish", &unpub)
	if len(unpub.Unpublished) == 0 || unpub.Errors[0].RouteName == "" {
		t.Errorf("post-table-unpublish: %+v", unpub)
	}

	var rm RemoveResponse
	decodeFixture(t, "post-table-remove", &rm)
	if len(rm.Removed) == 0 || rm.Errors[0].RouteName == "" {
		t.Errorf("post-table-remove: %+v", rm)
	}

	var del DeleteRecycledResponse
	decodeFixture(t, "delete-table-recycled", &del)
	if del.RouteName == "" {
		t.Errorf("delete-table-recycled: %+v", del)
	}
}

func TestContractCustomApiResponses(t *testing.T) {
	var get CustomApiManagerResponse
	decodeFixture(t, "get-custom-api-published", &get)
	if get.Source == "" || len(get.Schema) == 0 {
		t.Errorf("get-custom-api-published: %+v", get)
	}

	// The recorded definition carries "description": null.
	var defs CustomApiDefinitionsResponse
	decodeFixture(t, "get-custom-api-definitions", &defs)
	if len(defs.Definitions) == 0 || defs.Definitions[0].DataverseUniqueName == "" {
		t.Errorf("get-custom-api-definitions: %+v", defs)
	}

	var save CustomApiSaveDraftResponse
	decodeFixture(t, "put-custom-api-draft", &save)
	if save.Draft == nil || save.Draft.RouteName == "" {
		t.Errorf("put-custom-api-draft: %+v", save)
	}

	var pub CustomApiPublishResponse
	decodeFixture(t, "post-custom-api-publish", &pub)
	if len(pub.Published) == 0 || pub.Errors[0].RouteName == "" {
		t.Errorf("post-custom-api-publish: %+v", pub)
	}

	var rm CustomApiRemoveResponse
	decodeFixture(t, "post-custom-api-remove", &rm)
	if len(rm.Removed) == 0 || rm.Errors[0].RouteName == "" {
		t.Errorf("post-custom-api-remove: %+v", rm)
	}
}

func TestContractScopeResponses(t *testing.T) {
	var scopes ScopesResponse
	decodeFixture(t, "get-scopes", &scopes)
	if len(scopes.Scopes) == 0 {
		t.Errorf("get-scopes: %+v", scopes)
	}

	var defaults PublishDefaultsResponse
	decodeFixture(t, "put-defaults", &defaults)
	if defaults.Message == "" {
		t.Errorf("put-defaults: %+v", defaults)
	}
}

// A recorded 404 must be recognised as not-found — the table and custom API
// resources drop themselves from state on it — and surface the API's message.
func TestContractNotFound(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		get     func(*Client) error
	}{
		{"get-table-404", func(c *Client) error {
			_, err := c.GetTable(context.Background(), "default", "gone")
			return err
		}},
		{"get-custom-api-404", func(c *Client) error {
			_, err := c.GetCustomApi(context.Background(), "default", "gone")
			return err
		}},
	} {
		body := readFixture(t, tc.fixture)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(body)
		}))
		err := tc.get(NewClient(srv.URL, "k"))
		srv.Close()

		if !IsNotFound(err) {
			t.Errorf("%s: IsNotFound(%v) = false", tc.fixture, err)
		}
		var apiErr APIError
		_ = json.Unmarshal(body, &apiErr)
		if err == nil || !strings.Contains(err.Error(), apiErr.Message) {
			t.Errorf("%s: error %v does not carry the API message %q", tc.fixture, err, apiErr.Message)
		}
	}
}

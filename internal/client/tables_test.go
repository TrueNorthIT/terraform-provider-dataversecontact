package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetTableDefinitions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/default/admin/table-definitions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("unexpected method: %s", r.Method)
		}
		json.NewEncoder(w).Encode(TableDefinitionsResponse{
			Definitions: []TableDefinition{
				{
					RouteName:      "case",
					Source:         "published",
					DataverseTable: "incidents",
					FieldCount:     42,
				},
				{
					RouteName:      "contact",
					Source:         "built-in",
					DataverseTable: "contacts",
					FieldCount:     15,
				},
			},
		})
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-key")
	resp, err := c.GetTableDefinitions(context.Background(), "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Definitions) != 2 {
		t.Fatalf("expected 2 definitions, got %d", len(resp.Definitions))
	}
	if resp.Definitions[0].RouteName != "case" {
		t.Errorf("expected first table to be 'case', got %s", resp.Definitions[0].RouteName)
	}
	if resp.Definitions[0].FieldCount != 42 {
		t.Errorf("expected 42 fields, got %d", resp.Definitions[0].FieldCount)
	}
}

func TestGetTable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/default/admin/table-manager/case" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(TableManagerResponse{
			Source: "published",
			Schema: json.RawMessage(`{"routeName":"case","dataverseTable":"incidents","fields":{"id":{"type":"string"}}}`),
		})
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-key")
	resp, err := c.GetTable(context.Background(), "default", "case")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Source != "published" {
		t.Errorf("expected source 'published', got %s", resp.Source)
	}
	if len(resp.Schema) == 0 {
		t.Error("expected non-empty schema")
	}
}

func TestSaveAndPublishTable(t *testing.T) {
	var savedBody json.RawMessage
	callCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		switch {
		case r.Method == "PUT" && r.URL.Path == "/api/v2/default/admin/table-manager/case":
			json.NewDecoder(r.Body).Decode(&savedBody)
			json.NewEncoder(w).Encode(SaveDraftResponse{
				Message: "Draft saved",
				Draft: &DraftInfo{
					RouteName:  "case",
					ChangeType: "new",
				},
			})
		case r.Method == "POST" && r.URL.Path == "/api/v2/default/admin/table-manager/publish":
			json.NewEncoder(w).Encode(PublishResponse{
				Message:   "Published 1 table(s)",
				Published: []string{"case"},
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-key")
	schema := json.RawMessage(`{"routeName":"case","dataverseTable":"incidents"}`)
	resp, err := c.SaveAndPublishTable(context.Background(), "default", "case", schema)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 API calls (save + publish), got %d", callCount)
	}
	if len(resp.Published) != 1 || resp.Published[0] != "case" {
		t.Errorf("expected published=[case], got %v", resp.Published)
	}
}

func TestDeleteTable(t *testing.T) {
	calls := make([]string, 0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v2/default/admin/table-manager/unpublish":
			json.NewEncoder(w).Encode(UnpublishResponse{Unpublished: []string{"case"}})
		case r.Method == "POST" && r.URL.Path == "/api/v2/default/admin/table-manager/remove":
			json.NewEncoder(w).Encode(RemoveResponse{Removed: []string{"case"}})
		case r.Method == "DELETE" && r.URL.Path == "/api/v2/default/admin/table-manager/recycled/case":
			json.NewEncoder(w).Encode(DeleteRecycledResponse{RouteName: "case"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-key")
	err := c.DeleteTable(context.Background(), "default", "case")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 3 {
		t.Errorf("expected 3 API calls (unpublish + remove + delete), got %d: %v", len(calls), calls)
	}
}

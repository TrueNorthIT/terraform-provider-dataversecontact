package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublishDefaults(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody PublishDefaultsRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "published"})
	}))
	defer server.Close()

	c := NewClient(server.URL, "key")
	perms := map[string][]string{"case": {"team", "write", "create"}}
	resp, err := c.PublishDefaults(context.Background(), "default", perms, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Errorf("expected PUT, got %s", gotMethod)
	}
	if gotPath != "/api/v2/_admin/default/table-manager/defaults" {
		t.Errorf("unexpected path: %s", gotPath)
	}
	if !gotBody.AllowSelfRegister {
		t.Errorf("expected allowSelfRegister true")
	}
	if got := gotBody.Permissions["case"]; len(got) != 3 {
		t.Errorf("expected 3 case permissions, got %v", got)
	}
	if resp.Message != "published" {
		t.Errorf("expected message 'published', got %q", resp.Message)
	}
}

func TestPublishDefaultsNilPermissions(t *testing.T) {
	var gotBody map[string]json.RawMessage

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	c := NewClient(server.URL, "key")
	if _, err := c.PublishDefaults(context.Background(), "default", nil, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := gotBody["permissions"]; !ok {
		t.Errorf("expected permissions key to be present even when nil")
	}
}

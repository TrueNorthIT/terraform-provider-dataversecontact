package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClient(t *testing.T) {
	c := NewClient("https://example.com/", "test-key")
	if c.BaseURL != "https://example.com" {
		t.Errorf("expected trailing slash to be trimmed, got %s", c.BaseURL)
	}
	if c.APIKey != "test-key" {
		t.Errorf("expected API key to be set")
	}
}

func TestAdminURL(t *testing.T) {
	c := NewClient("https://example.com", "key")
	url := c.adminURL("default", "table-definitions")
	expected := "https://example.com/api/v2/default/admin/table-definitions"
	if url != expected {
		t.Errorf("expected %s, got %s", expected, url)
	}
}

func TestAdminURLWithScope(t *testing.T) {
	c := NewClient("https://example.com", "key")
	url := c.adminURL("hr", "table-manager/case")
	expected := "https://example.com/api/v2/hr/admin/table-manager/case"
	if url != expected {
		t.Errorf("expected %s, got %s", expected, url)
	}
}

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		err      error
		expected bool
	}{
		{nil, false},
		{fmt.Errorf("API error (404): not found"), true},
		{fmt.Errorf("API error (500): server error"), false},
		{fmt.Errorf("some other error"), false},
	}

	for _, tt := range tests {
		result := IsNotFound(tt.err)
		if result != tt.expected {
			t.Errorf("IsNotFound(%v) = %v, expected %v", tt.err, result, tt.expected)
		}
	}
}

func TestDoRequestSetsAuthHeader(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer server.Close()

	c := NewClient(server.URL, "my-secret-key")
	_, _, err := c.doRequest(context.Background(), "GET", server.URL+"/test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedAuth != "Bearer my-secret-key" {
		t.Errorf("expected Bearer my-secret-key, got %s", receivedAuth)
	}
}

func TestDoRequestSetsContentType(t *testing.T) {
	var receivedContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer server.Close()

	c := NewClient(server.URL, "key")
	_, _, err := c.doRequest(context.Background(), "PUT", server.URL+"/test", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedContentType != "application/json" {
		t.Errorf("expected application/json, got %s", receivedContentType)
	}
}

func TestDoRequestHandlesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(APIError{
			StatusCode: 404,
			Error:      "Not Found",
			Message:    "Table not found",
		})
	}))
	defer server.Close()

	c := NewClient(server.URL, "key")
	_, statusCode, err := c.doRequest(context.Background(), "GET", server.URL+"/test", nil)

	if err == nil {
		t.Fatal("expected error for 404 response")
	}
	if statusCode != 404 {
		t.Errorf("expected status 404, got %d", statusCode)
	}
	if !IsNotFound(err) {
		t.Errorf("expected IsNotFound to return true")
	}
}

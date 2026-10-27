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
	resp, err := c.PublishDefaults(context.Background(), "default", perms, true, nil, nil, "", nil, "")
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
	if gotBody.CompanyModel != nil {
		t.Errorf("expected no companyModel when nil, got %+v", gotBody.CompanyModel)
	}
	if resp.Message != "published" {
		t.Errorf("expected message 'published', got %q", resp.Message)
	}
}

func TestPublishDefaultsCompanyModel(t *testing.T) {
	var gotBody PublishDefaultsRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	c := NewClient(server.URL, "key")
	cm := &CompanyModel{
		Strategy:           "associated-accounts",
		AssociatedAccounts: &AssociatedAccountsQuery{Relationship: "cr_contact_accounts"},
	}
	if _, err := c.PublishDefaults(context.Background(), "default", nil, false, cm, nil, "", nil, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotBody.CompanyModel == nil {
		t.Fatalf("expected companyModel in request body")
	}
	if gotBody.CompanyModel.Strategy != "associated-accounts" {
		t.Errorf("unexpected strategy: %s", gotBody.CompanyModel.Strategy)
	}
	if gotBody.CompanyModel.AssociatedAccounts == nil ||
		gotBody.CompanyModel.AssociatedAccounts.Relationship != "cr_contact_accounts" {
		t.Errorf("unexpected associatedAccounts: %+v", gotBody.CompanyModel.AssociatedAccounts)
	}
}

func TestPublishDefaultsJoin(t *testing.T) {
	var gotBody PublishDefaultsRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	c := NewClient(server.URL, "key")
	join := &JoinConfig{Strategy: "domain-list", DomainField: "new_portaldomains", RequireMatch: true}
	if _, err := c.PublishDefaults(context.Background(), "default", nil, true, nil, join, "", nil, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotBody.Join == nil {
		t.Fatalf("expected join in request body")
	}
	if gotBody.Join.Strategy != "domain-list" {
		t.Errorf("unexpected strategy: %s", gotBody.Join.Strategy)
	}
	if gotBody.Join.DomainField != "new_portaldomains" {
		t.Errorf("unexpected domainField: %s", gotBody.Join.DomainField)
	}
	if !gotBody.Join.RequireMatch {
		t.Errorf("expected requireMatch true")
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
	if _, err := c.PublishDefaults(context.Background(), "default", nil, false, nil, nil, "", nil, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := gotBody["permissions"]; !ok {
		t.Errorf("expected permissions key to be present even when nil")
	}
}

func TestPublishDefaultsContactEmailColumn(t *testing.T) {
	var gotBody PublishDefaultsRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	c := NewClient(server.URL, "key")
	if _, err := c.PublishDefaults(context.Background(), "default", nil, false, nil, nil, "emailaddress2", nil, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotBody.ContactEmailColumn != "emailaddress2" {
		t.Errorf("expected contactEmailColumn emailaddress2, got %q", gotBody.ContactEmailColumn)
	}
}

// Unset, the key is left out, so the API keeps signing people in with emailaddress1.
func TestPublishDefaultsOmitsUnsetContactEmailColumn(t *testing.T) {
	var gotBody map[string]json.RawMessage

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	c := NewClient(server.URL, "key")
	if _, err := c.PublishDefaults(context.Background(), "default", nil, false, nil, nil, "", nil, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := gotBody["contactEmailColumn"]; ok {
		t.Errorf("expected no contactEmailColumn key when unset, got %s", gotBody["contactEmailColumn"])
	}
}

func TestPublishDefaultsContactFilters(t *testing.T) {
	var gotBody PublishDefaultsRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	c := NewClient(server.URL, "key")
	filters := []string{"cpa_portaluser eq true"}
	if _, err := c.PublishDefaults(context.Background(), "default", nil, false, nil, nil, "", filters, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gotBody.ContactFilters) != 1 || gotBody.ContactFilters[0] != "cpa_portaluser eq true" {
		t.Errorf("expected contactFilters [cpa_portaluser eq true], got %v", gotBody.ContactFilters)
	}
}

// Unset, the key is left out, so every active contact with the email still signs in.
func TestPublishDefaultsOmitsUnsetContactFilters(t *testing.T) {
	var gotBody map[string]json.RawMessage

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	c := NewClient(server.URL, "key")
	if _, err := c.PublishDefaults(context.Background(), "default", nil, false, nil, nil, "", nil, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := gotBody["contactFilters"]; ok {
		t.Errorf("expected no contactFilters key when unset, got %s", gotBody["contactFilters"])
	}
}

// invite_url goes as inviteUrl when set and is left out when not, so a scope
// without invites publishes exactly what it did before.
func TestPublishDefaultsInviteURL(t *testing.T) {
	var gotBody map[string]json.RawMessage

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = nil
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	c := NewClient(server.URL, "key")
	const url = "https://portal.example.gov.uk/invite#token={token}"
	if _, err := c.PublishDefaults(context.Background(), "default", nil, false, nil, nil, "", nil, url); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := string(gotBody["inviteUrl"]); got != `"`+url+`"` {
		t.Errorf("inviteUrl = %s, want %q", got, url)
	}

	if _, err := c.PublishDefaults(context.Background(), "default", nil, false, nil, nil, "", nil, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := gotBody["inviteUrl"]; ok {
		t.Errorf("inviteUrl sent when unset: %s", gotBody["inviteUrl"])
	}
}

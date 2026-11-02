package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	tenantB = "22222222-2222-2222-2222-222222222222"
)

// fakeCredential hands out "<tenant>|<scope>|<n>" tokens and counts them.
type fakeCredential struct {
	tenant  string
	mu      sync.Mutex
	calls   int
	expires time.Duration
	err     error
	delay   time.Duration
}

func (f *fakeCredential) GetToken(_ context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return azcore.AccessToken{}, f.err
	}
	f.calls++
	expires := f.expires
	if expires == 0 {
		expires = time.Hour
	}
	return azcore.AccessToken{
		Token:     strings.Join([]string{f.tenant, opts.Scopes[0], string(rune('0' + f.calls))}, "|"),
		ExpiresOn: time.Now().Add(expires),
	}, nil
}

// fakeEntra makes credentials per tenant and remembers them.
type fakeEntra struct {
	mu    sync.Mutex
	creds map[string]*fakeCredential
	tune  func(*fakeCredential)
}

func (f *fakeEntra) auth() *EntraAuth {
	return &EntraAuth{
		Method: "fake sign-in",
		Credential: func(tenant string) (azcore.TokenCredential, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.creds == nil {
				f.creds = map[string]*fakeCredential{}
			}
			c := &fakeCredential{tenant: tenant}
			if f.tune != nil {
				f.tune(c)
			}
			f.creds[tenant] = c
			return c, nil
		},
	}
}

// entraServer answers the scopes listing with scopeAuth and records the
// Authorization header of every admin request by path.
type entraServer struct {
	*httptest.Server
	mu        sync.Mutex
	auth      map[string]string
	scopeHits int
	listing   any
}

func newEntraServer(t *testing.T, scopeAuth []ScopeAuth) *entraServer {
	t.Helper()
	s := &entraServer{auth: map[string]string{}, listing: map[string]any{"scopes": []string{}, "scopeAuth": scopeAuth}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.auth[r.URL.Path] = r.Header.Get("Authorization")
		if r.URL.Path == "/api/v2/_admin/scopes" {
			s.scopeHits++
			_ = json.NewEncoder(w).Encode(s.listing)
			return
		}
		if r.Header.Get("Authorization") == "Bearer reject" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "Token validation failed"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"definitions": []any{}})
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *entraServer) authFor(path string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.auth[path]
}

func authority(tenant string) string { return "https://login.microsoftonline.com/" + tenant }

func TestEntra_tokenPerScopeTenantAndDataverse(t *testing.T) {
	s := newEntraServer(t, []ScopeAuth{
		{Name: "default", Authority: authority(tenantA), APIScope: "https://a.crm11.dynamics.com/.default"},
		{Name: "hr", Authority: authority(tenantB), APIScope: "https://b.crm11.dynamics.com/.default"},
	})
	f := &fakeEntra{}
	c := NewEntraClient(s.URL, f.auth())
	ctx := context.Background()

	for _, scope := range []string{"default", "hr"} {
		if _, err := c.GetTableDefinitions(ctx, scope); err != nil {
			t.Fatalf("%s: %v", scope, err)
		}
	}
	if got := s.authFor("/api/v2/_admin/default/table-definitions"); got != "Bearer "+tenantA+"|https://a.crm11.dynamics.com/.default|1" {
		t.Errorf("default scope's token = %q", got)
	}
	if got := s.authFor("/api/v2/_admin/hr/table-definitions"); got != "Bearer "+tenantB+"|https://b.crm11.dynamics.com/.default|1" {
		t.Errorf("hr scope's token = %q", got)
	}
	// The listing is public: no token goes to it.
	if got := s.authFor("/api/v2/_admin/scopes"); got != "" {
		t.Errorf("the scopes listing got Authorization %q", got)
	}
}

func TestEntra_cachesAndSharesOneSignIn(t *testing.T) {
	s := newEntraServer(t, []ScopeAuth{
		{Name: "default", Authority: authority(tenantA), APIScope: "https://a.crm11.dynamics.com/.default"},
		{Name: "other", Authority: authority(tenantA), APIScope: "https://a.crm11.dynamics.com/.default"},
	})
	f := &fakeEntra{tune: func(c *fakeCredential) { c.delay = 20 * time.Millisecond }}
	c := NewEntraClient(s.URL, f.auth())

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			scope := "default"
			if i%2 == 1 {
				scope = "other"
			}
			if _, err := c.GetTableDefinitions(context.Background(), scope); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	if len(f.creds) != 1 || f.creds[tenantA].calls != 1 {
		t.Errorf("want one credential signing in once, got %d credentials, %d sign-ins", len(f.creds), f.creds[tenantA].calls)
	}
	if s.scopeHits != 1 {
		t.Errorf("the scopes listing was read %d times, want once", s.scopeHits)
	}
}

func TestEntra_renewsATokenAboutToExpire(t *testing.T) {
	s := newEntraServer(t, []ScopeAuth{{Name: "default", Authority: authority(tenantA), APIScope: "https://a/.default"}})
	f := &fakeEntra{tune: func(c *fakeCredential) { c.expires = time.Minute }}
	c := NewEntraClient(s.URL, f.auth())
	for i := 0; i < 2; i++ {
		if _, err := c.GetTableDefinitions(context.Background(), "default"); err != nil {
			t.Fatal(err)
		}
	}
	if f.creds[tenantA].calls != 2 {
		t.Errorf("a token within the refresh margin was reused: %d sign-ins", f.creds[tenantA].calls)
	}
}

func TestEntra_unlistedScopeSignsInAsDefault(t *testing.T) {
	s := newEntraServer(t, []ScopeAuth{{Name: "default", Authority: authority(tenantA), APIScope: "https://a/.default"}})
	f := &fakeEntra{}
	c := NewEntraClient(s.URL, f.auth())
	if _, err := c.GetTableDefinitions(context.Background(), "brand-new"); err != nil {
		t.Fatal(err)
	}
	if got := s.authFor("/api/v2/_admin/brand-new/table-definitions"); !strings.HasPrefix(got, "Bearer "+tenantA+"|https://a/.default") {
		t.Errorf("token = %q, want the default scope's", got)
	}
}

func TestEntra_authorityWithoutTenant(t *testing.T) {
	listing := []ScopeAuth{{Name: "default", Authority: "https://login.microsoftonline.com/organizations", APIScope: "https://a/.default"}}

	s := newEntraServer(t, listing)
	f := &fakeEntra{}
	auth := f.auth()
	auth.TenantID = tenantB
	if _, err := NewEntraClient(s.URL, auth).GetTableDefinitions(context.Background(), "default"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.creds[tenantB]; !ok {
		t.Errorf("tenant_id wasn't used for an authority that names no tenant")
	}

	_, err := NewEntraClient(s.URL, (&fakeEntra{}).auth()).GetTableDefinitions(context.Background(), "default")
	if err == nil || !strings.Contains(err.Error(), "names no tenant; set tenant_id") {
		t.Errorf("err = %v", err)
	}
}

func TestEntra_failures(t *testing.T) {
	ctx := context.Background()

	t.Run("API publishes no scopeAuth", func(t *testing.T) {
		s := newEntraServer(t, nil)
		s.listing = map[string]any{"scopes": []string{"default"}}
		_, err := NewEntraClient(s.URL, (&fakeEntra{}).auth()).GetTableDefinitions(ctx, "default")
		if err == nil || !strings.Contains(err.Error(), "has no scopeAuth") || !strings.Contains(err.Error(), "connection_key") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("no entry for the scope or default", func(t *testing.T) {
		s := newEntraServer(t, []ScopeAuth{{Name: "hr", Authority: authority(tenantA), APIScope: "https://a/.default"}})
		_, err := NewEntraClient(s.URL, (&fakeEntra{}).auth()).GetTableDefinitions(ctx, "case")
		if err == nil || !strings.Contains(err.Error(), `scope "case"`) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("no Dataverse for the scope", func(t *testing.T) {
		s := newEntraServer(t, []ScopeAuth{{Name: "default", Authority: authority(tenantA)}})
		_, err := NewEntraClient(s.URL, (&fakeEntra{}).auth()).GetTableDefinitions(ctx, "default")
		if err == nil || !strings.Contains(err.Error(), "no Dataverse to sign in to") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("listing unreachable, then retried", func(t *testing.T) {
		fail := true
		var mu sync.Mutex
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			if r.URL.Path == "/api/v2/_admin/scopes" && fail {
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "blocked"})
				return
			}
			if r.URL.Path == "/api/v2/_admin/scopes" {
				_ = json.NewEncoder(w).Encode(map[string]any{"scopeAuth": []ScopeAuth{{Name: "default", Authority: authority(tenantA), APIScope: "https://a/.default"}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"definitions": []any{}})
		}))
		defer srv.Close()
		c := NewEntraClient(srv.URL, (&fakeEntra{}).auth())
		_, err := c.GetTableDefinitions(ctx, "default")
		if err == nil || !strings.Contains(err.Error(), "which tenant each scope signs in to") {
			t.Fatalf("err = %v", err)
		}
		mu.Lock()
		fail = false
		mu.Unlock()
		if _, err := c.GetTableDefinitions(ctx, "default"); err != nil {
			t.Errorf("a failed listing was remembered: %v", err)
		}
	})

	t.Run("credential can't be made", func(t *testing.T) {
		s := newEntraServer(t, []ScopeAuth{{Name: "default", Authority: authority(tenantA), APIScope: "https://a/.default"}})
		auth := &EntraAuth{Method: "fake sign-in", Credential: func(string) (azcore.TokenCredential, error) {
			return nil, errors.New("no az on PATH")
		}}
		_, err := NewEntraClient(s.URL, auth).GetTableDefinitions(ctx, "default")
		if err == nil || !strings.Contains(err.Error(), "no connection key is set, so the provider signs in with fake sign-in, and couldn't for tenant "+tenantA+": no az on PATH") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("sign-in refused", func(t *testing.T) {
		s := newEntraServer(t, []ScopeAuth{{Name: "default", Authority: authority(tenantA), APIScope: "https://a.crm11.dynamics.com/.default"}})
		f := &fakeEntra{tune: func(c *fakeCredential) { c.err = errors.New("AADSTS50076: MFA required") }}
		_, err := NewEntraClient(s.URL, f.auth()).GetTableDefinitions(ctx, "default")
		want := `no connection key is set, so the provider signs in with fake sign-in, and couldn't for scope "default" (tenant ` + tenantA + `, https://a.crm11.dynamics.com): AADSTS50076`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v\nwant %s", err, want)
		}
	})

	t.Run("API refuses the token", func(t *testing.T) {
		s := newEntraServer(t, []ScopeAuth{{Name: "default", Authority: authority(tenantA), APIScope: "https://a/.default"}})
		auth := &EntraAuth{Method: "fake sign-in", Credential: func(string) (azcore.TokenCredential, error) {
			return staticCredential("reject"), nil
		}}
		_, err := NewEntraClient(s.URL, auth).GetTableDefinitions(ctx, "default")
		if err == nil || !strings.Contains(err.Error(), "API error (401): Token validation failed (signed in with fake sign-in)") {
			t.Errorf("err = %v", err)
		}
	})
}

type staticCredential string

func (s staticCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: string(s), ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// The key goes everywhere it always did, the public listing included.
func TestConnectionKey_sentToTheScopesListingToo(t *testing.T) {
	s := newEntraServer(t, nil)
	if _, err := NewClient(s.URL, "the-key").GetScopes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.authFor("/api/v2/_admin/scopes"); got != "Bearer the-key" {
		t.Errorf("Authorization = %q", got)
	}
}

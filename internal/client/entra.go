package client

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	// tokenRefreshMargin is how long before it expires a cached token is
	// replaced, so no request leaves with a token that lapses on the way.
	tokenRefreshMargin = 5 * time.Minute
	// defaultScope is the scope every deployment has.
	defaultScope = "default"
)

var tenantGUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// EntraAuth signs admin requests in with Microsoft Entra ID instead of the
// admin connection key.
//
// The API checks an admin token against the scope it is for: issued by that
// scope's tenant, for that scope's Dataverse, to someone (a person, or an app
// registration's application user) with System Customizer or System
// Administrator in that Dataverse. GET /_admin/scopes publishes the tenant and
// the Dataverse per scope (scopeAuth), so each scope's token is asked for
// where the API will accept it, in another tenant included.
type EntraAuth struct {
	// Method says how tokens are got ("Azure CLI", ...), for messages.
	Method string
	// Credential returns the credential that gets tokens from tenantID. It is
	// called once per tenant.
	Credential func(tenantID string) (azcore.TokenCredential, error)
	// TenantID is used for a scope whose published authority names no tenant.
	TenantID string

	mu          sync.Mutex
	scopeAuth   map[string]ScopeAuth // nil until the API has answered
	credentials map[string]azcore.TokenCredential
	tokens      map[string]*cachedToken
}

type cachedToken struct {
	mu    sync.Mutex
	token azcore.AccessToken
}

// token returns an access token for scope's admin endpoints. Tokens are
// cached until shortly before they expire, and requests that need the same
// token at once wait for one sign-in: the Azure CLI starts a process per
// token, and Terraform runs ten resources at a time.
func (a *EntraAuth) token(ctx context.Context, c *Client, scope string) (string, error) {
	sa, err := a.signInFor(ctx, c, scope)
	if err != nil {
		return "", err
	}
	tenant, err := a.tenantFor(scope, sa)
	if err != nil {
		return "", err
	}

	entry := a.cacheEntry(tenant + " " + sa.APIScope)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.token.Token != "" && time.Until(entry.token.ExpiresOn) > tokenRefreshMargin {
		return entry.token.Token, nil
	}

	// Both failures name the key: someone who meant to use it and whose shell
	// lost DATAVERSE_CONTACT_CONNECTION_KEY lands here, not on a "missing key".
	cred, err := a.credentialFor(tenant)
	if err != nil {
		return "", fmt.Errorf("no connection key is set, so the provider signs in with %s, and couldn't for tenant %s: %w",
			a.Method, tenant, err)
	}
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{sa.APIScope}})
	if err != nil {
		return "", fmt.Errorf("no connection key is set, so the provider signs in with %s, and couldn't for scope %q (tenant %s, %s): %w",
			a.Method, scope, tenant, strings.TrimSuffix(sa.APIScope, "/.default"), err)
	}
	tflog.Debug(ctx, "Signed in for admin requests", map[string]interface{}{
		"method": a.Method, "scope": scope, "tenant": tenant, "expires": tok.ExpiresOn.Format(time.RFC3339),
	})
	entry.token = tok
	return tok.Token, nil
}

// signInFor returns the tenant and Dataverse scope's admin token must come
// from, as the API publishes them. The listing is read once.
//
// A scope the API doesn't list yet (Terraform is about to create it) takes
// the default scope's: the API resolves an unconfigured scope's admin sign-in
// to the deployment's own, which the default scope also uses.
func (a *EntraAuth) signInFor(ctx context.Context, c *Client, scope string) (ScopeAuth, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.scopeAuth == nil {
		resp, err := c.GetScopes(ctx)
		if err != nil {
			return ScopeAuth{}, fmt.Errorf("reading which tenant each scope signs in to (GET /api/v2/_admin/scopes): %w", err)
		}
		a.scopeAuth = map[string]ScopeAuth{}
		for _, sa := range resp.ScopeAuth {
			a.scopeAuth[sa.Name] = sa
		}
	}
	if len(a.scopeAuth) == 0 {
		return ScopeAuth{}, fmt.Errorf("the API at %s doesn't say which tenant its scopes sign in to "+
			"(GET /api/v2/_admin/scopes has no scopeAuth), so the provider can't sign in with %s. "+
			"Upgrade the API, or set connection_key to its ADMIN_CONNECTION_KEY", c.BaseURL, a.Method)
	}
	if sa, ok := a.scopeAuth[scope]; ok {
		return sa, nil
	}
	if sa, ok := a.scopeAuth[defaultScope]; ok {
		tflog.Debug(ctx, "Scope not listed by the API yet; signing in as for the default scope", map[string]interface{}{"scope": scope})
		return sa, nil
	}
	return ScopeAuth{}, fmt.Errorf("the API doesn't say which tenant scope %q signs in to, and lists no %q scope to go by",
		scope, defaultScope)
}

// tenantFor reads the tenant from the scope's published authority
// (https://login.microsoftonline.com/<tenant>), else TenantID.
func (a *EntraAuth) tenantFor(scope string, sa ScopeAuth) (string, error) {
	if sa.APIScope == "" {
		return "", fmt.Errorf("the API publishes no Dataverse to sign in to for scope %q", scope)
	}
	if u, err := url.Parse(sa.Authority); err == nil {
		if tenant := path.Base(u.Path); tenantGUID.MatchString(tenant) {
			return tenant, nil
		}
	}
	if a.TenantID != "" {
		return a.TenantID, nil
	}
	return "", fmt.Errorf("the API's sign-in authority for scope %q (%s) names no tenant; set tenant_id to the tenant its Dataverse is in",
		scope, sa.Authority)
}

func (a *EntraAuth) credentialFor(tenant string) (azcore.TokenCredential, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cred, ok := a.credentials[tenant]; ok {
		return cred, nil
	}
	cred, err := a.Credential(tenant)
	if err != nil {
		return nil, err
	}
	if a.credentials == nil {
		a.credentials = map[string]azcore.TokenCredential{}
	}
	a.credentials[tenant] = cred
	return cred, nil
}

func (a *EntraAuth) cacheEntry(key string) *cachedToken {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tokens == nil {
		a.tokens = map[string]*cachedToken{}
	}
	entry, ok := a.tokens[key]
	if !ok {
		entry = &cachedToken{}
		a.tokens[key] = entry
	}
	return entry
}

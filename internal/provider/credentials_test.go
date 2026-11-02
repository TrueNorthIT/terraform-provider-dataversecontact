package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// signInEnv is every environment variable that changes how the provider signs
// in. Tests clear them all so a developer's own shell can't leak in.
var signInEnv = []string{
	"DATAVERSE_CONTACT_CONNECTION_KEY", "DATAVERSE_CONTACT_API_KEY",
	"ARM_TENANT_ID", "ARM_CLIENT_ID", "ARM_USE_OIDC", "ARM_USE_MSI", "ARM_USE_CLI",
	"ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID", "ARM_OIDC_AZURE_SERVICE_CONNECTION_ID",
	"ARM_OIDC_REQUEST_TOKEN", "ARM_OIDC_REQUEST_URL", "SYSTEM_ACCESSTOKEN", "SYSTEM_OIDCREQUESTURI",
	"ACTIONS_ID_TOKEN_REQUEST_URL", "ACTIONS_ID_TOKEN_REQUEST_TOKEN",
	"ARM_OIDC_TOKEN", "ARM_OIDC_TOKEN_FILE_PATH",
}

const testTenant = "11111111-1111-1111-1111-111111111111"

func TestProviderConfigure_signIn(t *testing.T) {
	str := func(v string) tftypes.Value { return tftypes.NewValue(tftypes.String, v) }
	boolean := func(v bool) tftypes.Value { return tftypes.NewValue(tftypes.Bool, v) }
	noKey := tftypes.NewValue(tftypes.String, nil)

	cases := []struct {
		name       string
		attrs      map[string]tftypes.Value
		env        map[string]string
		wantKey    string
		wantMethod string
		wantErr    string
		wantWarn   string
	}{
		{name: "the key, as before", wantKey: "k"},
		{name: "the key from the environment",
			attrs: map[string]tftypes.Value{"connection_key": noKey},
			env:   map[string]string{"DATAVERSE_CONTACT_CONNECTION_KEY": "env-key"}, wantKey: "env-key"},
		{name: "the deprecated api_key",
			attrs: map[string]tftypes.Value{"connection_key": noKey, "api_key": str("old")}, wantKey: "old"},
		{name: "the key beats Entra settings in the environment",
			env:     map[string]string{"ARM_USE_OIDC": "true", "ARM_CLIENT_ID": "app"},
			wantKey: "k"},
		{name: "the key beats Entra settings in the provider block, with a warning",
			attrs:   map[string]tftypes.Value{"use_oidc": boolean(true), "client_id": str("app")},
			wantKey: "k", wantWarn: "Connection key in use"},
		{name: "no key: the Azure CLI",
			attrs:      map[string]tftypes.Value{"connection_key": noKey},
			wantMethod: "Azure CLI"},
		{name: "an empty key is no key",
			attrs:      map[string]tftypes.Value{"connection_key": str("")},
			wantMethod: "Azure CLI"},
		{name: "no key and the Azure CLI turned off",
			attrs:   map[string]tftypes.Value{"connection_key": noKey},
			env:     map[string]string{"ARM_USE_CLI": "false"},
			wantErr: "No way to sign in"},
		{name: "use_cli = false beats the environment",
			attrs:   map[string]tftypes.Value{"connection_key": noKey, "use_cli": boolean(false)},
			env:     map[string]string{"ARM_USE_CLI": "true"},
			wantErr: "No way to sign in"},
		{name: "Azure DevOps",
			attrs: map[string]tftypes.Value{"connection_key": noKey},
			env: map[string]string{"ARM_USE_OIDC": "true", "ARM_CLIENT_ID": "app",
				"ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID": "sc-1", "SYSTEM_ACCESSTOKEN": "job-token"},
			wantMethod: "Azure DevOps service connection sc-1"},
		{name: "Azure DevOps, the AzAPI name for the connection",
			attrs:      map[string]tftypes.Value{"connection_key": noKey, "use_oidc": boolean(true), "client_id": str("app")},
			env:        map[string]string{"ARM_OIDC_AZURE_SERVICE_CONNECTION_ID": "sc-2", "SYSTEM_ACCESSTOKEN": "job-token"},
			wantMethod: "Azure DevOps service connection sc-2"},
		{name: "Azure DevOps, configured connection beats the environment",
			attrs: map[string]tftypes.Value{"connection_key": noKey, "use_oidc": boolean(true), "client_id": str("app"),
				"ado_pipeline_service_connection_id": str("sc-config")},
			env:        map[string]string{"ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID": "sc-env", "ARM_OIDC_REQUEST_TOKEN": "job-token"},
			wantMethod: "Azure DevOps service connection sc-config"},
		{name: "Azure DevOps without System.AccessToken",
			attrs: map[string]tftypes.Value{"connection_key": noKey},
			env: map[string]string{"ARM_USE_OIDC": "true", "ARM_CLIENT_ID": "app",
				"ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID": "sc-1"},
			wantErr: "SYSTEM_ACCESSTOKEN: $(System.AccessToken)"},
		{name: "GitHub Actions",
			attrs: map[string]tftypes.Value{"connection_key": noKey},
			env: map[string]string{"ARM_USE_OIDC": "true", "ARM_CLIENT_ID": "app",
				"ACTIONS_ID_TOKEN_REQUEST_URL": "https://gh.example/token?x=1", "ACTIONS_ID_TOKEN_REQUEST_TOKEN": "rt"},
			wantMethod: "GitHub Actions OIDC"},
		{name: "OIDC with no token to use",
			attrs:   map[string]tftypes.Value{"connection_key": noKey},
			env:     map[string]string{"ARM_USE_OIDC": "true", "ARM_CLIENT_ID": "app"},
			wantErr: "No OIDC token to sign in with"},
		{name: "OIDC without client_id",
			attrs:   map[string]tftypes.Value{"connection_key": noKey, "use_oidc": boolean(true)},
			env:     map[string]string{"ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID": "sc-1", "SYSTEM_ACCESSTOKEN": "t"},
			wantErr: "Missing client_id"},
		{name: "managed identity",
			attrs:      map[string]tftypes.Value{"connection_key": noKey, "use_msi": boolean(true), "client_id": str("mi")},
			wantMethod: "managed identity"},
		{name: "OIDC before managed identity",
			attrs: map[string]tftypes.Value{"connection_key": noKey},
			env: map[string]string{"ARM_USE_MSI": "true", "ARM_USE_OIDC": "true", "ARM_CLIENT_ID": "app",
				"ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID": "sc-1", "SYSTEM_ACCESSTOKEN": "t"},
			wantMethod: "Azure DevOps service connection sc-1"},
		{name: "a bad boolean in the environment",
			attrs:   map[string]tftypes.Value{"connection_key": noKey},
			env:     map[string]string{"ARM_USE_OIDC": "maybe"},
			wantErr: `Invalid use_oidc: ARM_USE_OIDC must be true or false, got "maybe".`},
		{name: "a token the pipeline fetched, before a service connection",
			attrs: map[string]tftypes.Value{"connection_key": noKey},
			env: map[string]string{"ARM_USE_OIDC": "true", "ARM_CLIENT_ID": "app", "ARM_OIDC_TOKEN": "jwt",
				"ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID": "sc-1", "SYSTEM_ACCESSTOKEN": "t"},
			wantMethod: "the OIDC token in ARM_OIDC_TOKEN"},
		{name: "a token file",
			attrs:      map[string]tftypes.Value{"connection_key": noKey},
			env:        map[string]string{"ARM_USE_OIDC": "true", "ARM_CLIENT_ID": "app", "ARM_OIDC_TOKEN_FILE_PATH": "/var/run/token"},
			wantMethod: "the OIDC token in /var/run/token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, v := range signInEnv {
				t.Setenv(v, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			c, resp := configureResp(t, tc.attrs)
			var errs, warns []string
			for _, d := range resp.Diagnostics.Errors() {
				errs = append(errs, d.Summary()+": "+d.Detail())
			}
			for _, d := range resp.Diagnostics.Warnings() {
				warns = append(warns, d.Summary())
			}
			if tc.wantErr != "" {
				if !strings.Contains(strings.Join(errs, "; "), tc.wantErr) {
					t.Errorf("errors = %q, want %q", errs, tc.wantErr)
				}
				if c != nil {
					t.Error("a configuration that can't sign in must not hand resources a client")
				}
				return
			}
			if len(errs) > 0 {
				t.Fatalf("unexpected errors: %q", errs)
			}
			if got := strings.Join(warns, "; "); got != tc.wantWarn {
				t.Errorf("warnings = %q, want %q", got, tc.wantWarn)
			}
			if tc.wantKey != "" {
				if c.Entra != nil || c.APIKey != tc.wantKey {
					t.Errorf("key = %q (Entra %v), want key %q", c.APIKey, c.Entra != nil, tc.wantKey)
				}
				return
			}
			if c.Entra == nil || c.Entra.Method != tc.wantMethod {
				t.Fatalf("Entra = %+v, want method %q", c.Entra, tc.wantMethod)
			}
			// The credential behind it can be made: nothing is checked against
			// Entra until a token is asked for.
			t.Setenv("SYSTEM_OIDCREQUESTURI", "https://dev.azure.com/org/_apis/distributedtask/hubs/build/plans/p/jobs/j/oidctoken")
			if _, err := c.Entra.Credential(testTenant); err != nil {
				t.Errorf("credential: %v", err)
			}
		})
	}
}

func TestProviderConfigure_tenantIDAndManagedIdentityCredential(t *testing.T) {
	for _, v := range signInEnv {
		t.Setenv(v, "")
	}
	t.Setenv("ARM_TENANT_ID", testTenant)
	t.Setenv("ARM_USE_MSI", "true")
	c, errs := configure(t, map[string]tftypes.Value{"connection_key": tftypes.NewValue(tftypes.String, nil)})
	if errs != "" {
		t.Fatal(errs)
	}
	if c.Entra.TenantID != testTenant {
		t.Errorf("tenant_id = %q", c.Entra.TenantID)
	}
	// A managed identity has one tenant: every scope gets the same credential.
	a, err := c.Entra.Credential(testTenant)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := c.Entra.Credential("22222222-2222-2222-2222-222222222222")
	if a != b {
		t.Error("a managed identity got a second credential for another tenant")
	}
}

func TestTokenFile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("  the-jwt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if token, err := tokenFile(path)(ctx); err != nil || token != "the-jwt" {
		t.Errorf("token = %q, err = %v", token, err)
	}
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tokenFile(path)(ctx); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Errorf("empty file: err = %v", err)
	}
	if _, err := tokenFile(path + ".missing")(ctx); err == nil || !strings.Contains(err.Error(), "reading the OIDC token file") {
		t.Errorf("missing file: err = %v", err)
	}
}

func TestGithubAssertion(t *testing.T) {
	var gotAuth, gotAudience, gotX string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAudience = r.URL.Query().Get("audience")
		gotX = r.URL.Query().Get("x")
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte(`{"value":"the-oidc-token"}`))
		case "/empty":
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	token, err := githubAssertion(srv.URL+"/ok?x=1", "request-token")(ctx)
	if err != nil || token != "the-oidc-token" {
		t.Fatalf("token = %q, err = %v", token, err)
	}
	if gotAuth != "Bearer request-token" || gotAudience != "api://AzureADTokenExchange" || gotX != "1" {
		t.Errorf("request: auth %q, audience %q, x %q", gotAuth, gotAudience, gotX)
	}

	if _, err := githubAssertion(srv.URL+"/denied", "rt")(ctx); err == nil || !strings.Contains(err.Error(), "id-token: write") {
		t.Errorf("refused: err = %v", err)
	}
	if _, err := githubAssertion(srv.URL+"/empty", "rt")(ctx); err == nil || !strings.Contains(err.Error(), "without an OIDC token") {
		t.Errorf("empty: err = %v", err)
	}
	if _, err := githubAssertion("http://127.0.0.1:1/", "rt")(ctx); err == nil || !strings.Contains(err.Error(), "asking GitHub Actions") {
		t.Errorf("unreachable: err = %v", err)
	}
	if _, err := githubAssertion("://bad", "rt")(ctx); err == nil {
		t.Error("a bad URL was accepted")
	}
}

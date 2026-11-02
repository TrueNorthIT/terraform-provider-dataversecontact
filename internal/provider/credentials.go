package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/TrueNorthIT/terraform-provider-dataversecontact/internal/client"
)

// The Entra settings read the same ARM_* environment variables as the azurerm
// and azuread providers, so a pipeline that already signs those in signs this
// one in too.

// entraSettings says how to sign in with Microsoft Entra ID when there is no
// connection key.
type entraSettings struct {
	tenantID            string
	clientID            string
	useOIDC             bool
	serviceConnectionID string
	useMSI              bool
	useCLI              bool
}

// settingError is a configuration problem, reported as a Configure error.
type settingError struct{ summary, detail string }

func (e *settingError) Error() string { return e.summary + ": " + e.detail }

func resolveEntraSettings(config DataverseContactProviderModel) (entraSettings, error) {
	s := entraSettings{
		tenantID:            stringSetting(config.TenantID, "ARM_TENANT_ID"),
		clientID:            stringSetting(config.ClientID, "ARM_CLIENT_ID"),
		serviceConnectionID: stringSetting(config.ADOServiceConnectionID, "ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID", "ARM_OIDC_AZURE_SERVICE_CONNECTION_ID"),
	}
	var err error
	if s.useOIDC, err = boolSetting(config.UseOIDC, false, "use_oidc", "ARM_USE_OIDC"); err != nil {
		return s, err
	}
	if s.useMSI, err = boolSetting(config.UseMSI, false, "use_msi", "ARM_USE_MSI"); err != nil {
		return s, err
	}
	if s.useCLI, err = boolSetting(config.UseCLI, true, "use_cli", "ARM_USE_CLI"); err != nil {
		return s, err
	}
	return s, nil
}

// entraAuth picks the way to sign in: OIDC, then managed identity, then the
// Azure CLI, as azurerm does.
func (s entraSettings) entraAuth() (*client.EntraAuth, error) {
	switch {
	case s.useOIDC:
		if s.clientID == "" {
			return nil, &settingError{"Missing client_id",
				"use_oidc signs in as an app registration, so it needs client_id (or ARM_CLIENT_ID): " +
					"the application (client) ID of the app registration that holds the federated credential."}
		}
		// A token the pipeline fetched itself comes first, as in azurerm.
		if token := os.Getenv("ARM_OIDC_TOKEN"); token != "" {
			return s.withAssertion("the OIDC token in ARM_OIDC_TOKEN",
				func(context.Context) (string, error) { return token, nil }), nil
		}
		if path := os.Getenv("ARM_OIDC_TOKEN_FILE_PATH"); path != "" {
			return s.withAssertion("the OIDC token in "+path, tokenFile(path)), nil
		}
		if s.serviceConnectionID != "" {
			return s.azurePipelines()
		}
		return s.githubActions()
	case s.useMSI:
		opts := &azidentity.ManagedIdentityCredentialOptions{}
		if s.clientID != "" {
			opts.ID = azidentity.ClientID(s.clientID)
		}
		// A managed identity only has tokens from its own tenant, so one
		// credential serves every scope; a scope in another tenant answers 401.
		var cred azcore.TokenCredential
		return &client.EntraAuth{
			Method:   "managed identity",
			TenantID: s.tenantID,
			Credential: func(string) (azcore.TokenCredential, error) {
				if cred != nil {
					return cred, nil
				}
				c, err := azidentity.NewManagedIdentityCredential(opts)
				if err != nil {
					return nil, err
				}
				cred = c
				return c, nil
			},
		}, nil
	case s.useCLI:
		return &client.EntraAuth{
			Method:   "Azure CLI",
			TenantID: s.tenantID,
			Credential: func(tenant string) (azcore.TokenCredential, error) {
				return azidentity.NewAzureCLICredential(&azidentity.AzureCLICredentialOptions{TenantID: tenant})
			},
		}, nil
	}
	return nil, &settingError{"No way to sign in",
		"The provider needs either the admin connection key or a Microsoft Entra sign-in. Set connection_key " +
			"(or DATAVERSE_CONTACT_CONNECTION_KEY) to the API's ADMIN_CONNECTION_KEY; or set use_oidc in an " +
			"Azure DevOps or GitHub Actions pipeline, use_msi on an Azure-hosted agent, or leave use_cli on " +
			"and run az login."}
}

// azurePipelines signs in through an Azure DevOps service connection that
// uses workload identity federation: the job's OIDC token is exchanged for an
// Entra token, so no secret exists anywhere.
func (s entraSettings) azurePipelines() (*client.EntraAuth, error) {
	accessToken := firstEnv("ARM_OIDC_REQUEST_TOKEN", "SYSTEM_ACCESSTOKEN")
	if accessToken == "" {
		return nil, &settingError{"Missing System.AccessToken",
			"Signing in through an Azure DevOps service connection needs the job's access token, which " +
				"Azure Pipelines only passes to a step that asks for it. Add SYSTEM_ACCESSTOKEN: $(System.AccessToken) " +
				"to the env of the step that runs Terraform."}
	}
	clientID, connection := s.clientID, s.serviceConnectionID
	return &client.EntraAuth{
		Method:   fmt.Sprintf("Azure DevOps service connection %s", connection),
		TenantID: s.tenantID,
		Credential: func(tenant string) (azcore.TokenCredential, error) {
			return azidentity.NewAzurePipelinesCredential(tenant, clientID, connection, accessToken, nil)
		},
	}, nil
}

// githubActions signs in with the workflow's OIDC token, exchanged the same
// way. The job needs `permissions: id-token: write`.
func (s entraSettings) githubActions() (*client.EntraAuth, error) {
	requestURL := firstEnv("ARM_OIDC_REQUEST_URL", "ACTIONS_ID_TOKEN_REQUEST_URL")
	requestToken := firstEnv("ARM_OIDC_REQUEST_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if requestURL == "" || requestToken == "" {
		return nil, &settingError{"No OIDC token to sign in with",
			"use_oidc is set, but this isn't a pipeline the provider can get an OIDC token from. In Azure DevOps, " +
				"set ado_pipeline_service_connection_id (or ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID) to the service " +
				"connection's ID. In GitHub Actions, give the job permissions: id-token: write. A pipeline that " +
				"fetches its own token can pass it in ARM_OIDC_TOKEN or ARM_OIDC_TOKEN_FILE_PATH."}
	}
	return s.withAssertion("GitHub Actions OIDC", githubAssertion(requestURL, requestToken)), nil
}

// withAssertion signs in as clientID by exchanging the OIDC token assertion
// returns.
func (s entraSettings) withAssertion(method string, assertion func(context.Context) (string, error)) *client.EntraAuth {
	clientID := s.clientID
	return &client.EntraAuth{
		Method:   method,
		TenantID: s.tenantID,
		Credential: func(tenant string) (azcore.TokenCredential, error) {
			return azidentity.NewClientAssertionCredential(tenant, clientID, assertion, nil)
		},
	}
}

// tokenFile reads the OIDC token from a file each time one is needed, since
// whatever writes it (Kubernetes workload identity, a pipeline step) renews it
// in place.
func tokenFile(path string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("reading the OIDC token file: %w", err)
		}
		token := strings.TrimSpace(string(b))
		if token == "" {
			return "", fmt.Errorf("the OIDC token file %s is empty", path)
		}
		return token, nil
	}
}

// githubAssertion fetches the workflow's OIDC token for Entra's token
// exchange audience. It is called whenever a new Entra token is needed, since
// GitHub's tokens last minutes.
func githubAssertion(requestURL, requestToken string) func(context.Context) (string, error) {
	httpClient := &http.Client{Timeout: 30 * time.Second}
	return func(ctx context.Context) (string, error) {
		u, err := url.Parse(requestURL)
		if err != nil {
			return "", fmt.Errorf("GitHub's OIDC request URL is not a URL: %w", err)
		}
		q := u.Query()
		q.Set("audience", "api://AzureADTokenExchange")
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+requestToken)
		resp, err := httpClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("asking GitHub Actions for an OIDC token: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("reading GitHub Actions' OIDC token: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("GitHub Actions refused an OIDC token (%d); the job needs permissions: id-token: write", resp.StatusCode)
		}
		var token struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(body, &token); err != nil || token.Value == "" {
			return "", fmt.Errorf("GitHub Actions answered without an OIDC token")
		}
		return token.Value, nil
	}
}

// stringSetting is the configured value, else the first environment variable
// set.
func stringSetting(configured types.String, envVars ...string) string {
	if !configured.IsNull() && !configured.IsUnknown() && configured.ValueString() != "" {
		return configured.ValueString()
	}
	return firstEnv(envVars...)
}

// boolSetting is the configured value, else the environment variable, else
// def.
func boolSetting(configured types.Bool, def bool, name, envVar string) (bool, error) {
	if !configured.IsNull() && !configured.IsUnknown() {
		return configured.ValueBool(), nil
	}
	v := os.Getenv(envVar)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, &settingError{"Invalid " + name, fmt.Sprintf("%s must be true or false, got %q.", envVar, v)}
	}
	return b, nil
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

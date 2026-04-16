package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/tnapps/terraform-provider-dataversecontact/internal/client"
	"github.com/tnapps/terraform-provider-dataversecontact/internal/datasources"
	"github.com/tnapps/terraform-provider-dataversecontact/internal/resources"
)

var _ provider.Provider = &DataverseContactProvider{}

// DataverseContactProvider defines the provider implementation.
type DataverseContactProvider struct {
	version string
}

// DataverseContactProviderModel describes the provider data model.
type DataverseContactProviderModel struct {
	APIURL            types.String `tfsdk:"api_url"`
	APIKey            types.String `tfsdk:"api_key"`
	Auth0Domain       types.String `tfsdk:"auth0_domain"`
	Auth0ClientID     types.String `tfsdk:"auth0_client_id"`
	Auth0ClientSecret types.String `tfsdk:"auth0_client_secret"`
	Auth0Audience     types.String `tfsdk:"auth0_audience"`
}

// New returns a new provider factory function.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &DataverseContactProvider{
			version: version,
		}
	}
}

func (p *DataverseContactProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "dataversecontact"
	resp.Version = p.version
}

func (p *DataverseContactProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Terraform provider for managing Dataverse Contact API table schemas, custom APIs, and permissions.\n\n" +
			"Supports two authentication modes:\n" +
			"  1. Auth0 M2M (recommended): set auth0_domain, auth0_client_id, auth0_client_secret, and auth0_audience.\n" +
			"  2. Static API key: set api_key directly (e.g. a forged MCP key).",
		Attributes: map[string]schema.Attribute{
			"api_url": schema.StringAttribute{
				Description: "Base URL of the Dataverse Contact API (e.g. https://acme-api.vercel.app). " +
					"Can also be set with the DATAVERSE_CONTACT_API_URL environment variable.",
				Optional: true,
			},
			"api_key": schema.StringAttribute{
				Description: "Static API key (Bearer token) for authenticating with the admin endpoints. " +
					"Can also be set with the DATAVERSE_CONTACT_API_KEY environment variable. " +
					"Not needed when using Auth0 M2M authentication.",
				Optional:  true,
				Sensitive: true,
			},
			"auth0_domain": schema.StringAttribute{
				Description: "Auth0 tenant domain (e.g. your-tenant.auth0.com). " +
					"Can also be set with the AUTH0_DOMAIN environment variable.",
				Optional: true,
			},
			"auth0_client_id": schema.StringAttribute{
				Description: "Auth0 M2M application client ID. " +
					"Can also be set with the AUTH0_CLIENT_ID environment variable.",
				Optional: true,
			},
			"auth0_client_secret": schema.StringAttribute{
				Description: "Auth0 M2M application client secret. " +
					"Can also be set with the AUTH0_CLIENT_SECRET environment variable.",
				Optional:  true,
				Sensitive: true,
			},
			"auth0_audience": schema.StringAttribute{
				Description: "Auth0 API audience for the admin endpoint (e.g. https://tn-dataverse-contact-api/default/_admin). " +
					"Can also be set with the AUTH0_AUDIENCE environment variable.",
				Optional: true,
			},
		},
	}
}

func (p *DataverseContactProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config DataverseContactProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Resolve API URL
	apiURL := os.Getenv("DATAVERSE_CONTACT_API_URL")
	if !config.APIURL.IsNull() {
		apiURL = config.APIURL.ValueString()
	}
	if apiURL == "" {
		resp.Diagnostics.AddError(
			"Missing API URL",
			"The provider requires api_url to be set in the provider configuration or "+
				"the DATAVERSE_CONTACT_API_URL environment variable.",
		)
		return
	}

	// Resolve API Key — either static or via Auth0 M2M token exchange
	apiKey := os.Getenv("DATAVERSE_CONTACT_API_KEY")
	if !config.APIKey.IsNull() {
		apiKey = config.APIKey.ValueString()
	}

	// If no static key, try Auth0 M2M client credentials
	if apiKey == "" {
		auth0Domain := envOrConfig(config.Auth0Domain, "AUTH0_DOMAIN")
		auth0ClientID := envOrConfig(config.Auth0ClientID, "AUTH0_CLIENT_ID")
		auth0ClientSecret := envOrConfig(config.Auth0ClientSecret, "AUTH0_CLIENT_SECRET")
		auth0Audience := envOrConfig(config.Auth0Audience, "AUTH0_AUDIENCE")

		if auth0Domain != "" && auth0ClientID != "" && auth0ClientSecret != "" && auth0Audience != "" {
			token, err := client.Auth0TokenExchange(auth0Domain, auth0ClientID, auth0ClientSecret, auth0Audience)
			if err != nil {
				resp.Diagnostics.AddError(
					"Auth0 Token Exchange Failed",
					"Failed to obtain access token from Auth0: "+err.Error(),
				)
				return
			}
			apiKey = token
		}
	}

	if apiKey == "" {
		resp.Diagnostics.AddError(
			"Missing Authentication",
			"The provider requires either api_key or Auth0 M2M credentials "+
				"(auth0_domain, auth0_client_id, auth0_client_secret, auth0_audience). "+
				"These can be set in the provider configuration or via environment variables.",
		)
		return
	}

	c := client.NewClient(apiURL, apiKey)
	resp.DataSourceData = c
	resp.ResourceData = c
}

// envOrConfig returns the config value if set, otherwise the environment variable.
func envOrConfig(configVal types.String, envVar string) string {
	if !configVal.IsNull() {
		return configVal.ValueString()
	}
	return os.Getenv(envVar)
}

func (p *DataverseContactProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		resources.NewTableResource,
		resources.NewCustomApiResource,
		resources.NewPermissionsSyncResource,
	}
}

func (p *DataverseContactProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		datasources.NewScopesDataSource,
		datasources.NewTableDefinitionsDataSource,
		datasources.NewTableDataSource,
	}
}

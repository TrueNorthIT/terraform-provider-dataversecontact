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
	APIURL types.String `tfsdk:"api_url"`
	APIKey types.String `tfsdk:"api_key"`
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
		Description: "Terraform provider for managing Dataverse Contact API table schemas, custom APIs, and permissions.",
		Attributes: map[string]schema.Attribute{
			"api_url": schema.StringAttribute{
				Description: "Base URL of the Dataverse Contact API (e.g. https://acme-api.vercel.app). " +
					"Can also be set with the DATAVERSE_CONTACT_API_URL environment variable.",
				Optional: true,
			},
			"api_key": schema.StringAttribute{
				Description: "MCP API key (Bearer token) for authenticating with the admin endpoints. " +
					"Can also be set with the DATAVERSE_CONTACT_API_KEY environment variable.",
				Optional:  true,
				Sensitive: true,
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
	}

	// Resolve API Key
	apiKey := os.Getenv("DATAVERSE_CONTACT_API_KEY")
	if !config.APIKey.IsNull() {
		apiKey = config.APIKey.ValueString()
	}
	if apiKey == "" {
		resp.Diagnostics.AddError(
			"Missing API Key",
			"The provider requires api_key to be set in the provider configuration or "+
				"the DATAVERSE_CONTACT_API_KEY environment variable.",
		)
	}

	if resp.Diagnostics.HasError() {
		return
	}

	c := client.NewClient(apiURL, apiKey)
	resp.DataSourceData = c
	resp.ResourceData = c
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

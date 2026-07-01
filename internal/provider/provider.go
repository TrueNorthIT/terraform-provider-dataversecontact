package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

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
	APIURL        types.String `tfsdk:"api_url"`
	ConnectionKey types.String `tfsdk:"connection_key"`
	APIKey        types.String `tfsdk:"api_key"`
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
			"Authentication uses a static admin connection key. Mint an admin connection key on the API side, " +
			"set it as the `ADMIN_CONNECTION_KEY` environment variable on the API deployment, and pass the same " +
			"value here as `connection_key` (or via the `DATAVERSE_CONTACT_CONNECTION_KEY` environment variable). " +
			"The provider sends it as an `Authorization: Bearer <key>` header on every admin request.",
		Attributes: map[string]schema.Attribute{
			"api_url": schema.StringAttribute{
				Description: "Base URL of the Dataverse Contact API (e.g. https://api.dataverse-contact.tnapps.co.uk). " +
					"Can also be set with the DATAVERSE_CONTACT_API_URL environment variable.",
				Optional: true,
			},
			"connection_key": schema.StringAttribute{
				Description: "Admin connection key (Bearer token) for authenticating with the admin endpoints. " +
					"This is the same value configured as ADMIN_CONNECTION_KEY on the API deployment. " +
					"Can also be set with the DATAVERSE_CONTACT_CONNECTION_KEY environment variable.",
				Optional:  true,
				Sensitive: true,
			},
			"api_key": schema.StringAttribute{
				Description: "Deprecated: use connection_key instead. Legacy static API key (Bearer token). " +
					"Can also be set with the DATAVERSE_CONTACT_API_KEY environment variable.",
				Optional:           true,
				Sensitive:          true,
				DeprecationMessage: "Use connection_key (or DATAVERSE_CONTACT_CONNECTION_KEY) instead. api_key will be removed in a future release.",
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

	// Resolve the admin connection key.
	connectionKey := os.Getenv("DATAVERSE_CONTACT_CONNECTION_KEY")
	if !config.ConnectionKey.IsNull() {
		connectionKey = config.ConnectionKey.ValueString()
	}

	// Legacy fallback: the deprecated api_key attribute / DATAVERSE_CONTACT_API_KEY.
	if connectionKey == "" {
		legacyKey := os.Getenv("DATAVERSE_CONTACT_API_KEY")
		if !config.APIKey.IsNull() {
			legacyKey = config.APIKey.ValueString()
		}
		if legacyKey != "" {
			tflog.Warn(ctx, "The 'api_key' attribute (and DATAVERSE_CONTACT_API_KEY) is deprecated; "+
				"use 'connection_key' (or DATAVERSE_CONTACT_CONNECTION_KEY) instead.")
			connectionKey = legacyKey
		}
	}

	if connectionKey == "" {
		resp.Diagnostics.AddError(
			"Missing Admin Connection Key",
			"The provider requires an admin connection key. Set connection_key in the provider "+
				"configuration or the DATAVERSE_CONTACT_CONNECTION_KEY environment variable. This must "+
				"match the ADMIN_CONNECTION_KEY configured on the API deployment.",
		)
		return
	}

	c := client.NewClient(apiURL, connectionKey)
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

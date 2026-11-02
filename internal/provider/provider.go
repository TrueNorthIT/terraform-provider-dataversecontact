package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/TrueNorthIT/terraform-provider-dataversecontact/internal/client"
	"github.com/TrueNorthIT/terraform-provider-dataversecontact/internal/datasources"
	"github.com/TrueNorthIT/terraform-provider-dataversecontact/internal/resources"
)

var _ provider.Provider = &DataverseContactProvider{}

// DataverseContactProvider defines the provider implementation.
type DataverseContactProvider struct {
	version string
}

// DataverseContactProviderModel describes the provider data model.
type DataverseContactProviderModel struct {
	APIURL                 types.String `tfsdk:"api_url"`
	ConnectionKey          types.String `tfsdk:"connection_key"`
	APIKey                 types.String `tfsdk:"api_key"`
	TenantID               types.String `tfsdk:"tenant_id"`
	ClientID               types.String `tfsdk:"client_id"`
	UseOIDC                types.Bool   `tfsdk:"use_oidc"`
	ADOServiceConnectionID types.String `tfsdk:"ado_pipeline_service_connection_id"`
	UseMSI                 types.Bool   `tfsdk:"use_msi"`
	UseCLI                 types.Bool   `tfsdk:"use_cli"`
	Timeout                types.String `tfsdk:"timeout"`
	MaxRetries             types.Int64  `tfsdk:"max_retries"`
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
			"The provider signs in to the API's admin endpoints in one of two ways. With the admin connection key " +
			"(`connection_key`), it sends the API's `ADMIN_CONNECTION_KEY` as the Bearer token. Without one, it signs " +
			"in with Microsoft Entra ID: your Azure CLI login, a pipeline's workload identity federation " +
			"(`use_oidc`) or a managed identity (`use_msi`). The identity must be a user or application user in " +
			"each scope's Dataverse with System Customizer or System Administrator.",
		Attributes: map[string]schema.Attribute{
			"api_url": schema.StringAttribute{
				Description: "Base URL of the Dataverse Contact API (e.g. https://api.dataverse-contact.tnapps.co.uk). " +
					"Can also be set with the DATAVERSE_CONTACT_API_URL environment variable.",
				Optional: true,
			},
			"connection_key": schema.StringAttribute{
				Description: "Admin connection key (Bearer token) for authenticating with the admin endpoints. " +
					"This is the same value configured as ADMIN_CONNECTION_KEY on the API deployment. " +
					"Can also be set with the DATAVERSE_CONTACT_CONNECTION_KEY environment variable. " +
					"When it is set, the provider uses it and ignores the Microsoft Entra settings.",
				Optional:  true,
				Sensitive: true,
			},
			"tenant_id": schema.StringAttribute{
				Description: "Microsoft Entra tenant to sign in to when the API doesn't say. The provider asks the API " +
					"which tenant each scope signs in to, so this is rarely needed. " +
					"Can also be set with the ARM_TENANT_ID environment variable.",
				Optional: true,
			},
			"client_id": schema.StringAttribute{
				Description: "Application (client) ID to sign in as: the app registration behind the pipeline's " +
					"federated credential (use_oidc), or a user-assigned managed identity (use_msi). " +
					"Can also be set with the ARM_CLIENT_ID environment variable.",
				Optional: true,
			},
			"use_oidc": schema.BoolAttribute{
				Description: "Sign in as an app registration through workload identity federation, from an Azure DevOps " +
					"service connection (with ado_pipeline_service_connection_id), a GitHub Actions workflow, or a token " +
					"the pipeline fetched itself (the ARM_OIDC_TOKEN or ARM_OIDC_TOKEN_FILE_PATH environment variable). " +
					"Nothing secret is stored. Needs client_id. Defaults to false. " +
					"Can also be set with the ARM_USE_OIDC environment variable.",
				Optional: true,
			},
			"ado_pipeline_service_connection_id": schema.StringAttribute{
				Description: "In Azure DevOps, the ID of the Azure Resource Manager service connection (workload identity " +
					"federation) to sign in through, with use_oidc. The step must also map SYSTEM_ACCESSTOKEN: " +
					"$(System.AccessToken). Can also be set with the ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID or " +
					"ARM_OIDC_AZURE_SERVICE_CONNECTION_ID environment variable.",
				Optional: true,
			},
			"use_msi": schema.BoolAttribute{
				Description: "Sign in with the managed identity of the machine running Terraform, such as a self-hosted " +
					"agent on an Azure VM. Set client_id for a user-assigned identity. Defaults to false. " +
					"Can also be set with the ARM_USE_MSI environment variable.",
				Optional: true,
			},
			"use_cli": schema.BoolAttribute{
				Description: "Sign in with your Azure CLI login (az login) when there is no connection key and neither " +
					"use_oidc nor use_msi is set. Defaults to true. " +
					"Can also be set with the ARM_USE_CLI environment variable.",
				Optional: true,
			},
			"api_key": schema.StringAttribute{
				Description: "Deprecated: use connection_key instead. Legacy static API key (Bearer token). " +
					"Can also be set with the DATAVERSE_CONTACT_API_KEY environment variable.",
				Optional:           true,
				Sensitive:          true,
				DeprecationMessage: "Use connection_key (or DATAVERSE_CONTACT_CONNECTION_KEY) instead. api_key will be removed in a future release.",
			},
			"timeout": schema.StringAttribute{
				Description: "How long each attempt at an API request may take, as a Go duration such as \"90s\" or \"2m\". " +
					"A retried request gets this long for every attempt. Defaults to \"60s\". " +
					"Can also be set with the DATAVERSE_CONTACT_TIMEOUT environment variable.",
				Optional:   true,
				Validators: []validator.String{positiveDuration{}},
			},
			"max_retries": schema.Int64Attribute{
				Description: "How many times a request is retried after a failure that is safe to retry: no response " +
					"(including an attempt that ran out of time), 429, 502, 503 or 504 for reads, saves and deletes, " +
					"and only 429 for publish, unpublish and remove. 0 disables retries. Defaults to 3. " +
					"Can also be set with the DATAVERSE_CONTACT_MAX_RETRIES environment variable.",
				Optional:   true,
				Validators: []validator.Int64{int64validator.AtLeast(0)},
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

	// The key, when there is one, signs every request exactly as it always
	// has. Without one, the provider signs in with Microsoft Entra ID.
	var c *client.Client
	if connectionKey != "" {
		if configuredTrue(config.UseOIDC) || configuredTrue(config.UseMSI) {
			resp.Diagnostics.AddWarning("Connection key in use",
				"connection_key is set, so the provider signs in with it and ignores use_oidc and use_msi. "+
					"Remove the key (and DATAVERSE_CONTACT_CONNECTION_KEY) to sign in with Microsoft Entra ID.")
		}
		c = client.NewClient(apiURL, connectionKey)
	} else {
		auth, err := entraAuthFor(config)
		if err != nil {
			var se *settingError
			if errors.As(err, &se) {
				resp.Diagnostics.AddError(se.summary, se.detail)
			} else {
				resp.Diagnostics.AddError("Can't sign in", err.Error())
			}
			return
		}
		tflog.Info(ctx, "No connection key; signing in with Microsoft Entra ID", map[string]interface{}{"method": auth.Method})
		c = client.NewEntraClient(apiURL, auth)
	}

	if timeout, ok := resolveTimeout(config.Timeout, resp); ok {
		c.HTTPClient.Timeout = timeout
	}
	if retries, ok := resolveMaxRetries(config.MaxRetries, resp); ok {
		c.MaxRetries = retries
	}
	if resp.Diagnostics.HasError() {
		return
	}

	resp.DataSourceData = c
	resp.ResourceData = c
}

// entraAuthFor reads the Entra settings and picks the way to sign in. Tests
// replace it to sign in with a fake credential.
var entraAuthFor = func(config DataverseContactProviderModel) (*client.EntraAuth, error) {
	s, err := resolveEntraSettings(config)
	if err != nil {
		return nil, err
	}
	return s.entraAuth()
}

func configuredTrue(v types.Bool) bool {
	return !v.IsNull() && !v.IsUnknown() && v.ValueBool()
}

// positiveDuration checks a configured timeout at validate time. Configure
// checks it again, along with the environment variable, which validation
// never sees.
type positiveDuration struct{}

func (positiveDuration) Description(context.Context) string {
	return `value must be a positive duration such as "90s" or "2m"`
}

func (v positiveDuration) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (positiveDuration) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if d, err := time.ParseDuration(req.ConfigValue.ValueString()); err != nil || d <= 0 {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid timeout",
			fmt.Sprintf(`timeout must be a positive duration such as "90s" or "2m", got %q.`, req.ConfigValue.ValueString()))
	}
}

// resolveTimeout reads timeout from the configuration, else
// DATAVERSE_CONTACT_TIMEOUT. ok is false when neither sets it, or when the
// value is invalid (reported as an error).
func resolveTimeout(configured types.String, resp *provider.ConfigureResponse) (time.Duration, bool) {
	value, source := os.Getenv("DATAVERSE_CONTACT_TIMEOUT"), "DATAVERSE_CONTACT_TIMEOUT"
	if !configured.IsNull() && !configured.IsUnknown() {
		value, source = configured.ValueString(), "timeout"
	}
	if value == "" {
		return 0, false
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		resp.Diagnostics.AddError(
			"Invalid timeout",
			fmt.Sprintf("%s must be a positive duration such as \"90s\" or \"2m\", got %q.", source, value),
		)
		return 0, false
	}
	return timeout, true
}

// resolveMaxRetries reads max_retries from the configuration, else
// DATAVERSE_CONTACT_MAX_RETRIES, the same way.
func resolveMaxRetries(configured types.Int64, resp *provider.ConfigureResponse) (int, bool) {
	if !configured.IsNull() && !configured.IsUnknown() {
		if n := configured.ValueInt64(); n >= 0 {
			return int(n), true
		}
		resp.Diagnostics.AddError("Invalid max_retries",
			fmt.Sprintf("max_retries must be 0 or more, got %d.", configured.ValueInt64()))
		return 0, false
	}
	value := os.Getenv("DATAVERSE_CONTACT_MAX_RETRIES")
	if value == "" {
		return 0, false
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		resp.Diagnostics.AddError("Invalid max_retries",
			fmt.Sprintf("DATAVERSE_CONTACT_MAX_RETRIES must be a whole number, 0 or more, got %q.", value))
		return 0, false
	}
	return n, true
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

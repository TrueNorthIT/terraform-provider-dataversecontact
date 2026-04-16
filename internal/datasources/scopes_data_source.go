package datasources

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/tnapps/terraform-provider-dataversecontact/internal/client"
)

var _ datasource.DataSource = &ScopesDataSource{}

// ScopesDataSource defines the data source implementation.
type ScopesDataSource struct {
	client *client.Client
}

// ScopesDataSourceModel describes the data source data model.
type ScopesDataSourceModel struct {
	ID     types.String `tfsdk:"id"`
	Scopes types.List   `tfsdk:"scopes"`
}

func NewScopesDataSource() datasource.DataSource {
	return &ScopesDataSource{}
}

func (d *ScopesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_scopes"
}

func (d *ScopesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists all configured scopes in the Dataverse Contact API.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier.",
				Computed:    true,
			},
			"scopes": schema.ListAttribute{
				Description: "List of scope names.",
				Computed:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (d *ScopesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected DataSource Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData),
		)
		return
	}
	d.client = c
}

func (d *ScopesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	scopesResp, err := d.client.GetScopes(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read scopes", err.Error())
		return
	}

	scopesList, diags := types.ListValueFrom(ctx, types.StringType, scopesResp.Scopes)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	state := ScopesDataSourceModel{
		ID:     types.StringValue("scopes"),
		Scopes: scopesList,
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

package datasources

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/tnapps/terraform-provider-dataversecontact/internal/client"
)

var _ datasource.DataSource = &TableDefinitionsDataSource{}

// TableDefinitionsDataSource defines the data source implementation.
type TableDefinitionsDataSource struct {
	client *client.Client
}

// TableDefinitionsDataSourceModel describes the data source data model.
type TableDefinitionsDataSourceModel struct {
	ID          types.String                    `tfsdk:"id"`
	Scope       types.String                    `tfsdk:"scope"`
	Definitions []TableDefinitionDataModel      `tfsdk:"definitions"`
}

// TableDefinitionDataModel describes a single table definition.
type TableDefinitionDataModel struct {
	RouteName           types.String `tfsdk:"route_name"`
	Source              types.String `tfsdk:"source"`
	Description         types.String `tfsdk:"description"`
	DataverseTable      types.String `tfsdk:"dataverse_table"`
	DataverseLogicalName types.String `tfsdk:"dataverse_logical_name"`
	RequiredPermission  types.String `tfsdk:"required_permission"`
	PrimaryKey          types.String `tfsdk:"primary_key"`
	FieldCount          types.Int64  `tfsdk:"field_count"`
}

func NewTableDefinitionsDataSource() datasource.DataSource {
	return &TableDefinitionsDataSource{}
}

func (d *TableDefinitionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_table_definitions"
}

func (d *TableDefinitionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists all table definitions (published and built-in) for a scope.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier.",
				Computed:    true,
			},
			"scope": schema.StringAttribute{
				Description: "The scope to list table definitions for.",
				Required:    true,
			},
			"definitions": schema.ListNestedAttribute{
				Description: "List of table definitions.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"route_name": schema.StringAttribute{
							Description: "The table's route name.",
							Computed:    true,
						},
						"source": schema.StringAttribute{
							Description: "The source (\"published\" or \"built-in\").",
							Computed:    true,
						},
						"description": schema.StringAttribute{
							Description: "Human-readable description.",
							Computed:    true,
						},
						"dataverse_table": schema.StringAttribute{
							Description: "The Dataverse OData entity set name.",
							Computed:    true,
						},
						"dataverse_logical_name": schema.StringAttribute{
							Description: "The Dataverse entity logical name.",
							Computed:    true,
						},
						"required_permission": schema.StringAttribute{
							Description: "The base permission name.",
							Computed:    true,
						},
						"primary_key": schema.StringAttribute{
							Description: "The primary key field name.",
							Computed:    true,
						},
						"field_count": schema.Int64Attribute{
							Description: "Number of fields defined.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *TableDefinitionsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *TableDefinitionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config TableDefinitionsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	scope := config.Scope.ValueString()

	defsResp, err := d.client.GetTableDefinitions(ctx, scope)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read table definitions", err.Error())
		return
	}

	definitions := make([]TableDefinitionDataModel, len(defsResp.Definitions))
	for i, def := range defsResp.Definitions {
		definitions[i] = TableDefinitionDataModel{
			RouteName:           types.StringValue(def.RouteName),
			Source:              types.StringValue(def.Source),
			Description:         types.StringValue(def.Description),
			DataverseTable:      types.StringValue(def.DataverseTable),
			DataverseLogicalName: types.StringValue(def.DataverseLogicalName),
			RequiredPermission:  types.StringValue(def.RequiredPermission),
			PrimaryKey:          types.StringValue(def.PrimaryKey),
			FieldCount:          types.Int64Value(int64(def.FieldCount)),
		}
	}

	state := TableDefinitionsDataSourceModel{
		ID:          types.StringValue(fmt.Sprintf("table-definitions/%s", scope)),
		Scope:       types.StringValue(scope),
		Definitions: definitions,
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

package datasources

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/tnapps/terraform-provider-dataversecontact/internal/client"
)

var _ datasource.DataSource = &TableDataSource{}

// TableDataSource defines the data source implementation.
type TableDataSource struct {
	client *client.Client
}

// TableDataSourceModel describes the data source data model.
type TableDataSourceModel struct {
	ID                  types.String `tfsdk:"id"`
	Scope               types.String `tfsdk:"scope"`
	RouteName           types.String `tfsdk:"route_name"`
	Source              types.String `tfsdk:"source"`
	SchemaJSON          types.String `tfsdk:"schema_json"`
	DataverseTable      types.String `tfsdk:"dataverse_table"`
	DataverseLogicalName types.String `tfsdk:"dataverse_logical_name"`
	RequiredPermission  types.String `tfsdk:"required_permission"`
	PrimaryKey          types.String `tfsdk:"primary_key"`
	FieldCount          types.Int64  `tfsdk:"field_count"`
}

func NewTableDataSource() datasource.DataSource {
	return &TableDataSource{}
}

func (d *TableDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_table"
}

func (d *TableDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads a single table's published schema from the Dataverse Contact API.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Resource identifier in the format {scope}/{route_name}.",
				Computed:    true,
			},
			"scope": schema.StringAttribute{
				Description: "The scope to read the table from.",
				Required:    true,
			},
			"route_name": schema.StringAttribute{
				Description: "The table's route name.",
				Required:    true,
			},
			"source": schema.StringAttribute{
				Description: "The source (\"published\", \"built-in\", \"draft\").",
				Computed:    true,
			},
			"schema_json": schema.StringAttribute{
				Description: "The full table schema as a JSON string.",
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
				Description: "Number of fields defined in the schema.",
				Computed:    true,
			},
		},
	}
}

func (d *TableDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *TableDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config TableDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	scope := config.Scope.ValueString()
	routeName := config.RouteName.ValueString()

	tableResp, err := d.client.GetTable(ctx, scope, routeName)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read table",
			fmt.Sprintf("Could not read table %s/%s: %s", scope, routeName, err.Error()))
		return
	}

	state := TableDataSourceModel{
		ID:         types.StringValue(fmt.Sprintf("%s/%s", scope, routeName)),
		Scope:      types.StringValue(scope),
		RouteName:  types.StringValue(routeName),
		Source:     types.StringValue(tableResp.Source),
		SchemaJSON: types.StringValue(string(tableResp.Schema)),
	}

	// Parse schema to extract computed fields
	var schemaMap map[string]json.RawMessage
	if err := json.Unmarshal(tableResp.Schema, &schemaMap); err == nil {
		if v, ok := schemaMap["dataverseTable"]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil {
				state.DataverseTable = types.StringValue(s)
			}
		}
		if v, ok := schemaMap["dataverseLogicalName"]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil {
				state.DataverseLogicalName = types.StringValue(s)
			}
		}
		if v, ok := schemaMap["requiredPermission"]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil {
				state.RequiredPermission = types.StringValue(s)
			}
		}
		if v, ok := schemaMap["primaryKey"]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil {
				state.PrimaryKey = types.StringValue(s)
			}
		}
		if fields, ok := schemaMap["fields"]; ok {
			var fieldsMap map[string]json.RawMessage
			if json.Unmarshal(fields, &fieldsMap) == nil {
				state.FieldCount = types.Int64Value(int64(len(fieldsMap)))
			}
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

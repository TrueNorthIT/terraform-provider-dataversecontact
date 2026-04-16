package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/tnapps/terraform-provider-dataversecontact/internal/client"
)

var (
	_ resource.Resource                = &TableResource{}
	_ resource.ResourceWithImportState = &TableResource{}
)

// TableResource defines the resource implementation.
type TableResource struct {
	client *client.Client
}

// TableResourceModel describes the resource data model.
type TableResourceModel struct {
	ID             types.String `tfsdk:"id"`
	Scope          types.String `tfsdk:"scope"`
	RouteName      types.String `tfsdk:"route_name"`
	SchemaJSON     types.String `tfsdk:"schema_json"`
	Source         types.String `tfsdk:"source"`
	DataverseTable types.String `tfsdk:"dataverse_table"`
	FieldCount     types.Int64  `tfsdk:"field_count"`
}

func NewTableResource() resource.Resource {
	return &TableResource{}
}

func (r *TableResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_table"
}

func (r *TableResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a table schema in the Dataverse Contact API. " +
			"The schema is saved as a draft and immediately published.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Resource identifier in the format {scope}/{route_name}.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"scope": schema.StringAttribute{
				Description: "The scope this table belongs to (e.g. \"default\").",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"route_name": schema.StringAttribute{
				Description: "The URL-friendly route name for this table (e.g. \"case\").",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"schema_json": schema.StringAttribute{
				Description: "The table schema as a JSON string (SchemaHint format). " +
					"Can be loaded from a .schema.json file using the file() function.",
				Required: true,
			},
			"source": schema.StringAttribute{
				Description: "The source of the published schema (\"published\", \"built-in\").",
				Computed:    true,
			},
			"dataverse_table": schema.StringAttribute{
				Description: "The Dataverse OData entity set name (e.g. \"incidents\").",
				Computed:    true,
			},
			"field_count": schema.Int64Attribute{
				Description: "Number of fields defined in the schema.",
				Computed:    true,
			},
		},
	}
}

func (r *TableResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData),
		)
		return
	}
	r.client = c
}

func (r *TableResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan TableResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	scope := plan.Scope.ValueString()
	routeName := plan.RouteName.ValueString()
	schemaJSON := json.RawMessage(plan.SchemaJSON.ValueString())

	tflog.Info(ctx, "Creating table", map[string]interface{}{
		"scope":      scope,
		"route_name": routeName,
	})

	_, err := r.client.SaveAndPublishTable(ctx, scope, routeName, schemaJSON)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create table", err.Error())
		return
	}

	r.readIntoModel(ctx, scope, routeName, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TableResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TableResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	scope := state.Scope.ValueString()
	routeName := state.RouteName.ValueString()

	r.readIntoModel(ctx, scope, routeName, &state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *TableResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TableResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	scope := plan.Scope.ValueString()
	routeName := plan.RouteName.ValueString()
	schemaJSON := json.RawMessage(plan.SchemaJSON.ValueString())

	tflog.Info(ctx, "Updating table", map[string]interface{}{
		"scope":      scope,
		"route_name": routeName,
	})

	_, err := r.client.SaveAndPublishTable(ctx, scope, routeName, schemaJSON)
	if err != nil {
		resp.Diagnostics.AddError("Failed to update table", err.Error())
		return
	}

	r.readIntoModel(ctx, scope, routeName, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TableResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state TableResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	scope := state.Scope.ValueString()
	routeName := state.RouteName.ValueString()

	tflog.Info(ctx, "Deleting table", map[string]interface{}{
		"scope":      scope,
		"route_name": routeName,
	})

	if err := r.client.DeleteTable(ctx, scope, routeName); err != nil {
		resp.Diagnostics.AddError("Failed to delete table", err.Error())
	}
}

func (r *TableResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("Expected format: {scope}/{route_name}, got: %s", req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("scope"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("route_name"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// readIntoModel reads the current table state from the API and populates the model.
func (r *TableResource) readIntoModel(ctx context.Context, scope, routeName string, model *TableResourceModel, diagnostics *diag.Diagnostics) {
	tableResp, err := r.client.GetTable(ctx, scope, routeName)
	if err != nil {
		if client.IsNotFound(err) {
			diagnostics.AddWarning("Table not found",
				fmt.Sprintf("Table %s/%s not found, removing from state", scope, routeName))
			return
		}
		diagnostics.AddError("Failed to read table", err.Error())
		return
	}

	model.ID = types.StringValue(fmt.Sprintf("%s/%s", scope, routeName))
	model.Source = types.StringValue(tableResp.Source)

	// Parse schema to extract computed fields
	var schemaMap map[string]json.RawMessage
	if err := json.Unmarshal(tableResp.Schema, &schemaMap); err == nil {
		if dt, ok := schemaMap["dataverseTable"]; ok {
			var dtStr string
			if json.Unmarshal(dt, &dtStr) == nil {
				model.DataverseTable = types.StringValue(dtStr)
			}
		}
		if fields, ok := schemaMap["fields"]; ok {
			var fieldsMap map[string]json.RawMessage
			if json.Unmarshal(fields, &fieldsMap) == nil {
				model.FieldCount = types.Int64Value(int64(len(fieldsMap)))
			}
		}
	}

	// For imported resources, store the schema from the API
	if model.SchemaJSON.IsNull() || model.SchemaJSON.IsUnknown() {
		model.SchemaJSON = types.StringValue(string(tableResp.Schema))
	}
}

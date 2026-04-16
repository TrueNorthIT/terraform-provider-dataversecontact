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
	_ resource.Resource                = &CustomApiResource{}
	_ resource.ResourceWithImportState = &CustomApiResource{}
)

// CustomApiResource defines the resource implementation.
type CustomApiResource struct {
	client *client.Client
}

// CustomApiResourceModel describes the resource data model.
type CustomApiResourceModel struct {
	ID                  types.String `tfsdk:"id"`
	Scope               types.String `tfsdk:"scope"`
	RouteName           types.String `tfsdk:"route_name"`
	SchemaJSON          types.String `tfsdk:"schema_json"`
	Source              types.String `tfsdk:"source"`
	DataverseUniqueName types.String `tfsdk:"dataverse_unique_name"`
	IsFunction          types.Bool   `tfsdk:"is_function"`
	BindingType         types.String `tfsdk:"binding_type"`
}

func NewCustomApiResource() resource.Resource {
	return &CustomApiResource{}
}

func (r *CustomApiResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_custom_api"
}

func (r *CustomApiResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a custom API configuration in the Dataverse Contact API. " +
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
				Description: "The scope this custom API belongs to.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"route_name": schema.StringAttribute{
				Description: "The URL-friendly route name for this custom API.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"schema_json": schema.StringAttribute{
				Description: "The custom API schema as a JSON string (CustomApiHint format).",
				Required:    true,
			},
			"source": schema.StringAttribute{
				Description: "The source of the published schema (\"published\", \"built-in\").",
				Computed:    true,
			},
			"dataverse_unique_name": schema.StringAttribute{
				Description: "The Dataverse unique name for this custom API.",
				Computed:    true,
			},
			"is_function": schema.BoolAttribute{
				Description: "Whether this is a function (GET) or action (POST).",
				Computed:    true,
			},
			"binding_type": schema.StringAttribute{
				Description: "The binding type (\"global\", \"entity\", \"entityCollection\").",
				Computed:    true,
			},
		},
	}
}

func (r *CustomApiResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *CustomApiResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan CustomApiResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	scope := plan.Scope.ValueString()
	routeName := plan.RouteName.ValueString()
	schemaJSON := json.RawMessage(plan.SchemaJSON.ValueString())

	tflog.Info(ctx, "Creating custom API", map[string]interface{}{
		"scope":      scope,
		"route_name": routeName,
	})

	_, err := r.client.SaveAndPublishCustomApi(ctx, scope, routeName, schemaJSON)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create custom API", err.Error())
		return
	}

	r.readIntoModel(ctx, scope, routeName, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *CustomApiResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state CustomApiResourceModel
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

func (r *CustomApiResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan CustomApiResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	scope := plan.Scope.ValueString()
	routeName := plan.RouteName.ValueString()
	schemaJSON := json.RawMessage(plan.SchemaJSON.ValueString())

	tflog.Info(ctx, "Updating custom API", map[string]interface{}{
		"scope":      scope,
		"route_name": routeName,
	})

	_, err := r.client.SaveAndPublishCustomApi(ctx, scope, routeName, schemaJSON)
	if err != nil {
		resp.Diagnostics.AddError("Failed to update custom API", err.Error())
		return
	}

	r.readIntoModel(ctx, scope, routeName, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *CustomApiResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state CustomApiResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	scope := state.Scope.ValueString()
	routeName := state.RouteName.ValueString()

	tflog.Info(ctx, "Deleting custom API", map[string]interface{}{
		"scope":      scope,
		"route_name": routeName,
	})

	if err := r.client.DeleteCustomApi(ctx, scope, routeName); err != nil {
		resp.Diagnostics.AddError("Failed to delete custom API", err.Error())
	}
}

func (r *CustomApiResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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

func (r *CustomApiResource) readIntoModel(ctx context.Context, scope, routeName string, model *CustomApiResourceModel, diagnostics *diag.Diagnostics) {
	apiResp, err := r.client.GetCustomApi(ctx, scope, routeName)
	if err != nil {
		if client.IsNotFound(err) {
			diagnostics.AddWarning("Custom API not found",
				fmt.Sprintf("Custom API %s/%s not found, removing from state", scope, routeName))
			return
		}
		diagnostics.AddError("Failed to read custom API", err.Error())
		return
	}

	model.ID = types.StringValue(fmt.Sprintf("%s/%s", scope, routeName))
	model.Source = types.StringValue(apiResp.Source)

	// Parse schema to extract computed fields
	var schemaMap map[string]json.RawMessage
	if err := json.Unmarshal(apiResp.Schema, &schemaMap); err == nil {
		if v, ok := schemaMap["dataverseUniqueName"]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil {
				model.DataverseUniqueName = types.StringValue(s)
			}
		}
		if v, ok := schemaMap["isFunction"]; ok {
			var b bool
			if json.Unmarshal(v, &b) == nil {
				model.IsFunction = types.BoolValue(b)
			}
		}
		if v, ok := schemaMap["bindingType"]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil {
				model.BindingType = types.StringValue(s)
			}
		}
	}

	if model.SchemaJSON.IsNull() || model.SchemaJSON.IsUnknown() {
		model.SchemaJSON = types.StringValue(string(apiResp.Schema))
	}
}

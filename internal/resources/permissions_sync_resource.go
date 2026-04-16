package resources

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/tnapps/terraform-provider-dataversecontact/internal/client"
)

var _ resource.Resource = &PermissionsSyncResource{}

// PermissionsSyncResource defines the resource implementation.
type PermissionsSyncResource struct {
	client *client.Client
}

// PermissionsSyncResourceModel describes the resource data model.
type PermissionsSyncResourceModel struct {
	ID              types.String `tfsdk:"id"`
	Scope           types.String `tfsdk:"scope"`
	Triggers        types.Map    `tfsdk:"triggers"`
	Audience        types.String `tfsdk:"audience"`
	PermissionCount types.Int64  `tfsdk:"permission_count"`
}

func NewPermissionsSyncResource() resource.Resource {
	return &PermissionsSyncResource{}
}

func (r *PermissionsSyncResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_permissions_sync"
}

func (r *PermissionsSyncResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Syncs permissions to Auth0 for a scope. " +
			"Use depends_on to ensure all tables are published before syncing. " +
			"Use triggers to force re-sync when table definitions change.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Resource identifier (scope name).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"scope": schema.StringAttribute{
				Description: "The scope to sync permissions for.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"triggers": schema.MapAttribute{
				Description: "A map of trigger values. When any value changes, permissions are re-synced. " +
					"Use this to trigger re-sync when table schemas change.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"audience": schema.StringAttribute{
				Description: "The Auth0 API audience identifier.",
				Computed:    true,
			},
			"permission_count": schema.Int64Attribute{
				Description: "The total number of permissions synced.",
				Computed:    true,
			},
		},
	}
}

func (r *PermissionsSyncResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PermissionsSyncResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PermissionsSyncResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	scope := plan.Scope.ValueString()

	tflog.Info(ctx, "Syncing permissions to Auth0", map[string]interface{}{
		"scope": scope,
	})

	auth0Resp, err := r.client.EnsureAuth0(ctx, scope)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync permissions", err.Error())
		return
	}

	plan.ID = types.StringValue(scope)
	plan.Audience = types.StringValue(auth0Resp.Audience)
	plan.PermissionCount = types.Int64Value(int64(len(auth0Resp.Added) + len(auth0Resp.Unchanged)))

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PermissionsSyncResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PermissionsSyncResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Permission sync is a one-shot operation — state is preserved as-is.
	// The actual permissions are managed by Auth0 and re-synced on next apply
	// when triggers change.
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PermissionsSyncResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PermissionsSyncResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	scope := plan.Scope.ValueString()

	tflog.Info(ctx, "Re-syncing permissions to Auth0", map[string]interface{}{
		"scope": scope,
	})

	auth0Resp, err := r.client.EnsureAuth0(ctx, scope)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync permissions", err.Error())
		return
	}

	plan.ID = types.StringValue(scope)
	plan.Audience = types.StringValue(auth0Resp.Audience)
	plan.PermissionCount = types.Int64Value(int64(len(auth0Resp.Added) + len(auth0Resp.Unchanged)))

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PermissionsSyncResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// Permission sync has no server-side resource to delete.
	// Auth0 permissions are managed externally and persist after Terraform destroy.
	tflog.Info(ctx, "Permissions sync resource removed from state (Auth0 permissions are not deleted)")
}

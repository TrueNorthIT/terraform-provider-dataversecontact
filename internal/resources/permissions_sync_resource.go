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
	ID                 types.String `tfsdk:"id"`
	Scope              types.String `tfsdk:"scope"`
	Triggers           types.Map    `tfsdk:"triggers"`
	DefaultPermissions types.Map    `tfsdk:"default_permissions"`
	AllowSelfRegister  types.Bool   `tfsdk:"allow_self_register"`
	PermissionCount    types.Int64  `tfsdk:"permission_count"`
}

func NewPermissionsSyncResource() resource.Resource {
	return &PermissionsSyncResource{}
}

func (r *PermissionsSyncResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_permissions_sync"
}

func (r *PermissionsSyncResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Publishes a scope's baseline permissions (its defaults.json) to the API via " +
			"PUT /api/v2/_admin/{scope}/table-manager/defaults. " +
			"Use depends_on to ensure all tables are published before syncing. " +
			"Use triggers to force a re-publish when table definitions change.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Resource identifier (scope name).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"scope": schema.StringAttribute{
				Description: "The scope to publish permissions for.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"triggers": schema.MapAttribute{
				Description: "A map of trigger values. When any value changes, permissions are re-published. " +
					"Use this to trigger a re-publish when table schemas change.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"default_permissions": schema.MapAttribute{
				Description: "The baseline permissions granted to every authenticated contact, keyed by route name. " +
					"Each value is the list of permission tokens for that route (e.g. [\"team\", \"write\", \"create\"]). " +
					"Published as the `permissions` object of the scope's defaults.json.",
				Optional:    true,
				ElementType: types.ListType{ElemType: types.StringType},
			},
			"allow_self_register": schema.BoolAttribute{
				Description: "Whether contacts may self-register for this scope. Published as `allowSelfRegister` " +
					"in the scope's defaults.json. Defaults to false.",
				Optional: true,
			},
			"permission_count": schema.Int64Attribute{
				Description: "The number of routes with published baseline permissions.",
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

// buildPermissions converts the default_permissions map attribute into the
// map[string][]string shape expected by the API.
func buildPermissions(ctx context.Context, m types.Map) (map[string][]string, error) {
	permissions := map[string][]string{}
	if m.IsNull() || m.IsUnknown() {
		return permissions, nil
	}
	var raw map[string][]string
	diags := m.ElementsAs(ctx, &raw, false)
	if diags.HasError() {
		return nil, fmt.Errorf("failed to parse default_permissions")
	}
	for k, v := range raw {
		if v == nil {
			v = []string{}
		}
		permissions[k] = v
	}
	return permissions, nil
}

func (r *PermissionsSyncResource) publish(ctx context.Context, plan *PermissionsSyncResourceModel, resp *diagnosticsSink) {
	scope := plan.Scope.ValueString()

	permissions, err := buildPermissions(ctx, plan.DefaultPermissions)
	if err != nil {
		resp.AddError("Invalid default_permissions", err.Error())
		return
	}

	allowSelfRegister := plan.AllowSelfRegister.ValueBool()

	tflog.Info(ctx, "Publishing scope default permissions", map[string]interface{}{
		"scope":               scope,
		"routes":              len(permissions),
		"allow_self_register": allowSelfRegister,
	})

	if _, err := r.client.PublishDefaults(ctx, scope, permissions, allowSelfRegister); err != nil {
		resp.AddError("Failed to publish permissions", err.Error())
		return
	}

	plan.ID = types.StringValue(scope)
	plan.PermissionCount = types.Int64Value(int64(len(permissions)))
}

// diagnosticsSink is a tiny abstraction so Create/Update can share publish logic.
type diagnosticsSink struct {
	addError func(summary, detail string)
}

func (d *diagnosticsSink) AddError(summary, detail string) {
	d.addError(summary, detail)
}

func (r *PermissionsSyncResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PermissionsSyncResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.publish(ctx, &plan, &diagnosticsSink{addError: resp.Diagnostics.AddError})
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PermissionsSyncResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PermissionsSyncResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Publishing defaults is a one-shot operation — state is preserved as-is.
	// Defaults are re-published on the next apply when triggers or the
	// default_permissions map change.
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PermissionsSyncResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PermissionsSyncResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.publish(ctx, &plan, &diagnosticsSink{addError: resp.Diagnostics.AddError})
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PermissionsSyncResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// Publishing defaults has no server-side resource to delete.
	// Published permissions persist after Terraform destroy.
	tflog.Info(ctx, "Permissions sync resource removed from state (published defaults are not deleted)")
}

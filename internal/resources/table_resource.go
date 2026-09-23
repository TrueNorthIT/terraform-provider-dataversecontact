package resources

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/TrueNorthIT/terraform-provider-dataversecontact/internal/client"
)

var (
	_ resource.Resource                   = &TableResource{}
	_ resource.ResourceWithImportState    = &TableResource{}
	_ resource.ResourceWithValidateConfig = &TableResource{}
)

// TableResource defines the resource implementation.
type TableResource struct {
	client *client.Client
}

// ── Model types ─────────────────────────────────────────────────────────

// TableResourceModel describes the resource data model.
type TableResourceModel struct {
	// Identity
	ID        types.String `tfsdk:"id"`
	Scope     types.String `tfsdk:"scope"`
	RouteName types.String `tfsdk:"route_name"`

	// Required attributes
	DataverseTable       types.String `tfsdk:"dataverse_table"`
	DataverseLogicalName types.String `tfsdk:"dataverse_logical_name"`
	PrimaryKey           types.String `tfsdk:"primary_key"`
	RequiredPermission   types.String `tfsdk:"required_permission"`
	DefaultSelect        types.List   `tfsdk:"default_select"`
	LookupFields         types.List   `tfsdk:"lookup_fields"`

	// Optional attributes
	Description          types.String          `tfsdk:"description"`
	Icon                 types.String          `tfsdk:"icon"`
	PermissionGroup      types.String          `tfsdk:"permission_group"`
	FetchXml             types.String          `tfsdk:"fetch_xml"`
	Aliases              types.List            `tfsdk:"aliases"`
	LookupSearchContains types.List            `tfsdk:"lookup_search_contains"`
	Filters              types.List            `tfsdk:"filters"`
	PublicChoices        types.Bool            `tfsdk:"public_choices"`
	PublicRead           types.Bool            `tfsdk:"public_read"`
	PublicCreate         types.Bool            `tfsdk:"public_create"`
	BusinessProcess      *BusinessProcessModel `tfsdk:"business_process"`

	// Fields map
	Fields types.Map `tfsdk:"fields"`

	// Blocks
	ContactJoinStep          []JoinStepModel                 `tfsdk:"contact_join_step"`
	TeamJoinStep             []JoinStepModel                 `tfsdk:"team_join_step"`
	AlternateContactJoinPath []AlternateContactJoinPathModel `tfsdk:"alternate_contact_join_path"`
	CreateDefault            []CreateDefaultModel            `tfsdk:"create_default"`
	ParentTable              *ParentTableModel               `tfsdk:"parent_table"`
	PolymorphicLookup        *PolymorphicLookupModel         `tfsdk:"polymorphic_lookup"`
	Expand                   []ExpandModel                   `tfsdk:"expand"`

	// Computed
	Source     types.String `tfsdk:"source"`
	FieldCount types.Int64  `tfsdk:"field_count"`
}

// JoinStepModel represents a single step in a join path.
type JoinStepModel struct {
	Table   types.String `tfsdk:"table"`
	From    types.String `tfsdk:"from"`
	Key     types.String `tfsdk:"key"`
	Reverse types.Bool   `tfsdk:"reverse"`
}

// AlternateContactJoinPathModel is a single alternate path with nested steps.
type AlternateContactJoinPathModel struct {
	Step []JoinStepModel `tfsdk:"step"`
}

// CreateDefaultModel is a lookup field auto-bound on create.
type CreateDefaultModel struct {
	Field     types.String `tfsdk:"field"`
	BindTo    types.String `tfsdk:"bind_to"`
	EntitySet types.String `tfsdk:"entity_set"`
}

// ParentTableModel describes the parent table relationship.
type ParentTableModel struct {
	Table              types.String `tfsdk:"table"`
	NavigationProperty types.String `tfsdk:"navigation_property"`
}

// PolymorphicLookupModel publishes every target of a polymorphic lookup as a
// route of its own.
type PolymorphicLookupModel struct {
	Field              types.String          `tfsdk:"field"`
	RequiredPermission types.String          `tfsdk:"required_permission"`
	RoutePrefixStrip   types.String          `tfsdk:"route_prefix_strip"`
	TargetPrefix       types.String          `tfsdk:"target_prefix"`
	ExcludeTargets     types.List            `tfsdk:"exclude_targets"`
	ReadOnly           types.Bool            `tfsdk:"read_only"`
	BusinessProcess    *BusinessProcessModel `tfsdk:"business_process"`
}

// BusinessProcessModel asks the API to expose a business process flow on a
// table's rows. Nested attribute rather than block, so it is nil when unset —
// unlike a SingleNestedBlock, which Terraform always hands over non-nil.
type BusinessProcessModel struct {
	ExposeAs types.String `tfsdk:"expose_as"`
}

// exposeAsPattern is what the API accepts as a row property name.
var exposeAsPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// businessProcessAttribute is the one schema both places use: the table's
// own process, and each target's on a polymorphic rule.
func businessProcessAttribute(where string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Description: "Expose " + where + " business process flow on this table's rows. Every row " +
			"then carries an object (`progress` unless expose_as renames it) with the process " +
			"instance's state, active stage and ordered stages — or null when there is no process " +
			"or no instance for the row. Derived by the API per request, read-only, not selectable. " +
			"The API's Dataverse application user needs Read on workflow, processstage and the " +
			"process's instance table.",
		Optional: true,
		Attributes: map[string]schema.Attribute{
			"expose_as": schema.StringAttribute{
				Description: "Property name on each row. Defaults to \"progress\". Must not collide " +
					"with a field of this table.",
				Optional: true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(exposeAsPattern, "must be a plain identifier (letters, digits, underscores)"),
				},
			},
		},
	}
}

// ExpandModel is an expandable lookup into a related table.
type ExpandModel struct {
	LookupField  types.String       `tfsdk:"lookup_field"`
	RelatedTable types.String       `tfsdk:"related_table"`
	Field        []ExpandFieldModel `tfsdk:"field"`
}

// ExpandFieldModel is a single field within an expand definition.
type ExpandFieldModel struct {
	Name        types.String `tfsdk:"name"`
	Type        types.String `tfsdk:"type"`
	Description types.String `tfsdk:"description"`
}

// fieldCountFromFields plans field_count as the size of fields. Carrying the
// prior value forward (UseStateForUnknown) goes stale whenever a field is
// added or removed, and apply then fails with an inconsistent result.
type fieldCountFromFields struct{}

func (m fieldCountFromFields) Description(_ context.Context) string {
	return "Plans field_count as the number of entries in fields."
}

func (m fieldCountFromFields) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m fieldCountFromFields) PlanModifyInt64(ctx context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	var fields types.Map
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("fields"), &fields)...)
	if resp.Diagnostics.HasError() || fields.IsUnknown() {
		return
	}
	resp.PlanValue = types.Int64Value(int64(len(fields.Elements())))
}

// ── Resource interface ──────────────────────────────────────────────────

func NewTableResource() resource.Resource {
	return &TableResource{}
}

func (r *TableResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_table"
}

func (r *TableResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	joinStepBlock := schema.ListNestedBlock{
		NestedObject: schema.NestedBlockObject{
			Attributes: map[string]schema.Attribute{
				"table": schema.StringAttribute{
					Description: "Dataverse table name for this step.",
					Required:    true,
				},
				"from": schema.StringAttribute{
					Description: "Navigation property or lookup field to follow.",
					Required:    true,
				},
				"key": schema.StringAttribute{
					Description: "Primary key field on the target table.",
					Required:    true,
				},
				"reverse": schema.BoolAttribute{
					Description: "If true, `from` is a collection-valued navigation property; this " +
						"step compiles to an OData any() lambda that scopes an ownerless child " +
						"through the parent that references it (e.g. booking via servicebooking).",
					Optional: true,
				},
			},
		},
	}

	fieldTypeValidator := stringvalidator.OneOf(
		"string", "number", "datetime", "boolean", "lookup", "choice",
	)

	resp.Schema = schema.Schema{
		Description: "Manages a table schema in the Dataverse Contact API. " +
			"The schema is saved as a draft and immediately published.",
		Attributes: map[string]schema.Attribute{
			// Identity & computed
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
			"source": schema.StringAttribute{
				Description: "The source of the published schema (\"published\", \"built-in\").",
				Computed:    true,
			},
			"field_count": schema.Int64Attribute{
				Description: "Number of fields defined in the schema.",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					fieldCountFromFields{},
				},
			},

			// Required attributes
			"dataverse_table": schema.StringAttribute{
				Description: "The Dataverse OData entity set name (e.g. \"incidents\").",
				Required:    true,
			},
			"dataverse_logical_name": schema.StringAttribute{
				Description: "The Dataverse entity logical name (e.g. \"incident\"). " +
					"If omitted, derived by singularizing dataverse_table.",
				Optional: true,
				Computed: true,
			},
			"primary_key": schema.StringAttribute{
				Description: "The primary key field name.",
				Required:    true,
			},
			"required_permission": schema.StringAttribute{
				Description: "The base permission name (e.g. \"case\"). " +
					"If omitted, defaults to route_name.",
				Optional: true,
				Computed: true,
			},
			"default_select": schema.ListAttribute{
				Description: "Default $select fields when client doesn't specify.",
				Required:    true,
				ElementType: types.StringType,
			},
			"lookup_fields": schema.ListAttribute{
				Description: "Fields returned by the /lookup route.",
				Required:    true,
				ElementType: types.StringType,
			},

			// Optional attributes
			"description": schema.StringAttribute{
				Description: "Human-readable description of this table.",
				Optional:    true,
			},
			"icon": schema.StringAttribute{
				Description: "Icon filename (e.g. \"incident.svg\").",
				Optional:    true,
			},
			"permission_group": schema.StringAttribute{
				Description: "Permission group name for shared permissions.",
				Optional:    true,
			},
			"fetch_xml": schema.StringAttribute{
				Description: "FetchXML template for custom list queries.",
				Optional:    true,
			},
			"aliases": schema.ListAttribute{
				Description: "Optional route aliases.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"lookup_search_contains": schema.ListAttribute{
				Description: "Lookup fields that use 'contains' instead of 'startswith'.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"filters": schema.ListAttribute{
				Description: "Always-on filter expressions applied to every query. " +
					"Defaults to [\"statecode eq 0\"].",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"public_choices": schema.BoolAttribute{
				Description: "Whether choice/option set values are publicly accessible. Defaults to true.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"public_read": schema.BoolAttribute{
				Description: "Whether this table is publicly readable without authentication.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"business_process": businessProcessAttribute("this table's own"),
			"public_create": schema.BoolAttribute{
				Description: "Whether unauthenticated POST is allowed on the public tier.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},

			// Fields — map keyed by Dataverse logical name
			"fields": schema.MapNestedAttribute{
				Description: "Field definitions keyed by Dataverse logical name.",
				Required:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							Description: "Field data type: \"string\", \"number\", \"datetime\", " +
								"\"boolean\", \"lookup\" or \"choice\".",
							Required: true,
							Validators: []validator.String{
								fieldTypeValidator,
							},
						},
						"description": schema.StringAttribute{
							Description: "Human-readable field description.",
							Required:    true,
						},
						"read_only": schema.BoolAttribute{
							Description: "If true, field is read-only (not writable via PATCH).",
							Optional:    true,
						},
						"lookup_table": schema.StringAttribute{
							Description: "For lookup fields: the route name of the target table.",
							Optional:    true,
						},
						"value_field": schema.StringAttribute{
							Description: "For polymorphic lookups: the underlying OData value column name.",
							Optional:    true,
						},
						"bind_field": schema.StringAttribute{
							Description: "For aliased fields: the navigation property for @odata.bind writes.",
							Optional:    true,
						},
					},
				},
			},
		},

		Blocks: map[string]schema.Block{
			"contact_join_step": func() schema.Block {
				b := joinStepBlock
				b.Description = "Steps to join back to the contact from this table (ordered)."
				return b
			}(),
			"team_join_step": func() schema.Block {
				b := joinStepBlock
				b.Description = "Steps to join back to the account for team-scoped queries (ordered)."
				return b
			}(),
			"alternate_contact_join_path": schema.ListNestedBlock{
				Description: "Additional join paths to the contact (OR'd with contact_join_step).",
				NestedObject: schema.NestedBlockObject{
					Blocks: map[string]schema.Block{
						"step": func() schema.Block {
							b := joinStepBlock
							b.Description = "Steps in this alternate path (ordered)."
							return b
						}(),
					},
				},
			},
			"create_default": schema.ListNestedBlock{
				Description: "Lookup fields automatically bound when creating a record.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"field": schema.StringAttribute{
							Description: "The navigation property / lookup field name.",
							Required:    true,
						},
						"bind_to": schema.StringAttribute{
							Description: "What to bind it to: \"contact\" or \"account\".",
							Required:    true,
							Validators: []validator.String{
								stringvalidator.OneOf("contact", "account"),
							},
						},
						"entity_set": schema.StringAttribute{
							Description: "The Dataverse entity set for the @odata.bind URL.",
							Required:    true,
						},
					},
				},
			},
			"parent_table": schema.SingleNestedBlock{
				Description: "Parent table relationship for child record tables.",
				Attributes: map[string]schema.Attribute{
					"table": schema.StringAttribute{
						Description: "Route name of the parent table.",
						Optional:    true,
					},
					"navigation_property": schema.StringAttribute{
						Description: "OData navigation property from this table to the parent.",
						Optional:    true,
					},
				},
			},
			"polymorphic_lookup": schema.SingleNestedBlock{
				Description: "Publish every target of a polymorphic lookup on this table as a route " +
					"of its own, derived by the API from live Dataverse metadata — instead of declaring " +
					"one dataversecontact_table per target.\n\n" +
					"Scoping needs no declaring: a target row has no lookup back to the caller, so its " +
					"path is the reverse hop into this table (read from metadata) followed by this " +
					"table's own contact_join_step chain. Grant the whole family in one go by adding " +
					"required_permission to dataversecontact_permissions_sync.default_permissions.\n\n" +
					"A target's columns are published without anyone reviewing them — that is the trade " +
					"for never going stale. Fields are read-only by default; target_prefix and " +
					"exclude_targets bound the family if the lookup reaches further than intended.",
				Attributes: map[string]schema.Attribute{
					"field": schema.StringAttribute{
						Description: "The polymorphic lookup on this table whose targets become routes " +
							"(e.g. \"sb_service_recordid\"). Must also be declared in `fields` as a lookup, " +
							"or ?expand=<field> cannot be rewritten onto the concrete targets.",
						Optional: true,
					},
					"required_permission": schema.StringAttribute{
						Description: "Permission subject every derived route requires (e.g. \"servicerecord\"). " +
							"Use this same name as the key in default_permissions — the API fans it out onto " +
							"each derived route, which is what makes one entry cover the whole family.",
						Optional: true,
					},
					"route_prefix_strip": schema.StringAttribute{
						Description: "Prefix removed from a target's logical name to form its route name " +
							"(\"sb_\" turns sb_missed_bin into the route missed_bin). Also bounds which " +
							"single-target lookups are inlined as expands, unless target_prefix is set.",
						Optional: true,
					},
					"target_prefix": schema.StringAttribute{
						Description: "Only publish targets whose logical name starts with this. Unset " +
							"publishes every target the lookup reaches.",
						Optional: true,
					},
					"exclude_targets": schema.ListAttribute{
						Description: "Logical names never published, however they match.",
						ElementType: types.StringType,
						Optional:    true,
					},
					"read_only": schema.BoolAttribute{
						Description: "Mark every derived field read-only. Defaults to true — a rule that " +
							"publishes tables nobody reviewed should not also open them for writing.",
						Optional: true,
					},
					"business_process": businessProcessAttribute("each target's"),
				},
			},
			"expand": schema.ListNestedBlock{
				Description: "Expandable lookup fields — one level deep into related tables.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"lookup_field": schema.StringAttribute{
							Description: "Navigation property / lookup field name.",
							Required:    true,
						},
						"related_table": schema.StringAttribute{
							Description: "Related Dataverse table name.",
							Required:    true,
						},
					},
					Blocks: map[string]schema.Block{
						"field": schema.ListNestedBlock{
							Description: "Fields available when expanding this lookup.",
							NestedObject: schema.NestedBlockObject{
								Attributes: map[string]schema.Attribute{
									"name": schema.StringAttribute{
										Description: "Dataverse field name.",
										Required:    true,
									},
									"type": schema.StringAttribute{
										Description: "Field data type: \"string\", \"number\", \"datetime\", " +
											"\"boolean\", \"lookup\" or \"choice\".",
										Required: true,
										Validators: []validator.String{
											fieldTypeValidator,
										},
									},
									"description": schema.StringAttribute{
										Description: "Human-readable field description.",
										Required:    true,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// ValidateConfig catches cross-field mistakes the per-attribute validators
// can't: a lookup field without its target table, and a half-filled
// parent_table block (whose attributes must stay Optional because a
// SingleNestedBlock's Required attributes error even when the block is omitted).
func (r *TableResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config TableResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The field a polymorphic_lookup rule names is exempt from the
	// lookup_table requirement below. It CANNOT have one: a polymorphic lookup
	// points at many tables — that is the entire reason the rule exists — and
	// naming one of them would be a claim the data contradicts. Dataverse says
	// which target a given row used, in the lookuplogicalname annotation the
	// API surfaces as `<field>_logicalname`.
	polymorphicField := ""
	if config.PolymorphicLookup != nil &&
		!config.PolymorphicLookup.Field.IsNull() && !config.PolymorphicLookup.Field.IsUnknown() {
		polymorphicField = config.PolymorphicLookup.Field.ValueString()
	}

	if !config.Fields.IsNull() && !config.Fields.IsUnknown() {
		for name, val := range config.Fields.Elements() {
			obj, ok := val.(types.Object)
			if !ok || obj.IsNull() || obj.IsUnknown() {
				continue
			}
			attrs := obj.Attributes()
			fieldType, _ := attrs["type"].(types.String)
			lookupTable, _ := attrs["lookup_table"].(types.String)
			if fieldType.IsNull() || fieldType.IsUnknown() {
				continue
			}
			switch {
			case name == polymorphicField && fieldType.ValueString() == "lookup":
				// Exempt — see above.
			case fieldType.ValueString() == "lookup" && lookupTable.IsNull():
				resp.Diagnostics.AddAttributeError(
					path.Root("fields").AtMapKey(name).AtName("lookup_table"),
					"Lookup field missing lookup_table",
					fmt.Sprintf("Field %q has type \"lookup\" but no lookup_table. "+
						"Set lookup_table to the route name of the table the lookup points at "+
						"(e.g. \"contact\").", name),
				)
			case fieldType.ValueString() != "lookup" && !lookupTable.IsNull() && !lookupTable.IsUnknown():
				resp.Diagnostics.AddAttributeWarning(
					path.Root("fields").AtMapKey(name).AtName("lookup_table"),
					"lookup_table set on a non-lookup field",
					fmt.Sprintf("Field %q has type %q, so its lookup_table is ignored. "+
						"Either set type = \"lookup\" or remove lookup_table.", name, fieldType.ValueString()),
				)
			}
		}
	}

	// A progress object lands on the row beside its fields, so a name that is
	// also a field would overwrite that field on every read. The API drops such
	// a binding with a warning in its logs; better to say so here, at plan time.
	exposeAsOf := func(bp *BusinessProcessModel) string {
		if bp == nil {
			return ""
		}
		if bp.ExposeAs.IsNull() || bp.ExposeAs.IsUnknown() {
			return "progress"
		}
		return bp.ExposeAs.ValueString()
	}
	own := exposeAsOf(config.BusinessProcess)
	via := ""
	if config.PolymorphicLookup != nil {
		via = exposeAsOf(config.PolymorphicLookup.BusinessProcess)
	}
	if own != "" && own == via {
		resp.Diagnostics.AddAttributeError(
			path.Root("polymorphic_lookup").AtName("business_process").AtName("expose_as"),
			"Two business processes under one name",
			fmt.Sprintf("Both business_process and polymorphic_lookup.business_process would expose %q. "+
				"Give one of them a different expose_as.", own),
		)
	}
	if !config.Fields.IsNull() && !config.Fields.IsUnknown() {
		for name := range config.Fields.Elements() {
			if name == own {
				resp.Diagnostics.AddAttributeWarning(
					path.Root("business_process").AtName("expose_as"),
					"business_process would overwrite a field",
					fmt.Sprintf("%q is also a field on this table; the API will not expose the process under that name.", name),
				)
			}
			if name == via {
				resp.Diagnostics.AddAttributeWarning(
					path.Root("polymorphic_lookup").AtName("business_process").AtName("expose_as"),
					"business_process would overwrite a field",
					fmt.Sprintf("%q is also a field on this table; the API will not expose the process under that name.", name),
				)
			}
		}
	}

	if pt := config.ParentTable; pt != nil {
		if pt.Table.IsNull() || pt.NavigationProperty.IsNull() {
			resp.Diagnostics.AddAttributeError(
				path.Root("parent_table"),
				"Incomplete parent_table block",
				"A parent_table block needs both table (the parent's route name) and "+
					"navigation_property (the OData navigation property from this table to the parent).",
			)
		}
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
	schemaJSON := modelToSchemaJSON(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

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
	schemaJSON := modelToSchemaJSON(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

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
	model.Scope = types.StringValue(scope)
	model.Source = types.StringValue(tableResp.Source)

	// Parse the JSON schema into the HCL model
	schemaJSONToModel(ctx, tableResp.Schema, model, diagnostics)
}

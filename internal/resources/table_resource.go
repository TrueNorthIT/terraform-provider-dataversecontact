package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
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
	Files                *FilesModel           `tfsdk:"files"`

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
	Match   types.Map    `tfsdk:"match"`
}

// FilesModel declares downloadable files on a table's rows. Nested attribute,
// so it is nil when unset.
type FilesModel struct {
	Table        types.String `tfsdk:"table"`
	PrimaryKey   types.String `tfsdk:"primary_key"`
	ParentColumn types.String `tfsdk:"parent_column"`
	Content      types.String `tfsdk:"content"`
	FileName     types.String `tfsdk:"file_name"`
	MimeType     types.String `tfsdk:"mime_type"`
	Size         types.String `tfsdk:"size"`
}

// logicalNamePattern is what the API accepts for a column or entity set
// name in `files` and in a join step's `match`.
var logicalNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// matchElementType is a join step match's value type: column → option-set values.
var matchElementType = types.ListType{ElemType: types.Int64Type}

func logicalName(description string, required bool) schema.StringAttribute {
	return schema.StringAttribute{
		Description: description,
		Required:    required,
		Optional:    !required,
		Validators: []validator.String{
			stringvalidator.RegexMatches(logicalNamePattern, "must be a Dataverse logical name (lowercase letters, digits, underscores)"),
		},
	}
}

func filesAttribute() schema.SingleNestedAttribute {
	child := func(description string, others ...string) schema.StringAttribute {
		attr := logicalName(description, false)
		paths := make([]path.Expression, len(others))
		for i, other := range others {
			paths[i] = path.MatchRelative().AtParent().AtName(other)
		}
		attr.Validators = append(attr.Validators, stringvalidator.AlsoRequires(paths...))
		return attr
	}
	return schema.SingleNestedAttribute{
		Description: "Downloadable files on this table's rows (a note's attachment, an email's " +
			"attachments). Callers list them with GET /{tier}/{route}/{id}/files and get a " +
			"short-lived download link with POST .../files/{fileId}/link; the content is never " +
			"returned inline. Leave out table, primary_key and parent_column when the row is the " +
			"file; set all three when the files are rows of a child table. A file on the row " +
			"itself drops its content column from the table's fields. Needs Contact API 1.31.0 or " +
			"later: an older API drops this key without an error.",
		Optional: true,
		Attributes: map[string]schema.Attribute{
			"table":         child("Child files: the child table's entity set (e.g. \"activitymimeattachments\").", "primary_key", "parent_column"),
			"primary_key":   child("Child files: the child table's primary key (e.g. \"activitymimeattachmentid\").", "table", "parent_column"),
			"parent_column": child("Child files: the lookup on the child that points at this row (e.g. \"objectid\").", "table", "primary_key"),
			"content":       logicalName("Column holding the base64 content (e.g. \"documentbody\", \"body\").", true),
			"file_name":     logicalName("Column holding the file name.", true),
			"mime_type":     logicalName("Column holding the MIME type.", true),
			"size":          logicalName("Column holding the size in bytes.", true),
		},
	}
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

// bindFieldFromState plans an unset bind_field as what the API will read
// back (see plannedBindField).
type bindFieldFromState struct{}

func (m bindFieldFromState) Description(_ context.Context) string {
	return "Plans an unset bind_field as the API will read it back."
}

func (m bindFieldFromState) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m bindFieldFromState) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if !req.ConfigValue.IsNull() || req.State.Raw.IsNull() {
		return
	}

	field := req.Path.ParentPath()
	omits, diags := apiOmitsDerived(ctx, req.Private)
	resp.Diagnostics.Append(diags...)
	removed, diags := bindFieldRemoved(ctx, req.Private, field)
	resp.Diagnostics.Append(diags...)
	if removed {
		// Taken out of config: Terraform would carry the old value forward
		// unchanged, so the removal would never reach the API. The API reads
		// back what it then derives, or nothing.
		if omits {
			resp.PlanValue = types.StringNull()
		} else {
			resp.PlanValue = types.StringUnknown()
		}
		return
	}
	if !req.PlanValue.IsUnknown() {
		return
	}

	planned := map[string]types.String{}
	unchanged := true
	for _, name := range []string{"type", "lookup_table"} {
		var value, prior types.String
		resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, field.AtName(name), &value)...)
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, field.AtName(name), &prior)...)
		if resp.Diagnostics.HasError() {
			return
		}
		// A null prior type means the field is new to this table.
		if (name == "type" && prior.IsNull()) || !value.Equal(prior) {
			unchanged = false
		}
		planned[name] = value
	}
	derivable := planned["type"].ValueString() == "lookup" && !planned["lookup_table"].IsNull()
	resp.PlanValue = plannedBindField(omits, unchanged, derivable, req.StateValue)
}

// plannedBindField is what an unset bind_field will read back as after apply.
//
// An API that keeps what it derives out of the schema (Contact API #465)
// reads back what was sent: the prior value while the field is unchanged,
// since the provider sends it again, and otherwise nothing.
//
// An older API returns what it derives inside the schema. It derives a
// lookup's bind_field from the attribute's navigation property, so the prior
// value holds while the field is unchanged and is unknown once it changes. A
// null prior on a lookup with a lookup_table is unknown too: that API can
// derive one on any save, so a table last saved before it started deriving
// gets a bind_field the next time anything else changes (#30).
func plannedBindField(apiOmitsDerived, unchanged, derivable bool, prior types.String) types.String {
	switch {
	case apiOmitsDerived && unchanged:
		return prior
	case apiOmitsDerived:
		return types.StringNull()
	case !unchanged:
		return types.StringUnknown()
	case prior.IsNull() && derivable:
		return types.StringUnknown()
	default:
		return prior
	}
}

// apiOmitsDerivedKey is the private state key where readIntoModel records
// whether the API answered with `derived`, i.e. keeps what it derives out of
// the schema.
const apiOmitsDerivedKey = "api_omits_derived"

func apiOmitsDerived(ctx context.Context, private interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
}) (bool, diag.Diagnostics) {
	value, diags := private.GetKey(ctx, apiOmitsDerivedKey)
	return string(value) == "true", diags
}

// configuredBindFieldsKey is the private state key listing the fields whose
// bind_field the config set at the last apply.
const configuredBindFieldsKey = "configured_bind_fields"

// bindFieldRemoved reports whether the config set this field's bind_field at
// the last apply. State from before the key existed has no list, so nothing
// counts as removed and an upgrade plans no change.
func bindFieldRemoved(ctx context.Context, private interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
}, field path.Path) (bool, diag.Diagnostics) {
	raw, diags := private.GetKey(ctx, configuredBindFieldsKey)
	if diags.HasError() || raw == nil {
		return false, diags
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return false, diags
	}
	key, _ := field.Steps().LastStep()
	name, ok := key.(path.PathStepElementKeyString)
	return ok && slices.Contains(names, string(name)), diags
}

// recordConfiguredBindFields stores which fields set bind_field in config.
func recordConfiguredBindFields(ctx context.Context, private interface {
	SetKey(context.Context, string, []byte) diag.Diagnostics
}, config types.Map) diag.Diagnostics {
	names := []string{}
	for name, value := range config.Elements() {
		obj, ok := value.(types.Object)
		if !ok || obj.IsNull() || obj.IsUnknown() {
			continue
		}
		if bind, ok := obj.Attributes()["bind_field"].(types.String); ok && !bind.IsNull() {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	raw, _ := json.Marshal(names)
	return private.SetKey(ctx, configuredBindFieldsKey, raw)
}

// keepPlannedBindFields puts back, as planned, each bind_field the config
// leaves unset that the plan knew. The plan follows what the last read said
// about the API (plannedBindField); an instance that behaves the other way
// can answer the apply (old and new API side by side during a deploy, or a
// saved plan applied after a rollback) and read back something else, which
// Terraform would fail as an inconsistent result. The next refresh reads the
// real value, and an unchanged field keeps it without a diff.
func keepPlannedBindFields(read, planned, config types.Map) (types.Map, diag.Diagnostics) {
	plannedFields, configFields := planned.Elements(), config.Elements()
	elements := map[string]attr.Value{}
	changed := false
	for name, value := range read.Elements() {
		elements[name] = value
		readObj, ok1 := value.(types.Object)
		plannedObj, ok2 := plannedFields[name].(types.Object)
		configObj, ok3 := configFields[name].(types.Object)
		if !ok1 || !ok2 || !ok3 || readObj.IsNull() || plannedObj.IsNull() || configObj.IsNull() {
			continue
		}
		want, _ := plannedObj.Attributes()["bind_field"].(types.String)
		got, _ := readObj.Attributes()["bind_field"].(types.String)
		set, _ := configObj.Attributes()["bind_field"].(types.String)
		if !set.IsNull() || want.IsUnknown() || want.Equal(got) {
			continue
		}
		attrs := readObj.Attributes()
		attrs["bind_field"] = want
		obj, diags := types.ObjectValue(fieldAttrTypes(), attrs)
		if diags.HasError() {
			return read, diags
		}
		elements[name] = obj
		changed = true
	}
	if !changed {
		return read, nil
	}
	return types.MapValue(fieldObjectType(), elements)
}

// settleBindFields finishes a Create or Update's read-back: the planned
// bind_fields kept (keepPlannedBindFields), and the configured ones recorded.
func settleBindFields(ctx context.Context, model *TableResourceModel, planned types.Map, config tfsdk.Config, private interface {
	SetKey(context.Context, string, []byte) diag.Diagnostics
}, diagnostics *diag.Diagnostics) {
	var configured types.Map
	diagnostics.Append(config.GetAttribute(ctx, path.Root("fields"), &configured)...)
	if diagnostics.HasError() {
		return
	}
	fields, diags := keepPlannedBindFields(model.Fields, planned, configured)
	diagnostics.Append(diags...)
	model.Fields = fields
	diagnostics.Append(recordConfiguredBindFields(ctx, private, configured)...)
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
				"match": schema.MapAttribute{
					Description: "Reverse steps only: conditions the related row must also meet, " +
						"inside the same any() lambda — column logical name to the option-set " +
						"values it may have, e.g. { participationtypemask = [1, 2, 3, 4] } so an " +
						"email counts only when the contact is its sender or a recipient. Needs Contact API " +
						"1.31.0 or later: an older API drops match without an error and joins on every " +
						"related row, which for emails includes the Regarding party.",
					Optional:    true,
					ElementType: matchElementType,
					Validators: []validator.Map{
						mapvalidator.KeysAre(stringvalidator.RegexMatches(logicalNamePattern, "must be a column logical name")),
						mapvalidator.ValueListsAre(listvalidator.SizeAtLeast(1)),
					},
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
			// aliases and lookup_search_contains read back as [] when the API
			// has none, so they are Computed: omitting them or writing [] must
			// both apply without an inconsistent-result error.
			"aliases": schema.ListAttribute{
				Description: "Optional route aliases.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"lookup_search_contains": schema.ListAttribute{
				Description: "Lookup fields that use 'contains' instead of 'startswith'.",
				Optional:    true,
				Computed:    true,
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
			"files":            filesAttribute(),
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
							Description: "For aliased fields: the navigation property for @odata.bind writes. " +
								"If omitted, the API derives it from the lookup's navigation property. An API that " +
								"reports derived values separately (Contact API with admin GET `derived`) keeps them out of state.",
							Optional: true,
							Computed: true,
							PlanModifiers: []planmodifier.String{
								bindFieldFromState{},
							},
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
			readOnly, _ := attrs["read_only"].(types.Bool)
			if fieldType.IsNull() || fieldType.IsUnknown() {
				continue
			}
			switch {
			case name == polymorphicField && fieldType.ValueString() == "lookup":
				// Exempt — see above.
			case fieldType.ValueString() == "lookup" && lookupTable.IsNull() && !readOnly.ValueBool():
				// A write binds the lookup through its target's entity set, which
				// the API finds from lookup_table. A read-only lookup never writes,
				// and a polymorphic one such as customerid has no single target.
				resp.Diagnostics.AddAttributeError(
					path.Root("fields").AtMapKey(name).AtName("lookup_table"),
					"Lookup field missing lookup_table",
					fmt.Sprintf("Field %q is a writable lookup with no lookup_table. "+
						"Set lookup_table to the route name of the table the lookup points at "+
						"(e.g. \"contact\"), or set read_only = true if the portal never writes it.", name),
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

	planned := plan.Fields
	r.readIntoModel(ctx, scope, routeName, &plan, resp.Private, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	settleBindFields(ctx, &plan, planned, req.Config, resp.Private, &resp.Diagnostics)
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

	found := r.readIntoModel(ctx, scope, routeName, &state, resp.Private, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		// Deleted outside Terraform: drop it so the next plan re-creates it.
		resp.State.RemoveResource(ctx)
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

	planned := plan.Fields
	r.readIntoModel(ctx, scope, routeName, &plan, resp.Private, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	settleBindFields(ctx, &plan, planned, req.Config, resp.Private, &resp.Diagnostics)
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

// readIntoModel reads the current table state from the API and populates the
// model, and records in private state whether the API keeps derived values
// out of the schema (see plannedBindField).
func (r *TableResource) readIntoModel(ctx context.Context, scope, routeName string, model *TableResourceModel, private interface {
	SetKey(context.Context, string, []byte) diag.Diagnostics
}, diagnostics *diag.Diagnostics) (found bool) {
	tableResp, err := r.client.GetTable(ctx, scope, routeName)
	if err != nil {
		if client.IsNotFound(err) {
			diagnostics.AddWarning("Table not found",
				fmt.Sprintf("Table %s/%s not found, removing from state", scope, routeName))
			return false
		}
		diagnostics.AddError("Failed to read table", err.Error())
		return false
	}

	model.ID = types.StringValue(fmt.Sprintf("%s/%s", scope, routeName))
	model.Scope = types.StringValue(scope)
	model.Source = types.StringValue(tableResp.Source)
	diagnostics.Append(private.SetKey(ctx, apiOmitsDerivedKey, []byte(strconv.FormatBool(tableResp.Derived != nil)))...)

	// Parse the JSON schema into the HCL model
	schemaJSONToModel(ctx, tableResp.Schema, model, diagnostics)
	return true
}

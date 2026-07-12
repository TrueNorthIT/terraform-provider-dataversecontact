package resources

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// ── JSON wire-format structs (match the API's SchemaHint shape) ─────────

// SchemaHintJSON is the top-level JSON blob sent to / received from the API.
type SchemaHintJSON struct {
	RouteName                 string                   `json:"routeName"`
	Description               string                   `json:"description,omitempty"`
	Icon                      string                   `json:"icon,omitempty"`
	DataverseTable            string                   `json:"dataverseTable"`
	DataverseLogicalName      string                   `json:"dataverseLogicalName"`
	RequiredPermission        string                   `json:"requiredPermission"`
	PrimaryKey                string                   `json:"primaryKey"`
	Aliases                   []string                 `json:"aliases,omitempty"`
	DefaultSelect             []string                 `json:"defaultSelect"`
	ContactJoinPath           []JoinStepJSON           `json:"contactJoinPath,omitempty"`
	AlternateContactJoinPaths [][]JoinStepJSON         `json:"alternateContactJoinPaths,omitempty"`
	TeamJoinPath              []JoinStepJSON           `json:"teamJoinPath,omitempty"`
	CreateDefaults            []CreateDefaultJSON      `json:"createDefaults,omitempty"`
	LookupFields              []string                 `json:"lookupFields"`
	LookupSearchContains      []string                 `json:"lookupSearchContains,omitempty"`
	Filters                   []string                 `json:"filters,omitempty"`
	ParentTable               *ParentTableJSON         `json:"parentTable,omitempty"`
	Expands                   []ExpandJSON             `json:"expands,omitempty"`
	PublicChoices             *bool                    `json:"publicChoices,omitempty"`
	PublicRead                *bool                    `json:"publicRead,omitempty"`
	PublicCreate              *bool                    `json:"publicCreate,omitempty"`
	PermissionGroup           string                   `json:"permissionGroup,omitempty"`
	FetchXml                  string                   `json:"fetchXml,omitempty"`
	Fields                    map[string]FieldHintJSON `json:"fields"`
}

// JoinStepJSON is a single step in a contact/team join path.
type JoinStepJSON struct {
	Table   string `json:"table"`
	From    string `json:"from"`
	Key     string `json:"key"`
	Reverse bool   `json:"reverse,omitempty"`
}

// CreateDefaultJSON is a lookup field auto-bound on create.
type CreateDefaultJSON struct {
	Field     string `json:"field"`
	BindTo    string `json:"bindTo"`
	EntitySet string `json:"entitySet"`
}

// ParentTableJSON describes the parent table relationship.
type ParentTableJSON struct {
	Table              string `json:"table"`
	NavigationProperty string `json:"navigationProperty"`
}

// ExpandJSON is an expandable lookup into a related table.
type ExpandJSON struct {
	LookupField  string            `json:"lookupField"`
	RelatedTable string            `json:"relatedTable"`
	Fields       []ExpandFieldJSON `json:"fields"`
}

// ExpandFieldJSON is a single field within an expand definition.
type ExpandFieldJSON struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

// FieldHintJSON is a single field definition in the schema.
type FieldHintJSON struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	ReadOnly    bool   `json:"readOnly,omitempty"`
	LookupTable string `json:"lookupTable,omitempty"`
	ValueField  string `json:"valueField,omitempty"`
	BindField   string `json:"bindField,omitempty"`
}

// ── HCL → JSON (for Create / Update) ───────────────────────────────────

// modelToSchemaJSON converts the HCL resource model into a JSON blob for the API.
func modelToSchemaJSON(ctx context.Context, model *TableResourceModel, diags *diag.Diagnostics) json.RawMessage {
	// Apply defaults for optional-with-computed fields
	logicalName := model.DataverseLogicalName.ValueString()
	if logicalName == "" {
		logicalName = singularize(model.DataverseTable.ValueString())
	}
	requiredPermission := model.RequiredPermission.ValueString()
	if requiredPermission == "" {
		requiredPermission = model.RouteName.ValueString()
	}

	hint := SchemaHintJSON{
		RouteName:            model.RouteName.ValueString(),
		Description:          model.Description.ValueString(),
		Icon:                 model.Icon.ValueString(),
		DataverseTable:       model.DataverseTable.ValueString(),
		DataverseLogicalName: logicalName,
		RequiredPermission:   requiredPermission,
		PrimaryKey:           model.PrimaryKey.ValueString(),
		PermissionGroup:      model.PermissionGroup.ValueString(),
		FetchXml:             model.FetchXml.ValueString(),
	}

	// Simple string lists
	hint.DefaultSelect = tfListToStrings(ctx, model.DefaultSelect, diags)
	hint.LookupFields = tfListToStrings(ctx, model.LookupFields, diags)
	hint.Aliases = tfListToStrings(ctx, model.Aliases, diags)
	hint.LookupSearchContains = tfListToStrings(ctx, model.LookupSearchContains, diags)
	hint.Filters = tfListToStrings(ctx, model.Filters, diags)

	// Default filters to ["statecode eq 0"] if not specified
	if hint.Filters == nil {
		hint.Filters = []string{"statecode eq 0"}
	}

	// Booleans with non-false defaults
	if !model.PublicChoices.IsNull() && !model.PublicChoices.IsUnknown() {
		v := model.PublicChoices.ValueBool()
		hint.PublicChoices = &v
	}
	if !model.PublicRead.IsNull() && !model.PublicRead.IsUnknown() {
		v := model.PublicRead.ValueBool()
		if v {
			hint.PublicRead = &v
		}
	}
	if !model.PublicCreate.IsNull() && !model.PublicCreate.IsUnknown() {
		v := model.PublicCreate.ValueBool()
		if v {
			hint.PublicCreate = &v
		}
	}

	// Fields map
	hint.Fields = fieldsModelToJSON(ctx, model.Fields, diags)

	// Blocks: join steps
	hint.ContactJoinPath = joinStepsModelToJSON(ctx, model.ContactJoinStep, diags)
	hint.TeamJoinPath = joinStepsModelToJSON(ctx, model.TeamJoinStep, diags)

	// Alternate contact join paths
	hint.AlternateContactJoinPaths = alternateJoinPathsModelToJSON(ctx, model.AlternateContactJoinPath, diags)

	// Create defaults
	hint.CreateDefaults = createDefaultsModelToJSON(ctx, model.CreateDefault, diags)

	// Parent table — SingleNestedBlock is always non-nil; check inner values
	if model.ParentTable != nil && !model.ParentTable.Table.IsNull() && !model.ParentTable.Table.IsUnknown() {
		hint.ParentTable = &ParentTableJSON{
			Table:              model.ParentTable.Table.ValueString(),
			NavigationProperty: model.ParentTable.NavigationProperty.ValueString(),
		}
	}

	// Expands
	hint.Expands = expandsModelToJSON(ctx, model.Expand, diags)

	if diags.HasError() {
		return nil
	}

	data, err := json.Marshal(hint)
	if err != nil {
		diags.AddError("Failed to marshal schema JSON", err.Error())
		return nil
	}
	return data
}

// ── JSON → HCL (for Read / Import) ─────────────────────────────────────

// schemaJSONToModel populates the HCL model from the API's JSON blob.
// It preserves scope (not in JSON) and overwrites everything else.
func schemaJSONToModel(ctx context.Context, raw json.RawMessage, model *TableResourceModel, diags *diag.Diagnostics) {
	var hint SchemaHintJSON
	if err := json.Unmarshal(raw, &hint); err != nil {
		diags.AddError("Failed to parse schema JSON", err.Error())
		return
	}

	model.RouteName = types.StringValue(hint.RouteName)
	model.DataverseTable = types.StringValue(hint.DataverseTable)
	model.DataverseLogicalName = types.StringValue(hint.DataverseLogicalName)
	model.RequiredPermission = types.StringValue(hint.RequiredPermission)
	model.PrimaryKey = types.StringValue(hint.PrimaryKey)
	model.Description = stringOrNull(hint.Description)
	model.Icon = stringOrNull(hint.Icon)
	model.PermissionGroup = stringOrNull(hint.PermissionGroup)
	model.FetchXml = stringOrNull(hint.FetchXml)

	// Simple lists
	model.DefaultSelect = stringsToTFList(hint.DefaultSelect)
	model.LookupFields = stringsToTFList(hint.LookupFields)
	model.Aliases = stringsToTFListOrNull(hint.Aliases)
	model.LookupSearchContains = stringsToTFList(hint.LookupSearchContains)
	model.Filters = stringsToTFList(hint.Filters)

	// Booleans
	if hint.PublicChoices != nil {
		model.PublicChoices = types.BoolValue(*hint.PublicChoices)
	} else {
		model.PublicChoices = types.BoolValue(true) // API default
	}
	if hint.PublicRead != nil {
		model.PublicRead = types.BoolValue(*hint.PublicRead)
	} else {
		model.PublicRead = types.BoolValue(false)
	}
	if hint.PublicCreate != nil {
		model.PublicCreate = types.BoolValue(*hint.PublicCreate)
	} else {
		model.PublicCreate = types.BoolValue(false)
	}

	// Fields map
	model.Fields = fieldsJSONToModel(ctx, hint.Fields, diags)

	// Computed
	model.FieldCount = types.Int64Value(int64(len(hint.Fields)))

	// Join steps
	model.ContactJoinStep = joinStepsJSONToModel(hint.ContactJoinPath)
	model.TeamJoinStep = joinStepsJSONToModel(hint.TeamJoinPath)

	// Alternate contact join paths
	model.AlternateContactJoinPath = alternateJoinPathsJSONToModel(hint.AlternateContactJoinPaths)

	// Create defaults
	model.CreateDefault = createDefaultsJSONToModel(hint.CreateDefaults)

	// Parent table
	if hint.ParentTable != nil {
		model.ParentTable = &ParentTableModel{
			Table:              types.StringValue(hint.ParentTable.Table),
			NavigationProperty: types.StringValue(hint.ParentTable.NavigationProperty),
		}
	} else {
		model.ParentTable = nil
	}

	// Expands
	model.Expand = expandsJSONToModel(ctx, hint.Expands, diags)
}

// ── Fields helpers ──────────────────────────────────────────────────────

func fieldsModelToJSON(ctx context.Context, fields types.Map, diags *diag.Diagnostics) map[string]FieldHintJSON {
	if fields.IsNull() || fields.IsUnknown() {
		return nil
	}

	result := make(map[string]FieldHintJSON)

	elements := fields.Elements()
	for key, val := range elements {
		obj, ok := val.(types.Object)
		if !ok {
			continue
		}
		attrs := obj.Attributes()
		f := FieldHintJSON{}
		if v, ok := attrs["type"].(types.String); ok {
			f.Type = v.ValueString()
		}
		if v, ok := attrs["description"].(types.String); ok {
			f.Description = v.ValueString()
		}
		if v, ok := attrs["read_only"].(types.Bool); ok && !v.IsNull() && !v.IsUnknown() {
			f.ReadOnly = v.ValueBool()
		}
		if v, ok := attrs["lookup_table"].(types.String); ok && !v.IsNull() && !v.IsUnknown() {
			f.LookupTable = v.ValueString()
		}
		if v, ok := attrs["value_field"].(types.String); ok && !v.IsNull() && !v.IsUnknown() {
			f.ValueField = v.ValueString()
		}
		if v, ok := attrs["bind_field"].(types.String); ok && !v.IsNull() && !v.IsUnknown() {
			f.BindField = v.ValueString()
		}
		result[key] = f
	}
	return result
}

func fieldsJSONToModel(ctx context.Context, fields map[string]FieldHintJSON, diags *diag.Diagnostics) types.Map {
	if len(fields) == 0 {
		return types.MapNull(fieldObjectType())
	}

	elements := make(map[string]attr.Value, len(fields))

	for key, f := range fields {
		attrs := map[string]attr.Value{
			"type":         types.StringValue(f.Type),
			"description":  types.StringValue(f.Description),
			"read_only":    boolOrNull(f.ReadOnly),
			"lookup_table": stringOrNullAttr(f.LookupTable),
			"value_field":  stringOrNullAttr(f.ValueField),
			"bind_field":   stringOrNullAttr(f.BindField),
		}
		obj, d := types.ObjectValue(fieldAttrTypes(), attrs)
		diags.Append(d...)
		elements[key] = obj
	}

	result, d := types.MapValue(fieldObjectType(), elements)
	diags.Append(d...)
	return result
}

// ── Join step helpers ───────────────────────────────────────────────────

func joinStepsModelToJSON(ctx context.Context, steps []JoinStepModel, diags *diag.Diagnostics) []JoinStepJSON {
	if len(steps) == 0 {
		return nil
	}
	result := make([]JoinStepJSON, len(steps))
	for i, s := range steps {
		result[i] = JoinStepJSON{
			Table:   s.Table.ValueString(),
			From:    s.From.ValueString(),
			Key:     s.Key.ValueString(),
			Reverse: s.Reverse.ValueBool(),
		}
	}
	return result
}

func joinStepsJSONToModel(steps []JoinStepJSON) []JoinStepModel {
	if len(steps) == 0 {
		return nil
	}
	result := make([]JoinStepModel, len(steps))
	for i, s := range steps {
		result[i] = JoinStepModel{
			Table:   types.StringValue(s.Table),
			From:    types.StringValue(s.From),
			Key:     types.StringValue(s.Key),
			Reverse: boolOrNull(s.Reverse),
		}
	}
	return result
}

// ── Alternate contact join path helpers ─────────────────────────────────

func alternateJoinPathsModelToJSON(ctx context.Context, paths []AlternateContactJoinPathModel, diags *diag.Diagnostics) [][]JoinStepJSON {
	if len(paths) == 0 {
		return nil
	}
	result := make([][]JoinStepJSON, len(paths))
	for i, p := range paths {
		steps := make([]JoinStepJSON, len(p.Step))
		for j, s := range p.Step {
			steps[j] = JoinStepJSON{
				Table:   s.Table.ValueString(),
				From:    s.From.ValueString(),
				Key:     s.Key.ValueString(),
				Reverse: s.Reverse.ValueBool(),
			}
		}
		result[i] = steps
	}
	return result
}

func alternateJoinPathsJSONToModel(paths [][]JoinStepJSON) []AlternateContactJoinPathModel {
	if len(paths) == 0 {
		return nil
	}
	result := make([]AlternateContactJoinPathModel, len(paths))
	for i, p := range paths {
		steps := make([]JoinStepModel, len(p))
		for j, s := range p {
			steps[j] = JoinStepModel{
				Table:   types.StringValue(s.Table),
				From:    types.StringValue(s.From),
				Key:     types.StringValue(s.Key),
				Reverse: boolOrNull(s.Reverse),
			}
		}
		result[i] = AlternateContactJoinPathModel{Step: steps}
	}
	return result
}

// ── Create default helpers ──────────────────────────────────────────────

func createDefaultsModelToJSON(ctx context.Context, defaults []CreateDefaultModel, diags *diag.Diagnostics) []CreateDefaultJSON {
	if len(defaults) == 0 {
		return nil
	}
	result := make([]CreateDefaultJSON, len(defaults))
	for i, d := range defaults {
		result[i] = CreateDefaultJSON{
			Field:     d.Field.ValueString(),
			BindTo:    d.BindTo.ValueString(),
			EntitySet: d.EntitySet.ValueString(),
		}
	}
	return result
}

func createDefaultsJSONToModel(defaults []CreateDefaultJSON) []CreateDefaultModel {
	if len(defaults) == 0 {
		return nil
	}
	result := make([]CreateDefaultModel, len(defaults))
	for i, d := range defaults {
		result[i] = CreateDefaultModel{
			Field:     types.StringValue(d.Field),
			BindTo:    types.StringValue(d.BindTo),
			EntitySet: types.StringValue(d.EntitySet),
		}
	}
	return result
}

// ── Expand helpers ──────────────────────────────────────────────────────

func expandsModelToJSON(ctx context.Context, expands []ExpandModel, diags *diag.Diagnostics) []ExpandJSON {
	if len(expands) == 0 {
		return nil
	}
	result := make([]ExpandJSON, len(expands))
	for i, e := range expands {
		ej := ExpandJSON{
			LookupField:  e.LookupField.ValueString(),
			RelatedTable: e.RelatedTable.ValueString(),
		}
		for _, f := range e.Field {
			ej.Fields = append(ej.Fields, ExpandFieldJSON{
				Name:        f.Name.ValueString(),
				Type:        f.Type.ValueString(),
				Description: f.Description.ValueString(),
			})
		}
		result[i] = ej
	}
	return result
}

func expandsJSONToModel(ctx context.Context, expands []ExpandJSON, diags *diag.Diagnostics) []ExpandModel {
	if len(expands) == 0 {
		return nil
	}
	result := make([]ExpandModel, len(expands))
	for i, e := range expands {
		em := ExpandModel{
			LookupField:  types.StringValue(e.LookupField),
			RelatedTable: types.StringValue(e.RelatedTable),
		}
		for _, f := range e.Fields {
			em.Field = append(em.Field, ExpandFieldModel{
				Name:        types.StringValue(f.Name),
				Type:        types.StringValue(f.Type),
				Description: types.StringValue(f.Description),
			})
		}
		result[i] = em
	}
	return result
}

// ── Primitive conversion helpers ────────────────────────────────────────

func tfListToStrings(ctx context.Context, list types.List, diags *diag.Diagnostics) []string {
	if list.IsNull() || list.IsUnknown() {
		return nil
	}
	var result []string
	diags.Append(list.ElementsAs(ctx, &result, false)...)
	return result
}

func stringsToTFList(vals []string) types.List {
	if vals == nil {
		vals = []string{}
	}
	elements := make([]attr.Value, len(vals))
	for i, v := range vals {
		elements[i] = types.StringValue(v)
	}
	list, _ := types.ListValue(types.StringType, elements)
	return list
}

func stringsToTFListOrNull(vals []string) types.List {
	if len(vals) == 0 {
		return types.ListNull(types.StringType)
	}
	return stringsToTFList(vals)
}

func stringOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func stringOrNullAttr(s string) basetypes.StringValue {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func boolOrNull(b bool) basetypes.BoolValue {
	if !b {
		return types.BoolNull()
	}
	return types.BoolValue(true)
}

// singularize converts a Dataverse entity set name (plural) to the entity
// logical name (singular). Handles common English pluralization rules.
// Examples: incidents→incident, bookingstatuses→bookingstatus,
// bookableresourcecategories→bookableresourcecategory.
func singularize(word string) string {
	// Handle prefixed entities (e.g. tn_citizenservicebookings)
	for i, c := range word {
		if c == '_' && i > 0 {
			return word[:i+1] + singularize(word[i+1:])
		}
	}
	switch {
	case strings.HasSuffix(word, "ies"):
		return word[:len(word)-3] + "y"
	case strings.HasSuffix(word, "sses"):
		return word[:len(word)-2]
	case strings.HasSuffix(word, "uses"):
		return word[:len(word)-2]
	case strings.HasSuffix(word, "xes"):
		return word[:len(word)-2]
	case strings.HasSuffix(word, "shes"):
		return word[:len(word)-2]
	case strings.HasSuffix(word, "ches"):
		return word[:len(word)-2]
	case strings.HasSuffix(word, "s"):
		return word[:len(word)-1]
	default:
		return word
	}
}

// ── Attribute type helpers (for constructing types.Object / types.Map) ──

func fieldAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"type":         types.StringType,
		"description":  types.StringType,
		"read_only":    types.BoolType,
		"lookup_table": types.StringType,
		"value_field":  types.StringType,
		"bind_field":   types.StringType,
	}
}

func fieldObjectType() attr.Type {
	return types.ObjectType{AttrTypes: fieldAttrTypes()}
}

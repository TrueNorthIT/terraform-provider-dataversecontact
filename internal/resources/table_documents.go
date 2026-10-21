package resources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// SharePoint documents on a table's rows (`documents` on the API's schema
// hint, Contact API 1.34.0). The checks here are the ones the API's build
// makes (server/config/scopes/documents.ts). The API's save does not run them
// for documents, and a hint that fails them is published anyway, then left
// out of the scope when the registry builds: the whole route disappears. So
// they are made at plan time.

// DocumentsModel lists the SharePoint documents in each row's folder.
// SingleNestedBlock, like polymorphic_lookup: nil when the block is absent.
// Its attributes are all Optional (a SingleNestedBlock's Required attributes
// error even when the block is left out), so ValidateConfig requires
// permission.
type DocumentsModel struct {
	Permission types.String `tfsdk:"permission"`
	Folder     types.String `tfsdk:"folder"`
	Columns    types.Map    `tfsdk:"columns"`
	Filters    types.List   `tfsdk:"filters"`
}

// DocumentsJSON is the hint's `documents` key.
type DocumentsJSON struct {
	Permission string               `json:"permission"`
	Folder     string               `json:"folder,omitempty"`
	Columns    *DocumentColumnsJSON `json:"columns,omitempty"`
	Filters    []string             `json:"filters,omitempty"`
}

// DocumentColumnsJSON is `documents.columns`. The provider always writes the
// map form (column name → type). The API also accepts a plain list of names,
// typed from their values, so a hint written elsewhere may come back as one;
// Names holds it.
type DocumentColumnsJSON struct {
	Types map[string]string
	Names []string
}

func (c DocumentColumnsJSON) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.Types)
}

func (c *DocumentColumnsJSON) UnmarshalJSON(b []byte) error {
	if trimmed := bytes.TrimSpace(b); len(trimmed) > 0 && trimmed[0] == '[' {
		return json.Unmarshal(b, &c.Names)
	}
	return json.Unmarshal(b, &c.Types)
}

var (
	// documentsPermissionPattern is a permission subject: the shape of a route name.
	documentsPermissionPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	// documentColumnPattern is a SharePoint internal column name (e.g. "IsPublic", "Doc_x0020_Type").
	documentColumnPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// folderSegmentPattern is one folder name, without characters SharePoint refuses.
	folderSegmentPattern = regexp.MustCompile(`^[^"*:<>?/\\|#%]+$`)
)

// documentBuiltInFields are the fields every listed document carries, by type.
var documentBuiltInFields = map[string]string{
	"id":         "string",
	"name":       "string",
	"folder":     "string",
	"isFolder":   "boolean",
	"mimeType":   "string",
	"size":       "number",
	"createdon":  "datetime",
	"modifiedon": "datetime",
}

var documentColumnTypes = []string{"string", "number", "datetime", "boolean"}

// documentFilterOperators are the API's filter operators by field type.
var documentFilterOperators = map[string][]string{
	"string":   {"eq", "ne", "contains", "startswith", "endswith"},
	"number":   {"eq", "ne", "gt", "ge", "lt", "le"},
	"datetime": {"eq", "ne", "gt", "ge", "lt", "le"},
	"boolean":  {"eq", "ne"},
}

var allFilterOperators = []string{"eq", "ne", "gt", "ge", "lt", "le", "contains", "startswith", "endswith"}

const (
	maxDocumentColumns = 30
	maxDocumentFilters = 10
)

func documentsBlock() schema.SingleNestedBlock {
	builtIns := make([]string, 0, len(documentBuiltInFields))
	for name := range documentBuiltInFields {
		builtIns = append(builtIns, name)
	}
	sort.Strings(builtIns)

	return schema.SingleNestedBlock{
		Description: "SharePoint documents in each row's folder. Dataverse's SharePoint integration keeps a " +
			"folder per row; with this, GET /{tier}/{route}/{id}/documents lists what is in it (one folder " +
			"level, read through Microsoft Graph). The row is checked with this table's own scoping first. " +
			"Listing needs the documents permission at that tier, not this table's: grant it in " +
			"dataversecontact_permissions_sync.default_permissions under the same name (e.g. " +
			"casedocuments = [\"me\"]). Each document carries id, name, folder, isFolder, mimeType, size, " +
			"createdon and modifiedon, plus the columns declared here. Switch document management on for the " +
			"table in Dataverse, and grant the API's service principal Sites.Selected with Read on the site. " +
			"Needs Contact API 1.34.0 or later: an older API keeps the key without an error but serves no documents.",
		Attributes: map[string]schema.Attribute{
			"permission": schema.StringAttribute{
				Description: "Required in the block. Permission subject that lists the documents (e.g. " +
					"\"casedocuments\"): casedocuments, casedocuments:team and casedocuments:all per tier. " +
					"Only read grants under this name apply.",
				Optional: true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(documentsPermissionPattern,
						"must be a permission name like a route name (lowercase letters, digits, _ and -, e.g. \"casedocuments\")"),
				},
			},
			"folder": schema.StringAttribute{
				Description: "Sub-folder of each row's folder to serve instead (e.g. \"Public\" or \"Public/2026\"). " +
					"Leave it out to serve the row's folder itself.",
				Optional:   true,
				Validators: []validator.String{documentFolderValidator{}},
			},
			"columns": schema.MapAttribute{
				Description: "SharePoint library columns to return and allow filtering on: internal name " +
					"(e.g. \"ShowOnPortal\", \"Doc_x0020_Type\") to type, one of \"string\", \"number\", " +
					"\"datetime\" or \"boolean\". At most 30.",
				Optional:    true,
				ElementType: types.StringType,
				Validators: []validator.Map{
					mapvalidator.SizeBetween(1, maxDocumentColumns),
					mapvalidator.KeysAre(
						stringvalidator.RegexMatches(documentColumnPattern,
							"must be a SharePoint column internal name (letters, digits and underscores, e.g. \"ShowOnPortal\")"),
						stringvalidator.NoneOf(builtIns...),
					),
					mapvalidator.ValueStringsAre(stringvalidator.OneOf(documentColumnTypes...)),
				},
			},
			"filters": schema.ListAttribute{
				Description: "Restrictions applied to every listing, in the API's filter syntax: one " +
					"\"field operator value\" per entry (e.g. \"ShowOnPortal eq true\"), all ANDed, naming only " +
					"document fields and declared columns. Not OData: no and/or, no functions. A caller's own " +
					"filters narrow them and never widen them. At most 10.",
				Optional:    true,
				ElementType: types.StringType,
				Validators: []validator.List{
					listvalidator.SizeAtLeast(1),
				},
			},
		},
	}
}

// documentFolderValidator mirrors the API's folderSegments: below the row's
// folder, never "." or "..", no characters SharePoint refuses.
type documentFolderValidator struct{}

func (documentFolderValidator) Description(context.Context) string {
	return "must be a folder path below the row's folder"
}

func (v documentFolderValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (documentFolderValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if problem := documentFolderProblem(req.ConfigValue.ValueString()); problem != "" {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid documents folder", problem)
	}
}

func documentFolderProblem(folder string) string {
	trimmed := strings.Trim(strings.TrimSpace(folder), "/")
	if trimmed == "" {
		// The API reads this as the row's own folder. Sent as written it would
		// read back as unset, so ask for the equivalent that round-trips.
		return fmt.Sprintf("%q is the row's own folder: leave folder out to serve it.", folder)
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if strings.TrimSpace(segment) != segment || segment == "." || segment == ".." || !folderSegmentPattern.MatchString(segment) {
			return fmt.Sprintf("%q is not a folder path below the row's folder: use folder names separated "+
				"by /, without . or .. and without any of \" * : < > ? \\ | # %%.", folder)
		}
	}
	return ""
}

// validateDocuments is the part of ValidateConfig for the documents block:
// permission present, and every filter parses and names a field of the type
// its operator needs.
func validateDocuments(d *DocumentsModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if d == nil {
		return diags
	}
	root := path.Root("documents")
	if d.Permission.IsNull() {
		diags.AddAttributeError(root.AtName("permission"), "Incomplete documents block",
			"A documents block needs permission: the name the documents are listed under, granted in "+
				"dataversecontact_permissions_sync.default_permissions (e.g. permission = \"casedocuments\" "+
				"and casedocuments = [\"me\"]).")
	}

	if d.Filters.IsNull() || d.Filters.IsUnknown() {
		return diags
	}
	// Until the columns are known, a filter on one can't be checked.
	columnsKnown := !d.Columns.IsUnknown()
	fieldTypes := make(map[string]string, len(documentBuiltInFields))
	for name, typ := range documentBuiltInFields {
		fieldTypes[name] = typ
	}
	if columnsKnown {
		for name, v := range d.Columns.Elements() {
			typ := "" // unknown: the operator can't be checked yet
			if s, ok := v.(types.String); ok && !s.IsNull() && !s.IsUnknown() {
				typ = s.ValueString()
			}
			fieldTypes[name] = typ
		}
	}

	distinct := map[string]bool{}
	for i, v := range d.Filters.Elements() {
		s, ok := v.(types.String)
		if !ok || s.IsNull() || s.IsUnknown() {
			continue
		}
		entry := strings.TrimSpace(s.ValueString())
		if entry == "" {
			continue // the API skips an empty entry
		}
		distinct[entry] = true
		at := root.AtName("filters").AtListIndex(i)
		field, operator, problem := parseDocumentFilter(entry)
		if problem != "" {
			diags.AddAttributeError(at, "Invalid documents filter", problem)
			continue
		}
		if !columnsKnown {
			continue
		}
		typ, known := fieldTypes[field]
		switch {
		case !known:
			diags.AddAttributeError(at, "Invalid documents filter",
				fmt.Sprintf("%q isn't a document field: declare the column in documents.columns, or use one of "+
					"id, name, folder, isFolder, mimeType, size, createdon, modifiedon.", field))
		case typ != "" && !contains(documentFilterOperators[typ], operator):
			diags.AddAttributeError(at, "Invalid documents filter",
				fmt.Sprintf("'%s' doesn't apply to %s field %q; use %s.", operator, typ, field,
					strings.Join(documentFilterOperators[typ], ", ")))
		}
	}
	if len(distinct) > maxDocumentFilters {
		diags.AddAttributeError(root.AtName("filters"), "Invalid documents filter",
			fmt.Sprintf("Too many filters (max %d).", maxDocumentFilters))
	}
	return diags
}

// parseDocumentFilter splits "field operator value" the way the API's
// parseFilterEntries does.
func parseDocumentFilter(entry string) (field, operator, problem string) {
	syntax := fmt.Sprintf("%q: expected field operator value (e.g. \"ShowOnPortal eq true\").", entry)
	field, rest, ok := strings.Cut(entry, " ")
	if !ok {
		return "", "", syntax
	}
	field = strings.TrimSpace(field)
	if strings.HasPrefix(field, "parent.") {
		return "", "", fmt.Sprintf("%q: a documents filter can only name document fields, not the row's.", entry)
	}
	operator, value, ok := strings.Cut(strings.TrimSpace(rest), " ")
	if !ok {
		return "", "", syntax
	}
	operator = strings.ToLower(strings.TrimSpace(operator))
	if !contains(allFilterOperators, operator) {
		return "", "", fmt.Sprintf("%q: unknown operator '%s'; use %s.", entry, operator, strings.Join(allFilterOperators, ", "))
	}
	if strings.TrimSpace(value) == "" {
		return "", "", syntax
	}
	return field, operator, ""
}

func contains(values []string, v string) bool {
	for _, candidate := range values {
		if candidate == v {
			return true
		}
	}
	return false
}

// ── Conversion ──────────────────────────────────────────────────────────

// documentsModelToJSON: an absent block sends no `documents` key at all, so a
// config without one publishes exactly what it did before the block existed.
// Optional attributes left out are left out of the JSON too.
func documentsModelToJSON(ctx context.Context, d *DocumentsModel, diags *diag.Diagnostics) *DocumentsJSON {
	if d == nil || d.Permission.IsNull() || d.Permission.IsUnknown() {
		return nil
	}
	out := &DocumentsJSON{
		Permission: d.Permission.ValueString(),
		Folder:     d.Folder.ValueString(),
		Filters:    tfListToStrings(ctx, d.Filters, diags),
	}
	if !d.Columns.IsNull() && !d.Columns.IsUnknown() {
		var columns map[string]string
		diags.Append(d.Columns.ElementsAs(ctx, &columns, false)...)
		if len(columns) > 0 {
			out.Columns = &DocumentColumnsJSON{Types: columns}
		}
	}
	return out
}

// documentsJSONToModel: absent and null both read back as no block. prior is
// the plan or state being refreshed; see documentColumnsToModel.
func documentsJSONToModel(d *DocumentsJSON, prior *DocumentsModel) *DocumentsModel {
	if d == nil {
		return nil
	}
	return &DocumentsModel{
		Permission: types.StringValue(d.Permission),
		Folder:     stringOrNull(d.Folder),
		Columns:    documentColumnsToModel(d.Columns, prior),
		Filters:    stringsToTFListOrNull(d.Filters),
	}
}

// documentColumnsToModel reads columns back. The list form has no types: when
// it names exactly the columns already in the plan or state, those are kept
// (no diff); otherwise each name reads as "string", the type the API gives an
// untyped column, and the next apply writes the map form.
func documentColumnsToModel(c *DocumentColumnsJSON, prior *DocumentsModel) types.Map {
	if c == nil {
		return types.MapNull(types.StringType)
	}
	if c.Types != nil {
		return stringMapOrNull(c.Types)
	}
	if len(c.Names) == 0 {
		return types.MapNull(types.StringType)
	}
	if prior != nil && sameKeys(prior.Columns, c.Names) {
		return prior.Columns
	}
	untyped := make(map[string]string, len(c.Names))
	for _, name := range c.Names {
		untyped[name] = "string"
	}
	return stringMapOrNull(untyped)
}

func stringMapOrNull(m map[string]string) types.Map {
	if len(m) == 0 {
		return types.MapNull(types.StringType)
	}
	elements := make(map[string]attr.Value, len(m))
	for k, v := range m {
		elements[k] = types.StringValue(v)
	}
	return types.MapValueMust(types.StringType, elements)
}

func sameKeys(m types.Map, names []string) bool {
	if m.IsNull() || m.IsUnknown() {
		return false
	}
	elements := m.Elements()
	unique := map[string]bool{}
	for _, name := range names {
		if _, ok := elements[name]; !ok {
			return false
		}
		unique[name] = true
	}
	return len(unique) == len(elements)
}

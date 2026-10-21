package resources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
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
// hint, Contact API 1.34.0; `documents.upload`, 1.35.0;
// `documents.upload.invites`, 1.36.0). The checks here are
// the ones the API's build makes (server/config/scopes/documents.ts). The API's
// save does not run them for documents, and a hint that fails them is
// published anyway, then left out of the scope when the registry builds: the
// whole route disappears. So they are made at plan time.

// DocumentsModel lists the SharePoint documents in each row's folder.
// SingleNestedBlock, like polymorphic_lookup: nil when the block is absent.
// Its attributes are all Optional (a SingleNestedBlock's Required attributes
// error even when the block is left out), so ValidateConfig requires
// permission.
type DocumentsModel struct {
	Permission types.String          `tfsdk:"permission"`
	Folder     types.String          `tfsdk:"folder"`
	Columns    types.Map             `tfsdk:"columns"`
	Filters    types.List            `tfsdk:"filters"`
	Upload     *DocumentsUploadModel `tfsdk:"upload"`
}

// DocumentsUploadModel lets callers upload into a row's folder. A nested
// SingleNestedBlock: nil when the block is absent, which is what allows no
// uploads. Every attribute is optional, so `upload {}` is a real config: upload
// with the API's defaults.
type DocumentsUploadModel struct {
	MaxBytes   types.Int64 `tfsdk:"max_bytes"`
	Extensions types.List  `tfsdk:"extensions"`
	Columns    types.Map   `tfsdk:"columns"`
	Invites    types.Bool  `tfsdk:"invites"`
}

// DocumentsJSON is the hint's `documents` key.
type DocumentsJSON struct {
	Permission string               `json:"permission"`
	Folder     string               `json:"folder,omitempty"`
	Columns    *DocumentColumnsJSON `json:"columns,omitempty"`
	Filters    []string             `json:"filters,omitempty"`
	Upload     *DocumentsUploadJSON `json:"upload,omitempty"`
}

// DocumentsUploadJSON is `documents.upload`. Absent means no uploads; `{}`
// means uploads with the API's defaults. Columns holds each fixed value as
// sent or read: the provider writes it typed by the column's declared type,
// and the API also accepts (and keeps) strings. Invites is sent only when
// true: false and absent both mean no upload invites.
type DocumentsUploadJSON struct {
	MaxBytes   *int64                     `json:"maxBytes,omitempty"`
	Extensions []string                   `json:"extensions,omitempty"`
	Columns    map[string]json.RawMessage `json:"columns,omitempty"`
	Invites    bool                       `json:"invites,omitempty"`
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

	// documentUploadCap is the API's default DOCUMENT_UPLOAD_MAX_BYTES (25 MiB).
	// A route's max_bytes never lifts an upload above the deployment's cap, so
	// a larger value only takes effect where the deployment raised it.
	documentUploadCap = 26214400
)

var (
	// documentExtensionPattern is a file extension as the API's allowlist
	// holds it: lower case, no dot.
	documentExtensionPattern = regexp.MustCompile(`^[a-z0-9]+$`)
	// jsonNumberPattern is a JSON number, which is how a number column's fixed
	// value is sent.
	jsonNumberPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)
)

// documentExecutableExtensions are never allowed for upload, even if listed.
var documentExecutableExtensions = []string{"exe", "bat", "cmd", "com", "js", "msi", "ps1", "scr", "vbs", "sh", "dll"}

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
			"Add an upload block to let callers upload files too. " +
			"Needs Contact API 1.34.0 or later: an older API keeps the key without an error but serves no documents.",
		Attributes: map[string]schema.Attribute{
			"permission": schema.StringAttribute{
				Description: "Required in the block. Permission subject that lists the documents (e.g. " +
					"\"casedocuments\"): casedocuments, casedocuments:team and casedocuments:all per tier. " +
					"Its read grants list the documents; with an upload block, its create grants " +
					"(casedocuments:create, :create:team, :create:all) upload them.",
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
		Blocks: map[string]schema.Block{
			"upload": documentsUploadBlock(),
		},
	}
}

func documentsUploadBlock() schema.SingleNestedBlock {
	return schema.SingleNestedBlock{
		Description: "Lets callers upload a file into a row's folder, in three steps: POST " +
			"/{tier}/{route}/{id}/documents/uploads with the file's name, size and type returns a short-lived " +
			"upload URL; the caller PUTs the bytes to it; POST .../documents/uploads/{uploadId}/complete files " +
			"them in SharePoint. Leave the block out to allow no uploads; upload {} allows them with the API's " +
			"defaults. Uploading needs create on the documents permission at that tier, granted in " +
			"dataversecontact_permissions_sync.default_permissions (e.g. casedocuments = [\"me\", \"create\"]); " +
			"create doesn't imply listing. The row is checked with this table's own scoping first. A file never " +
			"overwrites another (SharePoint renames a clash), and a row with no folder yet gets one. Grant the " +
			"API's service principal Sites.Selected with Write on the site, and its Dataverse app user Create on " +
			"Document Location. The upload URL is in the API's downloads storage, as for file downloads, and the " +
			"storage account needs a CORS rule allowing PUT from the portal. Set invites = true to let a one-time " +
			"invite link upload one file too. Needs Contact API 1.35.0 or later: " +
			"an older API keeps the key without an error but accepts no uploads.",
		Attributes: map[string]schema.Attribute{
			"max_bytes": schema.Int64Attribute{
				Description: "Largest file accepted, in bytes. Leave it out for 10485760 (10 MiB). Uploads are " +
					"also capped by the deployment's DOCUMENT_UPLOAD_MAX_BYTES (26214400 = 25 MiB by default), " +
					"so a larger value only applies where the deployment raised that.",
				Optional:   true,
				Validators: []validator.Int64{int64validator.AtLeast(1)},
			},
			"extensions": schema.ListAttribute{
				Description: "File extensions accepted, lower case without the dot (e.g. [\"pdf\", \"png\", \"jpg\"]), " +
					"matched case-insensitively. Leave it out for the API's default list: pdf, png, jpg, jpeg, gif, " +
					"webp, heic, txt, rtf, doc, docx, xls, xlsx, csv, odt, ods. Executable types (exe, bat, cmd, com, " +
					"js, msi, ps1, scr, vbs, sh, dll) are never accepted.",
				Optional:    true,
				ElementType: types.StringType,
				Validators: []validator.List{
					listvalidator.SizeAtLeast(1),
					listvalidator.UniqueValues(),
					listvalidator.ValueStringsAre(documentExtensionValidator{}),
				},
			},
			"columns": schema.MapAttribute{
				Description: "SharePoint column values set on every uploaded file, e.g. { ShowOnPortal = \"true\" } so " +
					"an upload passes filters = [\"ShowOnPortal eq true\"]. Each column must be declared in " +
					"documents.columns, with its value written as a string that suits the declared type: \"true\" or " +
					"\"false\" for boolean, a number such as \"4\" for number; the provider sends each as its type. " +
					"Callers can't set or override them, and if they can't be set the upload is undone.",
				Optional:    true,
				ElementType: types.StringType,
				Validators: []validator.Map{
					mapvalidator.SizeAtLeast(1),
				},
			},
			"invites": schema.BoolAttribute{
				Description: "Accept one-time upload invites: a link that lets whoever holds it, with no account, " +
					"upload one document to the one row it was sent for, once, under this block's limits and fixed " +
					"columns. Sending one through the API needs invite on the documents permission at the sender's " +
					"tier plus create there (e.g. casedocuments = [\"team\", \"create:team\", \"invite:team\"] in " +
					"dataversecontact_permissions_sync.default_permissions); sending from Dataverse needs the Portal " +
					"Invite Sender role instead. Defaults to false. Needs Contact API 1.36.0 or later: an older API " +
					"keeps the key without an error but makes no invites.",
				Optional: true,
			},
		},
	}
}

// documentExtensionValidator: an extension the API's allowlist can hold, and
// never one it refuses outright.
type documentExtensionValidator struct{}

func (documentExtensionValidator) Description(context.Context) string {
	return "must be a file extension in lower case, without the dot, and not an executable type"
}

func (v documentExtensionValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (documentExtensionValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if problem := documentExtensionProblem(req.ConfigValue.ValueString()); problem != "" {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid upload extension", problem)
	}
}

func documentExtensionProblem(ext string) string {
	if !documentExtensionPattern.MatchString(ext) {
		return fmt.Sprintf("%q: write a file extension in lower case, without the dot (e.g. \"pdf\").", ext)
	}
	if contains(documentExecutableExtensions, ext) {
		return fmt.Sprintf("%q is an executable type, which the API never accepts for upload (%s).",
			ext, strings.Join(documentExecutableExtensions, ", "))
	}
	return ""
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
// permission present, every filter parses and names a field of the type its
// operator needs, and upload's fixed columns are declared (see
// validateDocumentsUpload).
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
	diags.Append(validateDocumentsUpload(d)...)

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

// validateDocumentsUpload checks the upload block against the rest of
// documents: each fixed column is declared in documents.columns with a type
// its value suits. A max_bytes above the default deployment cap is only a
// warning, as the API's save makes it: a deployment can raise the cap, and
// requests are capped anyway.
func validateDocumentsUpload(d *DocumentsModel) diag.Diagnostics {
	var diags diag.Diagnostics
	u := d.Upload
	if u == nil {
		return diags
	}
	at := path.Root("documents").AtName("upload")

	if !u.MaxBytes.IsNull() && !u.MaxBytes.IsUnknown() && u.MaxBytes.ValueInt64() > documentUploadCap {
		diags.AddAttributeWarning(at.AtName("max_bytes"), "max_bytes above the default upload cap",
			fmt.Sprintf("%d is above the API's default deployment cap of %d bytes (25 MiB). Uploads are "+
				"capped by the deployment's DOCUMENT_UPLOAD_MAX_BYTES anyway, so this only applies where the "+
				"deployment raised it.",
				u.MaxBytes.ValueInt64(), documentUploadCap))
	}

	// Until the columns are known, nothing can be checked against them.
	if u.Columns.IsNull() || u.Columns.IsUnknown() || d.Columns.IsUnknown() {
		return diags
	}
	declared := d.Columns.Elements() // empty when documents.columns is null
	names := make([]string, 0, len(u.Columns.Elements()))
	for name := range u.Columns.Elements() {
		names = append(names, name)
	}
	sort.Strings(names) // diagnostics in a stable order
	for _, name := range names {
		columnAt := at.AtName("columns").AtMapKey(name)
		t, ok := declared[name]
		if !ok {
			diags.AddAttributeError(columnAt, "Undeclared upload column",
				fmt.Sprintf("%q isn't declared in documents.columns: declare it there with its type "+
					"(e.g. %s = \"boolean\") before setting it on uploads.", name, name))
			continue
		}
		typ, value := knownString(t), knownString(u.Columns.Elements()[name])
		if typ == nil || value == nil {
			continue // unknown until apply
		}
		if problem := documentColumnValueProblem(*typ, *value); problem != "" {
			diags.AddAttributeError(columnAt, "Invalid upload column value",
				fmt.Sprintf("%q is a %s column: %s", name, *typ, problem))
		}
	}
	return diags
}

// knownString is a known string's value, or nil while null or unknown.
func knownString(v attr.Value) *string {
	s, ok := v.(types.String)
	if !ok || s.IsNull() || s.IsUnknown() {
		return nil
	}
	value := s.ValueString()
	return &value
}

// documentColumnValueProblem: whether value, written as a string, suits a
// column of type typ. string and datetime take any string.
func documentColumnValueProblem(typ, value string) string {
	switch typ {
	case "boolean":
		if value != "true" && value != "false" {
			return fmt.Sprintf("%q isn't a boolean; use \"true\" or \"false\".", value)
		}
	case "number":
		if _, ok := documentNumber(value); !ok {
			return fmt.Sprintf("%q isn't a number; write it like \"4\" or \"2.5\".", value)
		}
	}
	return ""
}

// documentNumber parses a number column's value as the JSON number it is sent
// as. A value JSON can't carry (too large for a float64) doesn't parse.
func documentNumber(value string) (float64, bool) {
	if !jsonNumberPattern.MatchString(value) {
		return 0, false
	}
	f, err := strconv.ParseFloat(value, 64)
	return f, err == nil
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
	var columns map[string]string
	if !d.Columns.IsNull() && !d.Columns.IsUnknown() {
		diags.Append(d.Columns.ElementsAs(ctx, &columns, false)...)
		if len(columns) > 0 {
			out.Columns = &DocumentColumnsJSON{Types: columns}
		}
	}
	out.Upload = documentsUploadModelToJSON(ctx, d.Upload, columns, diags)
	return out
}

// documentsUploadModelToJSON: no block, no key, so a documents block without
// upload publishes exactly what it did before upload existed. `upload {}` is
// sent as {}: uploads with the API's defaults. columnTypes is documents.columns.
func documentsUploadModelToJSON(ctx context.Context, u *DocumentsUploadModel, columnTypes map[string]string, diags *diag.Diagnostics) *DocumentsUploadJSON {
	if u == nil {
		return nil
	}
	out := &DocumentsUploadJSON{
		Extensions: tfListToStrings(ctx, u.Extensions, diags),
		Invites:    u.Invites.ValueBool(), // false for null or unknown
	}
	if !u.MaxBytes.IsNull() && !u.MaxBytes.IsUnknown() {
		maxBytes := u.MaxBytes.ValueInt64()
		out.MaxBytes = &maxBytes
	}
	if !u.Columns.IsNull() && !u.Columns.IsUnknown() {
		var values map[string]string
		diags.Append(u.Columns.ElementsAs(ctx, &values, false)...)
		if len(values) > 0 {
			out.Columns = make(map[string]json.RawMessage, len(values))
			for name, value := range values {
				out.Columns[name] = documentColumnValueJSON(columnTypes[name], value)
			}
		}
	}
	return out
}

// documentColumnValueJSON sends a fixed column value as its declared type:
// "true" as true for a boolean, "4" as 4 for a number. Anything else, or a
// value that doesn't suit its type (ValidateConfig refuses those), goes as a
// string, which the API also accepts.
func documentColumnValueJSON(typ, value string) json.RawMessage {
	switch {
	case typ == "boolean" && (value == "true" || value == "false"):
		return json.RawMessage(value)
	case typ == "number":
		if _, ok := documentNumber(value); ok {
			return json.RawMessage(value)
		}
	}
	quoted, _ := json.Marshal(value) // a string always marshals
	return quoted
}

// documentsJSONToModel: absent and null both read back as no block. prior is
// the plan or state being refreshed; see documentColumnsToModel.
func documentsJSONToModel(d *DocumentsJSON, prior *DocumentsModel) *DocumentsModel {
	if d == nil {
		return nil
	}
	var priorUpload *DocumentsUploadModel
	if prior != nil {
		priorUpload = prior.Upload
	}
	return &DocumentsModel{
		Permission: types.StringValue(d.Permission),
		Folder:     stringOrNull(d.Folder),
		Columns:    documentColumnsToModel(d.Columns, prior),
		Filters:    stringsToTFListOrNull(d.Filters),
		Upload:     documentsUploadJSONToModel(d.Upload, priorUpload),
	}
}

// documentsUploadJSONToModel: absent and null read back as no block, {} as a
// block with every attribute null, matching `upload {}` in the config.
// invites is never sent as false, so it reads back as keepFalse does.
func documentsUploadJSONToModel(u *DocumentsUploadJSON, prior *DocumentsUploadModel) *DocumentsUploadModel {
	if u == nil {
		return nil
	}
	maxBytes := types.Int64Null()
	if u.MaxBytes != nil {
		maxBytes = types.Int64Value(*u.MaxBytes)
	}
	var priorInvites attr.Value
	if prior != nil {
		priorInvites = prior.Invites
	}
	return &DocumentsUploadModel{
		MaxBytes:   maxBytes,
		Extensions: stringsToTFListOrNull(u.Extensions),
		Columns:    documentUploadColumnsToModel(u.Columns, prior),
		Invites:    keepFalse(u.Invites, priorInvites),
	}
}

// documentUploadColumnsToModel reads the fixed values back as the strings the
// config writes: true as "true", 4 as "4", a string as itself. A number equal
// to the one already in the plan or state keeps that spelling ("4.0" stays
// "4.0"), since the API hands back the number, not the text it was sent. A
// null value is no value.
func documentUploadColumnsToModel(columns map[string]json.RawMessage, prior *DocumentsUploadModel) types.Map {
	var priorValues map[string]attr.Value
	if prior != nil && !prior.Columns.IsNull() && !prior.Columns.IsUnknown() {
		priorValues = prior.Columns.Elements()
	}
	values := make(map[string]string, len(columns))
	for name, raw := range columns {
		value, isNumber, ok := documentColumnValueString(raw)
		if !ok {
			continue
		}
		if p := knownString(priorValues[name]); isNumber && p != nil {
			if was, ok := documentNumber(*p); ok {
				if now, ok := documentNumber(value); ok && was == now {
					value = *p
				}
			}
		}
		values[name] = value
	}
	return stringMapOrNull(values)
}

// documentColumnValueString is one fixed value as a string, and whether the
// API sent it as a JSON number. ok is false for null.
func documentColumnValueString(raw json.RawMessage) (value string, isNumber, ok bool) {
	trimmed := bytes.TrimSpace(raw)
	switch {
	case len(trimmed) == 0 || string(trimmed) == "null":
		return "", false, false
	case trimmed[0] == '"':
		if err := json.Unmarshal(trimmed, &value); err == nil {
			return value, false, true
		}
	case trimmed[0] == '-' || (trimmed[0] >= '0' && trimmed[0] <= '9'):
		return string(trimmed), true, true
	}
	// true and false, and anything else (an object or array written outside
	// Terraform) as its JSON text, so the next apply puts the config back.
	return string(trimmed), false, true
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

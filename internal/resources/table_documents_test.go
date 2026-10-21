package resources

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// documentsHint is a minimal published case hint, with `extra` spliced in as
// further top-level keys.
func documentsHint(extra string) []byte {
	return []byte(`{
	  "routeName": "case",
	  "dataverseTable": "incidents",
	  "dataverseLogicalName": "incident",
	  "requiredPermission": "case",
	  "primaryKey": "incidentid",
	  "defaultSelect": ["incidentid"],
	  "lookupFields": ["title"],
	  "fields": {"title": {"type": "string", "description": "Title"}}` + extra + `
	}`)
}

func readHint(t *testing.T, raw []byte, model *TableResourceModel) {
	t.Helper()
	var diags diag.Diagnostics
	schemaJSONToModel(context.Background(), raw, model, &diags)
	if diags.HasError() {
		t.Fatalf("schemaJSONToModel: %v", diags.Errors())
	}
}

func writeHint(t *testing.T, model *TableResourceModel) map[string]any {
	t.Helper()
	var diags diag.Diagnostics
	raw := modelToSchemaJSON(context.Background(), model, &diags)
	if diags.HasError() {
		t.Fatalf("modelToSchemaJSON: %v", diags.Errors())
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func columnsOf(m map[string]string) types.Map { return stringMapOrNull(m) }

func filtersOf(f ...string) types.List { return stringsToTFList(f) }

// A table with no documents must publish exactly what it did before the block
// existed: no `documents` key at all. The API's published hint spells "none"
// as null; that and a missing key both read back as no block, so an existing
// config plans no change either way.
func TestNoDocumentsOmitsKey(t *testing.T) {
	for name, extra := range map[string]string{
		"absent": ``,
		"null":   `, "documents": null`,
	} {
		t.Run(name, func(t *testing.T) {
			model := &TableResourceModel{}
			readHint(t, documentsHint(extra), model)
			if model.Documents != nil {
				t.Fatalf("read back %+v, want no block", model.Documents)
			}
			if _, ok := writeHint(t, model)["documents"]; ok {
				t.Fatal("a table without documents sent a documents key")
			}
		})
	}

	// As Terraform could hand over a SingleNestedBlock left out of the config.
	model := &TableResourceModel{}
	readHint(t, documentsHint(``), model)
	model.Documents = &DocumentsModel{
		Permission: types.StringNull(),
		Folder:     types.StringNull(),
		Columns:    types.MapNull(types.StringType),
		Filters:    types.ListNull(types.StringType),
	}
	if _, ok := writeHint(t, model)["documents"]; ok {
		t.Fatal("an empty block sent a documents key")
	}
}

// Every attribute goes out under the API's names and comes back unchanged.
func TestDocumentsRoundTrip(t *testing.T) {
	model := &TableResourceModel{}
	readHint(t, documentsHint(``), model)
	in := &DocumentsModel{
		Permission: types.StringValue("casedocuments"),
		Folder:     types.StringValue("Public"),
		Columns:    columnsOf(map[string]string{"ShowOnPortal": "boolean", "DocType": "string"}),
		Filters:    filtersOf("ShowOnPortal eq true"),
	}
	model.Documents = in

	got, _ := json.Marshal(writeHint(t, model)["documents"])
	want := `{"columns":{"DocType":"string","ShowOnPortal":"boolean"},"filters":["ShowOnPortal eq true"],"folder":"Public","permission":"casedocuments"}`
	if string(got) != want {
		t.Fatalf("sent %s\nwant %s", got, want)
	}

	back := &TableResourceModel{}
	readHint(t, documentsHint(`, "documents": `+string(got)), back)
	d := back.Documents
	if d == nil || !d.Permission.Equal(in.Permission) || !d.Folder.Equal(in.Folder) ||
		!d.Columns.Equal(in.Columns) || !d.Filters.Equal(in.Filters) {
		t.Fatalf("changed in the round trip: %+v", d)
	}
}

// Only permission is required; the optional attributes left out are left out
// of the JSON, and read back null rather than empty.
func TestDocumentsOptionalOmitted(t *testing.T) {
	model := &TableResourceModel{}
	readHint(t, documentsHint(``), model)
	model.Documents = &DocumentsModel{
		Permission: types.StringValue("casedocuments"),
		Folder:     types.StringNull(),
		Columns:    types.MapNull(types.StringType),
		Filters:    types.ListNull(types.StringType),
	}
	got, _ := json.Marshal(writeHint(t, model)["documents"])
	if want := `{"permission":"casedocuments"}`; string(got) != want {
		t.Fatalf("sent %s, want %s", got, want)
	}

	back := &TableResourceModel{}
	readHint(t, documentsHint(`, "documents": {"permission": "casedocuments", "folder": "", "columns": {}, "filters": []}`), back)
	d := back.Documents
	if d == nil || d.Permission.ValueString() != "casedocuments" || !d.Folder.IsNull() || !d.Columns.IsNull() || !d.Filters.IsNull() {
		t.Fatalf("empty optional values should read back null: %+v", d)
	}
}

// The API also takes columns as a plain list of names. The provider never
// writes that, but a hint saved elsewhere may come back so; it must not fail
// the read, and names that match the config keep the config's types.
func TestDocumentColumnsListForm(t *testing.T) {
	list := `, "documents": {"permission": "casedocuments", "columns": ["ShowOnPortal", "DocType"]}`

	fresh := &TableResourceModel{}
	readHint(t, documentsHint(list), fresh)
	want := columnsOf(map[string]string{"ShowOnPortal": "string", "DocType": "string"})
	if !fresh.Documents.Columns.Equal(want) {
		t.Fatalf("list form without a prior: got %v, want %v", fresh.Documents.Columns, want)
	}

	configured := columnsOf(map[string]string{"ShowOnPortal": "boolean", "DocType": "string"})
	prior := &TableResourceModel{Documents: &DocumentsModel{Columns: configured}}
	readHint(t, documentsHint(list), prior)
	if !prior.Documents.Columns.Equal(configured) {
		t.Fatalf("same names should keep the configured types: got %v", prior.Documents.Columns)
	}

	other := &TableResourceModel{Documents: &DocumentsModel{Columns: columnsOf(map[string]string{"ShowOnPortal": "boolean"})}}
	readHint(t, documentsHint(list), other)
	if !other.Documents.Columns.Equal(want) {
		t.Fatalf("different names should read as strings: got %v", other.Documents.Columns)
	}

	empty := &TableResourceModel{}
	readHint(t, documentsHint(`, "documents": {"permission": "casedocuments", "columns": []}`), empty)
	if !empty.Documents.Columns.IsNull() {
		t.Fatalf("an empty list should read back null, got %v", empty.Documents.Columns)
	}

	// Duplicates in the list name each column once.
	dup := &TableResourceModel{Documents: &DocumentsModel{Columns: columnsOf(map[string]string{"A": "number", "B": "string"})}}
	readHint(t, documentsHint(`, "documents": {"permission": "casedocuments", "columns": ["A", "A"]}`), dup)
	if !dup.Documents.Columns.Equal(columnsOf(map[string]string{"A": "string"})) {
		t.Fatalf("[A, A] against {A, B} should not keep the prior: got %v", dup.Documents.Columns)
	}

	var diags diag.Diagnostics
	schemaJSONToModel(context.Background(), documentsHint(`, "documents": {"permission": "x", "columns": [1]}`), &TableResourceModel{}, &diags)
	if !diags.HasError() {
		t.Fatal("a list of non-strings should be a parse error, not a crash")
	}
}

func TestDocumentFolderProblem(t *testing.T) {
	for folder, ok := range map[string]bool{
		"Public":        true,
		"Public/2026":   true,
		"/Public/":      true,
		" Public ":      true,
		"Case Photos":   true,
		"":              false,
		"/":             false,
		"..":            false,
		"Public/../x":   false,
		"./Public":      false,
		"Public//2026":  false,
		"Public/ 2026":  false,
		"Pub:lic":       false,
		`Pub\lic`:       false,
		"100%":          false,
		"Public/#draft": false,
	} {
		if got := documentFolderProblem(folder) == ""; got != ok {
			t.Errorf("folder %q: valid = %v, want %v (%s)", folder, got, ok, documentFolderProblem(folder))
		}
	}
}

func TestValidateDocuments(t *testing.T) {
	block := func(columns types.Map, filters types.List) *DocumentsModel {
		return &DocumentsModel{
			Permission: types.StringValue("casedocuments"),
			Folder:     types.StringNull(),
			Columns:    columns,
			Filters:    filters,
		}
	}
	boolColumn := columnsOf(map[string]string{"ShowOnPortal": "boolean"})
	noColumns := types.MapNull(types.StringType)

	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = "size gt " + strings.Repeat("1", i+1)
	}

	cases := []struct {
		name string
		d    *DocumentsModel
		want string // substring of the error; "" = valid
	}{
		{"no block", nil, ""},
		{"permission only", block(noColumns, types.ListNull(types.StringType)), ""},
		{"missing permission", &DocumentsModel{Permission: types.StringNull(), Filters: types.ListNull(types.StringType)}, "Incomplete documents block"},
		{"unknown permission", &DocumentsModel{Permission: types.StringUnknown(), Filters: types.ListNull(types.StringType)}, ""},
		{"declared column", block(boolColumn, filtersOf("ShowOnPortal eq true")), ""},
		{"built-in fields", block(noColumns, filtersOf("name endswith .pdf", "size le 1000", "isFolder eq false", "modifiedon ge 2026-01-01")), ""},
		{"operator case", block(boolColumn, filtersOf("ShowOnPortal EQ true")), ""},
		{"empty and duplicate entries", block(boolColumn, filtersOf("", "  ", "ShowOnPortal eq true", "ShowOnPortal eq true")), ""},
		{"undeclared column", block(noColumns, filtersOf("ShowOnPortal eq true")), `"ShowOnPortal" isn't a document field`},
		{"wrong operator for type", block(boolColumn, filtersOf("ShowOnPortal gt true")), `'gt' doesn't apply to boolean field "ShowOnPortal"`},
		{"string operator on a number", block(noColumns, filtersOf("size contains 1")), `doesn't apply to number field "size"`},
		{"unknown operator", block(boolColumn, filtersOf("ShowOnPortal is true")), "unknown operator 'is'"},
		{"no operator", block(boolColumn, filtersOf("ShowOnPortal")), "expected field operator value"},
		{"no value", block(boolColumn, filtersOf("ShowOnPortal eq")), "expected field operator value"},
		{"parent field", block(noColumns, filtersOf("parent.statecode eq 0")), "only name document fields"},
		{"too many", block(noColumns, filtersOf(eleven...)), "Too many filters"},
		{"unknown columns skip field checks", block(types.MapUnknown(types.StringType), filtersOf("Anything eq 1")), ""},
		{"unknown columns still check syntax", block(types.MapUnknown(types.StringType), filtersOf("Anything")), "expected field operator value"},
		{"unknown column type skips operator check", block(
			types.MapValueMust(types.StringType, map[string]attr.Value{"Rank": types.StringUnknown()}),
			filtersOf("Rank gt 3")), ""},
		{"unknown filter entry", block(boolColumn, types.ListValueMust(types.StringType, []attr.Value{types.StringUnknown()})), ""},
		{"unknown filters", block(boolColumn, types.ListUnknown(types.StringType)), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			diags := validateDocuments(c.d)
			switch {
			case c.want == "" && diags.HasError():
				t.Fatalf("unexpected error: %v", diags.Errors())
			case c.want != "" && !diags.HasError():
				t.Fatalf("expected an error containing %q", c.want)
			case c.want != "":
				var all strings.Builder
				for _, e := range diags.Errors() {
					all.WriteString(e.Summary() + ": " + e.Detail() + "\n")
				}
				if !strings.Contains(all.String(), c.want) {
					t.Fatalf("errors %q do not mention %q", all.String(), c.want)
				}
			}
		})
	}
}

func TestDocumentFolderValidatorDescription(t *testing.T) {
	ctx := context.Background()
	v := documentFolderValidator{}
	if v.Description(ctx) == "" || v.MarkdownDescription(ctx) != v.Description(ctx) {
		t.Fatal("folder validator needs a description")
	}
}

// ── upload ──────────────────────────────────────────────────────────────

// documentsWith is a documents block with the given columns and upload.
func documentsWith(columns types.Map, upload *DocumentsUploadModel) *DocumentsModel {
	return &DocumentsModel{
		Permission: types.StringValue("casedocuments"),
		Folder:     types.StringNull(),
		Columns:    columns,
		Filters:    types.ListNull(types.StringType),
		Upload:     upload,
	}
}

func uploadOf(maxBytes types.Int64, extensions types.List, columns types.Map) *DocumentsUploadModel {
	return &DocumentsUploadModel{MaxBytes: maxBytes, Extensions: extensions, Columns: columns}
}

func emptyUpload() *DocumentsUploadModel {
	return uploadOf(types.Int64Null(), types.ListNull(types.StringType), types.MapNull(types.StringType))
}

// A documents block without upload publishes exactly what it did before
// upload existed: no `upload` key. A published hint with the key null or
// absent reads back as no block, so an existing config plans no change.
func TestDocumentsNoUploadOmitsKey(t *testing.T) {
	model := &TableResourceModel{}
	readHint(t, documentsHint(``), model)
	model.Documents = documentsWith(columnsOf(map[string]string{"ShowOnPortal": "boolean"}), nil)
	got, _ := json.Marshal(writeHint(t, model)["documents"])
	if want := `{"columns":{"ShowOnPortal":"boolean"},"permission":"casedocuments"}`; string(got) != want {
		t.Fatalf("sent %s, want %s", got, want)
	}

	for name, upload := range map[string]string{"absent": ``, "null": `, "upload": null`} {
		t.Run(name, func(t *testing.T) {
			back := &TableResourceModel{}
			readHint(t, documentsHint(`, "documents": {"permission": "casedocuments"`+upload+`}`), back)
			if back.Documents == nil || back.Documents.Upload != nil {
				t.Fatalf("read back %+v, want documents without upload", back.Documents)
			}
		})
	}
}

// `upload {}` is upload with the API's defaults: sent as {}, and {} reads back
// as a block with every attribute null, which is what the config holds.
func TestDocumentsEmptyUpload(t *testing.T) {
	model := &TableResourceModel{}
	readHint(t, documentsHint(``), model)
	model.Documents = documentsWith(types.MapNull(types.StringType), emptyUpload())
	got, _ := json.Marshal(writeHint(t, model)["documents"])
	if want := `{"permission":"casedocuments","upload":{}}`; string(got) != want {
		t.Fatalf("sent %s, want %s", got, want)
	}

	back := &TableResourceModel{}
	readHint(t, documentsHint(`, "documents": `+string(got)), back)
	u := back.Documents.Upload
	if u == nil || !u.MaxBytes.IsNull() || !u.Extensions.IsNull() || !u.Columns.IsNull() {
		t.Fatalf("{} should read back as an empty block, got %+v", u)
	}

	// Empty values the API might hand back read as null too, not as empty.
	readHint(t, documentsHint(`, "documents": {"permission": "casedocuments", "upload": {"extensions": [], "columns": {}}}`), back)
	if u := back.Documents.Upload; u == nil || !u.Extensions.IsNull() || !u.Columns.IsNull() {
		t.Fatalf("empty values should read back null, got %+v", u)
	}
}

// Every attribute goes out under the API's names, each fixed column typed by
// its declared type, and comes back as the strings the config wrote.
func TestDocumentsUploadRoundTrip(t *testing.T) {
	columns := columnsOf(map[string]string{"ShowOnPortal": "boolean", "Rank": "number", "DocType": "string", "Due": "datetime"})
	in := uploadOf(types.Int64Value(1048576), filtersOf("pdf", "png"),
		columnsOf(map[string]string{"ShowOnPortal": "true", "Rank": "4", "DocType": "true", "Due": "2026-01-01"}))
	model := &TableResourceModel{}
	readHint(t, documentsHint(``), model)
	model.Documents = documentsWith(columns, in)

	got, _ := json.Marshal(writeHint(t, model)["documents"].(map[string]any)["upload"])
	want := `{"columns":{"DocType":"true","Due":"2026-01-01","Rank":4,"ShowOnPortal":true},"extensions":["pdf","png"],"maxBytes":1048576}`
	if string(got) != want {
		t.Fatalf("sent %s\nwant %s", got, want)
	}

	// Read back with no prior (an import) and with one (a refresh): no change either way.
	for name, prior := range map[string]*DocumentsModel{"import": nil, "refresh": documentsWith(columns, in)} {
		t.Run(name, func(t *testing.T) {
			back := &TableResourceModel{Documents: prior}
			readHint(t, documentsHint(`, "documents": {"permission": "casedocuments", "upload": `+string(got)+`}`), back)
			u := back.Documents.Upload
			if u == nil || !u.MaxBytes.Equal(in.MaxBytes) || !u.Extensions.Equal(in.Extensions) || !u.Columns.Equal(in.Columns) {
				t.Fatalf("changed in the round trip: %+v", u)
			}
		})
	}
}

// Optional attributes left out are left out of the JSON.
func TestDocumentsUploadOptionalOmitted(t *testing.T) {
	for name, c := range map[string]struct {
		upload *DocumentsUploadModel
		want   string
	}{
		"max_bytes only":  {uploadOf(types.Int64Value(1000), types.ListNull(types.StringType), types.MapNull(types.StringType)), `{"maxBytes":1000}`},
		"extensions only": {uploadOf(types.Int64Null(), filtersOf("pdf"), types.MapNull(types.StringType)), `{"extensions":["pdf"]}`},
		"columns only":    {uploadOf(types.Int64Null(), types.ListNull(types.StringType), columnsOf(map[string]string{"ShowOnPortal": "false"})), `{"columns":{"ShowOnPortal":false}}`},
		"unknown values":  {uploadOf(types.Int64Unknown(), types.ListUnknown(types.StringType), types.MapUnknown(types.StringType)), `{}`},
	} {
		t.Run(name, func(t *testing.T) {
			model := &TableResourceModel{}
			readHint(t, documentsHint(``), model)
			model.Documents = documentsWith(columnsOf(map[string]string{"ShowOnPortal": "boolean"}), c.upload)
			got, _ := json.Marshal(writeHint(t, model)["documents"].(map[string]any)["upload"])
			if string(got) != c.want {
				t.Fatalf("sent %s, want %s", got, c.want)
			}
		})
	}
}

// What the API (or the Table Manager) hands back reads as the config's
// strings: typed values, the same values as strings, and a number the API
// re-spelled.
func TestDocumentUploadColumnsRead(t *testing.T) {
	prior := uploadOf(types.Int64Null(), types.ListNull(types.StringType),
		columnsOf(map[string]string{"Rank": "4.0", "Score": "1000", "Other": "7"}))
	raw := map[string]json.RawMessage{
		"Flag":   json.RawMessage(`true`),
		"Off":    json.RawMessage(`false`),
		"AsText": json.RawMessage(`"true"`),
		"Rank":   json.RawMessage(`4`),     // sent as 4.0, kept as the config spelt it
		"Score":  json.RawMessage(`1e3`),   // the same number, spelt differently
		"Other":  json.RawMessage(`8`),     // a different number: the API's value
		"Neg":    json.RawMessage(` -2.5`), // no prior
		"Gone":   json.RawMessage(`null`),
		"Odd":    json.RawMessage(`{"a": 1}`),
	}
	got := documentUploadColumnsToModel(raw, prior)
	want := columnsOf(map[string]string{
		"Flag": "true", "Off": "false", "AsText": "true", "Rank": "4.0", "Score": "1000",
		"Other": "8", "Neg": "-2.5", "Odd": `{"a": 1}`,
	})
	if !got.Equal(want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}

	// A string the API holds is never matched as a number.
	textPrior := uploadOf(types.Int64Null(), types.ListNull(types.StringType), columnsOf(map[string]string{"Rank": "4.0"}))
	if got := documentUploadColumnsToModel(map[string]json.RawMessage{"Rank": json.RawMessage(`"4"`)}, textPrior); !got.Equal(columnsOf(map[string]string{"Rank": "4"})) {
		t.Fatalf("a string value should read as itself, got %v", got)
	}

	if got := documentUploadColumnsToModel(map[string]json.RawMessage{"Gone": json.RawMessage(`null`)}, nil); !got.IsNull() {
		t.Fatalf("only null values should read back as no columns, got %v", got)
	}
	if got := documentUploadColumnsToModel(nil, prior); !got.IsNull() {
		t.Fatalf("no columns should read back null, got %v", got)
	}
}

func TestDocumentColumnValueJSON(t *testing.T) {
	for _, c := range []struct{ typ, value, want string }{
		{"boolean", "true", `true`},
		{"boolean", "false", `false`},
		{"boolean", "yes", `"yes"`}, // refused at plan time; a string if it ever got here
		{"number", "4", `4`},
		{"number", "-2.5e3", `-2.5e3`},
		{"number", "four", `"four"`},
		{"number", "1e999", `"1e999"`},
		{"string", "true", `"true"`},
		{"datetime", "2026-01-01", `"2026-01-01"`},
		{"", "undeclared", `"undeclared"`},
		{"string", `say "hi"`, `"say \"hi\""`},
	} {
		if got := string(documentColumnValueJSON(c.typ, c.value)); got != c.want {
			t.Errorf("%s %q: sent %s, want %s", c.typ, c.value, got, c.want)
		}
	}
}

func TestDocumentExtensionProblem(t *testing.T) {
	for ext, ok := range map[string]bool{
		"pdf":    true,
		"docx":   true,
		"7z":     true,
		"":       false,
		"PDF":    false,
		".pdf":   false,
		"tar.gz": false,
		"p df":   false,
		"exe":    false,
		"js":     false,
		"ps1":    false,
		"dll":    false,
	} {
		if got := documentExtensionProblem(ext) == ""; got != ok {
			t.Errorf("extension %q: valid = %v, want %v (%s)", ext, got, ok, documentExtensionProblem(ext))
		}
	}
	if !strings.Contains(documentExtensionProblem("exe"), "executable") {
		t.Fatal("an executable should say so")
	}
}

func TestValidateDocumentsUpload(t *testing.T) {
	columns := columnsOf(map[string]string{"ShowOnPortal": "boolean", "Rank": "number", "DocType": "string", "Due": "datetime"})
	noList := types.ListNull(types.StringType)
	fixed := func(values map[string]string) *DocumentsUploadModel {
		return uploadOf(types.Int64Null(), noList, columnsOf(values))
	}

	cases := []struct {
		name    string
		d       *DocumentsModel
		want    string // substring of the error; "" = valid
		warning string // substring of a warning; "" = none
	}{
		{name: "no upload", d: documentsWith(columns, nil)},
		{name: "empty upload", d: documentsWith(types.MapNull(types.StringType), emptyUpload())},
		{name: "every type", d: documentsWith(columns, fixed(map[string]string{
			"ShowOnPortal": "false", "Rank": "-2.5", "DocType": "anything", "Due": "2026-01-01"}))},
		{name: "undeclared column", d: documentsWith(columns, fixed(map[string]string{"Hidden": "x"})),
			want: `"Hidden" isn't declared in documents.columns`},
		{name: "no columns declared", d: documentsWith(types.MapNull(types.StringType), fixed(map[string]string{"ShowOnPortal": "true"})),
			want: `"ShowOnPortal" isn't declared in documents.columns`},
		{name: "boolean spelt otherwise", d: documentsWith(columns, fixed(map[string]string{"ShowOnPortal": "True"})),
			want: `"ShowOnPortal" is a boolean column: "True" isn't a boolean`},
		{name: "boolean as yes", d: documentsWith(columns, fixed(map[string]string{"ShowOnPortal": "yes"})),
			want: `isn't a boolean`},
		{name: "not a number", d: documentsWith(columns, fixed(map[string]string{"Rank": "four"})),
			want: `"Rank" is a number column: "four" isn't a number`},
		{name: "number JSON can't carry", d: documentsWith(columns, fixed(map[string]string{"Rank": "1e999"})),
			want: `isn't a number`},
		{name: "number with spaces", d: documentsWith(columns, fixed(map[string]string{"Rank": " 4"})),
			want: `isn't a number`},
		{name: "unknown declared columns", d: documentsWith(types.MapUnknown(types.StringType), fixed(map[string]string{"Hidden": "x"}))},
		{name: "unknown declared type", d: documentsWith(
			types.MapValueMust(types.StringType, map[string]attr.Value{"Rank": types.StringUnknown()}),
			fixed(map[string]string{"Rank": "four"}))},
		{name: "unknown value", d: documentsWith(columns, uploadOf(types.Int64Null(), noList,
			types.MapValueMust(types.StringType, map[string]attr.Value{"Rank": types.StringUnknown()})))},
		{name: "unknown upload columns", d: documentsWith(columns, uploadOf(types.Int64Null(), noList, types.MapUnknown(types.StringType)))},
		{name: "max_bytes at the cap", d: documentsWith(columns, uploadOf(types.Int64Value(4194304), noList, types.MapNull(types.StringType)))},
		{name: "max_bytes above the cap", d: documentsWith(columns, uploadOf(types.Int64Value(4194305), noList, types.MapNull(types.StringType))),
			warning: "above the API's default cap"},
		{name: "unknown max_bytes", d: documentsWith(columns, uploadOf(types.Int64Unknown(), noList, types.MapNull(types.StringType)))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			diags := validateDocuments(c.d)
			var errs, warns strings.Builder
			for _, e := range diags.Errors() {
				errs.WriteString(e.Summary() + ": " + e.Detail() + "\n")
			}
			for _, w := range diags.Warnings() {
				warns.WriteString(w.Summary() + ": " + w.Detail() + "\n")
			}
			switch {
			case c.want == "" && diags.HasError():
				t.Fatalf("unexpected error: %s", errs.String())
			case c.want != "" && !strings.Contains(errs.String(), c.want):
				t.Fatalf("errors %q do not mention %q", errs.String(), c.want)
			case c.warning == "" && diags.WarningsCount() > 0:
				t.Fatalf("unexpected warning: %s", warns.String())
			case c.warning != "" && !strings.Contains(warns.String(), c.warning):
				t.Fatalf("warnings %q do not mention %q", warns.String(), c.warning)
			}
		})
	}
}

func TestDocumentExtensionValidatorDescription(t *testing.T) {
	ctx := context.Background()
	v := documentExtensionValidator{}
	if v.Description(ctx) == "" || v.MarkdownDescription(ctx) != v.Description(ctx) {
		t.Fatal("extension validator needs a description")
	}
}

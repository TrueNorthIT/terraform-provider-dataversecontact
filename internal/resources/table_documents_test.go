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

package resources

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// fieldObj is one field of `fields`: the given attributes, every other one null.
func fieldObj(t *testing.T, set map[string]attr.Value) types.Object {
	t.Helper()
	attrs := map[string]attr.Value{}
	for name, typ := range fieldAttrTypes() {
		attrs[name] = nullOf(typ)
	}
	for name, v := range set {
		attrs[name] = v
	}
	obj, diags := types.ObjectValue(fieldAttrTypes(), attrs)
	if diags.HasError() {
		t.Fatalf("field object: %v", diags.Errors())
	}
	return obj
}

func nullOf(typ attr.Type) attr.Value {
	switch typ {
	case types.StringType:
		return types.StringNull()
	case types.BoolType:
		return types.BoolNull()
	case types.Int64Type:
		return types.Int64Null()
	case types.Float64Type:
		return types.Float64Null()
	default:
		return types.ListNull(types.StringType)
	}
}

func fieldsOf(t *testing.T, fields map[string]map[string]attr.Value) types.Map {
	t.Helper()
	elements := map[string]attr.Value{}
	for name, set := range fields {
		elements[name] = fieldObj(t, set)
	}
	return types.MapValueMust(fieldObjectType(), elements)
}

func str(s string) types.String { return types.StringValue(s) }

// The case from #589: rules go out typed by the field's type, under the API's
// names, and come back as the config wrote them, on an import and a refresh.
func TestFieldWriteRulesRoundTrip(t *testing.T) {
	fields := fieldsOf(t, map[string]map[string]attr.Value{
		"title":          {"type": str("string"), "description": str("Title"), "required": types.BoolValue(true), "min_length": types.Int64Value(3), "max_length": types.Int64Value(200)},
		"caseorigincode": {"type": str("choice"), "description": str("Origin"), "fixed": str("3")},
		"casetypecode":   {"type": str("choice"), "description": str("Type"), "allowed": filtersOf("1", "2"), "default": str("1")},
		"category":       {"type": str("string"), "description": str("Category"), "allowed": filtersOf("Comp*", "Enquiry")},
		"estimate":       {"type": str("number"), "description": str("Estimate"), "min": types.Float64Value(0), "max": types.Float64Value(1000.5)},
		"flag":           {"type": str("boolean"), "description": str("Flag"), "read_only": types.BoolValue(true), "fixed": str("true")},
	})
	model := &TableResourceModel{}
	readHint(t, documentsHint(``), model)
	model.Fields = fields

	sent := writeHint(t, model)["fields"].(map[string]any)
	got, _ := json.Marshal(sent)
	want := `{"caseorigincode":{"description":"Origin","fixed":3,"type":"choice"},` +
		`"casetypecode":{"allowed":[1,2],"default":1,"description":"Type","type":"choice"},` +
		`"category":{"allowed":["Comp*","Enquiry"],"description":"Category","type":"string"},` +
		`"estimate":{"description":"Estimate","max":1000.5,"min":0,"type":"number"},` +
		`"flag":{"description":"Flag","fixed":true,"readOnly":true,"type":"boolean"},` +
		`"title":{"description":"Title","maxLength":200,"minLength":3,"required":true,"type":"string"}}`
	if string(got) != want {
		t.Fatalf("sent %s\nwant %s", got, want)
	}

	hint := documentsHint(``)
	hint = []byte(strings.Replace(string(hint), `"fields": {"title": {"type": "string", "description": "Title"}}`, `"fields": `+string(got), 1))
	for name, prior := range map[string]types.Map{"import": types.MapNull(fieldObjectType()), "refresh": fields} {
		t.Run(name, func(t *testing.T) {
			back := &TableResourceModel{Fields: prior}
			readHint(t, hint, back)
			if !back.Fields.Equal(fields) {
				t.Fatalf("changed in the round trip:\n got %v\nwant %v", back.Fields, fields)
			}
		})
	}
}

// A field with no rules publishes exactly what it did before they existed.
func TestFieldWithoutWriteRulesSendsNone(t *testing.T) {
	model := &TableResourceModel{}
	readHint(t, documentsHint(``), model)
	model.Fields = fieldsOf(t, map[string]map[string]attr.Value{
		"title": {"type": str("string"), "description": str("Title"), "required": types.BoolValue(false)},
	})
	got, _ := json.Marshal(writeHint(t, model)["fields"])
	if want := `{"title":{"description":"Title","type":"string"}}`; string(got) != want {
		t.Fatalf("sent %s, want %s", got, want)
	}

	// required = false isn't sent, and reads back false, not null.
	back := &TableResourceModel{Fields: model.Fields}
	readHint(t, documentsHint(``), back)
	if !back.Fields.Equal(model.Fields) {
		t.Fatalf("required = false didn't survive: %v", back.Fields)
	}
}

// A number the API hands back keeps the config's spelling when it's the same number.
func TestRuleValueKeepsPriorSpelling(t *testing.T) {
	if got := ruleValueModel(json.RawMessage(`3`), str("3.0")); got.ValueString() != "3.0" {
		t.Fatalf("got %q, want the prior spelling 3.0", got.ValueString())
	}
	if got := ruleValueModel(json.RawMessage(`4`), str("3.0")); got.ValueString() != "4" {
		t.Fatalf("got %q, want 4 (a different number)", got.ValueString())
	}
	if got := ruleValueModel(json.RawMessage(`"Sent from the portal"`), nil); got.ValueString() != "Sent from the portal" {
		t.Fatalf("got %q", got.ValueString())
	}
	if got := ruleValueModel(nil, nil); !got.IsNull() {
		t.Fatalf("absent should read null, got %v", got)
	}
}

func TestRuleValueJSON(t *testing.T) {
	for _, c := range []struct{ typ, value, want string }{
		{"choice", "3", `3`},
		{"choice", "three", `"three"`},
		{"number", "1.5", `1.5`},
		{"boolean", "true", `true`},
		{"boolean", "yes", `"yes"`},
		{"string", "3", `"3"`},
		{"datetime", "2026-12-01T09:00:00Z", `"2026-12-01T09:00:00Z"`},
	} {
		if got := string(ruleValueJSON(c.typ, c.value)); got != c.want {
			t.Errorf("%s %q: got %s, want %s", c.typ, c.value, got, c.want)
		}
	}
}

func TestValidateFieldWriteRules(t *testing.T) {
	for _, c := range []struct {
		name  string
		field map[string]attr.Value
		want  string // "" = no error
	}{
		{"none", map[string]attr.Value{"type": str("string")}, ""},
		{"the #589 case", map[string]attr.Value{"type": str("choice"), "allowed": filtersOf("1", "2"), "default": str("1")}, ""},
		{"fixed on read-only", map[string]attr.Value{"type": str("choice"), "read_only": types.BoolValue(true), "fixed": str("3")}, ""},
		{"pattern on text", map[string]attr.Value{"type": str("string"), "allowed": filtersOf("Comp*")}, ""},
		{"fixed beside allowed", map[string]attr.Value{"type": str("choice"), "fixed": str("3"), "allowed": filtersOf("3")}, "fixed is the only value it can take, so allowed"},
		{"lookup", map[string]attr.Value{"type": str("lookup"), "required": types.BoolValue(true)}, "takes no write rules (required)"},
		{"required on read-only", map[string]attr.Value{"type": str("string"), "read_only": types.BoolValue(true), "required": types.BoolValue(true)}, "a caller never sends it and required"},
		{"choice label", map[string]attr.Value{"type": str("choice"), "fixed": str("Web")}, "isn't a choice value"},
		{"bad boolean", map[string]attr.Value{"type": str("boolean"), "default": str("yes")}, "isn't true or false"},
		{"pattern in fixed", map[string]attr.Value{"type": str("string"), "fixed": str("A*")}, "can't be a pattern"},
		{"pattern on a number", map[string]attr.Value{"type": str("number"), "allowed": filtersOf("1*")}, "isn't a number"},
		{"allowed on a date", map[string]attr.Value{"type": str("datetime"), "allowed": filtersOf("2026-01-01")}, "allowed doesn't apply to dates"},
		{"length on a number", map[string]attr.Value{"type": str("number"), "min_length": types.Int64Value(1)}, "min_length applies to string fields"},
		{"range on text", map[string]attr.Value{"type": str("string"), "max": types.Float64Value(1)}, "max applies to number fields"},
		{"lengths backwards", map[string]attr.Value{"type": str("string"), "min_length": types.Int64Value(5), "max_length": types.Int64Value(2)}, "min_length (5) is more than max_length (2)"},
		{"range backwards", map[string]attr.Value{"type": str("number"), "min": types.Float64Value(5), "max": types.Float64Value(2)}, "min (5) is more than max (2)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			attrs := fieldObj(t, c.field).Attributes()
			diags := validateFieldWriteRules("f", attrs)
			if c.want == "" {
				if diags.HasError() {
					t.Fatalf("unexpected: %v", diags.Errors())
				}
				return
			}
			if !diags.HasError() || !strings.Contains(diags.Errors()[0].Detail(), c.want) {
				t.Fatalf("want an error containing %q, got %v", c.want, diags.Errors())
			}
		})
	}
}

// ── documents.upload.allowed ────────────────────────────────────────────

func uploadAllowed(library, folder types.List) *DocumentsUploadModel {
	u := emptyUpload()
	u.Allowed = &DocumentsUploadAllowedModel{Library: library, Folder: folder}
	return u
}

func TestUploadAllowedRoundTrip(t *testing.T) {
	in := uploadAllowed(filtersOf("*-Shared-*"), filtersOf("", "Photos*"))
	model := &TableResourceModel{}
	readHint(t, documentsHint(``), model)
	model.Documents = documentsWith(types.MapNull(types.StringType), in)

	got, _ := json.Marshal(writeHint(t, model)["documents"].(map[string]any)["upload"])
	if want := `{"allowed":{"folder":["","Photos*"],"library":["*-Shared-*"]}}`; string(got) != want {
		t.Fatalf("sent %s, want %s", got, want)
	}
	back := &TableResourceModel{}
	readHint(t, documentsHint(`, "documents": {"permission": "casedocuments", "upload": `+string(got)+`}`), back)
	a := back.Documents.Upload.Allowed
	if a == nil || !a.Library.Equal(in.Allowed.Library) || !a.Folder.Equal(in.Allowed.Folder) {
		t.Fatalf("changed in the round trip: %+v", a)
	}
}

// No allowed: no key, so an upload block publishes what it did before; and
// a hint without it reads back as unset.
func TestUploadAllowedOmitted(t *testing.T) {
	model := &TableResourceModel{}
	readHint(t, documentsHint(``), model)
	model.Documents = documentsWith(types.MapNull(types.StringType), emptyUpload())
	got, _ := json.Marshal(writeHint(t, model)["documents"].(map[string]any)["upload"])
	if string(got) != `{}` {
		t.Fatalf("sent %s, want {}", got)
	}
	back := &TableResourceModel{}
	readHint(t, documentsHint(`, "documents": {"permission": "casedocuments", "upload": {}}`), back)
	if back.Documents.Upload.Allowed != nil {
		t.Fatalf("want no allowed, got %+v", back.Documents.Upload.Allowed)
	}
}

func TestValidateUploadAllowed(t *testing.T) {
	declared := columnsOf(map[string]string{"ShowOnPortal": "boolean"})
	for _, c := range []struct {
		name   string
		upload *DocumentsUploadModel
		want   string
	}{
		{"library pattern", uploadAllowed(filtersOf("*-Shared-*"), types.ListNull(types.StringType)), ""},
		{"beside libraries", func() *DocumentsUploadModel {
			u := uploadAllowed(filtersOf("*"), types.ListNull(types.StringType))
			u.Libraries = filtersOf("Evidence")
			return u
		}(), "allowed.library beside libraries"},
		{"beside a fixed library", func() *DocumentsUploadModel {
			u := uploadAllowed(filtersOf("*"), types.ListNull(types.StringType))
			u.Columns = columnsOf(map[string]string{"library": "Evidence"})
			return u
		}(), "allowed.library beside a fixed library"},
		{"beside a fixed folder", func() *DocumentsUploadModel {
			u := uploadAllowed(types.ListNull(types.StringType), filtersOf("Photos"))
			u.Columns = columnsOf(map[string]string{"folder": "Evidence"})
			return u
		}(), "allowed.folder beside a fixed folder"},
	} {
		t.Run(c.name, func(t *testing.T) {
			diags := validateDocumentsUpload(documentsWith(declared, c.upload))
			if c.want == "" {
				if diags.HasError() {
					t.Fatalf("unexpected: %v", diags.Errors())
				}
				return
			}
			found := false
			for _, d := range diags.Errors() {
				if strings.Contains(d.Summary(), c.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("want %q, got %v", c.want, diags.Errors())
			}
		})
	}
}

func TestAllowedLibraryAndFolderValidators(t *testing.T) {
	check := func(v validator.String, value string) bool {
		resp := &validator.StringResponse{}
		v.ValidateString(context.Background(), validator.StringRequest{Path: path.Root("x"), ConfigValue: types.StringValue(value)}, resp)
		return !resp.Diagnostics.HasError()
	}
	for value, ok := range map[string]bool{"*-Shared-*": true, "Case-Docs": true, "Bad/Name": false, "": false} {
		if got := check(allowedLibraryValidator{}, value); got != ok {
			t.Errorf("library %q: valid = %v, want %v", value, got, ok)
		}
	}
	for value, ok := range map[string]bool{"": true, "Photos": true, "Evidence*": true, "Photos/2026": true, "/Photos": false, "Photos ": false, "../up": false} {
		if got := check(allowedFolderValidator{}, value); got != ok {
			t.Errorf("folder %q: valid = %v, want %v", value, got, ok)
		}
	}
}

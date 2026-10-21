package resources

import (
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"

	"github.com/TrueNorthIT/terraform-provider-dataversecontact/internal/client"
)

func TestAddSaveWarnings(t *testing.T) {
	route := "case"
	v := &client.ValidationInfo{
		Errors: []client.ValidationIssue{
			{Field: "fields.gone", Code: "FIELD_NOT_FOUND", Message: "reported as an error instead"},
			{Field: "defaultSelect", Code: "DEFAULT_SELECT_UNKNOWN", Message: "x is not a field"},
		},
		Warnings: []client.ValidationIssue{
			{Field: "fields.title", Code: "TYPE_MISMATCH", Message: "declared as string"},
			{Code: "METADATA_UNAVAILABLE", Message: "could not validate"},
		},
	}
	rules := []client.ConfigFinding{{Rule: "me-only-route-name", Route: &route, Message: "answers on /team and /all only"}}

	var diags diag.Diagnostics
	addSaveWarnings(&diags, route, v, rules)
	if diags.WarningsCount() != 1 || diags.HasError() {
		t.Fatalf("want one warning, got %v", diags)
	}
	w := diags.Warnings()[0]
	if w.Summary() != `The API reported 4 problem(s) with "case"` {
		t.Errorf("summary %q", w.Summary())
	}
	for _, want := range []string{
		"- defaultSelect: x is not a field (DEFAULT_SELECT_UNKNOWN, saved anyway)",
		"- fields.title: declared as string (TYPE_MISMATCH)",
		"- could not validate (METADATA_UNAVAILABLE)",
		"- answers on /team and /all only (me-only-route-name)",
	} {
		if !strings.Contains(w.Detail(), want) {
			t.Errorf("detail missing %q:\n%s", want, w.Detail())
		}
	}
	if strings.Contains(w.Detail(), "fields.gone") {
		t.Error("a stripped field is an error, not a warning")
	}

	var none diag.Diagnostics
	addSaveWarnings(&none, route, &client.ValidationInfo{}, nil)
	addSaveWarnings(&none, route, nil, nil)
	if len(none) != 0 {
		t.Errorf("nothing to report, got %v", none)
	}
}

func TestAddSaveError(t *testing.T) {
	var diags diag.Diagnostics
	addSaveError(&diags, "Failed to create table", &client.StrippedFieldsError{
		Route: "case",
		Fields: []client.ValidationIssue{
			{Field: "fields.a", Code: "FIELD_NOT_FOUND", Message: `Field "a" does not exist on incident`},
			{Field: "fields.b", Code: "FIELD_NOT_FOUND", Message: `Field "b" does not exist on incident.`},
		},
	})
	if diags.ErrorsCount() != 2 {
		t.Fatalf("want an error per field, got %v", diags)
	}
	for i, name := range []string{"a", "b"} {
		d, ok := diags.Errors()[i].(diag.DiagnosticWithPath)
		if !ok || !d.Path().Equal(path.Root("fields").AtMapKey(name)) {
			t.Errorf("error %d not on fields[%q]: %v", i, name, diags.Errors()[i])
			continue
		}
		if !strings.HasPrefix(d.Detail(), `Field "`+name+`" does not exist on incident.`+"\n") {
			t.Errorf("detail %q", d.Detail())
		}
	}

	var other diag.Diagnostics
	addSaveError(&other, "Failed to update table", errors.New("boom"))
	if other.ErrorsCount() != 1 || other.Errors()[0].Summary() != "Failed to update table" || other.Errors()[0].Detail() != "boom" {
		t.Errorf("got %v", other)
	}
}

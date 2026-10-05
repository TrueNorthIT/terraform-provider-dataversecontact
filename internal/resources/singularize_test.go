package resources

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

// dataverse_logical_name defaults to the singular of dataverse_table, so a
// wrong rule here publishes a table against an entity that doesn't exist.
func TestSingularize(t *testing.T) {
	for set, want := range map[string]string{
		"incidents":                  "incident",
		"bookableresourcecategories": "bookableresourcecategory",
		"bookingstatuses":            "bookingstatus",
		"addresses":                  "address",
		"boxes":                      "box",
		"wishes":                     "wish",
		"matches":                    "match",
		"tn_citizenservicebookings":  "tn_citizenservicebooking",
		"msdyn_projectcategories":    "msdyn_projectcategory",
		"equipment":                  "equipment",
	} {
		if got := singularize(set); got != want {
			t.Errorf("singularize(%q) = %q, want %q", set, got, want)
		}
	}
}

func TestSchemaJSONToModelRejectsInvalidJSON(t *testing.T) {
	var diags diag.Diagnostics
	var model TableResourceModel
	schemaJSONToModel(context.Background(), json.RawMessage(`{"routeName":`), &model, &diags)
	if !diags.HasError() {
		t.Fatal("expected an error for a truncated schema")
	}
}

func TestDropDerivedExpandsWithoutField(t *testing.T) {
	in := []ExpandJSON{{LookupField: "regardingobjectid_sb_bin"}}
	if got := dropDerivedExpands(in, ""); len(got) != 1 {
		t.Errorf("no polymorphic field must keep every expand, got %v", got)
	}
}

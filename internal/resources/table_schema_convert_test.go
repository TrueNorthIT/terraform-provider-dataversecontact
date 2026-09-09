package resources

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestJoinStepReverseRoundTrip verifies the `reverse` flag survives the
// model → JSON → model conversion, and that a forward-only step reads back as
// a null bool (so forward tables never show a perpetual `reverse` plan diff).
func TestJoinStepReverseRoundTrip(t *testing.T) {
	var diags diag.Diagnostics

	in := []JoinStepModel{
		{
			Table:   types.StringValue("tn_citizenservicebookings"),
			From:    types.StringValue("tn_booking_csb"),
			Key:     types.StringValue(""),
			Reverse: types.BoolValue(true),
		},
		{
			Table: types.StringValue("contacts"),
			From:  types.StringValue("tn_Citizen"),
			Key:   types.StringValue("contactid"),
		},
	}

	j := joinStepsModelToJSON(context.Background(), in, &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if len(j) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(j))
	}
	if !j[0].Reverse {
		t.Errorf("step 0: reverse should marshal to true")
	}
	if j[1].Reverse {
		t.Errorf("step 1: reverse should marshal to false")
	}

	out := joinStepsJSONToModel(j)
	if !out[0].Reverse.ValueBool() {
		t.Errorf("step 0: round-trip lost reverse=true")
	}
	if !out[1].Reverse.IsNull() {
		t.Errorf("step 1: forward-only reverse should read back as null, got %v", out[1].Reverse)
	}
}

// TestPolymorphicLookupRoundTrip covers the family rule through
// model → JSON → model.
//
// The read_only assertions are the point. The API's default is read-only, and
// the block is a SingleNestedBlock — always present in the model, whether or
// not the author wrote one. So an unset read_only must serialise as ABSENT
// rather than `false`: sending false would silently open every derived table
// for writing, on a config that never mentioned writing.
func TestPolymorphicLookupRoundTrip(t *testing.T) {
	var diags diag.Diagnostics

	model := &TableResourceModel{
		RouteName:            types.StringValue("case"),
		DataverseTable:       types.StringValue("incidents"),
		DataverseLogicalName: types.StringValue("incident"),
		PrimaryKey:           types.StringValue("incidentid"),
		RequiredPermission:   types.StringValue("case"),
		DefaultSelect:        stringsToTFList([]string{"incidentid"}),
		LookupFields:         stringsToTFList([]string{"title"}),
		Fields:               fieldsJSONToModel(context.Background(), map[string]FieldHintJSON{}, &diags),
		PolymorphicLookup: &PolymorphicLookupModel{
			Field:              types.StringValue("sb_service_recordid"),
			RequiredPermission: types.StringValue("servicerecord"),
			RoutePrefixStrip:   types.StringValue("sb_"),
			TargetPrefix:       types.StringNull(),
			ExcludeTargets:     stringsToTFListOrNull([]string{"sb_service_request"}),
			ReadOnly:           types.BoolNull(),
		},
	}

	raw := modelToSchemaJSON(context.Background(), model, &diags)
	if diags.HasError() {
		t.Fatalf("modelToSchemaJSON: %v", diags.Errors())
	}
	if !strings.Contains(string(raw), `"polymorphicLookup"`) {
		t.Fatalf("rule missing from payload: %s", raw)
	}
	// An unset read_only must not reach the API at all.
	if strings.Contains(string(raw), `"readOnly"`) {
		t.Fatalf("unset read_only was serialised; it would override the API default: %s", raw)
	}
	// An unset optional string must not reach it either, or the API sees an
	// empty targetPrefix where it should see none.
	if strings.Contains(string(raw), `"targetPrefix"`) {
		t.Fatalf("unset target_prefix was serialised: %s", raw)
	}

	out := &TableResourceModel{}
	schemaJSONToModel(context.Background(), raw, out, &diags)
	if diags.HasError() {
		t.Fatalf("schemaJSONToModel: %v", diags.Errors())
	}
	if out.PolymorphicLookup == nil {
		t.Fatal("rule did not survive the round trip")
	}
	if got := out.PolymorphicLookup.Field.ValueString(); got != "sb_service_recordid" {
		t.Errorf("field = %q, want sb_service_recordid", got)
	}
	if got := out.PolymorphicLookup.RequiredPermission.ValueString(); got != "servicerecord" {
		t.Errorf("required_permission = %q, want servicerecord", got)
	}
	if got := out.PolymorphicLookup.RoutePrefixStrip.ValueString(); got != "sb_" {
		t.Errorf("route_prefix_strip = %q, want sb_", got)
	}
	// Null, not false — otherwise every apply shows a perpetual diff on a
	// value the author never set.
	if !out.PolymorphicLookup.ReadOnly.IsNull() {
		t.Errorf("read_only read back as %v, want null", out.PolymorphicLookup.ReadOnly)
	}
	if !out.PolymorphicLookup.TargetPrefix.IsNull() {
		t.Errorf("target_prefix read back as %v, want null", out.PolymorphicLookup.TargetPrefix)
	}
	excludes := tfListToStrings(context.Background(), out.PolymorphicLookup.ExcludeTargets, &diags)
	if len(excludes) != 1 || excludes[0] != "sb_service_request" {
		t.Errorf("exclude_targets = %v, want [sb_service_request]", excludes)
	}
}

// TestPolymorphicLookupExplicitFalseIsSent is the other half: an author who
// deliberately writes `read_only = false` must have it honoured, so the value
// has to travel when it IS set.
func TestPolymorphicLookupExplicitFalseIsSent(t *testing.T) {
	var diags diag.Diagnostics

	model := &TableResourceModel{
		RouteName:            types.StringValue("case"),
		DataverseTable:       types.StringValue("incidents"),
		DataverseLogicalName: types.StringValue("incident"),
		PrimaryKey:           types.StringValue("incidentid"),
		RequiredPermission:   types.StringValue("case"),
		DefaultSelect:        stringsToTFList([]string{"incidentid"}),
		LookupFields:         stringsToTFList([]string{"title"}),
		Fields:               fieldsJSONToModel(context.Background(), map[string]FieldHintJSON{}, &diags),
		PolymorphicLookup: &PolymorphicLookupModel{
			Field:              types.StringValue("sb_service_recordid"),
			RequiredPermission: types.StringValue("servicerecord"),
			ReadOnly:           types.BoolValue(false),
		},
	}

	raw := modelToSchemaJSON(context.Background(), model, &diags)
	if diags.HasError() {
		t.Fatalf("modelToSchemaJSON: %v", diags.Errors())
	}
	if !strings.Contains(string(raw), `"readOnly":false`) {
		t.Fatalf("explicit read_only = false did not reach the API: %s", raw)
	}
}

// TestNoPolymorphicLookupOmitsRule guards the SingleNestedBlock trap from the
// other direction: a table that declares no block must send no rule, or every
// existing table would start deriving routes from a lookup it never named.
func TestNoPolymorphicLookupOmitsRule(t *testing.T) {
	var diags diag.Diagnostics

	model := &TableResourceModel{
		RouteName:            types.StringValue("contact"),
		DataverseTable:       types.StringValue("contacts"),
		DataverseLogicalName: types.StringValue("contact"),
		PrimaryKey:           types.StringValue("contactid"),
		RequiredPermission:   types.StringValue("contact"),
		DefaultSelect:        stringsToTFList([]string{"contactid"}),
		LookupFields:         stringsToTFList([]string{"fullname"}),
		Fields:               fieldsJSONToModel(context.Background(), map[string]FieldHintJSON{}, &diags),
		// As Terraform hands it over for a config with no block written.
		PolymorphicLookup: &PolymorphicLookupModel{Field: types.StringNull()},
	}

	raw := modelToSchemaJSON(context.Background(), model, &diags)
	if diags.HasError() {
		t.Fatalf("modelToSchemaJSON: %v", diags.Errors())
	}
	if strings.Contains(string(raw), `"polymorphicLookup"`) {
		t.Fatalf("a table with no block sent a rule: %s", raw)
	}
}

// The anchor of a polymorphic family gets that family's expands back from the
// API, derived from live metadata rather than declared here. Adopting them as
// state made every apply fail — "Provider produced inconsistent result after
// apply: .expand block count changed from 0 to N" — and then offer to delete
// expands the config never wrote. They must not reach the model.
func TestDerivedExpandsAreNotAdopted(t *testing.T) {
	var diags diag.Diagnostics
	var model TableResourceModel

	raw := []byte(`{
	  "routeName": "case",
	  "dataverseTable": "incidents",
	  "dataverseLogicalName": "incident",
	  "requiredPermission": "case",
	  "primaryKey": "incidentid",
	  "defaultSelect": ["incidentid"],
	  "lookupFields": ["title"],
	  "fields": {"incidentid": {"type": "string", "description": "Id"}},
	  "polymorphicLookup": {
	    "field": "sb_service_recordid",
	    "requiredPermission": "servicerecord"
	  },
	  "expands": [
	    {"lookupField": "sb_service_recordid_sb_missed_bin", "relatedTable": "sb_missed_bin", "fields": []},
	    {"lookupField": "sb_service_recordid_sb_report_flooding", "relatedTable": "sb_report_flooding", "fields": []},
	    {"lookupField": "sb_address", "relatedTable": "sb_location", "fields": []}
	  ]
	}`)

	schemaJSONToModel(context.Background(), raw, &model, &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	// The declared expand on an unrelated lookup survives; the two derived off
	// the rule's own field do not.
	if len(model.Expand) != 1 {
		got := make([]string, 0, len(model.Expand))
		for _, e := range model.Expand {
			got = append(got, e.LookupField.ValueString())
		}
		t.Fatalf("expected only the declared expand to survive, got %v", got)
	}
	if model.Expand[0].LookupField.ValueString() != "sb_address" {
		t.Errorf("expected sb_address to survive, got %s", model.Expand[0].LookupField.ValueString())
	}
}

// Without a rule, nothing is filtered: a table that genuinely declares expands
// named after a lookup keeps every one of them.
func TestExpandsKeptWithoutPolymorphicRule(t *testing.T) {
	var diags diag.Diagnostics
	var model TableResourceModel

	raw := []byte(`{
	  "routeName": "case",
	  "dataverseTable": "incidents",
	  "dataverseLogicalName": "incident",
	  "requiredPermission": "case",
	  "primaryKey": "incidentid",
	  "defaultSelect": ["incidentid"],
	  "lookupFields": ["title"],
	  "fields": {"incidentid": {"type": "string", "description": "Id"}},
	  "expands": [
	    {"lookupField": "sb_service_recordid_sb_missed_bin", "relatedTable": "sb_missed_bin", "fields": []},
	    {"lookupField": "sb_address", "relatedTable": "sb_location", "fields": []}
	  ]
	}`)

	schemaJSONToModel(context.Background(), raw, &model, &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if len(model.Expand) != 2 {
		t.Fatalf("expected both expands to survive without a rule, got %d", len(model.Expand))
	}
}

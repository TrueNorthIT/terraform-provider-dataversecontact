package resources

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
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

	out := joinStepsJSONToModel(j, nil)
	if !out[0].Reverse.ValueBool() {
		t.Errorf("step 0: round-trip lost reverse=true")
	}
	if !out[1].Reverse.IsNull() {
		t.Errorf("step 1: forward-only reverse should read back as null, got %v", out[1].Reverse)
	}
}

// TestJoinStepMatchRoundTrip: a reverse step's match goes out as integers and
// comes back as the same map; a step without one reads back null, so a
// config that never mentions match plans no change.
func TestJoinStepMatchRoundTrip(t *testing.T) {
	var diags diag.Diagnostics
	ctx := context.Background()
	in := []JoinStepModel{
		{
			Table:   types.StringValue("activityparties"),
			From:    types.StringValue("email_activity_parties"),
			Key:     types.StringValue("activitypartyid"),
			Reverse: types.BoolValue(true),
			Match:   matchJSONToModel(map[string][]int64{"participationtypemask": {1, 2, 3, 4}}),
		},
		{
			Table: types.StringValue("contacts"),
			From:  types.StringValue("partyid_contact"),
			Key:   types.StringValue("contactid"),
			Match: types.MapNull(matchElementType),
		},
	}

	j := joinStepsModelToJSON(ctx, in, &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	raw, _ := json.Marshal(j)
	if want := `"match":{"participationtypemask":[1,2,3,4]}`; !strings.Contains(string(raw), want) {
		t.Errorf("expected %s in %s", want, raw)
	}
	if strings.Count(string(raw), `"match"`) != 1 {
		t.Errorf("a step without match should send none: %s", raw)
	}

	out := joinStepsJSONToModel(j, nil)
	if !out[0].Match.Equal(in[0].Match) {
		t.Errorf("step 0: match changed in the round trip: %v", out[0].Match)
	}
	if !out[1].Match.IsNull() {
		t.Errorf("step 1: no match should read back null, got %v", out[1].Match)
	}
}

// TestFilesRoundTrip: own-row files send no child keys; child files send all
// three; absent files are nil both ways.
func TestFilesRoundTrip(t *testing.T) {
	own := &FilesModel{
		Table:        types.StringNull(),
		PrimaryKey:   types.StringNull(),
		ParentColumn: types.StringNull(),
		Content:      types.StringValue("documentbody"),
		FileName:     types.StringValue("filename"),
		MimeType:     types.StringValue("mimetype"),
		Size:         types.StringValue("filesize"),
	}
	raw, _ := json.Marshal(filesModelToJSON(own))
	if want := `{"content":"documentbody","fileName":"filename","mimeType":"mimetype","size":"filesize"}`; string(raw) != want {
		t.Errorf("own-row files: got %s, want %s", raw, want)
	}
	back := filesJSONToModel(filesModelToJSON(own))
	if !back.Table.IsNull() || back.Content.ValueString() != "documentbody" {
		t.Errorf("own-row files changed in the round trip: %+v", back)
	}

	child := *own
	child.Table = types.StringValue("activitymimeattachments")
	child.PrimaryKey = types.StringValue("activitymimeattachmentid")
	child.ParentColumn = types.StringValue("objectid")
	raw, _ = json.Marshal(filesModelToJSON(&child))
	for _, want := range []string{`"table":"activitymimeattachments"`, `"primaryKey":"activitymimeattachmentid"`, `"parentColumn":"objectid"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("child files: expected %s in %s", want, raw)
		}
	}

	if filesModelToJSON(nil) != nil || filesJSONToModel(nil) != nil {
		t.Error("absent files should stay absent")
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
		Fields:               fieldsJSONToModel(context.Background(), map[string]FieldHintJSON{}, types.MapNull(fieldObjectType()), &diags),
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
	// No business_process written, none sent, none read back — a nil pointer
	// on both sides, or every plan would show one appearing.
	if strings.Contains(string(raw), `"businessProcess"`) {
		t.Fatalf("unset business_process was serialised: %s", raw)
	}
	if out.PolymorphicLookup.BusinessProcess != nil {
		t.Errorf("business_process read back as %v, want nil", out.PolymorphicLookup.BusinessProcess)
	}
	if out.BusinessProcess != nil {
		t.Errorf("table-level business_process read back as %v, want nil", out.BusinessProcess)
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
		Fields:               fieldsJSONToModel(context.Background(), map[string]FieldHintJSON{}, types.MapNull(fieldObjectType()), &diags),
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
		Fields:               fieldsJSONToModel(context.Background(), map[string]FieldHintJSON{}, types.MapNull(fieldObjectType()), &diags),
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

// TestBusinessProcessRoundTrip: the flag travels on the rule as
// `businessProcess`, with `exposeAs` only when the author named one, and
// comes back exactly as written.
func TestBusinessProcessRoundTrip(t *testing.T) {
	var diags diag.Diagnostics

	model := &TableResourceModel{
		RouteName:            types.StringValue("case"),
		DataverseTable:       types.StringValue("incidents"),
		DataverseLogicalName: types.StringValue("incident"),
		PrimaryKey:           types.StringValue("incidentid"),
		RequiredPermission:   types.StringValue("case"),
		DefaultSelect:        stringsToTFList([]string{"incidentid"}),
		LookupFields:         stringsToTFList([]string{"title"}),
		Fields:               fieldsJSONToModel(context.Background(), map[string]FieldHintJSON{}, types.MapNull(fieldObjectType()), &diags),
		PolymorphicLookup: &PolymorphicLookupModel{
			Field:              types.StringValue("sb_service_recordid"),
			RequiredPermission: types.StringValue("servicerecord"),
			BusinessProcess:    &BusinessProcessModel{ExposeAs: types.StringValue("progress")},
		},
	}

	raw := modelToSchemaJSON(context.Background(), model, &diags)
	if diags.HasError() {
		t.Fatalf("modelToSchemaJSON: %v", diags.Errors())
	}
	if !strings.Contains(string(raw), `"businessProcess":{"exposeAs":"progress"}`) {
		t.Fatalf("business_process missing or misspelt in payload: %s", raw)
	}

	out := &TableResourceModel{}
	schemaJSONToModel(context.Background(), raw, out, &diags)
	if diags.HasError() {
		t.Fatalf("schemaJSONToModel: %v", diags.Errors())
	}
	if out.PolymorphicLookup == nil || out.PolymorphicLookup.BusinessProcess == nil {
		t.Fatal("business_process did not survive the round trip")
	}
	if got := out.PolymorphicLookup.BusinessProcess.ExposeAs.ValueString(); got != "progress" {
		t.Errorf("expose_as = %q, want progress", got)
	}
}

// TestBusinessProcessEmptyObjectRoundTrip: `business_process = {}` means
// "yes, with the default name". It has to reach the API as an empty object
// (not vanish) and read back with a NULL expose_as (not an empty string), or
// the config `{}` would diff against state on every plan.
func TestBusinessProcessEmptyObjectRoundTrip(t *testing.T) {
	var diags diag.Diagnostics

	model := &TableResourceModel{
		RouteName:            types.StringValue("request"),
		DataverseTable:       types.StringValue("sb_requests"),
		DataverseLogicalName: types.StringValue("sb_request"),
		PrimaryKey:           types.StringValue("sb_requestid"),
		RequiredPermission:   types.StringValue("request"),
		DefaultSelect:        stringsToTFList([]string{"sb_requestid"}),
		LookupFields:         stringsToTFList([]string{"sb_name"}),
		Fields:               fieldsJSONToModel(context.Background(), map[string]FieldHintJSON{}, types.MapNull(fieldObjectType()), &diags),
		BusinessProcess:      &BusinessProcessModel{ExposeAs: types.StringNull()},
		PolymorphicLookup:    &PolymorphicLookupModel{Field: types.StringNull()},
	}

	raw := modelToSchemaJSON(context.Background(), model, &diags)
	if diags.HasError() {
		t.Fatalf("modelToSchemaJSON: %v", diags.Errors())
	}
	if !strings.Contains(string(raw), `"businessProcess":{}`) {
		t.Fatalf("table-level business_process = {} did not reach the API as an empty object: %s", raw)
	}
	if strings.Contains(string(raw), `"exposeAs"`) {
		t.Fatalf("an unset expose_as was serialised: %s", raw)
	}

	out := &TableResourceModel{}
	schemaJSONToModel(context.Background(), raw, out, &diags)
	if diags.HasError() {
		t.Fatalf("schemaJSONToModel: %v", diags.Errors())
	}
	if out.BusinessProcess == nil {
		t.Fatal("table-level business_process did not survive the round trip")
	}
	if !out.BusinessProcess.ExposeAs.IsNull() {
		t.Errorf("expose_as read back as %v, want null", out.BusinessProcess.ExposeAs)
	}
	if out.PolymorphicLookup != nil {
		t.Errorf("a rule appeared from nowhere: %v", out.PolymorphicLookup)
	}
}

// aliases and lookup_search_contains come back from the API as [] when unset.
// They must read as a known empty list, and be Computed, so a config that omits
// them and one that writes [] both apply without an inconsistent-result error.
func TestEmptyListAttrsReadAsEmptyList(t *testing.T) {
	ctx := context.Background()
	var diags diag.Diagnostics

	raw, err := json.Marshal(SchemaHintJSON{
		RouteName:      "contact",
		DataverseTable: "contacts",
		PrimaryKey:     "contactid",
		DefaultSelect:  []string{"contactid"},
		LookupFields:   []string{"fullname"},
	})
	if err != nil {
		t.Fatal(err)
	}
	model := &TableResourceModel{}
	schemaJSONToModel(ctx, raw, model, &diags)
	if diags.HasError() {
		t.Fatalf("schemaJSONToModel: %v", diags.Errors())
	}

	var schemaResp resource.SchemaResponse
	NewTableResource().Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	for name, got := range map[string]types.List{
		"aliases":                model.Aliases,
		"lookup_search_contains": model.LookupSearchContains,
	} {
		if got.IsNull() || got.IsUnknown() || len(got.Elements()) != 0 {
			t.Errorf("%s = %v, want []", name, got)
		}
		if !schemaResp.Schema.Attributes[name].IsComputed() {
			t.Errorf("%s is not Computed", name)
		}
	}
}

// TestReferenceRoundTrip: the column logged with every request that touches a
// row is sent when set and read back as written; a table without one sends no
// key and reads back null, so neither shows a plan diff.
func TestReferenceRoundTrip(t *testing.T) {
	ctx := context.Background()
	var diags diag.Diagnostics

	model := func(reference types.String) *TableResourceModel {
		return &TableResourceModel{
			RouteName:            types.StringValue("case"),
			DataverseTable:       types.StringValue("incidents"),
			DataverseLogicalName: types.StringValue("incident"),
			PrimaryKey:           types.StringValue("incidentid"),
			RequiredPermission:   types.StringValue("case"),
			DefaultSelect:        stringsToTFList([]string{"incidentid", "ticketnumber"}),
			LookupFields:         stringsToTFList([]string{"ticketnumber"}),
			Fields: fieldsJSONToModel(ctx, map[string]FieldHintJSON{
				"ticketnumber": {Type: "string", Description: "Case number", ReadOnly: true},
			}, types.MapNull(fieldObjectType()), &diags),
			Reference: reference,
		}
	}

	raw := modelToSchemaJSON(ctx, model(types.StringValue("ticketnumber")), &diags)
	if diags.HasError() {
		t.Fatalf("modelToSchemaJSON: %v", diags.Errors())
	}
	if !strings.Contains(string(raw), `"reference":"ticketnumber"`) {
		t.Fatalf("reference not sent: %s", raw)
	}
	back := &TableResourceModel{}
	schemaJSONToModel(ctx, raw, back, &diags)
	if diags.HasError() {
		t.Fatalf("schemaJSONToModel: %v", diags.Errors())
	}
	if got := back.Reference.ValueString(); got != "ticketnumber" {
		t.Fatalf("reference read back as %q", got)
	}

	raw = modelToSchemaJSON(ctx, model(types.StringNull()), &diags)
	if strings.Contains(string(raw), `"reference"`) {
		t.Fatalf("a table with no reference sent one: %s", raw)
	}
	back = &TableResourceModel{}
	schemaJSONToModel(ctx, raw, back, &diags)
	if !back.Reference.IsNull() {
		t.Fatalf("reference = %v, want null", back.Reference)
	}
}

// TestInvitesRoundTrip: a table's invites and documents.upload.invites are sent
// as written and read back the same; a table without them sends no keys and
// reads back null, so neither shows a plan diff.
func TestInvitesRoundTrip(t *testing.T) {
	ctx := context.Background()
	var diags diag.Diagnostics

	model := func(invites types.List, upload *DocumentsUploadModel) *TableResourceModel {
		var documents *DocumentsModel
		if upload != nil {
			documents = &DocumentsModel{
				Permission: types.StringValue("casedocuments"),
				Columns:    types.MapNull(types.StringType),
				Filters:    types.ListNull(types.StringType),
				Upload:     upload,
			}
		}
		return &TableResourceModel{
			RouteName:            types.StringValue("casenotes"),
			DataverseTable:       types.StringValue("annotations"),
			DataverseLogicalName: types.StringValue("annotation"),
			PrimaryKey:           types.StringValue("annotationid"),
			RequiredPermission:   types.StringValue("casenotes"),
			DefaultSelect:        stringsToTFList([]string{"annotationid"}),
			LookupFields:         stringsToTFList([]string{"subject"}),
			Fields:               fieldsJSONToModel(ctx, map[string]FieldHintJSON{}, types.MapNull(fieldObjectType()), &diags),
			Invites:              invites,
			Documents:            documents,
		}
	}
	upload := func(invites types.Bool) *DocumentsUploadModel {
		return &DocumentsUploadModel{
			MaxBytes:   types.Int64Null(),
			Extensions: types.ListNull(types.StringType),
			Columns:    types.MapNull(types.StringType),
			Invites:    invites,
		}
	}

	raw := modelToSchemaJSON(ctx, model(stringsToTFList([]string{"create"}), upload(types.BoolValue(true))), &diags)
	if diags.HasError() {
		t.Fatalf("modelToSchemaJSON: %v", diags.Errors())
	}
	for _, want := range []string{`"invites":["create"]`, `"upload":{"invites":true}`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("%s not sent: %s", want, raw)
		}
	}
	back := &TableResourceModel{}
	schemaJSONToModel(ctx, raw, back, &diags)
	if diags.HasError() {
		t.Fatalf("schemaJSONToModel: %v", diags.Errors())
	}
	if got := tfListToStrings(ctx, back.Invites, &diags); len(got) != 1 || got[0] != "create" {
		t.Fatalf("invites read back as %v", got)
	}
	if !back.Documents.Upload.Invites.ValueBool() {
		t.Fatalf("documents.upload.invites read back as %v", back.Documents.Upload.Invites)
	}

	// false is sent and read back as false, not dropped to null.
	raw = modelToSchemaJSON(ctx, model(types.ListNull(types.StringType), upload(types.BoolValue(false))), &diags)
	if strings.Contains(string(raw), `"invites":[`) || !strings.Contains(string(raw), `"upload":{"invites":false}`) {
		t.Fatalf("unexpected invites: %s", raw)
	}
	back = &TableResourceModel{}
	schemaJSONToModel(ctx, raw, back, &diags)
	if !back.Invites.IsNull() {
		t.Fatalf("invites = %v, want null", back.Invites)
	}
	if v := back.Documents.Upload.Invites; v.IsNull() || v.ValueBool() {
		t.Fatalf("documents.upload.invites = %v, want false", v)
	}

	// Unset upload invites: no key, null back.
	raw = modelToSchemaJSON(ctx, model(types.ListNull(types.StringType), upload(types.BoolNull())), &diags)
	if strings.Contains(string(raw), `"invites"`) {
		t.Fatalf("invites sent when unset: %s", raw)
	}
	back = &TableResourceModel{}
	schemaJSONToModel(ctx, raw, back, &diags)
	if !back.Documents.Upload.Invites.IsNull() {
		t.Fatalf("documents.upload.invites = %v, want null", back.Documents.Upload.Invites)
	}
}

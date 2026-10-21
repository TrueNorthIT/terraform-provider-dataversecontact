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

func invitesOf(ops ...string) types.Set {
	elements := make([]attr.Value, len(ops))
	for i, op := range ops {
		elements[i] = types.StringValue(op)
	}
	return types.SetValueMust(types.StringType, elements)
}

// A table without invites publishes exactly what it did before they existed:
// no `invites` key. Absent, null and [] (as the Table Manager may save "none")
// all read back as null, so a config that leaves invites out plans no change.
func TestNoInvitesOmitsKey(t *testing.T) {
	for name, extra := range map[string]string{
		"absent": ``,
		"null":   `, "invites": null`,
		"empty":  `, "invites": []`,
	} {
		t.Run(name, func(t *testing.T) {
			model := &TableResourceModel{}
			readHint(t, documentsHint(extra), model)
			if !model.Invites.IsNull() {
				t.Fatalf("read back %v, want null", model.Invites)
			}
			if _, ok := writeHint(t, model)["invites"]; ok {
				t.Fatal("a table without invites sent an invites key")
			}
		})
	}

	// Unknown until apply: nothing to send yet.
	model := &TableResourceModel{}
	readHint(t, documentsHint(``), model)
	model.Invites = types.SetUnknown(types.StringType)
	if _, ok := writeHint(t, model)["invites"]; ok {
		t.Fatal("unknown invites sent an invites key")
	}
}

// The set goes out sorted, so the same config always sends the same list, and
// any order the API holds reads back as the same set: no diff from ordering.
func TestInvitesRoundTrip(t *testing.T) {
	in := invitesOf("update", "create")
	model := &TableResourceModel{}
	readHint(t, documentsHint(``), model)
	model.Invites = in

	got, _ := json.Marshal(writeHint(t, model)["invites"])
	if want := `["create","update"]`; string(got) != want {
		t.Fatalf("sent %s, want %s", got, want)
	}

	for name, stored := range map[string]string{
		"as sent":     `["create","update"]`,
		"reordered":   `["update","create"]`,
		"with repeat": `["update","create","update"]`,
	} {
		t.Run(name, func(t *testing.T) {
			back := &TableResourceModel{}
			readHint(t, documentsHint(`, "invites": `+stored), back)
			if !back.Invites.Equal(in) {
				t.Fatalf("read back %v, want %v", back.Invites, in)
			}
		})
	}

	// One operation on its own.
	back := &TableResourceModel{}
	readHint(t, documentsHint(`, "invites": ["create"]`), back)
	if !back.Invites.Equal(invitesOf("create")) {
		t.Fatalf("read back %v, want [create]", back.Invites)
	}
}

func TestValidateInvites(t *testing.T) {
	str := types.StringValue
	null := types.StringNull()
	for name, c := range map[string]struct {
		table, logical types.String
		invites        types.Set
		wantError      bool
	}{
		"contact by table name":      {str("contacts"), null, invitesOf("create"), true},
		"contact by logical name":    {str("cr123_people"), str("contact"), invitesOf("update", "create"), true},
		"contact update":             {str("contacts"), null, invitesOf("update"), false},
		"case create":                {str("incidents"), null, invitesOf("create", "update"), false},
		"logical name wins":          {str("contacts"), str("cr123_person"), invitesOf("create"), false},
		"no invites":                 {str("contacts"), null, types.SetNull(types.StringType), false},
		"invites unknown":            {str("contacts"), null, types.SetUnknown(types.StringType), false},
		"logical name unknown":       {str("contacts"), types.StringUnknown(), invitesOf("create"), false},
		"table unknown":              {types.StringUnknown(), null, invitesOf("create"), false},
		"an element still unknown":   {str("contacts"), null, types.SetValueMust(types.StringType, []attr.Value{types.StringUnknown()}), false},
		"contact create among nulls": {str("contacts"), null, types.SetValueMust(types.StringType, []attr.Value{types.StringNull(), str("create")}), true},
	} {
		t.Run(name, func(t *testing.T) {
			diags := validateInvites(&TableResourceModel{DataverseTable: c.table, DataverseLogicalName: c.logical, Invites: c.invites})
			if diags.HasError() != c.wantError {
				t.Fatalf("errors = %v, want error %v", diags.Errors(), c.wantError)
			}
			if c.wantError && !strings.Contains(diags.Errors()[0].Detail(), "a new contact is a way into somebody's company") {
				t.Errorf("unexpected detail: %s", diags.Errors()[0].Detail())
			}
		})
	}
}

// The schema refuses an empty set and anything but create and update.
func TestInvitesAttributeValidators(t *testing.T) {
	attribute := invitesAttribute()
	if !attribute.Optional || attribute.Required || attribute.Computed {
		t.Fatal("invites must be Optional only, so leaving it out reads back null")
	}
	if len(attribute.Validators) != 2 {
		t.Fatalf("expected a size and a value validator, got %d", len(attribute.Validators))
	}
	ctx := context.Background()
	descriptions := attribute.Validators[0].Description(ctx) + " " + attribute.Validators[1].Description(ctx)
	for _, want := range []string{"at least 1", `"create"`, `"update"`} {
		if !strings.Contains(descriptions, want) {
			t.Errorf("validators %q don't mention %s", descriptions, want)
		}
	}
}

// ── documents.upload.invites ────────────────────────────────────────────

// invites = true goes out as true. false and null aren't sent, so a block
// without it publishes what it did before; false reads back as false only
// where the config said so, as keepFalse does for every omitempty boolean.
func TestDocumentsUploadInvites(t *testing.T) {
	ctx := context.Background()
	columns := types.MapNull(types.StringType)
	withInvites := func(b types.Bool) *DocumentsUploadModel {
		u := emptyUpload()
		u.Invites = b
		return u
	}
	for name, c := range map[string]struct {
		invites types.Bool
		want    string
	}{
		"true":    {types.BoolValue(true), `{"invites":true}`},
		"false":   {types.BoolValue(false), `{}`},
		"null":    {types.BoolNull(), `{}`},
		"unknown": {types.BoolUnknown(), `{}`},
	} {
		t.Run(name, func(t *testing.T) {
			model := &TableResourceModel{}
			readHint(t, documentsHint(``), model)
			model.Documents = documentsWith(columns, withInvites(c.invites))
			got, _ := json.Marshal(writeHint(t, model)["documents"].(map[string]any)["upload"])
			if string(got) != c.want {
				t.Fatalf("sent %s, want %s", got, c.want)
			}
		})
	}

	for name, c := range map[string]struct {
		stored string
		prior  *DocumentsModel
		want   types.Bool
	}{
		"true":                         {`{"invites": true}`, nil, types.BoolValue(true)},
		"absent, no prior":             {`{}`, nil, types.BoolNull()},
		"absent, config said false":    {`{}`, documentsWith(columns, withInvites(types.BoolValue(false))), types.BoolValue(false)},
		"false, config left it out":    {`{"invites": false}`, documentsWith(columns, withInvites(types.BoolNull())), types.BoolNull()},
		"null":                         {`{"invites": null}`, nil, types.BoolNull()},
		"absent, config said true":     {`{}`, documentsWith(columns, withInvites(types.BoolValue(true))), types.BoolNull()},
		"true, config said false":      {`{"invites": true}`, documentsWith(columns, withInvites(types.BoolValue(false))), types.BoolValue(true)},
		"false, config without upload": {`{"invites": false}`, documentsWith(columns, nil), types.BoolNull()},
	} {
		t.Run("read "+name, func(t *testing.T) {
			back := &TableResourceModel{Documents: c.prior}
			readHint(t, documentsHint(`, "documents": {"permission": "casedocuments", "upload": `+c.stored+`}`), back)
			if got := back.Documents.Upload.Invites; !got.Equal(c.want) {
				t.Fatalf("read back %v, want %v", got, c.want)
			}
		})
	}

	// The fixed columns still go out typed beside it.
	model := &TableResourceModel{}
	readHint(t, documentsHint(``), model)
	upload := uploadOf(types.Int64Null(), types.ListNull(types.StringType), columnsOf(map[string]string{"ShowOnPortal": "true"}))
	upload.Invites = types.BoolValue(true)
	model.Documents = documentsWith(columnsOf(map[string]string{"ShowOnPortal": "boolean"}), upload)
	var diags diag.Diagnostics
	got, _ := json.Marshal(documentsModelToJSON(ctx, model.Documents, &diags).Upload)
	if want := `{"columns":{"ShowOnPortal":true},"invites":true}`; string(got) != want {
		t.Fatalf("sent %s, want %s", got, want)
	}
}

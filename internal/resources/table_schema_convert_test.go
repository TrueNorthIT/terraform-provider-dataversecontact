package resources

import (
	"context"
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

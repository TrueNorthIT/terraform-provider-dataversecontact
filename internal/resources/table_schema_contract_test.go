package resources

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// A real published schema (internal/testdata/contract) must survive
// API → model → API unchanged. Anything the model can't hold is dropped on the
// way in and then sent back without it — silently unsetting it on the next
// apply.
func TestContractTableSchemaRoundTrip(t *testing.T) {
	for _, fixture := range []string{"get-table-published", "get-table-published-case"} {
		t.Run(fixture, func(t *testing.T) { roundTrip(t, fixture) })
	}
}

func roundTrip(t *testing.T, fixture string) {
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "contract", fixture+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Schema json.RawMessage `json:"schema"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	var diags diag.Diagnostics
	model := TableResourceModel{Scope: types.StringValue("default")}
	schemaJSONToModel(ctx, resp.Schema, &model, &diags)
	if diags.HasError() {
		t.Fatalf("schemaJSONToModel: %v", diags.Errors())
	}
	out := modelToSchemaJSON(ctx, &model, &diags)
	if diags.HasError() {
		t.Fatalf("modelToSchemaJSON: %v", diags.Errors())
	}

	var want, got map[string]any
	_ = json.Unmarshal(resp.Schema, &want)
	_ = json.Unmarshal(out, &got)
	for key, w := range want {
		if g, ok := got[key]; !ok {
			t.Errorf("%q is in the API's schema but lost by the provider", key)
		} else if !reflect.DeepEqual(w, g) {
			t.Errorf("%q changed in the round trip:\n api:      %v\n provider: %v", key, w, g)
		}
	}
	for key := range got {
		if _, ok := want[key]; !ok {
			t.Errorf("%q is sent by the provider but absent from the API's schema", key)
		}
	}
}

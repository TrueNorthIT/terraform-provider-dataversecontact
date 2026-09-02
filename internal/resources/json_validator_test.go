package resources

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestValidJSONObject(t *testing.T) {
	cases := []struct {
		name      string
		value     types.String
		expectErr bool
	}{
		{"valid object", types.StringValue(`{"routeName":"x","isFunction":true}`), false},
		{"null", types.StringNull(), false},
		{"unknown", types.StringUnknown(), false},
		{"trailing comma", types.StringValue(`{"routeName":"x",}`), true},
		{"not json", types.StringValue(`routeName: x`), true},
		{"array", types.StringValue(`[1,2,3]`), true},
		{"bare string", types.StringValue(`"hello"`), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := validator.StringRequest{
				Path:        path.Root("schema_json"),
				ConfigValue: tc.value,
			}
			resp := &validator.StringResponse{}
			ValidJSONObject().ValidateString(context.Background(), req, resp)
			if got := resp.Diagnostics.HasError(); got != tc.expectErr {
				t.Fatalf("expected error=%v, got diagnostics: %v", tc.expectErr, resp.Diagnostics)
			}
		})
	}
}

package resources

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestFieldCountFromFields verifies field_count is planned from fields rather
// than carried over from state, so adding a field doesn't fail apply (#9).
func TestFieldCountFromFields(t *testing.T) {
	ctx := context.Background()

	var schemaResp resource.SchemaResponse
	NewTableResource().Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	s := schemaResp.Schema
	objType := s.Type().TerraformType(ctx).(tftypes.Object)
	fieldsType := objType.AttributeTypes["fields"]
	fieldType := fieldsType.(tftypes.Map).ElementType

	// planWith builds a plan where every attribute is null except fields.
	planWith := func(fields tftypes.Value) tfsdk.Plan {
		vals := map[string]tftypes.Value{}
		for name, typ := range objType.AttributeTypes {
			vals[name] = tftypes.NewValue(typ, nil)
		}
		vals["fields"] = fields
		return tfsdk.Plan{Schema: s, Raw: tftypes.NewValue(objType, vals)}
	}

	nullField := tftypes.NewValue(fieldType, nil)
	cases := []struct {
		name   string
		fields tftypes.Value
		want   types.Int64
	}{
		{"three fields", tftypes.NewValue(fieldsType, map[string]tftypes.Value{
			"a": nullField, "b": nullField, "c": nullField,
		}), types.Int64Value(3)},
		{"unknown fields", tftypes.NewValue(fieldsType, tftypes.UnknownValue), types.Int64Unknown()},
		{"null fields", tftypes.NewValue(fieldsType, nil), types.Int64Value(0)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := planmodifier.Int64Request{
				Path:       path.Root("field_count"),
				Plan:       planWith(tc.fields),
				StateValue: types.Int64Value(11),
				PlanValue:  types.Int64Unknown(),
			}
			resp := &planmodifier.Int64Response{PlanValue: req.PlanValue}
			fieldCountFromFields{}.PlanModifyInt64(ctx, req, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diags: %v", resp.Diagnostics)
			}
			if !resp.PlanValue.Equal(tc.want) {
				t.Errorf("planned %s, want %s", resp.PlanValue, tc.want)
			}
		})
	}
}

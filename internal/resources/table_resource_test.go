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

// TestBindFieldFromState verifies an unset bind_field keeps its derived value
// only while the field is unchanged, so lookups without bind_field don't fail
// apply (#8) and unchanged fields don't show "(known after apply)".
func TestBindFieldFromState(t *testing.T) {
	ctx := context.Background()

	var schemaResp resource.SchemaResponse
	NewTableResource().Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	s := schemaResp.Schema
	objType := s.Type().TerraformType(ctx).(tftypes.Object)
	fieldsType := objType.AttributeTypes["fields"]
	fieldType := fieldsType.(tftypes.Map).ElementType.(tftypes.Object)

	field := func(typ, lookupTable string) tftypes.Value {
		vals := map[string]tftypes.Value{}
		for name, at := range fieldType.AttributeTypes {
			vals[name] = tftypes.NewValue(at, nil)
		}
		vals["type"] = tftypes.NewValue(tftypes.String, typ)
		if lookupTable != "" {
			vals["lookup_table"] = tftypes.NewValue(tftypes.String, lookupTable)
		}
		return tftypes.NewValue(fieldType, vals)
	}
	// withFields builds a resource value where every attribute is null except fields.
	withFields := func(fields map[string]tftypes.Value) tftypes.Value {
		vals := map[string]tftypes.Value{}
		for name, typ := range objType.AttributeTypes {
			vals[name] = tftypes.NewValue(typ, nil)
		}
		vals["fields"] = tftypes.NewValue(fieldsType, fields)
		return tftypes.NewValue(objType, vals)
	}

	prior := withFields(map[string]tftypes.Value{"abc_project": field("lookup", "project")})
	cases := []struct {
		name   string
		state  tftypes.Value
		plan   tftypes.Value
		config types.String
		want   types.String
	}{
		{"unchanged field", prior,
			withFields(map[string]tftypes.Value{"abc_project": field("lookup", "project")}),
			types.StringNull(), types.StringValue("abc_Project")},
		{"lookup_table changed", prior,
			withFields(map[string]tftypes.Value{"abc_project": field("lookup", "programme")}),
			types.StringNull(), types.StringUnknown()},
		{"type changed", prior,
			withFields(map[string]tftypes.Value{"abc_project": field("string", "")}),
			types.StringNull(), types.StringUnknown()},
		{"new field", prior,
			withFields(map[string]tftypes.Value{"abc_other": field("lookup", "project")}),
			types.StringNull(), types.StringUnknown()},
		{"create", tftypes.NewValue(objType, nil),
			withFields(map[string]tftypes.Value{"abc_project": field("lookup", "project")}),
			types.StringNull(), types.StringUnknown()},
		{"configured", prior,
			withFields(map[string]tftypes.Value{"abc_project": field("lookup", "project")}),
			types.StringValue("abc_Custom"), types.StringValue("abc_Custom")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			planValue := types.StringUnknown()
			if !tc.config.IsNull() {
				planValue = tc.config
			}
			req := planmodifier.StringRequest{
				Path:        path.Root("fields").AtMapKey("abc_project").AtName("bind_field"),
				Plan:        tfsdk.Plan{Schema: s, Raw: tc.plan},
				State:       tfsdk.State{Schema: s, Raw: tc.state},
				ConfigValue: tc.config,
				StateValue:  types.StringValue("abc_Project"),
				PlanValue:   planValue,
			}
			if tc.name == "new field" {
				req.Path = path.Root("fields").AtMapKey("abc_other").AtName("bind_field")
				req.StateValue = types.StringNull()
			}
			resp := &planmodifier.StringResponse{PlanValue: req.PlanValue}
			bindFieldFromState{}.PlanModifyString(ctx, req, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diags: %v", resp.Diagnostics)
			}
			if !resp.PlanValue.Equal(tc.want) {
				t.Errorf("planned %s, want %s", resp.PlanValue, tc.want)
			}
		})
	}
}

package resources

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// jsonObjectValidator rejects strings that are not a JSON object at plan time,
// so a malformed schema_json fails the plan instead of the publish call.
type jsonObjectValidator struct{}

func (v jsonObjectValidator) Description(_ context.Context) string {
	return "value must be a valid JSON object"
}

func (v jsonObjectValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v jsonObjectValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(req.ConfigValue.ValueString()), &obj); err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid JSON",
			"Expected a JSON object: "+err.Error(),
		)
	}
}

// ValidJSONObject returns a validator asserting the string parses as a JSON object.
func ValidJSONObject() validator.String {
	return jsonObjectValidator{}
}

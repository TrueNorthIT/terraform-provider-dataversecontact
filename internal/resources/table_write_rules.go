package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Write rules (Contact API 1.58.0, #589): what a caller may write to one
// column. Each is optional on a field, and a field with none publishes
// exactly what it did before. The API builds and checks them
// (server/config/scopes/write-rules.ts) and refuses a save that breaks one;
// the cheap checks are repeated here at plan time. Values are written as
// strings, as documents.upload.columns are, and sent typed by the field's
// type: a choice as an integer, a number as a number, a boolean as true or
// false, text and dates as strings.

const writeRulesVersion = "Needs Contact API 1.58.0 or later: an older API keeps the key but doesn't enforce it."

// integerPattern is a choice value: a whole number, as JSON writes it.
var integerPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// fieldWriteRuleAttributes are the write-rule attributes of a field in `fields`.
func fieldWriteRuleAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"fixed": schema.StringAttribute{
			Description: "Write rule: the value the API writes on every create. A caller may send only this value, and " +
				"can't change it on update. Written as a string and sent as the field's type (\"3\" for a choice, " +
				"\"true\" for a boolean). The only rule a field with fixed may have; allowed on a read_only field, " +
				"where the API writes it and the caller never sends it. Not on a lookup. " + writeRulesVersion,
			Optional: true,
		},
		"default": schema.StringAttribute{
			Description: "Write rule: written on a create that leaves the field out; the caller may send another " +
				"allowed value. Must pass the field's own allowed, length and range rules. " + writeRulesVersion,
			Optional: true,
		},
		"allowed": schema.ListAttribute{
			Description: "Write rule: the values a caller may send (e.g. [\"1\", \"2\"] for a choice). On a string " +
				"field an entry may be a pattern where * is any characters, case ignored (e.g. \"Comp*\"); nothing " +
				"else is special. Not on a datetime field. " + writeRulesVersion,
			Optional:    true,
			ElementType: types.StringType,
			Validators: []validator.List{
				listvalidator.SizeAtLeast(1),
				listvalidator.UniqueValues(),
			},
		},
		"required": schema.BoolAttribute{
			Description: "Write rule: a create must end up with a value (sent, preset by an invite, or default), " +
				"and an update can't clear it. " + writeRulesVersion,
			Optional: true,
		},
		"min_length": schema.Int64Attribute{
			Description: "Write rule: the shortest text a caller may send. String fields only. " + writeRulesVersion,
			Optional:    true,
			Validators:  []validator.Int64{int64validator.AtLeast(0)},
		},
		"max_length": schema.Int64Attribute{
			Description: "Write rule: the longest text a caller may send. String fields only. " + writeRulesVersion,
			Optional:    true,
			Validators:  []validator.Int64{int64validator.AtLeast(0)},
		},
		"min": schema.Float64Attribute{
			Description: "Write rule: the smallest number a caller may send. Number fields only. " + writeRulesVersion,
			Optional:    true,
		},
		"max": schema.Float64Attribute{
			Description: "Write rule: the largest number a caller may send. Number fields only. " + writeRulesVersion,
			Optional:    true,
		},
	}
}

func fieldWriteRuleAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"fixed":      types.StringType,
		"default":    types.StringType,
		"allowed":    types.ListType{ElemType: types.StringType},
		"required":   types.BoolType,
		"min_length": types.Int64Type,
		"max_length": types.Int64Type,
		"min":        types.Float64Type,
		"max":        types.Float64Type,
	}
}

// ruleValueJSON sends a rule's value as the field's type: "3" as 3 for a
// choice or number, "true" as true for a boolean. Anything that doesn't suit
// (ValidateConfig refuses those) goes as a string, for the API to refuse.
func ruleValueJSON(fieldType, value string) json.RawMessage {
	switch {
	case fieldType == "choice" && integerPattern.MatchString(value):
		return json.RawMessage(value)
	case fieldType == "number":
		if _, ok := documentNumber(value); ok {
			return json.RawMessage(value)
		}
	case fieldType == "boolean" && (value == "true" || value == "false"):
		return json.RawMessage(value)
	}
	quoted, _ := json.Marshal(value) // a string always marshals
	return quoted
}

// ruleValueModel reads a rule's value back as the string the config writes.
// A number equal to the one in the plan or state keeps that spelling ("3.0"
// stays "3.0"), since the API hands back the number, not the text it was sent.
func ruleValueModel(raw json.RawMessage, prior attr.Value) types.String {
	value, isNumber, ok := documentColumnValueString(raw)
	if !ok {
		return types.StringNull()
	}
	if p := knownString(prior); isNumber && p != nil {
		if was, ok := documentNumber(*p); ok {
			if now, ok := documentNumber(value); ok && was == now {
				value = *p
			}
		}
	}
	return types.StringValue(value)
}

// fieldRulesModelToJSON copies a field's write rules into its hint. Unset
// rules are left out, so a field without any publishes what it did before.
func fieldRulesModelToJSON(ctx context.Context, attrs map[string]attr.Value, f *FieldHintJSON, diags *diag.Diagnostics) {
	if v := knownString(attrs["fixed"]); v != nil {
		f.Fixed = ruleValueJSON(f.Type, *v)
	}
	if v := knownString(attrs["default"]); v != nil {
		f.Default = ruleValueJSON(f.Type, *v)
	}
	if l, ok := attrs["allowed"].(types.List); ok {
		for _, entry := range tfListToStrings(ctx, l, diags) {
			f.Allowed = append(f.Allowed, ruleValueJSON(f.Type, entry))
		}
	}
	if v, ok := attrs["required"].(types.Bool); ok && !v.IsNull() && !v.IsUnknown() {
		f.Required = v.ValueBool()
	}
	if v, ok := attrs["min_length"].(types.Int64); ok && !v.IsNull() && !v.IsUnknown() {
		n := v.ValueInt64()
		f.MinLength = &n
	}
	if v, ok := attrs["max_length"].(types.Int64); ok && !v.IsNull() && !v.IsUnknown() {
		n := v.ValueInt64()
		f.MaxLength = &n
	}
	if v, ok := attrs["min"].(types.Float64); ok && !v.IsNull() && !v.IsUnknown() {
		n := v.ValueFloat64()
		f.Min = &n
	}
	if v, ok := attrs["max"].(types.Float64); ok && !v.IsNull() && !v.IsUnknown() {
		n := v.ValueFloat64()
		f.Max = &n
	}
}

// fieldRulesToModel reads a field's write rules back. prior is the field's
// object in the plan or state being refreshed (nil if none): it keeps a
// number's spelling and a `required = false`.
func fieldRulesToModel(f FieldHintJSON, prior map[string]attr.Value) map[string]attr.Value {
	allowed := types.ListNull(types.StringType)
	if len(f.Allowed) > 0 {
		var priorEntries []attr.Value
		if l, ok := prior["allowed"].(types.List); ok && !l.IsNull() && !l.IsUnknown() {
			priorEntries = l.Elements()
		}
		entries := make([]attr.Value, 0, len(f.Allowed))
		for i, raw := range f.Allowed {
			var p attr.Value
			if i < len(priorEntries) {
				p = priorEntries[i]
			}
			value := ruleValueModel(raw, p)
			if value.IsNull() {
				continue
			}
			entries = append(entries, value)
		}
		if len(entries) > 0 {
			allowed = types.ListValueMust(types.StringType, entries)
		}
	}
	int64OrNull := func(n *int64) types.Int64 {
		if n == nil {
			return types.Int64Null()
		}
		return types.Int64Value(*n)
	}
	float64OrNull := func(n *float64) types.Float64 {
		if n == nil {
			return types.Float64Null()
		}
		return types.Float64Value(*n)
	}
	return map[string]attr.Value{
		"fixed":      ruleValueModel(f.Fixed, prior["fixed"]),
		"default":    ruleValueModel(f.Default, prior["default"]),
		"allowed":    allowed,
		"required":   keepFalse(f.Required, prior["required"]),
		"min_length": int64OrNull(f.MinLength),
		"max_length": int64OrNull(f.MaxLength),
		"min":        float64OrNull(f.Min),
		"max":        float64OrNull(f.Max),
	}
}

// ruleValueProblem: whether value, written as a string, suits a rule on a
// field of type typ. Dates are left to the API.
func ruleValueProblem(typ, value string) string {
	switch typ {
	case "choice":
		if !integerPattern.MatchString(value) {
			return fmt.Sprintf("%q isn't a choice value: use the option's whole number (e.g. \"3\"), not its label.", value)
		}
	case "number":
		if _, ok := documentNumber(value); !ok {
			return fmt.Sprintf("%q isn't a number.", value)
		}
	case "boolean":
		if value != "true" && value != "false" {
			return fmt.Sprintf("%q isn't true or false.", value)
		}
	}
	return ""
}

// validateFieldWriteRules repeats the API build's checks on one field's write
// rules (buildFieldRules): fixed stands alone; no rules on a lookup; the rules
// other than fixed not on a read_only field (a caller never sends it); values
// suit the field's type; * only in a string field's allowed; allowed not on a
// date; lengths on strings and range on numbers, each pair in order. Whether
// a default passes its own allowed, length and range is left to the API's save.
func validateFieldWriteRules(name string, attrs map[string]attr.Value) diag.Diagnostics {
	var diags diag.Diagnostics
	at := path.Root("fields").AtMapKey(name)
	fieldType, _ := attrs["type"].(types.String)
	if fieldType.IsNull() || fieldType.IsUnknown() {
		return diags
	}
	typ := fieldType.ValueString()

	set := []string{}
	for _, key := range []string{"fixed", "default", "allowed", "required", "min_length", "max_length", "min", "max"} {
		v := attrs[key]
		if v == nil || v.IsNull() || v.IsUnknown() {
			continue
		}
		if b, ok := v.(types.Bool); ok && !b.ValueBool() {
			continue // required = false is no rule
		}
		set = append(set, key)
	}
	if len(set) == 0 {
		return diags
	}

	if typ == "lookup" {
		diags.AddAttributeError(at, "Write rules on a lookup",
			fmt.Sprintf("Field %q is a lookup, which takes no write rules (%s). create_default binds a lookup to the caller.",
				name, strings.Join(set, ", ")))
		return diags
	}
	if readOnly, ok := attrs["read_only"].(types.Bool); ok && readOnly.ValueBool() {
		others := []string{}
		for _, key := range set {
			if key != "fixed" {
				others = append(others, key)
			}
		}
		if len(others) > 0 {
			diags.AddAttributeError(at, "Write rules on a read-only field",
				fmt.Sprintf("Field %q is read_only, so a caller never sends it and %s would never apply. "+
					"Use fixed to have the API write it.", name, strings.Join(others, ", ")))
		}
	}

	if fixed := knownString(attrs["fixed"]); fixed != nil {
		others := []string{}
		for _, key := range set {
			if key != "fixed" {
				others = append(others, key)
			}
		}
		sort.Strings(others)
		if len(others) > 0 {
			diags.AddAttributeError(at.AtName("fixed"), "fixed beside other write rules",
				fmt.Sprintf("Field %q: fixed is the only value it can take, so %s would never apply. Leave them out.",
					name, strings.Join(others, ", ")))
		}
		if problem := ruleValueProblem(typ, *fixed); problem != "" {
			diags.AddAttributeError(at.AtName("fixed"), "fixed doesn't suit the field", problem)
		} else if typ == "string" && strings.Contains(*fixed, "*") {
			diags.AddAttributeError(at.AtName("fixed"), "Pattern in fixed",
				fmt.Sprintf("Field %q: fixed is written as it is, so it can't be a pattern (%q).", name, *fixed))
		}
	}
	if def := knownString(attrs["default"]); def != nil {
		if problem := ruleValueProblem(typ, *def); problem != "" {
			diags.AddAttributeError(at.AtName("default"), "default doesn't suit the field", problem)
		} else if typ == "string" && strings.Contains(*def, "*") {
			diags.AddAttributeError(at.AtName("default"), "Pattern in default",
				fmt.Sprintf("Field %q: default is written as it is, so it can't be a pattern (%q).", name, *def))
		}
	}
	if l, ok := attrs["allowed"].(types.List); ok && !l.IsNull() && !l.IsUnknown() {
		if typ == "datetime" {
			diags.AddAttributeError(at.AtName("allowed"), "allowed on a date",
				fmt.Sprintf("Field %q is a datetime, and allowed doesn't apply to dates.", name))
		}
		for i, v := range l.Elements() {
			entry := knownString(v)
			if entry == nil {
				continue
			}
			if problem := ruleValueProblem(typ, *entry); problem != "" {
				diags.AddAttributeError(at.AtName("allowed").AtListIndex(i), "allowed value doesn't suit the field", problem)
			} else if typ != "string" && strings.Contains(*entry, "*") {
				diags.AddAttributeError(at.AtName("allowed").AtListIndex(i), "Pattern on a non-string field",
					fmt.Sprintf("Field %q: only a string field takes * patterns.", name))
			}
		}
	}

	for _, key := range []string{"min_length", "max_length"} {
		if v := attrs[key]; v != nil && !v.IsNull() && !v.IsUnknown() && typ != "string" {
			diags.AddAttributeError(at.AtName(key), key+" on a non-string field",
				fmt.Sprintf("Field %q is a %s field; %s applies to string fields.", name, typ, key))
		}
	}
	for _, key := range []string{"min", "max"} {
		if v := attrs[key]; v != nil && !v.IsNull() && !v.IsUnknown() && typ != "number" {
			diags.AddAttributeError(at.AtName(key), key+" on a non-number field",
				fmt.Sprintf("Field %q is a %s field; %s applies to number fields.", name, typ, key))
		}
	}
	minLength, ok1 := attrs["min_length"].(types.Int64)
	maxLength, ok2 := attrs["max_length"].(types.Int64)
	if ok1 && ok2 && !minLength.IsNull() && !minLength.IsUnknown() && !maxLength.IsNull() && !maxLength.IsUnknown() &&
		minLength.ValueInt64() > maxLength.ValueInt64() {
		diags.AddAttributeError(at.AtName("min_length"), "min_length above max_length",
			fmt.Sprintf("Field %q: min_length (%d) is more than max_length (%d).", name, minLength.ValueInt64(), maxLength.ValueInt64()))
	}
	minimum, ok1 := attrs["min"].(types.Float64)
	maximum, ok2 := attrs["max"].(types.Float64)
	if ok1 && ok2 && !minimum.IsNull() && !minimum.IsUnknown() && !maximum.IsNull() && !maximum.IsUnknown() &&
		minimum.ValueFloat64() > maximum.ValueFloat64() {
		diags.AddAttributeError(at.AtName("min"), "min above max",
			fmt.Sprintf("Field %q: min (%g) is more than max (%g).", name, minimum.ValueFloat64(), maximum.ValueFloat64()))
	}
	return diags
}

// withWriteRuleAttributes adds the write-rule attributes to a field's.
func withWriteRuleAttributes(attrs map[string]schema.Attribute) map[string]schema.Attribute {
	for name, a := range fieldWriteRuleAttributes() {
		attrs[name] = a
	}
	return attrs
}

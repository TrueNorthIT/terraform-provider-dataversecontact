package resources

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"

	"github.com/TrueNorthIT/terraform-provider-dataversecontact/internal/client"
)

// addSaveError reports a failed save-and-publish. Fields the API dropped
// because Dataverse doesn't have them each get an error on their own entry in
// fields, so Terraform points at the line to fix.
func addSaveError(diags *diag.Diagnostics, summary string, err error) {
	var stripped *client.StrippedFieldsError
	if !errors.As(err, &stripped) {
		diags.AddError(summary, err.Error())
		return
	}
	for _, issue := range stripped.Fields {
		diags.AddAttributeError(
			path.Root("fields").AtMapKey(stripped.FieldName(issue)),
			"Field not in Dataverse",
			fmt.Sprintf("%s.\n\nThe API drops a field its Dataverse table doesn't have, so %q was not published "+
				"and the draft was discarded. Check the column's logical name, or remove the field.",
				strings.TrimSuffix(issue.Message, "."), stripped.Route),
		)
	}
}

// addSaveWarnings shows what the API reported about a saved schema without
// refusing it: validation warnings, validation errors it saved anyway, and
// config-rule warnings. They go in one warning per save, so an apply across
// many tables doesn't print a wall of them.
func addSaveWarnings(diags *diag.Diagnostics, route string, v *client.ValidationInfo, rules []client.ConfigFinding) {
	var lines []string
	if v != nil {
		for _, issue := range v.Errors {
			if !client.IsStrippedField(issue) {
				lines = append(lines, issueLine(issue, "saved anyway"))
			}
		}
		for _, issue := range v.Warnings {
			lines = append(lines, issueLine(issue, ""))
		}
	}
	for _, f := range rules {
		lines = append(lines, fmt.Sprintf("%s (%s)", f.Message, f.Rule))
	}
	if len(lines) == 0 {
		return
	}
	diags.AddWarning(
		fmt.Sprintf("The API reported %d problem(s) with %q", len(lines), route),
		"- "+strings.Join(lines, "\n- "),
	)
}

func issueLine(issue client.ValidationIssue, note string) string {
	tag := issue.Code
	if note != "" {
		tag += ", " + note
	}
	if issue.Field == "" {
		return fmt.Sprintf("%s (%s)", issue.Message, tag)
	}
	return fmt.Sprintf("%s: %s (%s)", issue.Field, issue.Message, tag)
}

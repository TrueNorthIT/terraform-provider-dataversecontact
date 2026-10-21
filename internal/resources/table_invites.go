package resources

import (
	"context"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// One-time invite links (`invites` on the API's schema hint, Contact API
// 1.36.0): what a link may do on a table's rows. Uploads are
// documents.upload.invites (table_documents.go).
//
// The API keeps the list as sent and reads it as a set, so the provider sends
// it sorted and holds it as a set: an order the Table Manager saved differently
// is no change. An empty list means none, as an absent one does, so it is
// refused in config (leave the attribute out) and read back as null.

// inviteOperations are the operations a table's invites may list.
var inviteOperations = []string{"create", "update"}

func invitesAttribute() schema.SetAttribute {
	return schema.SetAttribute{
		Description: "What one-time invite links may do on this table's rows: \"create\" and/or \"update\". " +
			"An invite link lets whoever holds it, with no account, do one thing once. \"update\" changes one " +
			"row, and only the fields the invite names (this table's writable fields, never a lookup). " +
			"\"create\" on a child table (one with parent_table) adds one row under the parent row the invite " +
			"was sent for, e.g. a note on a case; on any other table it logs one new row, which a create_default " +
			"binding the contact ties to the contact the invite was sent for. A contact table can't take " +
			"\"create\". Leave it out for no invites. An invite to upload a document is " +
			"documents.upload.invites. Sending one through the API needs invite on this table's permission at " +
			"the sender's tier (e.g. case = [\"team\", \"write:team\", \"invite:team\"] in " +
			"dataversecontact_permissions_sync.default_permissions) plus write for an update or create " +
			"otherwise; sending from Dataverse needs the Portal Invite Sender role instead. Set the portal page " +
			"links open on with invite_url there. Needs Contact API 1.36.0 or later: an older API keeps the key " +
			"without an error but makes no invites.",
		Optional:    true,
		ElementType: types.StringType,
		Validators: []validator.Set{
			setvalidator.SizeAtLeast(1),
			setvalidator.ValueStringsAre(stringvalidator.OneOf(inviteOperations...)),
		},
	}
}

// validateInvites is the part of ValidateConfig for invites. The API's build
// refuses "create" on a contact table, because a new contact is a way into
// somebody's company. Its save doesn't run that check: the table is published,
// then left out of the scope when the registry builds, so every route on it
// disappears. So it is made at plan time.
func validateInvites(config *TableResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if config.Invites.IsNull() || config.Invites.IsUnknown() || !setHasString(config.Invites, "create") {
		return diags
	}
	logical := ""
	switch {
	case !config.DataverseLogicalName.IsNull() && !config.DataverseLogicalName.IsUnknown():
		logical = config.DataverseLogicalName.ValueString()
	case config.DataverseLogicalName.IsNull() && !config.DataverseTable.IsNull() && !config.DataverseTable.IsUnknown():
		logical = singularize(config.DataverseTable.ValueString())
	}
	if logical == "contact" {
		diags.AddAttributeError(path.Root("invites"), "Contacts take no create invites",
			"A contact table can't take \"create\" invites: a new contact is a way into somebody's company, so "+
				"the API can't build a table that offers one and leaves it out of the scope. \"update\" is allowed, "+
				"and never offers the sign-in email column.")
	}
	return diags
}

// setHasString reports whether a set holds the known string want.
func setHasString(set types.Set, want string) bool {
	for _, v := range set.Elements() {
		if s, ok := v.(types.String); ok && !s.IsNull() && !s.IsUnknown() && s.ValueString() == want {
			return true
		}
	}
	return false
}

// invitesModelToJSON: no attribute, no key, so a table without invites
// publishes exactly what it did before they existed. Sorted, so the same set
// always sends the same list.
func invitesModelToJSON(ctx context.Context, set types.Set, diags *diag.Diagnostics) []string {
	if set.IsNull() || set.IsUnknown() {
		return nil
	}
	var operations []string
	diags.Append(set.ElementsAs(ctx, &operations, false)...)
	slices.Sort(operations)
	operations = slices.Compact(operations)
	if len(operations) == 0 {
		return nil
	}
	return operations
}

// invitesJSONToModel: absent, null and [] all read back as null, matching a
// config that leaves invites out. A repeated operation (written outside
// Terraform) reads back once, since a set can't hold it twice.
func invitesJSONToModel(operations []string) types.Set {
	if len(operations) == 0 {
		return types.SetNull(types.StringType)
	}
	unique := slices.Clone(operations)
	slices.Sort(unique)
	unique = slices.Compact(unique)
	elements := make([]attr.Value, len(unique))
	for i, op := range unique {
		elements[i] = types.StringValue(op)
	}
	return types.SetValueMust(types.StringType, elements)
}

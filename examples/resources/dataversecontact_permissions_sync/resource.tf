# Publishes the scope's baseline permissions (its defaults.json).
# Every scope needs exactly one of these: with no published defaults every
# route answers "403 Missing required permission" — including reads.
resource "dataversecontact_permissions_sync" "default" {
  scope = "default"

  # Baseline permissions granted to every authenticated contact, keyed by
  # route name. Tokens: "me" / "team" / "all" read tiers, plus "write",
  # "write:all" and "create".
  default_permissions = {
    case    = ["me", "write", "create"]
    contact = ["me"]
  }

  # Let signed-in callers without a Dataverse contact self-register
  # (defaults to false).
  allow_self_register = true

  # Re-publish whenever any table definition changes. With real table
  # resources in the same config, hash their ids and add depends_on:
  #
  #   triggers = {
  #     tables_hash = sha256(join(",", [
  #       dataversecontact_table.case.id,
  #       dataversecontact_table.contact.id,
  #     ]))
  #   }
  #   depends_on = [
  #     dataversecontact_table.case,
  #     dataversecontact_table.contact,
  #   ]
  triggers = {
    tables_hash = "manual-1"
  }

  # For scopes where one signed-in person acts for several companies:
  #
  #   company_model = {
  #     strategy = "associated-accounts"
  #     associated_accounts = {
  #       relationship = "new_contact_account_assoc"
  #     }
  #   }
  #
  # For scopes where callers may self-join a company by email domain:
  #
  #   join = {
  #     strategy     = "domain-list"
  #     domain_field = "new_portaldomains"
  #   }
}

output "permission_count" {
  value = dataversecontact_permissions_sync.default.permission_count
}

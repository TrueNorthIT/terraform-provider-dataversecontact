resource "dataversecontact_permissions_sync" "default" {
  scope = "default"

  # Baseline permissions granted to every authenticated contact, keyed by route name.
  # Published as the `permissions` object of the scope's defaults.json.
  default_permissions = {
    case    = ["team", "write", "create"]
    contact = ["team"]
  }

  # Enable self-registration for citizen-facing scopes (optional, defaults to false).
  allow_self_register = false

  # Optional: auto-link newly provisioned contacts to their company by email
  # domain. account_field names an account text column holding the company's
  # email-domain list (e.g. "acme.com, acme.co.uk"). Omit to disable.
  # self_register_auto_link = {
  #   account_field = "tn_emaildomains"
  # }

  triggers = {
    tables_hash = sha256(join(",", [
      dataversecontact_table.case.id,
      dataversecontact_table.contact.id,
    ]))
  }

  depends_on = [
    dataversecontact_table.case,
    dataversecontact_table.contact,
  ]
}

output "permission_count" {
  value = dataversecontact_permissions_sync.default.permission_count
}

resource "dataversecontact_permissions_sync" "default" {
  scope = "default"

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

output "audience" {
  value = dataversecontact_permissions_sync.default.audience
}

output "permission_count" {
  value = dataversecontact_permissions_sync.default.permission_count
}

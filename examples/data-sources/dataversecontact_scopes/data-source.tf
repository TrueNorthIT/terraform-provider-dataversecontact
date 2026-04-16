data "dataversecontact_scopes" "all" {}

output "scopes" {
  value = data.dataversecontact_scopes.all.scopes
}

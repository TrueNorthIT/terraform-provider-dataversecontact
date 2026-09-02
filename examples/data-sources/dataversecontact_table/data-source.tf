data "dataversecontact_table" "case" {
  scope      = "default"
  route_name = "case"
}

output "dataverse_table" {
  value = data.dataversecontact_table.case.dataverse_table
}

output "field_count" {
  value = data.dataversecontact_table.case.field_count
}

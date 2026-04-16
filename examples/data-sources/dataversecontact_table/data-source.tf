data "dataversecontact_table" "case" {
  scope      = "default"
  route_name = "case"
}

output "schema" {
  value = data.dataversecontact_table.case.schema_json
}

output "dataverse_table" {
  value = data.dataversecontact_table.case.dataverse_table
}

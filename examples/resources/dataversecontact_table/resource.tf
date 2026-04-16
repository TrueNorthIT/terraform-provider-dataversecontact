resource "dataversecontact_table" "case" {
  scope       = "default"
  route_name  = "case"
  schema_json = file("${path.module}/case.schema.json")
}

output "table_id" {
  value = dataversecontact_table.case.id
}

output "field_count" {
  value = dataversecontact_table.case.field_count
}

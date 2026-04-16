data "dataversecontact_table_definitions" "default" {
  scope = "default"
}

output "table_count" {
  value = length(data.dataversecontact_table_definitions.default.definitions)
}

output "table_names" {
  value = [for d in data.dataversecontact_table_definitions.default.definitions : d.route_name]
}

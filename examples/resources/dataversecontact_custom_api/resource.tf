resource "dataversecontact_custom_api" "my_action" {
  scope       = "default"
  route_name  = "my-action"
  schema_json = file("${path.module}/my-action.customapi.json")
}

output "custom_api_id" {
  value = dataversecontact_custom_api.my_action.id
}

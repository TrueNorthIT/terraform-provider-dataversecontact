terraform {
  required_providers {
    dataversecontact = {
      source = "tnapps/dataversecontact"
    }
  }
}

variable "api_url" {
  type    = string
  default = "https://api.dataverse-contact.tnapps.co.uk"
}

# The admin connection key. This must match the ADMIN_CONNECTION_KEY
# configured on the API deployment.
variable "connection_key" {
  type      = string
  sensitive = true
}

variable "scope_name" {
  type    = string
  default = "default"
}

provider "dataversecontact" {
  api_url        = var.api_url
  connection_key = var.connection_key
}

# List existing scopes
data "dataversecontact_scopes" "all" {}

# Auto-discover schemas from directory
resource "dataversecontact_table" "tables" {
  for_each    = fileset("${path.module}/schemas/${var.scope_name}", "*.schema.json")
  scope       = var.scope_name
  route_name  = trimsuffix(each.value, ".schema.json")
  schema_json = file("${path.module}/schemas/${var.scope_name}/${each.value}")
}

# Auto-discover custom APIs from directory
resource "dataversecontact_custom_api" "apis" {
  for_each    = fileset("${path.module}/schemas/${var.scope_name}", "*.customapi.json")
  scope       = var.scope_name
  route_name  = trimsuffix(each.value, ".customapi.json")
  schema_json = file("${path.module}/schemas/${var.scope_name}/${each.value}")
}

# Publish baseline permissions (defaults.json) after all tables and custom APIs are published
resource "dataversecontact_permissions_sync" "scope" {
  scope = var.scope_name

  # Baseline permissions granted to every authenticated contact, keyed by route name.
  default_permissions = {
    case = ["team", "write", "create"]
  }

  # Enable self-registration for citizen-facing scopes.
  allow_self_register = false

  triggers = {
    tables_hash = sha256(join(",", [for t in dataversecontact_table.tables : t.id]))
    apis_hash   = sha256(join(",", [for a in dataversecontact_custom_api.apis : a.id]))
  }

  depends_on = [
    dataversecontact_table.tables,
    dataversecontact_custom_api.apis,
  ]
}

# Outputs
output "scopes" {
  value = data.dataversecontact_scopes.all.scopes
}

output "published_tables" {
  value = { for k, t in dataversecontact_table.tables : k => {
    id             = t.id
    dataverse_table = t.dataverse_table
    field_count    = t.field_count
  }}
}

output "published_apis" {
  value = { for k, a in dataversecontact_custom_api.apis : k => {
    id                   = a.id
    dataverse_unique_name = a.dataverse_unique_name
  }}
}

output "permission_count" {
  value = dataversecontact_permissions_sync.scope.permission_count
}

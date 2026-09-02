---
page_title: "Getting started"
subcategory: ""
description: |-
  Define your first scope: provider setup, a table, and the mandatory permissions publish.
---

# Getting started

## 1. Require the provider

```terraform
terraform {
  required_providers {
    dataversecontact = {
      source  = "TrueNorthIT/dataversecontact"
      version = "~> 0.1"
    }
  }
}

provider "dataversecontact" {
  api_url = "https://api.dataverse-contact.tnapps.co.uk"
  # connection_key read from DATAVERSE_CONTACT_CONNECTION_KEY
}
```

Pin a version. After `terraform init` downloads the provider, editors running
the Terraform language server (e.g. VS Code with the HashiCorp Terraform
extension) autocomplete every resource attribute and show its documentation on
hover — most of the guidance in these docs is available without leaving your
editor.

-> **Migrating from a local build?** Older configs used
`source = "tnapps/dataversecontact"` resolved through a `dev_overrides` CLI
config. Change the source to `TrueNorthIT/dataversecontact` with a version
constraint, run
`terraform state replace-provider tnapps/dataversecontact TrueNorthIT/dataversecontact`,
and remove the `dev_overrides` stanza (keep it only for provider development —
it bypasses `terraform init` and with it the language server's schema download).

## 2. Publish a table

```terraform
resource "dataversecontact_table" "case" {
  scope           = "default"
  route_name      = "case"
  dataverse_table = "incidents"
  primary_key     = "incidentid"

  default_select = ["title", "ticketnumber", "createdon"]
  lookup_fields  = ["title", "ticketnumber"]

  fields = {
    title = {
      type        = "string"
      description = "Case title."
    }
    customerid = {
      type         = "lookup"
      description  = "The citizen this case belongs to."
      lookup_table = "contact"
      bind_field   = "customerid_contact"
    }
  }

  contact_join_step {
    table = "contacts"
    from  = "customerid_contact"
    key   = "contactid"
  }
}
```

Three attributes have smart defaults you can usually omit:
`dataverse_logical_name` (derived by singularizing `dataverse_table`),
`required_permission` (defaults to `route_name`) and `filters` (defaults to
`["statecode eq 0"]`).

## 3. Publish the scope's permissions — mandatory

```terraform
resource "dataversecontact_permissions_sync" "default" {
  scope = "default"

  default_permissions = {
    case = ["me", "write", "create"]
  }

  triggers = {
    tables_hash = sha256(dataversecontact_table.case.id)
  }

  depends_on = [dataversecontact_table.case]
}
```

A scope with no published defaults grants nothing: every route answers
`403 Missing required permission`, including reads. See the
[Permissions](permissions) guide.

## 4. Go further

The [full-scope example](https://github.com/TrueNorthIT/terraform-provider-dataversecontact/tree/main/examples/full-scope)
is a complete copy-paste starting point: an owned parent table, an ownerless
child scoped through it, a custom API and the permissions publish. The
[repository README](https://github.com/TrueNorthIT/terraform-provider-dataversecontact#readme)
also documents a VS Code snippets pack (`dvc-` prefixes) you can copy into a
consuming repo.

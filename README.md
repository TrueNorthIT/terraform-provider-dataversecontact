# terraform-provider-dataversecontact

Terraform provider for the **Dataverse Contact API** — the API layer that sits
in front of Microsoft Dataverse and exposes only what has been explicitly
configured. This provider manages that configuration as code: which Dataverse
tables are published, which fields callers can read and write, which Custom
APIs are callable, and the baseline permissions every authenticated caller
gets in each scope.

**Registry docs:** <https://registry.terraform.io/providers/TrueNorthIT/dataversecontact/latest/docs>
· [Getting started](https://registry.terraform.io/providers/TrueNorthIT/dataversecontact/latest/docs/guides/getting-started)
· [Permissions](https://registry.terraform.io/providers/TrueNorthIT/dataversecontact/latest/docs/guides/permissions)
· [Parent-scoped children](https://registry.terraform.io/providers/TrueNorthIT/dataversecontact/latest/docs/guides/parent-scoped-children)

For a complete copy-paste starting point (tables, reverse joins, a custom API
and the permissions publish) see [`examples/full-scope`](examples/full-scope/main.tf).

## Quickstart

```terraform
terraform {
  required_providers {
    dataversecontact = {
      source  = "TrueNorthIT/dataversecontact"
      version = "~> 1.0"
    }
  }
}

# connection_key comes from DATAVERSE_CONTACT_CONNECTION_KEY — keep it out of
# config. It must match ADMIN_CONNECTION_KEY on the API deployment.
provider "dataversecontact" {
  api_url = "https://api.dataverse-contact.tnapps.co.uk"
}

resource "dataversecontact_table" "case" {
  scope           = "default"
  route_name      = "case"
  dataverse_table = "incidents"
  primary_key     = "incidentid"

  default_select = ["title", "ticketnumber"]
  lookup_fields  = ["title"]

  fields = {
    title = { type = "string", description = "Case title." }
  }
}

# Mandatory: a scope with no published defaults grants nothing — every route
# answers "403 Missing required permission", including reads.
resource "dataversecontact_permissions_sync" "default" {
  scope               = "default"
  default_permissions = { case = ["me", "write", "create"] }
  triggers            = { tables_hash = sha256(dataversecontact_table.case.id) }
  depends_on          = [dataversecontact_table.case]
}
```

## Editor setup — autocomplete for everything

Install the official [HashiCorp Terraform](https://marketplace.visualstudio.com/items?itemName=HashiCorp.terraform)
VS Code extension — either:

- in VS Code: **Extensions** view (`Ctrl+Shift+X`), search for
  "HashiCorp Terraform", click **Install**; or
- from a terminal:

  ```sh
  code --install-extension hashicorp.terraform
  ```

(Any editor running `terraform-ls` works the same way.) Then, in your
Terraform repo, run `terraform init` once. That downloads the provider, and
the language server serves its full schema from then on: every attribute
autocompletes, and every description in these docs appears on hover. No extra
tooling needed — but it depends on `init` resolving the provider from the
registry, so pin a version and avoid `dev_overrides` outside provider
development.

> Migrating an older config off a local build? Change
> `source = "tnapps/dataversecontact"` to `TrueNorthIT/dataversecontact`, run
> `terraform state replace-provider tnapps/dataversecontact TrueNorthIT/dataversecontact`,
> and drop the `dev_overrides` stanza.

### Snippets

[`snippets/dataversecontact.code-snippets`](snippets/dataversecontact.code-snippets)
is a VS Code snippets pack for the common shapes: type `dvc` in a `.tf` file to
list them (`dvc-table`, `dvc-field`, `dvc-field-lookup`, `dvc-expand`,
`dvc-join-step`, `dvc-create-default`, `dvc-permissions-sync`,
`dvc-custom-api`). Copy the file into your repo's `.vscode/` folder — no
extension install needed:

```sh
curl -o .vscode/dataversecontact.code-snippets \
  https://raw.githubusercontent.com/TrueNorthIT/terraform-provider-dataversecontact/main/snippets/dataversecontact.code-snippets
```

## Development

```sh
make build              # build the provider binary
make dev-override       # print the dev_overrides CLI config for local testing
make test               # unit tests
make testacc            # acceptance tests (needs DATAVERSE_CONTACT_* env vars)
make docs               # regenerate docs/ from schema + templates/ + examples/
make docs-check         # fail if committed docs are stale
make validate-examples  # terraform-validate every example against the schema
```

Docs are generated with [tfplugindocs](https://github.com/hashicorp/terraform-plugin-docs)
(wired as a `go.mod` tool; needs a `terraform` binary on PATH). Attribute
descriptions live in the Go schemas under `internal/resources/` and
`internal/datasources/` — edit those, then `make docs`.

Architecture notes live in [OVERVIEW.md](OVERVIEW.md).

## Releasing

Push a `v*` tag. `.github/workflows/release.yml` runs goreleaser and publishes
signed artifacts the Terraform Registry picks up. Commit regenerated `docs/`
before tagging — the registry reads them from the tagged commit.

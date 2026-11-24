# CLAUDE.md

The Terraform provider (`TrueNorthIT/dataversecontact`) for the Contact Portal API (CPA): it
manages a scope's configuration (tables, custom APIs, default access) through the API's admin
endpoints. What it does and how it's built is in [OVERVIEW.md](OVERVIEW.md).

## Before any piece of work: check it against the CPA personas

The roles the CPA serves, and the rules each one holds us to, are in the API repo's
[`personas/`](https://github.com/TrueNorthIT/dataverse-contact-api/tree/develop/personas) (on
`develop`). Before planning a change here, run the `persona-check` skill (the
`dataverse-contact-api` plugin) and show what each persona says; wait on any amber or red.

This repo belongs to the **platform engineer**. Their rules that bite most here:

- **If Terraform can't say it, it doesn't exist.** Every configuration concept the API has is an
  attribute here. A new API config option without a provider attribute is unfinished work in the
  API's PR checklist; when it lands here, link the API PR.
- **No drift.** What an admin read returns is what was saved, so `terraform plan` is clean after
  an apply. Values the API derives stay out of state (the API leaves them out of admin reads and
  sends `derived` instead).
- **An upgrade never breaks an apply that worked.** A new validation that refuses config which
  used to apply ships as a warning first.
- **Pipelines sign in without a shared key.** Entra (OIDC, managed identity, Azure CLI) first;
  `connection_key` stays optional.

## Build and test

```bash
make build              # build the provider
make test               # unit tests
make testacc            # acceptance tests (TF_ACC=1)
make docs               # regenerate docs/ from schemas, templates/ and examples/
make docs-check         # fail if docs/ or examples/ are stale
make validate-examples  # terraform validate every example
make lint               # golangci-lint
```

Go and Terraform may not be installed locally; CI (`.github/workflows/test.yml`) runs the
acceptance tests and the docs check. Commit regenerated `docs/` with every schema change.

# terraform-provider-dataversecontact

## What it does — user, customer & business value

This is a custom Terraform provider (`TrueNorthIT/dataversecontact`) that lets teams define a local authority's citizen-facing API surface as code. The Contact Portal API sits in front of Microsoft Dataverse and only exposes the tables, fields and custom actions that have been explicitly configured for it. This provider manages that configuration: which Dataverse tables are published to the API, which fields citizens or staff can read and write, which Dataverse Custom APIs (e.g. calendar/availability functions) are callable, and the baseline permissions every authenticated caller gets in each scope.

Its users are developers and platform engineers at TrueNorth IT or at a council, not citizens directly. The value it delivers is repeatability and safety: instead of hand-configuring API table definitions through an admin UI for every council, environment or pilot, a whole portal backend (e.g. a citizen booking service) can be provisioned with `terraform apply`, reviewed in version control, reproduced in a new environment in minutes, and torn down cleanly. That dramatically lowers the cost of standing up new citizen services and keeps environments consistent — a key enabler for offering the LA Stack to multiple authorities.

## Architecture overview

- **Language/stack:** Go 1.24, HashiCorp Terraform Plugin Framework (v1.16), distributed as `registry.terraform.io/TrueNorthIT/dataversecontact`.
- **`internal/provider`** — provider definition and configuration (API URL plus credentials).
- **`internal/client`** — HTTP client for the Dataverse Contact API admin endpoints: table definitions, custom APIs and scope defaults.
- **`internal/resources`** — Terraform resources:
  - `dataversecontact_table` — publishes a Dataverse table to the API (route name, fields, types, read-only flags, aliases, default select, expands/relationships, contact join paths, public read/create flags).
  - `dataversecontact_custom_api` — registers a Dataverse Custom API route from a JSON schema.
  - `dataversecontact_permissions_sync` — publishes the scope's **`defaults.json`**: the baseline permissions every authenticated caller gets. See the note below — the name is misleading.
- **`internal/datasources`** — read-only data sources: `scopes`, `table`, `table_definitions`.
- **`examples/`** — usage examples per resource/data source plus a full-scope example.

Authentication is the pre-shared admin connection key, sent as the admin Bearer token (`connection_key`, or `DATAVERSE_CONTACT_CONNECTION_KEY` via a runner script). One key administers every scope; it must be byte-identical to `ADMIN_CONNECTION_KEY` on the API deployment.

```
Terraform config (.tf)
        │ plan/apply
        ▼
terraform-provider-dataversecontact (Go)
        │ Admin REST calls (Bearer: connection key)
        ▼
Dataverse Contact API  ──►  Microsoft Dataverse (table metadata, custom APIs)
                       └──►  Azure Blob (published/<scope>/… config + defaults.json)
```

### `permissions_sync` does not sync anything to an IdP

The name is left over from when the provider pushed a scope's permission set into
Auth0 so it could be assigned there. **That code is gone** — there is no
`auth0.go`, and no Auth0 call anywhere in the provider.

Today the resource makes exactly one request: `PublishDefaults` →
`PUT /api/v2/_admin/{scope}/table-manager/defaults` (`internal/client/permissions.go`),
which writes `published/<scope>/defaults.json` to blob storage. That file is the
**only** way a Terraform-provisioned scope declares baseline permissions, and it
is very much live: the API merges it into table config at registry build
(`defaultActions`) and `getDefaultPermissions(scope)` resolves it on every
request, unioned with per-user `cr_apipermission` rows.

So it is required, not vestigial. **A blob-only scope with no published
`defaults.json` grants nothing** — every route answers
`403 Missing required permission: <route>`, including reads, which is a
confusing way to discover you left the resource out.

Renaming it would be a breaking change to every consumer's state, so the name
stays and this note exists instead.

Built locally with the GNUmakefile (`go install`); during development a Terraform CLI `dev_overrides` entry points at the locally built binary.

## Parent-scoped children (ownerless records)

Some Dataverse tables have **no per-citizen owner** — no contact/account column to
row-scope by (a venue slot, an order line, an uploaded file). You can't add a contact to
every table, so an ownerless child is **hung off an owned parent** and the parent governs it.

Declare it with `dataversecontact_table`:

- On the **parent** route (the thing the citizen owns): add an `expand` for the lookup that
  points at the child, plus a `create_default` binding the citizen (so the parent is always
  owned by the caller). The child is then created by **nesting it inside the parent's
  create** — the API authorises against the **parent's** `create` permission, so the child
  needs **no `create` permission and no `contact_join_step`** of its own.
- On the **child** route: give it a **reverse `contact_join_step`** so `/me` scoping works
  through the parent. The child has no forward contact column, so the first step walks the
  parent's lookup **backwards** — set `reverse = true` and put the parent's collection-valued
  navigation property in `from` — then continue forward to the contact. The API compiles this
  to an OData `any()` lambda. **Don't** give an ownerless child a broad `["all", "write:all"]`
  to work around its lack of an owner — the reverse join lets you scope it to `["me", "write"]`.

Example (rbooking): `servicebooking` (parent — `create_default { tn_Citizen → contact }` +
`expand { tn_Booking → bookableresourcebooking }`) wraps the ownerless `booking`. A citizen
`POST /me/servicebooking` with a nested `tn_booking` creates both in one call; only
`servicebooking:create` is required and the booking is always tied to *their* own
servicebooking. The `booking` route then scopes reads/writes back to the caller with a reverse
join (`booking ← servicebooking → contact`):

```hcl
contact_join_step {              # hop 1: reverse — booking ← servicebooking
  table   = "tn_citizenservicebookings"
  from    = "tn_booking_csb"     # the collection nav (relationship schema name)
  key     = ""
  reverse = true
}
contact_join_step {              # hop 2: forward — servicebooking → contact
  table = "contacts"
  from  = "tn_Citizen"
  key   = "contactid"
}
```

Generic shape: **Order → OrderLine** (the order is customer-owned; the line has no customer).
See the Contact API's `docs/API.md` "Parent-scoped children".

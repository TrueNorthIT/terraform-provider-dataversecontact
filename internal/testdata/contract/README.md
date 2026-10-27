# API contract fixtures

Real response bodies from the Dataverse Contact API's admin endpoints. Two tests use them:

- `internal/client/contract_test.go` decodes each one into the client type the provider uses for
  it. A field typed differently from what the API sends fails here. That is the bug fixed in v1.2.0,
  where validation warnings were objects but the client expected strings.
- `internal/fakeapi/contract_test.go` checks that the fake API used by the acceptance tests
  answers in the same shape, so the fake cannot drift away from the real API.

## Where each fixture comes from

**Recorded** (`get-*`): read-only GETs against the live API on 2026-10-05, then scrubbed. Tenant
authorities and Dynamics URLs are zeroed, and the scope list and table definitions are trimmed.
Nothing else is changed.

**From the API source** (`put-*`, `post-*`, `delete-*`): write endpoints. Recording these would
mean changing a live scope, so they are written by hand from the handlers in
`dataverse-contact-api` (main, 2026-10-05). Each one includes an error and a warning entry, so
nested shapes are covered:

| Fixture | Handler |
|---|---|
| `put-table-draft` | `server/handlers/admin.ts`, PUT branch of the table-manager handler. `validation` is `ValidationResult` in `server/services/discover/types.ts`; `warnings` are `ConfigFinding`s from `server/config/scopes/config-rules.ts`. The PUT strips `FIELD_NOT_FOUND` fields and answers with a validation of what is left, so that code can't appear here: `new_gone` was stripped from `fields` but not from `lookupFields`, hence `LOOKUP_FIELD_UNKNOWN` |
| `post-table-publish` | `server/handlers/admin.ts`, publish handler (`PublishResult` in `server/services/draft-lifecycle.ts`, plus `validation`) |
| `post-table-unpublish`, `post-table-remove`, `post-custom-api-remove` | `createBatchActionHandler` in `server/handlers/admin-shared.ts` |
| `delete-table-recycled` | `createSingleItemHandler` in `server/handlers/admin-shared.ts` |
| `put-custom-api-draft`, `post-custom-api-publish` | `server/handlers/custom-apis.ts` |
| `put-defaults` | `handleScopeDefaults` PUT in `server/handlers/admin.ts` |

**From the API source, until it ships** (`get-table-published-emails`): the bundled `emails`
route from `server/config/scopes/default/email/emails.schema.json` (API 1.31.0, PR #447), in
the admin GET's envelope. It carries `files` (child files) and a reverse join step with
`match`. Re-record it from the live API once 1.31.0 is deployed.

**From the API source, until it ships** (`get-table-published-case-documents`): the recorded
`get-table-published-case` with a `documents` key added, shaped as `DocumentsSettings` in
`server/config/scopes/types.ts` (API 1.34.0, #460): permission, folder, typed columns and a
filter. The admin GET returns the hint as authored, so this is what comes back. Re-record it from
the live API once 1.34.0 is deployed.

**From the API contract, until it ships** (`get-table-published-case-documents-upload`): the same,
with `documents.upload` (API 1.35.0): `maxBytes`, `extensions`, and fixed `columns` values typed
as their declared column types (a JSON boolean, string and number), which is how the provider sends
them. Re-record it from the live API once 1.35.0 is deployed.

**From the API contract, until it ships** (`get-table-published-case-reference`): the recorded
`get-table-published-case` with `"reference": "ticketnumber"` added (API 1.40.0): the column logged
with every request that touches a case, so App Insights can be searched for the case number. The
admin GET returns the hint as authored. Re-record it from the live API once 1.40.0 is deployed.

**From the API contract, until it ships** (`get-table-published-case-invites`): the recorded
`get-table-published-case-documents-upload` with `"invites": ["update"]` on the table and
`"invites": true` in `documents.upload` (API 1.37.0): what one-time invite links may do on the
route. The admin GET returns the hint as authored. Re-record it from the live API once 1.37.0 is
deployed.

## Refreshing

When the API changes a response, update the fixture first, then make the client and the fake
match it. Re-record a `get-*` fixture with a GET only, never against a customer org, and scrub it
the same way before committing. This repo is public.

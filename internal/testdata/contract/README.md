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
| `put-table-draft` | `server/handlers/admin.ts`, PUT branch of the table-manager handler. `validation` is `ValidationResult` in `server/services/discover/types.ts` |
| `post-table-publish` | `server/handlers/admin.ts`, publish handler (`PublishResult` in `server/services/draft-lifecycle.ts`, plus `validation`) |
| `post-table-unpublish`, `post-table-remove`, `post-custom-api-remove` | `createBatchActionHandler` in `server/handlers/admin-shared.ts` |
| `delete-table-recycled` | `createSingleItemHandler` in `server/handlers/admin-shared.ts` |
| `put-custom-api-draft`, `post-custom-api-publish` | `server/handlers/custom-apis.ts` |
| `put-defaults` | `handleScopeDefaults` PUT in `server/handlers/admin.ts` |

## Refreshing

When the API changes a response, update the fixture first, then make the client and the fake
match it. Re-record a `get-*` fixture with a GET only, never against a customer org, and scrub it
the same way before committing. This repo is public.

---
page_title: "Custom APIs on the me, team, all and public tiers"
subcategory: ""
description: |-
  How a dataversecontact_custom_api definition decides who may call a Dataverse function or action, and on which record.
---

# Custom APIs on the me, team, all and public tiers

`dataversecontact_custom_api` publishes a Dataverse custom API (a function or
an action, bound or unbound) as a route of the Contact API. The definition is
the `schema_json` you pass; the provider sends it as is, so everything below is
a property of that JSON.

-> **Requires the API release with tiered custom APIs.** A definition that uses
`tiers`, `target` or `callerParameters` must only be applied once every API
deployment reading the scope runs that release. An older deployment reads such
a definition as a legacy API with **no ownership check**.

## Routes

Callers reach a custom API on the tier the URL names. `GET` calls a function
(inputs in the query string), `POST` calls an action (inputs in a JSON body).

| Tier | URL | Permission | The record must |
|---|---|---|---|
| `public` | `/api/v2/{scope}/public/actions/{name}[/{id}]` | none, no sign-in | — |
| `me` | `/api/v2/{scope}/me/actions/{name}[/{id}]` | `{name}:invoke` | belong to the caller's contact |
| `team` | `/api/v2/{scope}/team/actions/{name}[/{id}]` | `{name}:invoke:team` | belong to the caller's company account |
| `all` | `/api/v2/{scope}/all/actions/{name}[/{id}]` | `{name}:invoke:all` | exist and pass `target.filters` |

`tiers` lists which of these URLs exist; any other tier answers 404. Grant the
permissions in `dataversecontact_permissions_sync.default_permissions`, keyed by
the route name — for example `close-incident = ["invoke", "invoke:team"]`.

The caller always names the record in the URL. A bound API runs on it; an
unbound one has its id written into the parameter `target.recordIdPath` names.
Either way the record Dataverse acts on is the record the API checked.

## Shapes

### Bound to a record

`target` must be the bound table. `target.table` names a published table route
in the same scope, and its contact and account scoping is reused.

```terraform
resource "dataversecontact_custom_api" "escalate_case" {
  scope      = var.scope
  route_name = "escalate-case"

  schema_json = jsonencode({
    routeName              = "escalate-case"
    dataverseUniqueName    = "tn_EscalateCase"
    isFunction             = false
    bindingType            = "entity"
    boundEntityLogicalName = "incident"
    boundEntitySetName     = "incidents"
    tiers                  = ["me", "team", "all"]
    target                 = { table = "case" }
  })
}
```

Called as `POST /api/v2/{scope}/me/actions/escalate-case/{caseId}`.

### Unbound, acting on a record

`target.recordIdPath` is the parameter the record id is written into. A dotted
path reaches inside an `entity` parameter; a final `x@odata.bind` segment is
written as `/{entityset}({id})`.

```terraform
resource "dataversecontact_custom_api" "close_incident" {
  scope      = var.scope
  route_name = "close-incident"

  schema_json = jsonencode({
    routeName           = "close-incident"
    dataverseUniqueName = "CloseIncident"
    isFunction          = false
    bindingType         = "global"
    tiers               = ["me", "team"]
    target = {
      table        = "case"
      recordIdPath = "IncidentResolution.incidentid@odata.bind"
      filters      = ["statecode eq 0"]
    }
    requestParameters = [
      { uniqueName = "IncidentResolution", type = "entity" },
      { uniqueName = "Status", type = "integer" },
    ]
  })
}
```

Called as `POST /api/v2/{scope}/me/actions/close-incident/{caseId}` with
`{ "IncidentResolution": { "subject": "Done" }, "Status": 5 }`.

### Caller-scoped (no record)

`callerParameters` fills parameters from the signed-in caller — `contactId` or
`accountId` — and ignores whatever the client sent. The plugin behind the API
is trusted to filter by them.

```terraform
resource "dataversecontact_custom_api" "my_upcoming_bookings" {
  scope      = var.scope
  route_name = "my-upcoming-bookings"

  schema_json = jsonencode({
    routeName           = "my-upcoming-bookings"
    dataverseUniqueName = "tn_MyUpcomingBookings"
    isFunction          = true
    bindingType         = "global"
    tiers               = ["me"]
    callerParameters    = { CitizenId = "contactId" }
    requestParameters = [
      { uniqueName = "CitizenId", type = "guid" },
    ]
  })
}
```

### Public

```terraform
resource "dataversecontact_custom_api" "expand_calendar" {
  scope      = var.scope
  route_name = "expand-calendar"

  schema_json = jsonencode({
    routeName           = "expand-calendar"
    dataverseUniqueName = "ExpandCalendar"
    isFunction          = true
    bindingType         = "entity"
    boundEntitySetName  = "calendars"
    tiers               = ["public"]
    requestParameters = [
      { uniqueName = "Start", type = "datetime" },
      { uniqueName = "End", type = "datetime" },
    ]
  })
}
```

### `target` without a table route

When the record's table isn't published as a route, declare it inline. The
owner columns are navigation properties, one hop from the record; several are
OR'd.

```terraform
target = {
  dataverseTable = "incidents"
  primaryKey     = "incidentid"
  contactColumn  = ["customerid_contact", "primarycontactid"]
  accountColumn  = "customerid_account"
  recordIdPath   = "CaseId" # unbound APIs only
}
```

## Rules the API enforces on apply

A definition that breaks one of these fails `terraform apply` with a 400 that
names the problem.

- An entity-bound API needs `boundEntitySetName`, its `target` must be that
  table, and it takes no `recordIdPath`.
- An unbound API with a `target` needs `recordIdPath`, rooted at a declared
  request parameter. On a function it must be a top-level `guid`,
  `entityReference` or `string` parameter.
- `me` needs a `target` (with `contactColumn`, or a table route with a contact
  path) or a `contactId` caller parameter; `team` the same with `accountColumn`
  / `accountId`.
- `public` can't carry a `target` or `callerParameters`.
- Every other `guid`, `entityReference` or `entity` request parameter is
  refused unless marked `unchecked = true` — a record id the API doesn't check
  has to be a decision.
- A `target.table` route must be on the bound table, have the paths its tiers
  check through, and not be a lake table. If the route isn't published yet the
  definition is accepted, and calls fail closed until it is — add a
  `depends_on` on the `dataversecontact_table`.
- `ownershipCheck` can't be combined with `tiers`; `target` and
  `callerParameters` need `tiers`.

`requiredPermission` is optional; it defaults to `{routeName}:invoke`.

## Definitions without `tiers`

Existing definitions keep working unchanged. `publicInvoke = true` means
`tiers = ["public"]`. A private definition with an `ownershipCheck` answers on
the tiers its columns can check (`me` with `contactColumn`, `team` with
`accountColumn`, and `all`); one without answers on all three. Signed-in calls
to `/public/actions/{name}` for such a definition still work but are
deprecated.

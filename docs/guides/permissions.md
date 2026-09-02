---
page_title: "Permissions and defaults.json"
subcategory: ""
description: |-
  What dataversecontact_permissions_sync actually publishes, and why every scope needs one.
---

# Permissions and defaults.json

`dataversecontact_permissions_sync` publishes one thing: the scope's
`defaults.json` — the baseline permissions every authenticated caller gets —
via `PUT /api/v2/_admin/{scope}/table-manager/defaults`.

Despite the name, nothing is synced to an identity provider. The name is left
over from an earlier design that pushed permission sets into Auth0; that code
is gone. The resource stays named `permissions_sync` because renaming it would
break every consumer's state.

## Why it is mandatory

The published `defaults.json` is the **only** way a Terraform-provisioned scope
declares baseline permissions. The API merges it into table config at registry
build and resolves it on every request, unioned with per-user permission rows.

A scope with no published `defaults.json` grants nothing: every route answers
`403 Missing required permission` — including reads, which is a confusing way
to discover you left the resource out.

## Permission tokens

Each `default_permissions` entry maps a route name to a list of tokens:

- `me` — read rows reachable from the caller's own contact via the table's
  `contact_join_step`.
- `team` — read rows reachable via the table's `team_join_step`.
- `all` — read every row.
- `write` — update rows within the granted read tier.
- `write:all` — update every row.
- `create` — create rows (combine with a `create_default` on the table to
  bind new rows to the caller).

Prefer the narrowest tier that works. In particular, don't give an ownerless
child table `["all", "write:all"]` to work around its lack of an owner — a
reverse join lets you scope it to `["me", "write"]` (see
[Parent-scoped children](parent-scoped-children)).

## Re-publishing when tables change

Publishing is one-shot: the resource re-publishes only when its inputs change.
Hash your table ids into `triggers` and add `depends_on` so defaults are
re-published after every table change:

```terraform
resource "dataversecontact_permissions_sync" "this" {
  scope = var.scope

  default_permissions = {
    servicebooking = ["me", "write", "create"]
    booking        = ["me", "write"]
  }

  triggers = {
    tables_hash = sha256(join(",", [
      dataversecontact_table.servicebooking.id,
      dataversecontact_table.booking.id,
    ]))
  }

  depends_on = [
    dataversecontact_table.servicebooking,
    dataversecontact_table.booking,
  ]
}
```

---
page_title: "Parent-scoped children"
subcategory: ""
description: |-
  Publishing ownerless tables safely: nested creates through an owned parent, and reverse contact joins for /me scoping.
---

# Parent-scoped children (ownerless records)

Some Dataverse tables have **no per-citizen owner** — no contact or account
column to row-scope by (a venue slot, an order line, an uploaded file). You
can't add a contact to every table, so an ownerless child is **hung off an
owned parent** and the parent governs it.

## On the parent (the thing the citizen owns)

- Add an `expand` for the lookup that points at the child.
- Add a `create_default` binding the citizen, so the parent is always owned by
  the caller.

The child is then created by **nesting it inside the parent's create** — the
API authorises against the **parent's** `create` permission, so the child needs
no `create` permission and no `contact_join_step` of its own for that path.

## On the child

Give it a **reverse `contact_join_step`** so `/me` scoping works through the
parent. The child has no forward contact column, so the first step walks the
parent's lookup **backwards**: set `reverse = true` and put the parent
relationship's collection-valued navigation property in `from`, then continue
forward to the contact. The API compiles the reverse step to an OData `any()`
lambda.

```terraform
# booking ← servicebooking → contact
contact_join_step {
  table   = "tn_citizenservicebookings"
  from    = "tn_booking_csb" # the relationship's collection nav (schema name)
  key     = ""
  reverse = true
}
contact_join_step {
  table = "contacts"
  from  = "tn_Citizen"
  key   = "contactid"
}
```

Don't guess the collection navigation property — read it from the
relationship's `ReferencedEntityNavigationPropertyName` in Dataverse metadata.
Polymorphic lookups have a differently-named relationship per target.

**Don't** give an ownerless child a broad `["all", "write:all"]` grant to work
around its lack of an owner — the reverse join lets you scope it to
`["me", "write"]`.

## Worked example

A citizen `POST /me/servicebooking` with a nested `tn_booking` creates both the
owned `servicebooking` (parent, bound to the caller by `create_default`) and
the ownerless `booking` (child) in one call; only `servicebooking:create` is
required, and the booking is always tied to *their own* servicebooking. The
`booking` route then scopes reads and writes back to the caller with the
reverse join above, so the cancel flow needs only `["me", "write"]`.

The generic shape is **Order → OrderLine**: the order is customer-owned; the
line has no customer. See the
[full-scope example](https://github.com/TrueNorthIT/terraform-provider-dataversecontact/tree/main/examples/full-scope)
for the complete configuration.

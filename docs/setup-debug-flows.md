# BookWarehouse Ebook docs

This directory is the operator handbook for `silo.bookwarehouse-ebook`.
The [README](../README.md) is the overview (what the plugin is, capabilities,
config keys, signed-URL contract). The pages below go deeper on the parts an
operator touches when something is wrong or a new instance is being brought
up.

- [operations.md](operations.md) — install/upgrade lifecycle, config save
  semantics, the `/admin` dashboard, scheduled reconciler tuning, Postgres
  role and pool sizing.
- [debugging.md](debugging.md) — symptom-driven runbook: upstream
  connectivity, signed-URL 401/503, range requests for reader apps, stuck
  forwarded requests, auto-monitoring sanity checks.
- [architecture.md](architecture.md) — request lifecycle from portal event
  to terminal state, reconciler bounded-tick mechanics, signed file/cover
  delivery internals, range pass-through, audience separation from the
  audiobook backend.

This plugin is admin-only — there is no end-user surface in the binary
itself. Every workflow below assumes operator access to the host's `/admin`
console and to the plugin process logs.

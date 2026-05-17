# BookWarehouse Ebook Setup, Debugging, And Flows

Plugin ID: `continuum.bookwarehouse-ebook`
Version documented: `0.1.0`

## Purpose

ebook backend connector for BookWarehouse/Calibre-backed catalog and request workflows.

## Runtime Dependencies

- Continuum plugin host
- Postgres schema for this plugin
- Reachable BookWarehouse API
- continuum.ebooks for the user-facing portal

## Setup Checklist

1. Create schema and configure database_url.
2. Configure base_url, api_key, default cover size, request profile, and auto-monitoring behavior.
3. Install continuum.ebooks and select this backend for catalog or request handling.
4. Test search/detail/download from the portal.
5. Submit a request and verify monitor/import behavior upstream.

## Configuration Reference

- `database_url`
- `base_url`
- `api_key`
- `default_cover_size`
- `request_quality_profile`
- `enable_auto_monitoring`

Use the plugin manifest/admin form as the source of truth for field validation and defaults. Keep database credentials scoped to the plugin schema unless a plugin explicitly needs read access to Continuum core tables.

## Exposed Routes

- `* /api/v1/* [authenticated]`

## Capabilities

- `http_routes.v1 (backend) - Calibre-backed ebook source: catalog, file streaming, request forwarding.`
- `event_consumer.v1 (request_handler) - Forwards ebook request_submitted events to Book Warehouse monitoring.`
- `ebook_backend.v1 (default) - Owned-library ebook source backed by an external Book Warehouse instance.`
- `scheduled_task.v1 (reconciler) - Polls upstream Book Warehouse for status changes on non-terminal requests.`

## Operational Flows

### Catalog/download

1. Ebooks portal calls this backend for search, detail, cover, and file operations.
2. The plugin proxies those calls to BookWarehouse and returns normalized ebook backend responses.

### Requests

1. Ebooks emits request_submitted for this provider.
2. The plugin forwards to BookWarehouse, stores upstream state, and reconciles until fulfilled/failed.

## How This Plugin Communicates

- Serves ebook_backend.v1 to continuum.ebooks.
- Consumes ebook request events when selected as a request provider.
- Publishes status events back to the Ebooks portal.

## Debugging Runbook

- Check BookWarehouse API credentials and base URL.
- If downloads fail, inspect upstream file availability and proxy logs.
- If auto monitoring is unexpected, review enable_auto_monitoring and request_quality_profile.
- Use plugin logs to correlate request IDs between Ebooks and BookWarehouse.

## Log And Health Checks

- Start with Continuum Admin -> Plugins and confirm the installation is enabled.
- Check the plugin process logs around startup for manifest loading, migration, and route registration.
- Check scheduled task logs when a workflow depends on polling or reconciliation.
- Confirm the plugin routes are reachable through Continuum using the access level shown above.
- For database-backed plugins, verify the configured role can connect, create/migrate tables in its schema, and read/write expected rows.

## Common Failure Patterns

- Wrong installation ID selected in a portal or router setting after reinstalling a plugin.
- Plugin database URL points at the public schema instead of the dedicated plugin schema.
- Reverse proxy forwards the SPA route but not `/api/*`, `/api/v1/*`, `/assets/*`, or provider-specific public routes.
- Network checks are run from the operator laptop instead of from the Continuum/plugin runtime network.
- Secrets are regenerated during restart, invalidating signed URLs, encrypted fields, or login state.

## Verification After Changes

1. Restart or reload the plugin installation.
2. Open the plugin route or admin page in Continuum.
3. Exercise the smallest workflow that crosses a plugin boundary.
4. Confirm both the source plugin and destination plugin record the same request/session/login identifier.
5. Leave the scheduled reconciler enough time to run, then confirm terminal state or a useful error.

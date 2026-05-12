# continuum-plugin-bookwarehouse-ebook

Continuum plugin: thin adapter exposing a Calibre-backed BookWarehouse
instance to the `continuum.ebooks` portal via the `ebook_backend.v1`
capability.

See `/opt/worktrees/continuum-rh/docs/superpowers/specs/2026-05-11-ebooks-portal-and-backends-design.md`.

## Build & test

```bash
go build ./cmd/continuum-plugin-bookwarehouse-ebook
go test ./...   # requires Postgres for store tests
```

## Operator runbook

### Postgres pre-flight

```sql
CREATE ROLE plugin_bookwarehouse_ebook WITH LOGIN PASSWORD '<chosen>';
CREATE SCHEMA bookwarehouse_ebook AUTHORIZATION plugin_bookwarehouse_ebook;
GRANT CONNECT ON DATABASE continuum TO plugin_bookwarehouse_ebook;
```

### Configuration (admin UI)

| Key | Required | Notes |
|-----|----------|-------|
| `database_url` | yes | `postgres://plugin_bookwarehouse_ebook:<pwd>@host/continuum?search_path=bookwarehouse_ebook` |
| `base_url` | yes | Calibre/BookWarehouse instance base URL. |
| `api_key` | yes | X-API-Key for the BookWarehouse instance. |
| `enable_auto_monitoring` | optional | Off by default; flips `features:[auto_monitoring]` in capabilities. |

### Capabilities exposed

* `http_routes.v1` — serves `/api/v1/{health,capabilities,catalog,catalog/{id},catalog/search,cover/{id}/{size},file/{id},requests,requests/{external_id},external_search}`
* `event_publisher.v1` — emits `request_acknowledged`, `request_status_changed`, `request_fulfilled`, `request_failed`
* `event_consumer.v1` — subscribes to `plugin.continuum.ebooks.request_submitted`
* `scheduled_task.v1` — `monitoring_reconciler` (1m) polls upstream for non-terminal requests

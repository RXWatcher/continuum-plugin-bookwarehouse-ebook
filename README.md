# continuum-plugin-bookwarehouse-ebook

Thin adapter exposing a Calibre-backed BookWarehouse instance to the [`continuum.ebooks`](../continuum-plugin-ebooks/) portal via the `ebook_backend.v1` capability. Maintains long-lived monitoring on requests (Sonarr-style: keep searching until found) when `enable_auto_monitoring` is on.

## Capabilities

| Capability | Notes |
|---|---|
| `ebook_backend.v1` (`default`) | Owned-library ebook source. |
| `http_routes.v1` (`backend`) | `/api/v1/{health,capabilities,catalog,catalog/{id},catalog/search,cover/{id}/{size},file/{id},requests,requests/{external_id},external_search}`. |
| `event_consumer.v1` (`request_handler`) | Subscribes to `plugin.continuum.ebooks.request_submitted`; forwards new requests to BookWarehouse monitoring. |
| `scheduled_task.v1` (`reconciler`) | Cron `*/1 * * * *`. Polls upstream for status changes on non-terminal requests. |

Emits to the bus: `request_acknowledged`, `request_status_changed`, `request_fulfilled`, `request_failed`.

## Configuration

| Key | Required | Description |
|---|---|---|
| `database_url` | yes | DSN for the `bookwarehouse_ebook` schema. |
| `base_url` | yes | BookWarehouse base URL, no trailing slash. |
| `api_key` | yes | `X-API-Key` for upstream calls. |
| `default_cover_size` | no | One of `small \| medium \| large \| original` (default `large`). |
| `request_quality_profile` | no | BookWarehouse-side quality tier for new requests. |
| `enable_auto_monitoring` | no | Flips the `auto_monitoring` feature flag in capabilities; off by default. |

## Provider Role

This plugin can act as both a presentation library source and a download
provider for the Ebooks portal:

- `ebook_roles`: `library_source`, `download_provider`
- `supports_catalog`: true
- `supports_requests`: true
- `supports_auto_monitoring`: controlled by `enable_auto_monitoring`

Use the Ebooks portal admin UI to decide which user-facing libraries point at
this source and whether requests should route here or to another download
provider.

## Dependencies

- Postgres role + `bookwarehouse_ebook` schema.
- An external BookWarehouse/Calibre instance.

## Install

```sql
CREATE ROLE plugin_bookwarehouse_ebook WITH LOGIN PASSWORD '<chosen>';
CREATE SCHEMA bookwarehouse_ebook AUTHORIZATION plugin_bookwarehouse_ebook;
GRANT CONNECT ON DATABASE continuum TO plugin_bookwarehouse_ebook;
```

Then select this plugin as the ebooks-portal backend from `/admin/settings`.

## Build & test

```bash
go build ./cmd/continuum-plugin-bookwarehouse-ebook
go test ./...    # requires Postgres for store tests
```

## Status

v0.1.0. Functional.

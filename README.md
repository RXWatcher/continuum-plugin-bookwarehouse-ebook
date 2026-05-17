# BookWarehouse Ebooks for Continuum

`continuum.bookwarehouse-ebook` connects the Continuum Ebooks portal to a
Calibre-backed BookWarehouse instance. It exposes owned-library catalog data,
cover art, file delivery, external search, and request monitoring through
Continuum's `ebook_backend.v1` capability.

Use this plugin when BookWarehouse owns your ebook library and Continuum should
provide the user portal, OPDS/Kobo/Kindle integrations, and request workflow.

## Detailed Operations Docs

- [Setup, debugging, and communication flows](docs/setup-debug-flows.md)

## Features

- Catalog, search, detail, cover, and ebook file endpoints for
  `continuum.ebooks`.
- External search support for request previews.
- Request forwarding from the Ebooks portal to BookWarehouse monitoring.
- Scheduled reconciliation of non-terminal request state.
- Optional auto-monitoring so BookWarehouse keeps searching until a requested
  title is found.
- No user-facing SPA; the Ebooks portal owns the UI and client protocols.

## Configuration

| Key | Required | Description |
|---|---|---|
| `database_url` | yes | Postgres DSN for the `bookwarehouse_ebook` schema. |
| `base_url` | yes | BookWarehouse base URL, no trailing slash. |
| `api_key` | yes | API key sent to BookWarehouse as `X-API-Key`. |
| `default_cover_size` | no | `small`, `medium`, `large`, or `original`. Defaults to `large`. |
| `request_quality_profile` | no | BookWarehouse-side quality tier for new requests. |
| `enable_auto_monitoring` | no | Enable Sonarr-style monitoring for new ebook requests. |

Example DSN:

```text
postgres://plugin_bookwarehouse_ebook:password@postgres:5432/continuum?search_path=bookwarehouse_ebook&sslmode=disable
```

## Database Setup

```sql
CREATE ROLE plugin_bookwarehouse_ebook WITH LOGIN PASSWORD '<chosen>';
CREATE SCHEMA bookwarehouse_ebook AUTHORIZATION plugin_bookwarehouse_ebook;
GRANT CONNECT ON DATABASE continuum TO plugin_bookwarehouse_ebook;
```

## Portal Integration

1. Install and configure `continuum.ebooks`.
2. Install this plugin and configure BookWarehouse connection settings.
3. Select `continuum.bookwarehouse-ebook` as an ebook backend or default request
   provider in the Ebooks admin settings.
4. Enable auto-monitoring only if the upstream BookWarehouse instance should
   continue searching after a request is submitted.

## Build And Test

```bash
make build
make test
```

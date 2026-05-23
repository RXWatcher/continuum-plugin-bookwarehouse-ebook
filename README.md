# BookWarehouse Ebooks for Silo

`silo.bookwarehouse-ebook` is the Silo ebook backend that fronts an external BookWarehouse (Calibre-style) instance. It serves the owned-library catalog, streams cover art and book files, forwards portal-originated requests into BookWarehouse monitoring, and reconciles those requests on a cron until they reach a terminal state.

Use this plugin when BookWarehouse owns the ebook library and `silo.ebooks` should present it as the user portal.

## Category

Lives under **Books/Ebooks**.

## Capabilities

| Type | ID | Purpose |
| --- | --- | --- |
| `http_routes.v1` | `backend` | Chi-based handler mounted at `/api/v1/*` for catalog, browse, cover, file, external search, and request snapshot endpoints; also serves an `/admin` operator dashboard. |
| `event_consumer.v1` | `request_handler` | Subscribes to `plugin.silo.ebooks.request_submitted` and forwards each event to BookWarehouse monitoring, recording an upstream `external_id` in the plugin's `forwarded_request` table. |
| `ebook_backend.v1` | `default` | Advertises this plugin to `silo.ebooks` as a `library_source` and `download_provider` with `supports_catalog`, `supports_requests`, and `supports_auto_monitoring`. Exposes a single synthetic library (id `1`). |
| `scheduled_task.v1` | `reconciler` | `*/1 * * * *` cron tick that polls BookWarehouse for non-terminal forwarded requests and republishes status changes. Each tick is bounded to 45s with a 10s per-row upstream timeout and uses an in-process mutex to drop overlapping invocations. |

## Dependencies

- Paired with the **[`silo-plugin-ebooks`](https://github.com/RXWatcher/silo-plugin-ebooks)** portal plugin, which owns the user-facing UI, OPDS/Kobo/Kindle surfaces, and the `plugin.silo.ebooks.request_submitted` event this backend consumes.
- Consumes a single host event: `plugin.silo.ebooks.request_submitted`. The handler ignores events not targeted at `silo.bookwarehouse-ebook` so multiple ebook backends can coexist.
- Alternates within the ecosystem: **[`silo-plugin-local-ebooks`](https://github.com/RXWatcher/silo-plugin-local-ebooks)** (filesystem-backed library/download provider) and **[`silo-plugin-ebook-requests`](https://github.com/RXWatcher/silo-plugin-ebook-requests)** (alternate request provider). Use whichever combination matches the deployment; this plugin can play both roles against a BookWarehouse instance.
- Requires an external **BookWarehouse** server reachable over HTTP(S) and an API key.

Host app: [`ContinuumApp/silo`](https://github.com/ContinuumApp/silo). SDK: [`ContinuumApp/continuum-plugin-sdk`](https://github.com/ContinuumApp/continuum-plugin-sdk).

## External services

- **BookWarehouse HTTP API** — typed client in `internal/bookwarehouse/`. Authenticates with `X-API-Key` (stripped on cross-host redirects), enforces a 30s default timeout, caps response bodies at 10 MiB, and exposes streaming variants (`GetStream`, `GetStreamWithRange`) so cover and file routes can pass through bytes and `Range` requests without buffering. The client is reconfigurable at runtime so admin saves take effect without a restart.
- **Postgres** — a dedicated `bookwarehouse_ebook` schema for plugin-owned state: the persisted app config, forwarded request tracking, and reconciler bookkeeping. The pool is sized with a `MaxConns` floor of 16 to keep the catalog API, consumer, and reconciler from starving one another.

## Configuration

Only `database_url` is host-managed via `global_config_schema`; the remaining keys live in the plugin's own `app_config` row and are edited through the `/admin` UI (which writes them via `PATCH /api/v1/admin/config`).

| Key | Required | Description |
| --- | --- | --- |
| `database_url` | yes | Postgres DSN for the `bookwarehouse_ebook` schema. Set via host global config; never edited from the plugin UI. |
| `base_url` | yes | BookWarehouse base URL (must be `http`/`https`, no trailing slash). Validated on every `Configure`. |
| `api_key` | yes | API key forwarded to BookWarehouse as `X-API-Key`. Redacted in logs and never returned from `GET /api/v1/admin/config`. |
| `default_cover_size` | no | `thumbnail`, `medium`, or `original` (legacy aliases `small` and `large` are normalized). Falls back to `medium`. |
| `request_quality_profile` | no | Quality profile name passed to BookWarehouse when forwarding new requests. |
| `enable_auto_monitoring` | no | When true, forwarded requests ask BookWarehouse to keep searching until a match is imported. |
| `stream_signing_secret` | yes (for cover/file routes) | HMAC key shared with the ebooks portal for verifying signed media URLs. Base64-decoded if possible, otherwise treated as raw bytes. |

Example DSN:

```text
postgres://plugin_bookwarehouse_ebook:password@postgres:5432/silo?search_path=bookwarehouse_ebook&sslmode=disable
```

Database role and schema:

```sql
CREATE ROLE plugin_bookwarehouse_ebook WITH LOGIN PASSWORD '<chosen>';
CREATE SCHEMA bookwarehouse_ebook AUTHORIZATION plugin_bookwarehouse_ebook;
GRANT CONNECT ON DATABASE silo TO plugin_bookwarehouse_ebook;
```

## Signed file delivery

The manifest declares `/api/v1/cover/*` and `/api/v1/file/*` as `public` routes so browsers can load `<img>` and `<a download>` URLs without sending an `Authorization` header. Auth is enforced inside the handlers by `internal/tokens`:

- The portal mints a short-TTL HS256 JWT bound to `book_id`, `file_idx`, `sub` (user id), `exp`, and `aud = "ebook_backend"`. Cover tokens use the sentinel `file_idx = -1`; ebook files use `file_idx = 0` (ebooks are single-file per format).
- `tokens.Verify` requires HS256, a non-empty signing secret, a matching audience, an `exp` claim, and exact equality on `book_id` / `file_idx`. Missing tokens return `401`; an unconfigured secret returns `503`.
- The audience is distinct from the audiobook backend's so tokens cannot cross media types, and the per-resource claim binding prevents a leaked token from being reused against another book.

The cover handler stream-proxies the upstream `/api/v1/books/{id}/cover/{size}` response (`large` is remapped to `original`); the file handler stream-proxies `/api/v1/books/{id}/download` and forwards the client's `Range` header so reader apps and Kindle resume support produce a real `206 Partial Content`.

## Detailed docs

- [Setup, debugging, and communication flows](docs/setup-debug-flows.md)

## Build and release

```bash
make build
make test
```

CI builds linux-amd64 binaries on push to main via the reusable workflow in [RXWatcher/silo-plugin-repository](https://github.com/RXWatcher/silo-plugin-repository) and publishes them to the catalog at [`./binaries/`](https://github.com/RXWatcher/silo-plugin-repository/tree/main/binaries).

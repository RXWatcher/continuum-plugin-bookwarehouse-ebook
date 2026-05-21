# Operations

Operator-facing workflows. The README covers what each config key means; this
page is about the procedures around them.

## Install / upgrade lifecycle

1. Install the binary via Continuum's plugin manager. The host runs the
   `Runtime.GetManifest` RPC, picks up the embedded `manifest.json`, and
   registers all four capabilities (HTTP routes, event consumer, ebook
   backend, scheduled task).
2. Set `database_url` via the host's plugin config form. This is the only
   key in `global_config_schema`. Until it is set, `Configure` returns
   early without touching the pool, the upstream client, or the schedulers;
   `HttpRoutes.Handle` answers `503 not_ready` for every request.
3. Open `/admin` (linked from **Admin → Plugins → BookWarehouse Ebook** via
   the manifest's `navigable: true` route). The `Config` tab writes
   `base_url`, `api_key`, `default_cover_size`, `request_quality_profile`,
   and `enable_auto_monitoring` into the plugin's own `app_config` row
   (singleton, JSONB blob in the `bookwarehouse_ebook` schema).
4. The `stream_signing_secret` is supplied by the host via the same
   `Configure` call (it is shared with the `continuum.ebooks` portal so
   the two ends agree on signed-URL verification). It is NOT user-editable
   from `/admin`.
5. On every restart `Configure` runs again with the host-supplied keys.
   `ImportLegacyAppConfig` only promotes the host's snapshot into the
   `app_config` row if the row still matches `DefaultAppConfig()`; once
   the operator has saved anything via `/admin`, the row wins.

`PATCH /api/v1/admin/config` (the `/admin` save handler) is hot:
- Persists the new JSON blob.
- Calls `BookwarehouseClient.Reconfigure(base_url, api_key)` and
  `SetDefaultCoverSize` — both are atomic in-place updates so in-flight
  catalog requests don't observe a half-applied client. No plugin restart
  is needed for the new BookWarehouse URL/key to take effect.
- Leaves `database_url` untouched. To change the DSN, edit the host plugin
  config and restart the plugin (the pool is built once in `Configure` and
  swapped wholesale; `app_config` strips `database_url` on every save).
- Blanking the API key in the form leaves the current key in place
  (the GET handler redacts it to `""`, so the form preserves "leave blank
  to keep current" semantics).

## The `/admin` dashboard

The dashboard is a single static HTML page with embedded JS, served by
`internal/server.Server.handleAdminHome`. It calls three JSON endpoints:

| Endpoint | Surfaces |
| --- | --- |
| `GET /api/v1/admin/diagnostics` | the Readiness, Reconcile, and status-strip cells |
| `GET /api/v1/admin/config` | populates the Config form (API key always blank) |
| `GET /api/v1/admin/test-search?q=…` | the Browser tab (proxies `ExternalSearch`) |

### Status-strip cells

| Cell | Source field | "OK" when |
| --- | --- | --- |
| `DB` | `database.ok` | `pgxpool.Ping` succeeds within the 5s `handleDiagnostics` budget |
| `Configured` | `configured` | `Config.ProviderConfigured()` — base_url **and** api_key are set |
| `BookWarehouse` | `upstream.ok` | `Client.Ping` (tries `/api/v1/health` then `/health`) returns nil |
| `Auto monitor` | `auto_monitoring_enabled` | reflects the saved flag; not a health signal |
| `Failed reqs` | `requests.failed == 0` | no rows with `status = 'failed'` |

The `Readiness` cards expose the same data plus `request_quality_profile`
and `base_url` so you can confirm what was actually saved (the field is
echoed back from `app_config`, not the host snapshot). The `Reconcile` tab
shows the row counts straight off `forwarded_request`:

- `Total` — all rows.
- `Active` — `status NOT IN ('imported','failed')`.
- `Failed`, `Imported` — terminal states.
- `With errors` — `error_text` is non-empty. This is sticky on
  `UpsertForwardedRequest` (COALESCE keeps the old value) and only
  cleared by `MarkPolled` on a successful poll, so a non-zero value here
  is a real "something failed at least once" signal.
- `Unsubmitted` — `external_id IS NULL/'' AND status NOT IN
  ('imported','failed')`. Non-zero means the consumer accepted an event
  but never got an `external_id` from `monitoring/add` (see the consumer
  flow in [architecture.md](architecture.md)).

If `/admin/diagnostics` itself returns an error, the strip renders a
single red `Diagnostics` cell with the error string in the tooltip.

## Configuration knobs you actually tune

### `request_quality_profile`

Optional. When set, this string is forwarded to BookWarehouse as the
`quality_profile` field in `MonitoringRequest`. BookWarehouse uses it to
filter ingest sources. Empty means "upstream default profile". Mistyped
profile names typically surface as an upstream 4xx error on
`monitoring/add` — visible as a failed event in plugin logs and as a row
with `status='failed'` and a populated `error_text` in
`forwarded_request`.

### `enable_auto_monitoring`

When **true**, the consumer still forwards each request with whatever
`auto_monitor` flag the portal event carried. The flag in `app_config` is
mostly an operator-visible default; the actual decision per request is
made by the portal. The reconciler does not look at this value.

### `default_cover_size`

Stored as one of `thumbnail`, `medium`, `original` (legacy `small` and
`large` are normalized). The value is read atomically by in-flight cover
URL builders, so a save takes effect immediately for new catalog
responses. Existing cover URLs already minted by the portal still resolve
because the size in the URL path overrides the default at request time;
`Cover()` always uses the URL parameter, not this default.

### `database_url`

Host-managed only. The DSN should target the dedicated `bookwarehouse_ebook`
schema:

```
postgres://plugin_bookwarehouse_ebook:<pw>@host:5432/continuum
  ?search_path=bookwarehouse_ebook&sslmode=disable
```

Pool sizing: the plugin enforces a floor of `MaxConns=16`. The pgx default
scales with `GOMAXPROCS` and can be as low as 4, which is not enough for
the catalog API + reconciler + consumer to coexist without one of them
blocking. To raise the cap above 16 use `?pool_max_conns=N` on the DSN —
operators never need to drop below the floor.

### Postgres role and schema

```sql
CREATE ROLE plugin_bookwarehouse_ebook WITH LOGIN PASSWORD '<chosen>';
CREATE SCHEMA bookwarehouse_ebook AUTHORIZATION plugin_bookwarehouse_ebook;
GRANT CONNECT ON DATABASE continuum TO plugin_bookwarehouse_ebook;
```

Migrations live in `internal/migrate/files`. The runner is called from
`Configure` before the store is wired up. Both `forwarded_request` and
`app_config` are created idempotently; rerunning `Configure` against a
populated schema is safe.

## Reconciler scheduling

The manifest cron is `*/1 * * * *`. The reconciler:

- Runs through the SDK's `ScheduledTask` capability, so failure is recorded
  by the host scheduler with the per-tick error returned from `Tick`.
- Holds an in-process `sync.Mutex` (`tickMu.TryLock`) — overlapping
  invocations from clock skew or SDK retries are silently dropped, not
  queued. There is no global mutex across plugin processes; deploying two
  instances of this plugin against the same DB is unsupported.
- Bounds each tick at 45s (`tickTimeout`) and each upstream lookup at 10s
  (`perRowTimeout`). When the per-tick budget is exhausted the loop
  breaks rather than letting every remaining row record a
  `context deadline exceeded` error.
- Processes up to 200 non-terminal rows per tick, oldest `last_polled`
  first.

If the reconciler is stuck (e.g. all rows always have `last_polled` in the
past), confirm: (a) upstream `Ping` is succeeding in `/admin`, (b) the
scheduler is firing — host logs show the cron firing on the minute, (c)
no row holds an `external_id` of `""` (those are skipped forever; see
the consumer / "Unsubmitted" notes above).

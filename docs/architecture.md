# Architecture

How the plugin's internals fit together at runtime. Useful when the
debugging runbook isn't enough and you need to read code.

## Process layout

`cmd/continuum-plugin-bookwarehouse-ebook/main.go` is the entry point.
It builds a plugin process with four SDK capability servers:

```
sdkruntime.Serve
  ├── Runtime        — internal/runtime.Server (GetManifest, Configure)
  ├── HttpRoutes     — internal/httproutes.Server (adapts net/http handler)
  ├── EventConsumer  — internal/consumer.Handler   (subscribes via manifest)
  └── ScheduledTask  — internal/scheduler.Server   (cron from manifest)
```

The HTTP handler, store, upstream client, consumer deps, and reconciler are
all rebuilt every time `Configure` runs. The capability servers themselves
are constructed once and read their downstream state through `atomic.Pointer`
loads:

- `httproutes.Server.SetHandler(http.Handler)` swaps the active handler.
  Until set, every RPC returns `503 not_ready`. Set to a "no-op" handler
  before `Configure` so the manifest's `/admin` route is registered with
  the host but answers `503` for everything until the DB is wired.
- `consumer.Handler` resolves `*Deps` per-event via the `depsFn` closure.
  When `Deps` is nil the handler returns an error (nack) so the host
  redelivers once `Configure` has run — accidentally acking and dropping
  the event would lose the request permanently.
- `scheduler.Server` resolves `*reconciler.Reconciler` per-tick the same
  way.

The whole reconfigure-on-`Configure` design exists because the SDK runs
capability RPCs before `Configure` completes (so the host can probe the
manifest, route topology, etc.), and operator config saves via
`PATCH /admin/config` need to take effect without a process restart.

## Catalog / download request lifecycle

```
portal ─► host plugin proxy ─► http_routes.v1 (RPC) ─► httproutes.Server.Handle
                                                       │ reconstruct net/http.Request
                                                       ▼
                                       chi router (internal/server.Server)
                                                       │
                ┌──────────────────────────────────────┴────────────────┐
                ▼                                                       ▼
           /api/v1/catalog* + /browse/* +                  /api/v1/cover/* + /file/*
           /external_search + /requests/:id                (token-gated, see below)
                │                                                       │
                ▼                                                       ▼
        bookwarehouse.Client.Get / PostJSON                bookwarehouse.Client.GetStreamWithRange
        (10 MiB body cap, X-API-Key,                       (no body cap, Range pass-through, ditto)
         30s default timeout)                              X-API-Key
                │                                                       │
                ▼                                                       ▼
                          External BookWarehouse server
```

### Body / size caps

- JSON paths (catalog list, detail, browse, external search, monitoring
  add/get) read at most 10 MiB. Larger bodies fail with `read body: …` —
  defends against a runaway or hostile upstream.
- File / cover paths stream upstream → client via `io.Copy`. No body cap;
  no buffering. The handler copies a fixed allowlist of upstream headers
  (`Content-Type`, `Content-Length`, `Content-Range`, `Accept-Ranges`,
  `Content-Disposition`, etc.) and forwards the upstream status code,
  which is how `206 Partial Content` and `416 Range Not Satisfiable`
  reach the client unchanged.

### Catalog list dedup and limit clamp

- `limit` is clamped to `[1, 100]` by `clampLimit`. Unbounded values were
  driving giant upstream pages that overran the 10 MiB body cap.
- `ListBooksDeduped` collapses visual duplicates (same title+author
  across multiple editions stored as separate Calibre rows) so the
  portal's infinite scroll observer doesn't loop on an all-duplicate
  page.
- `library_id` mismatch (anything other than the synthetic library id
  `1`) returns an empty page WITHOUT calling upstream. This isolates this
  plugin's books from foreign library ids when multiple ebook backends
  are installed.
- `genre` is matched against the upstream **slug**, not the row id.
  `BrowseGenres` remaps `id → slug` in the response so the slug is what
  downstream filters echo back.

## Signed file/cover delivery

The portal mints a short-TTL HS256 JWT per resource and the byte routes
verify it. Browsers won't send an `Authorization` header on `<img>` or
`<a download>`, so the manifest declares these two paths as
`access: public` and the verifier inside the handlers is the sole gate.

Claims (every one is checked):

| Claim | Required value |
| --- | --- |
| `alg` (header) | `HS256` — anything else (including `none`) is rejected at parse time |
| `aud` | `ebook_backend` — distinct from the audiobook backend, so a leaked audiobook token cannot be replayed against an ebook |
| `exp` | required; expired tokens reject |
| `book_id` | exact equality with the URL path `book_id` |
| `file_idx` | `-1` for `/cover/*`, `0` for `/file/*` (ebooks are single-file per format) |
| `sub` | non-empty (the requesting user id; not otherwise checked) |

Failure modes:

- Missing or empty `?token=` → `401` (`ErrTokenMissing`).
- Unconfigured `stream_signing_secret` → `503` (`ErrSecretUnconfigured`).
  This is a deliberate distinction: `503` says "the operator hasn't
  finished setup" rather than "you sent a bad token", so a portal that
  retries on 5xx but not 4xx will keep waiting rather than burning tokens
  on auth failures.
- Anything else (signature mismatch, audience mismatch, claim mismatch,
  exp expired) → `401` with `{"error":"verify: …"}`.

Secret decoding: `decodeSecret` tries `base64.StdEncoding`, then
`base64.RawStdEncoding`, then falls back to raw bytes. Both ends must
agree on which form is used; the simplest pattern is a value that is
clearly NOT valid base64 so both ends fall through to the raw bytes
branch.

The cover handler maps the URL's `large` size to `original` before
proxying upstream (BookWarehouse's API uses `original`, but legacy
portal URLs still ship `large`).

## Forwarded-request lifecycle (portal → reconciled)

```
portal           plugin consumer                    BookWarehouse           plugin reconciler
─────            ──────────────                    ──────────────           ─────────────────
emit                                                                       (cron */1)
request_submitted ─►
                 verify target == us
                 upsert: status='submitted'
                 POST /monitoring/add ──────────►
                                              ◄──── { id, status }
                 upsert: status='acknowledged',
                        external_id=resp.id
                 publish request_acknowledged ─►
                                                                            list non-terminal
                                                                            for each row:
                                                                              GET /monitoring/{id}
                                                                              translate status
                                                                              MarkPolled or publish
                                                                                request_status_changed
                                                                                request_fulfilled
                                                                                request_failed
```

Key properties:

1. **At-least-once redelivery is safe.** `UpsertForwardedRequest` is
   conflict-keyed on `request_id`. The `ON CONFLICT` clause has a
   terminal guard: once `status IN ('imported','failed')`, no incoming
   event can move it off the terminal value. The same guard appears in
   `MarkPolled`.
2. **Persistence happens before and after every upstream call.** If the
   pre-call upsert fails the consumer nacks; if the post-call upsert
   fails the consumer nacks too — losing the `external_id` would make
   the row invisible to the reconciler forever, because `Tick` skips
   rows with `ExternalID == ""`.
3. **Per-poll error state is sticky-but-recoverable.** A failed upstream
   poll writes `error_text` via `UpsertForwardedRequest` (COALESCE means
   subsequent success-path upserts can't clear it). The next successful
   `GetMonitoring` calls `MarkPolled`, which explicitly nulls
   `error_text`. So the `with_errors` count in diagnostics reflects "has
   an outstanding failure as of the most recent attempt", not "ever had
   a failure".
4. **Unknown upstream status holds the row.** `translateStatus` returns
   `""` for anything it doesn't recognise; the reconciler treats that
   as "no transition" — `MarkPolled` stamps `last_polled`, no event is
   published, and the row's existing status is preserved. The prior
   implementation defaulted to `acknowledged`, which regressed
   `downloading → acknowledged` on every poll and spammed the portal
   with phantom status changes.

### Reconciler tick budget

`tickTimeout = 45s`, `perRowTimeout = 10s`, cron `*/1 * * * *`,
`ListNonTerminal(ctx, 200)` per tick. Once `ctx.Err()` trips the loop
breaks rather than letting every remaining row record a
`context deadline exceeded` in `error_text`.

`tickMu.TryLock` drops overlapping ticks. The mutex is in-process only,
so running two instances of this plugin against the same `forwarded_request`
table is unsupported (both reconcilers would poll the same rows and emit
duplicate events).

### Status translation table

| Upstream | Portal-facing |
| --- | --- |
| `queued`, `monitoring` | `searching` |
| `found` | `found` |
| `downloading`, `grabbing` | `downloading` |
| `imported`, `completed` | `imported` (terminal) |
| `failed`, `error` | `failed` (terminal) |
| anything else | (no transition) |

Terminal states are the same set the SQL queries treat as terminal
(`status IN ('imported','failed')`), which is the same set
`ListNonTerminal` filters out and the terminal guard in
`UpsertForwardedRequest` / `MarkPolled` protects.

## Schema

Two tables in the `bookwarehouse_ebook` schema:

```sql
forwarded_request (
  request_id    TEXT PRIMARY KEY,
  external_id   TEXT,
  status        TEXT NOT NULL,   -- see lifecycle above
  last_polled   TIMESTAMPTZ,
  error_text    TEXT,
  auto_monitor  BOOL NOT NULL DEFAULT false,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX forwarded_request_status_polled_idx
  ON forwarded_request (status, last_polled);

app_config (
  id         INT PRIMARY KEY DEFAULT 1,
  data       JSONB NOT NULL DEFAULT '{}',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT app_config_singleton CHECK (id = 1)
);
```

`app_config` is a singleton row (CHECK constraint enforces `id=1`).
`Configure`'s `ImportLegacyAppConfig` step copies the host's snapshot
into this row exactly once — when the row still matches
`DefaultAppConfig()`. After that, `/admin → Config` saves are
authoritative.

`database_url` is intentionally NOT written to `app_config.data`;
`UpdateAppConfig` strips it before encoding. The host snapshot remains
the only source for the DSN.

## Logging and secret redaction

`runtime.Config` implements `slog.LogValuer` and `fmt.Stringer` with
masking for `database_url` (DSN embeds the DB password),
`api_key`, and `stream_signing_secret`. `slog.Any("cfg", cfg)` and
`%v` formatting both go through `LogValue`. New fields added to
`Config` that carry secrets must be added to `LogValue` explicitly —
slog's default reflection won't know to mask them.

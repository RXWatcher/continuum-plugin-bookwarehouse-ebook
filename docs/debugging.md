# Debugging runbook

Symptom-keyed. Each entry starts from a thing the operator sees and works
back to the cause. Pair this with [architecture.md](architecture.md) for
the lifecycle diagrams these symptoms map onto.

## Upstream BookWarehouse connectivity

### `/admin` strip shows `BookWarehouse` red

The diagnostics handler calls `Client.Ping`, which tries
`GET /api/v1/health` then falls back to `GET /health`. The strip's tooltip
contains the error string from whichever call failed last. Common causes:

- **Wrong `base_url`.** `Configure` validates the URL on every save —
  invalid scheme/host returns `base_url must be a valid http(s) URL` from
  `PATCH /admin/config` and the new value is never applied. If the
  upstream is still red after a successful save, the URL parsed fine but
  the host isn't reachable from the plugin runtime network.
- **Wrong `api_key`.** The strip will show `upstream NNN: …` with the
  upstream's response body truncated to 512 bytes. A `401` body that
  mentions `X-API-Key` is the dead-giveaway sign. Re-save the key via
  `/admin → Config` — the GET handler always returns `api_key: ""` so the
  form's "leave blank to keep current" semantics work.
- **Cross-host redirect stripping the key.** The HTTP client's
  `CheckRedirect` deliberately deletes `X-API-Key` whenever the redirect
  target's host differs from the original. If BookWarehouse is fronted by
  a reverse proxy that redirects to a different hostname (e.g. canonical
  domain redirect), every request will land at the redirect target
  unauthenticated. Either point `base_url` at the canonical host directly
  or fix the upstream redirect to be same-host.
- **Body too big.** Every JSON response is capped at 10 MiB
  (`maxResponseBytes`). A runaway upstream body shows up as `read body`
  errors in the catalog or reconciler logs, not in Ping (Ping returns
  bytes well under the cap unless the upstream is hostile).

### `request_quality_profile` errors on submission

`monitoring/add` rejects an unknown profile with a `4xx`. The plugin
records this in `forwarded_request.error_text`, publishes
`request_failed`, and the row is terminal. The truncated upstream body in
the error text is the operator-actionable detail. Update the profile via
`/admin → Config` and resubmit from the portal — the previous failed row
stays terminal (intentional: the terminal guard in `UpsertForwardedRequest`
prevents at-least-once event replay from resurrecting a failed request).

## Signed file/cover delivery

### `GET /api/v1/cover/*` or `/file/*` returns 503

The handler returned `ErrSecretUnconfigured`. The plugin has no
`stream_signing_secret` set. This secret is supplied by the host via
`Configure`, not from `/admin`. Check:

- The host's plugin config form has `stream_signing_secret` set and
  matches the portal's `media_signing_secret`.
- The plugin has been restarted/configured since the secret was added —
  the handler reads the value captured in `server.Deps.Config` at the
  time `srv := server.New(...)` was called, which is on every `Configure`.
- The secret is base64-decodable if you provided it as base64; the
  verifier tries `StdEncoding`, then `RawStdEncoding`, then falls back to
  raw bytes. If the portal mints with the decoded bytes but the plugin
  treats it as raw (or vice versa), signatures will never match. The
  safest pattern is to use a value that is NOT valid base64 (e.g. an
  obvious hex blob with non-base64 chars), so both ends treat it as raw.

### Cover/file URLs return 401

The token verifier failed for a reason other than missing secret. Causes
in order of likelihood:

- **Audience mismatch.** Cover/file tokens must carry
  `aud = "ebook_backend"`. The audiobook backend uses a distinct audience
  (`audiobook_backend` in that plugin) — if the portal's URL minter
  accidentally signs ebook URLs with the audiobook audience, every
  request hits 401 here. Inspect the JWT payload (the token in
  `?token=…`) and confirm `aud`.
- **Token bound to the wrong `book_id` or `file_idx`.** The verifier
  requires exact equality between the URL path id and the token's
  `book_id` claim, and between the URL's resource (`-1` for covers, `0`
  for files) and the token's `file_idx`. Browsers caching an old token
  against a new path will hit this.
- **`exp` missing or in the past.** The verifier requires `exp` (it is
  configured with `jwt.WithExpirationRequired()`). Portal-side TTLs are
  short by design; a clock-skewed plugin host can reject a freshly minted
  token. Confirm host clock is NTP-synced.
- **Algorithm not HS256.** The keyfunc rejects everything other than
  HS256, including `alg: none`. Surfaced as `verify: unexpected signing
  method: …`. This means the token was minted by a non-portal signer.

The `401` body is JSON: `{"error":"verify: …"}` — the exact verifier
error is in the body, useful when correlating with portal logs.

### Reader apps (Kobo / Kindle) re-download or fail to resume

The `File` handler forwards the client's `Range` header upstream via
`Client.GetStreamWithRange` and copies back `Content-Range`,
`Accept-Ranges`, `Content-Length`, and `Content-Disposition`. For
resume/seek to work end-to-end:

- BookWarehouse's `/api/v1/books/{id}/download` must serve `206 Partial
  Content` for `Range` requests. If upstream only ever returns `200`,
  resume can't work — there is no place for the plugin to fabricate
  ranges. Test with `curl -H 'Range: bytes=0-1023' …` against upstream
  directly.
- The reverse proxy in front of the host must not strip or rewrite the
  `Range` request header or the `Content-Range`/`Accept-Ranges` response
  headers. Some proxies disable byte ranges when buffering responses.
- A `416 Range Not Satisfiable` from upstream is returned to the client
  verbatim (the client passed `GetStreamWithRange` doesn't treat 416 as
  an error). Repeated `416`s indicate the client is asking for a range
  past EOF, usually because cached length metadata is stale.

## Forwarded-request lifecycle problems

### Row stuck at `status='submitted'`, `external_id=''`

The portal event was accepted but `BW.AddMonitoring` failed before we
recorded an `external_id`. Two distinct cases:

- `error_text` is populated and `status='failed'` — already terminal. The
  consumer published `request_failed`. The row will not be retried. (The
  portal can resubmit, which produces a new `request_id`.)
- `error_text` is empty and `status='submitted'` — the consumer crashed,
  the host nacked the event, and the host has not redelivered yet. This
  resolves on the next redelivery (the upsert with the same `request_id`
  is idempotent because of the terminal guard).

A row stuck at `status='acknowledged'` without `external_id` is **not
possible** in normal flow — `acknowledged` is only written together with
`resp.ID`. If you see this, it indicates either manual SQL or an old
build before the consumer was hardened.

### Row stuck at `status='searching'` or `'downloading'`

Confirm:

1. `last_polled` is recent (within a couple of minutes). If not, the
   reconciler isn't running or isn't picking up this row. Check the host
   scheduler for the `reconciler` task and the plugin logs for
   `Tick` errors.
2. `error_text` is empty. A populated `error_text` means the upstream
   `GetMonitoring` failed (network error, 5xx, malformed response — the
   monitoring decoder rejects `{}` and `null` responses to avoid silently
   masking a deleted upstream request). Each failed poll re-stamps
   `error_text` until a successful poll clears it.
3. Upstream status is what we expect. `curl` against
   `${base_url}/api/v1/monitoring/${external_id}` with `X-API-Key` set
   — that's exactly what the reconciler is doing. Status strings the
   plugin understands are listed in `translateStatus`:
   `queued`/`monitoring` → `searching`, `found`, `downloading`/`grabbing`
   → `downloading`, `imported`/`completed` → `imported`, `failed`/`error`
   → `failed`. Anything else holds the current status with no transition
   event (the previous "default to acknowledged" behaviour was a bug that
   regressed `downloading` → `acknowledged` on each tick).

### Reconciler appears to skip rows

`ListNonTerminal` skips nothing — but `Tick` skips a row when
`row.ExternalID == ""`. The `Unsubmitted` count in `/admin → Reconcile`
is exactly this set; if it is non-zero, those rows will never be polled
until the consumer succeeds in submitting them upstream. They are
effectively orphaned and need either a portal resubmit (new `request_id`)
or operator cleanup.

### Reconciler tick logs `context deadline exceeded`

The per-row timeout is 10s and the per-tick timeout is 45s. If many rows
in the same tick log `deadline exceeded`, upstream is unhealthy and tick
processing was cut short before the loop got to them. If only a couple of
rows log it per tick, the upstream is responding but slowly for those
specific monitoring ids. The first DB-write error (if any) is surfaced as
the tick's return error; per-upstream errors are recorded in `error_text`
on the row and do not fail the tick.

## Auto monitoring

`enable_auto_monitoring` is the operator's stored default; the actual
per-event flag comes from the portal payload (`auto_monitor`). If
operators report "auto-monitoring isn't enabled even though I set it":

- Confirm `/admin → Config` shows `auto_monitor: true` after save.
- Confirm the portal is sending `auto_monitor: true` in the event
  payload. The consumer doesn't override — it forwards whatever the
  portal sent. The portal plugin's own admin page controls this.
- Confirm BookWarehouse's monitoring entry actually has the flag — the
  upstream `GET /api/v1/monitoring/{id}` returns the live record; the
  consumer logs the request payload it built (`title`, `authors`,
  `auto_monitor`) on every event.

## Capability surface gotchas

- The plugin proxy declares `/api/v1/file/*` and `/api/v1/cover/*` as
  `access: public`. Browsers don't send `Authorization` on `<img>` or
  `<a download>`. The token verifier inside the handlers is the only
  auth gate for these paths. Any reverse-proxy rule that requires
  auth on `/api/v1/*` must explicitly exclude these two paths or
  signed-URL delivery breaks.
- `/admin` and `/admin/*` are `access: admin` — they require an admin
  session token, surfaced via `Authorization: Bearer <token>` and via the
  `?token=` query string the dashboard captures into JS `hostToken` and
  echoes back on every fetch. If the dashboard 401s on every endpoint
  but `curl -H Authorization: ...` works, the operator opened
  `/admin` without going through Continuum's auth.
- The plugin ignores `plugin.continuum.ebooks.request_submitted` events
  that target a different `provider_plugin_id`. Multiple ebook backends
  can coexist; "no request was forwarded" usually means the portal
  routed the request to a different provider.

# Gate Worker (Cloudflare Workers + D1)

The gate scanner page and its API. It keeps working when the Pi is down: it holds a copy of the active
permits and decides every scan itself. The Pi is the source of truth for permits; this Worker is the source
of truth for gate events. Rules: `../plan/business-rules-gate-jobs.md`.

Moved here from `legacy/entry-permit-old/gate` in slice 5a. The Pi side (`gatesync`) is slice 5b.

## Run the tests

```bash
cd gate-worker
npm test        # node --test, no install needed; uses node:sqlite as a stand-in for D1
```

The tests run the real Worker code and the real `schema.sql` against an in-memory SQLite database
(`test/d1shim.js`). They do not need Cloudflare.

## Deploy

```bash
npm install
npx wrangler login
npx wrangler secret put SYNC_SECRET     # the same value as GATE_SHARED_SECRET on the Pi
npm run d1:schema                       # idempotent; adds officer_attempts on first run
npm run deploy
```

`SYNC_SECRET` must be set: a Worker without it refuses every sync call.

## Scanner API (public)

| Route | What |
|---|---|
| `GET /api/lookup?lep=` | Can this number enter or leave now? Same decision function as `/api/action`. |
| `POST /api/action` | `{lep, action: checkin\|checkout\|force, gateName, officerName, officerCode, reason}` |
| `GET /api/references` | Gate names, officer **names**, check-out reasons. Never a PIN or verifier. |
| `POST /api/officer/login` | `{name, code}`. Checks the PIN on the server. 5 wrong tries lock that name for 15 minutes. |

Officers are stored as `pbkdf2-sha256$<iterations>$<salt hex>$<key hex>` (made by `internal/gateofficer`
in the Go app, at most 100,000 iterations because Workers allow no more). A plain-text code left over
from the old system still works during the cut-over (compared without case) and is replaced by the first push.

## Sync API (Pi only)

Every call needs the secret, as `Authorization: Bearer <secret>` or, during the cut-over, as `secret` in the JSON body.

| Route | What |
|---|---|
| `POST /api/sync` | Push `permits`, `tripTickets`, `references {officers, gates, reasons, completingReasons, settings}`. Chunks of 40 statements, each chunk atomic. An **empty** officer, gate, reason or completing-reason list is skipped (the old copy stays) and named in `skipped`. |
| `POST /api/sync/pull-gatelog` / `ack-gatelog` | Up to 100 rows (max 200) not yet acked; ack by `ids`. |
| `POST /api/sync/pull-gatestate` / `ack-gatestate` | Same, ack by `leps`. |
| `POST /api/sync/pull-tickets` / `ack-tickets` | Same, ack by `ticketIds`. |
| `POST /api/permit/deactivate` | `{status, leps}`. A worker still inside stays `INSIDE` so check-out remains possible. |
| `POST /api/sync/reset-sync-flags` | Recovery: acked rows are pulled again. |

Pull never marks a row, so a lost ack just means the row comes back: the Pi must apply every row idempotently.
The column is still called `synced_to_sheet` (it is the old name; renaming would need a D1 migration).

## What changed from the old Worker

1. One decision function (`src/rules.js`) for lookup and action, so a scan page left open can no longer check in an expired worker.
2. A check-out is fully validated first, then written in one batch (ticket, state, permit, log). The old code closed the ticket before refusing the check-out.
3. A ticket is completed by a reason in the **completing reasons** list (default *Sesuai jadwal keluar*, *Selesai awal*), not by a phrase no check-out used.
4. The Jakarta calendar day everywhere (the old code used the server's UTC day between 00:00 and 07:00 Jakarta).
5. The duplicate-scan window is real time (default 5 s, setting `gate_duplicate_scan_seconds`), not a fixed bucket.
6. The gate follows the **LEP valid-until** (`permit_valid_until`), not the raw MCU date.
7. Officer PINs are checked on the server against a verifier; `/api/references` no longer lists them.
8. `/api/ticket/submit` and `/api/ticket/info` are removed; tickets reach the gate only through the sync.
9. The secret is also accepted as a Bearer header; no secret configured means every sync call is refused.
10. A worker already inside is always let through to check-out, even after the permit was expired or resigned
    (the old lookup refused them, so they could not be checked out).

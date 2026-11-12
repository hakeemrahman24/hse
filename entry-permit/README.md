# entry-permit

> **Vibe-coded project.** Review it before relying on it.

Work entry permits (WEP/LEP), waivers, trip tickets and gate sync, migrated from
the Google Apps Script system. One Go binary, MariaDB in production, on the
Orange Pi. Plan and rules: `plan/entry-permit-plan.md`.

## Run locally

```bash
go run ./cmd/server          # SQLite at data/entry_permit.db, port 8114
curl localhost:8114/healthz  # {"status":"ok"}
```

Migrations run at every start (`internal/db`), so there is no manual SQL step.
Add a new schema change as `migrations/sqlite/00N_x.sql` **and**
`migrations/mysql/00N_x.sql` with the same tables.

## Test

```bash
go vet ./... && go test -race -cover ./...
```

## Deploy

First time, on the Pi: `deploy/setup-host.sh`, then copy
`systemd/entry-permit.service` to `~/.config/systemd/user/`. Afterwards, from
this machine: `deploy/deploy.sh full` (or `web`). `DRY_RUN=1` prints the steps.
`.env` stays on the Pi and is never copied; see `.env.example`.

## Rules for dates

Days are `yyyy-mm-dd`, moments `yyyy-mm-dd hh:mm:ss`, Jakarta wall-clock text,
never `time.Time` across package boundaries (`internal/dates`).

<!-- Generated: 2026-10-20 | Files scanned: ~140 Go/JS/HTML/SQL | Token estimate: ~700 -->
# Architecture

Single Go binary (`cmd/server/main.go`, stdlib `net/http` mux, html/template pages parsed at startup)
+ static files from `web/` on disk. Prod: Orange Pi 3B, user systemd service `hse-medpost`, port 8113, MariaDB.
Dev/tests: SQLite (`DB_DRIVER` unset). Plain JS front end, no build step.

```
browser ──► mux ──► pages.* (html/template)        /, /start, /programs, /mcu, /emr, /inventory, /admin/settings
   │          └──► /api/* handlers ──► internal/<pkg> API structs ──► *sql.DB (MariaDB | SQLite)
   │                   ▲ middleware: RequireAuth → RequireAdmin | RequireProgramManager | clinician(DoctorOrCMO)
   └─ /static/* (web/static, no cache build)
worksite tablets ──► /api/screening/* (own token login, CORS preflight) ──► screening pkg
public ──► /verification/{checkUpID}/{seal} (MCU certificate check)
```

## Packages (non-test lines)
| internal/ | role | ~lines |
|---|---|---|
| entrymcu | MCU records, certificates (PDF+sign+QR), lookups, import/export, reports, email notify | 12000 |
| activity-log | programs, activities, evidence, sub-headers, documents, supervisors | 4500 |
| emr | Medical Record: malaria screening (create/list/summary/export/result) | 2200 |
| directory | read-only token API for the entry-permit system | 1900 |
| screening | drug/alcohol + fatigue submissions, results table/dashboard, web entry | 1800 |
| staff | login, users, staff, position roles, signing certs | 1800 |
| company, patient, region | masters; region = 91,599 Indonesian regions | 1600/700/360 |
| storage, middleware, auth, db, dates, config, presence, display | infra | ~2100 |

## cmd/
server (main app) · migrate-activities · prepare-old-mcu · link-relations · recompress-pdfs (one-off tools)

## Ops
`systemd/hse-medpost.service`, `deploy/prune-backups.sh`. Deploy = build linux/arm64 → scp → rsync web/ → restart (backups `*.bak-<ts>`).
Production MariaDB migrations in `migrations/mysql/` are run **by hand** (app skips them when DB_DRIVER=mysql).
Region data: `indonesia-region/` (update.py, load.sh).

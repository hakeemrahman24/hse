<!-- Generated: 2026-10-20 | Files scanned: cmd/server/main.go + internal/* | Token estimate: ~900 -->
# Backend (routes in cmd/server/main.go)

Auth levels: `RequireAuth` (any staff) · `RequireAdmin` · `RequireProgramManager` · `clinician` (RequireDoctorOrCMO).

## Medical Record (internal/emr) — all RequireAuth
GET  /emr                                   → page (template data {Name})
GET  /api/malaria-screenings?from&to&q      → API.List  (newest first, max 500)
POST /api/malaria-screenings                → API.Create (tx: Visit + MalariaScreening; server sets age/company/position/region names/examiner/slide initial)
GET  /api/malaria-screenings.xlsx           → API.Export (max 50,000 rows)
GET  /api/malaria-screenings/summary?by=regency|district|village&from&to → API.Summary (totals, species Pv/Pf/Po/Mixed, top 15 regions)
GET  /api/malaria-screenings/slide-initial  → SlideInitialPreview
GET  /api/malaria-screenings/patient-context→ PatientContext (age, company, position)
PUT  /api/malaria-screenings/{id}/result    → SetResult   ({id} = MalariaID)
GET  /api/regions?parent|level&q&limit      → region.List
Key files: emr/malaria.go (Create/List/scanRow), malaria_summary.go (filters, Summary), malaria_export.go, rules.go (AgeAt, ISOWeek, SlideInitialCandidates, TestID), malaria_migrate.go (EnsureMalariaPurpose, EnsureMalariaID — SQLite upgrades)

## MCU (internal/entrymcu) — clinician unless noted
/api/mcu-records [GET list|POST|PUT {id}|DELETE admin], /new-patient, /search, /browse, /parent-candidates, /import(admin), /export.xlsx(admin), /mcu-data.xlsx, certificates zip, /{id}/certificate.pdf (public-signed), /api/mcu-reports(+summary.pdf admin)
Lookups (admin CRUD, auth GET): checkup-criteria, review-result-criteria, work-locations, task-categories (+/{id}/status)
/api/mcu-email-notification (admin, shelved scheduler) · GET /verification/{checkUpID}/{seal} (public)

## Activity log (internal/activity-log) — RequireAuth, manager for admin writes
/api/procedures, /api/programs/{id}/{activities,subheaders,subheader-media,activities-view,activities/download}, /api/activities/{id}[/evidence], /api/subheaders/{id}[/entries], /api/subheader-entries/{id}, /api/uploads/{evidenceId}, /api/documents, /api/supervisors, /api/entries/download(admin)

## Screening (internal/screening)
Tablet (token, CORS): POST /api/screening/{login,drug-alcohol,fatigue}
Staff: GET /api/screening/submissions (alcohol lookup for linking an entry); the old /api/test-log* routes were removed 2026-10-21
Medical Record Table+Dashboard tabs (RequireAuth, filtered by test date + name): GET /api/screening-log?test=alcohol|fatigue&from&to&q → LogViewList · /api/screening-log.xlsx → LogViewExport · /api/screening-log/summary?by=… → LogViewSummary (screening/log_view.go; reuses logSheets columns)

## Staff / masters
staff: /api/{login,logout,me,me/password,staff,users,position-roles} · company: /api/{companies,company-relations,projects,entity-types} · patient: /api/{patients,nationalities}
directory (token): /api/directory/{employees,fitness,checkups,activities,subheaders,documents,evidence,token*}
presence: /api/presence/{heartbeat,who} · GET /health

## Conventions
PascalCase tables, FK_<Name>ID, UTC `YYYY-MM-DDTHH:MM:SS` stamps (dates.Stamp), clinic zone Asia/Jayapura (dates.Today). Handlers = methods on `API{DB,...}`; errors via writeError (invalidInput → 400).

Drug & Alcohol web entry (RequireAuth, session staff): POST /api/screening-log/alcohol → screening.CreateAlcoholEntry (web_entry.go; parseRecord + insert, 201 new / 200 repeat by ClientID).
Fatigue web entry: POST /api/screening-log/fatigue → screening.CreateFatigueEntry (web_fatigue.go; parseFatigueRecord + insertFatigue, 201 new / 200 repeat by ClientID).

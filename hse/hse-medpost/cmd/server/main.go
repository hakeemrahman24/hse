package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	activitylog "hse-medpost/internal/activity-log"
	"hse-medpost/internal/auth"
	"hse-medpost/internal/company"
	"hse-medpost/internal/config"
	"hse-medpost/internal/correctiveaction"
	"hse-medpost/internal/db"
	"hse-medpost/internal/directory"
	"hse-medpost/internal/emr"
	"hse-medpost/internal/entrymcu"
	"hse-medpost/internal/mcusubmit"
	"hse-medpost/internal/middleware"
	"hse-medpost/internal/patient"
	"hse-medpost/internal/presence"
	"hse-medpost/internal/region"
	"hse-medpost/internal/screening"
	"hse-medpost/internal/sofi"
	"hse-medpost/internal/staff"
	"hse-medpost/internal/storage"
	"hse-medpost/internal/turnstile"
)

func main() {
	cfg := config.Load()

	database, err := db.Open(cfg.DBDriver, cfg.DSN())
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	if !cfg.UseMySQL() {
		// Before the migrations: 011 and 012 create the renamed tables with
		// IF NOT EXISTS, so an older database must be renamed first or it would
		// end up with an empty new table next to the old one that holds the rows.
		if err := screening.EnsureScreeningTableNames(database); err != nil {
			log.Fatal(err)
		}
		// The first version of the SOFI wording tables had no foreign key; migrations 026/027 create the complete ones.
		if err := sofi.DropOldQuestionnaireTables(database); err != nil {
			log.Fatal(err)
		}
		for _, name := range []string{"001_sqlite.sql", "002_position_role.sql", "003_subheaders.sql", "004_company.sql", "005_patient.sql", "006_entrymcu.sql", "007_mcu_certificate_log.sql", "008_mcu_email_notification.sql", "009_patient_name_index.sql", "010_project.sql", "011_screening.sql", "012_fatigue.sql", "013_region.sql", "014_visit_malaria.sql", "015_sofi.sql", "016_mcu_submission.sql", "017_mcu_submission_batches.sql", "018_sofi_window.sql", "019_sofi_period_note.sql", "020_mcu_portal_digest.sql", "021_mcu_mail_setting.sql", "022_mcu_submission_record_unique.sql", "023_unfit_is_open.sql", "024_sofi_preview.sql", "025_document_reference.sql", "026_sofi_dimensions.sql", "027_sofi_items.sql", "028_corrective_action.sql", "029_mcu_notice_setting.sql"} {
			sqlText, err := db.LoadMigrationFile(filepath.Join("migrations", "sqlite", name))
			if err != nil {
				log.Fatal(err)
			}
			if err := db.Migrate(database, sqlText); err != nil {
				log.Fatal(err)
			}
		}
		if err := mcusubmit.EnsureGenderColumn(database); err != nil {
			log.Fatal(err)
		}
		if err := mcusubmit.EnsureNoticeColumn(database); err != nil {
			log.Fatal(err)
		}
		if err := mcusubmit.EnsureNotifyRequestedColumn(database); err != nil {
			log.Fatal(err)
		}
		if err := sofi.EnsureWindowColumn(database); err != nil {
			log.Fatal(err)
		}
		if err := emr.EnsureMalariaPurpose(database); err != nil {
			log.Fatal(err)
		}
		if err := activitylog.EnsureWebsiteDocumentType(database); err != nil {
			log.Fatal(err)
		}
		if err := emr.EnsureMalariaID(database); err != nil {
			log.Fatal(err)
		}
		if err := company.EnsureCompanyArchivedColumn(database); err != nil {
			log.Fatal(err)
		}
		if err := screening.EnsureDrugAlcoholColumns(database); err != nil {
			log.Fatal(err)
		}
		if err := screening.EnsureFatigueColumns(database); err != nil {
			log.Fatal(err)
		}
		if err := activitylog.EnsureEvidenceUploader(database); err != nil {
			log.Fatal(err)
		}
		if err := activitylog.EnsureSubHeaderMerge(database); err != nil {
			log.Fatal(err)
		}
		if err := activitylog.EnsureTestLogKindColumn(database); err != nil {
			log.Fatal(err)
		}
		if err := activitylog.EnsureTestLogTitleColumn(database); err != nil {
			log.Fatal(err)
		}
		if err := activitylog.EnsureAuditLockedColumn(database); err != nil {
			log.Fatal(err)
		}
		if err := activitylog.EnsureEntrySubmissionLinkColumn(database); err != nil {
			log.Fatal(err)
		}
		if err := activitylog.EnsureEvidenceFileHashColumn(database); err != nil {
			log.Fatal(err)
		}
		if err := entrymcu.EnsureCheckUpStatusConstraint(database); err != nil {
			log.Fatal(err)
		}
		if err := entrymcu.EnsureTaskCategoryNamesColumn(database); err != nil {
			log.Fatal(err)
		}
		if err := entrymcu.EnsureRadiologyColumns(database); err != nil {
			log.Fatal(err)
		}
		if err := entrymcu.EnsureCtScanColumnsRenamed(database); err != nil {
			log.Fatal(err)
		}
		if err := entrymcu.EnsurePhysicalExamNotesColumn(database); err != nil {
			log.Fatal(err)
		}
		if err := entrymcu.EnsureBloodTypeColumn(database); err != nil {
			log.Fatal(err)
		}
		if err := entrymcu.EnsurePatientPersonalIDColumn(database); err != nil {
			log.Fatal(err)
		}
		if err := entrymcu.EnsureMcuRecordPersonalIDColumn(database); err != nil {
			log.Fatal(err)
		}
		if err := entrymcu.EnsureSchemaIndexes(database); err != nil {
			log.Fatal(err)
		}
	}

	// Patient.FK_CompanyID is derived from the latest visit; this backfills
	// existing rows and repairs drift from writes made outside the app. It runs
	// for both drivers (the statement is portable SQL).
	// A failure is logged, not fatal: the column is a derived cache and the
	// next visit save repairs it, so it must never keep the service down.
	if err := db.SyncAllPatientsFromLatestVisit(database); err != nil {
		log.Printf("patient company backfill failed: %v", err)
	}

	// Hash the files that existed before Reference versions (migration 025 gave
	// them a version 1 with no hash) so re-uploading one of them is refused as a
	// duplicate. Both drivers. Logged, not fatal: on MariaDB the table only exists
	// once migration 038 was run by hand, and a missing table must not keep the
	// service down; the Reference tab is the only thing that depends on it.
	if filled, err := activitylog.EnsureDocumentHashes(database); err != nil {
		log.Printf("reference document hashes failed: %v", err)
	} else if filled > 0 {
		log.Printf("reference document hashes: filled %d", filled)
	}

	// staff.EnsureAdminAccount runs first: activitylog.SeedIfEmpty looks up
	// an existing staff row for its demo activities and no-ops if none exists.
	if err := staff.EnsureAdminAccount(database); err != nil {
		log.Fatal(err)
	}
	if err := staff.EnsureAdminLogin(database); err != nil {
		log.Fatal(err)
	}
	if err := staff.EnsurePositionRoles(database); err != nil {
		log.Fatal(err)
	}
	if err := staff.EnsureManagerRoleRetired(database); err != nil {
		log.Fatal(err)
	}
	if err := activitylog.SeedIfEmpty(database); err != nil {
		log.Fatal(err)
	}
	if err := company.EnsureEntityTypeSeed(database); err != nil {
		log.Fatal(err)
	}
	if err := company.EnsureCompanySeed(database); err != nil {
		log.Fatal(err)
	}
	if err := company.EnsureCompanyRelationSeed(database); err != nil {
		log.Fatal(err)
	}
	if err := company.EnsureProjectSeed(database); err != nil {
		log.Fatal(err)
	}
	if err := company.EnsureRelationDepths(database); err != nil {
		log.Fatal(err)
	}
	if err := patient.EnsureNationalitySeed(database); err != nil {
		log.Fatal(err)
	}
	if err := entrymcu.EnsureLookupTables(database); err != nil {
		log.Fatal(err)
	}
	if err := entrymcu.EnsureWorkLocationColumn(database); err != nil {
		log.Fatal(err)
	}
	if err := entrymcu.EnsureResultCriteriaEditable(database); err != nil {
		log.Fatal(err)
	}
	if err := entrymcu.EnsureCheckUpCriteriaSeed(database); err != nil {
		log.Fatal(err)
	}
	if err := entrymcu.EnsureTaskCategorySeed(database); err != nil {
		log.Fatal(err)
	}
	if err := entrymcu.EnsureOccupationalRiskRevision(database); err != nil {
		log.Fatal(err)
	}
	if err := entrymcu.EnsureTaskCategoryTriggerWordSeed(database); err != nil {
		log.Fatal(err)
	}
	if err := entrymcu.EnsureRemoteTaskCategoriesRemoved(database); err != nil {
		log.Fatal(err)
	}
	if err := entrymcu.EnsureReviewResultSeed(database); err != nil {
		log.Fatal(err)
	}
	if err := entrymcu.EnsureWorkLocationSeed(database); err != nil {
		log.Fatal(err)
	}
	if err := entrymcu.EnsureEmailNotificationConfigSeed(database); err != nil {
		log.Fatal(err)
	}
	if err := directory.EnsureToken(database, cfg.DirectoryAPIToken); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(storage.CommonDir(cfg.UploadDir), 0o755); err != nil {
		log.Fatal(err)
	}

	store := auth.NewStore(cfg.SessionSecret)
	if err := os.MkdirAll(cfg.SigningDir(), 0o700); err != nil {
		log.Fatal(err)
	}
	staffAPI := &staff.API{DB: database, Store: store, SigningDir: cfg.SigningDir()}
	companyAPI := &company.API{DB: database}
	patientAPI := &patient.API{DB: database}
	entrymcuAPI := &entrymcu.API{DB: database}
	patientAPI.InvalidateCertificates = entrymcuAPI.InvalidateCertificatesForPatient
	presenceStore := presence.NewStore()
	presenceSession := func(r *http.Request) (int64, string, string, bool) {
		sess := middleware.Session(r)
		if sess == nil {
			return 0, "", "", false
		}
		var username string
		if err := database.QueryRow(`SELECT Username FROM AppUserTable WHERE UserID = ?`, sess.UserID).Scan(&username); err != nil {
			username = ""
		}
		return sess.StaffID, sess.Name, username, true
	}
	certConfig := entrymcu.CertConfig{Secret: cfg.CertSecret, LogoPath: cfg.CertLogoPath, SignaturePath: cfg.CertSignaturePath, UploadDir: cfg.UploadDir, SigningDir: cfg.SigningDir(), BaseURL: cfg.BaseURL}
	logAPI := &activitylog.API{DB: database, Store: store, UploadDir: cfg.UploadDir}
	pages, err := activitylog.LoadPages(filepath.Join("web", "templates"))
	if err != nil {
		log.Fatal(err)
	}
	pages.DB = database
	clinician := func(h http.Handler) http.Handler {
		return middleware.RequireAuth(middleware.RequireDoctorOrCMO(database)(h))
	}

	mux := http.NewServeMux()

	staticFS := http.StripPrefix("/static/", http.FileServer(http.Dir("web/static")))
	mux.Handle("GET /static/favicon.png", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		if p, ok := storage.FindCommonFile(cfg.UploadDir, "favicon"); ok {
			http.ServeFile(w, r, p)
			return
		}
		http.ServeFile(w, r, filepath.Join("web", "static", "favicon.png"))
	}))
	mux.Handle("GET /static/logo.png", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		if p, ok := storage.FindCommonFile(cfg.UploadDir, "logo"); ok {
			http.ServeFile(w, r, p)
			return
		}
		http.ServeFile(w, r, filepath.Join("data", "uploads", "_MedpostWide", "common", "logo.png"))
	}))
	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		staticFS.ServeHTTP(w, r)
	}))

	mux.HandleFunc("GET /{$}", pages.Login)
	mux.Handle("GET /start", middleware.RequireAuth(http.HandlerFunc(pages.Start)))
	mux.Handle("GET /programs", middleware.RequireAuth(http.HandlerFunc(pages.Programs)))
	mux.Handle("GET /programs/{id}", middleware.RequireAuth(http.HandlerFunc(pages.ProgramDetail)))
	mux.Handle("GET /admin/settings", middleware.RequireAuth(middleware.RequireProgramManager(http.HandlerFunc(pages.Settings))))

	mux.Handle("GET /emr", middleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := ""
		if sess := middleware.Session(r); sess != nil {
			name = sess.Name
		}
		if err := pages.Templates.ExecuteTemplate(w, "emr.html", map[string]any{"Name": name}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})))
	mux.Handle("GET /inventory", middleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := pages.Templates.ExecuteTemplate(w, "inventory.html", nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})))
	mux.Handle("GET /mcu", clinician(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := middleware.Session(r)
		if err := pages.Templates.ExecuteTemplate(w, "mcu.html", map[string]any{
			"Name":         sess.Name,
			"ReviewerName": entrymcu.ReviewerNameForSession(sess),
			"Role":         sess.Role,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})))

	mux.HandleFunc("POST /api/login", staffAPI.Login)
	mux.HandleFunc("GET /api/login/staff-usernames", staffAPI.StaffUsernames)
	mux.Handle("POST /api/logout", middleware.RequireAuth(http.HandlerFunc(staffAPI.Logout)))
	mux.Handle("GET /api/me", middleware.RequireAuth(http.HandlerFunc(staffAPI.Me)))
	mux.Handle("PUT /api/me/password", middleware.RequireAuth(http.HandlerFunc(staffAPI.ChangePassword)))

	mux.Handle("GET /api/procedures", middleware.RequireAuth(http.HandlerFunc(logAPI.ListPrograms)))
	mux.Handle("GET /api/activities-view", middleware.RequireAuth(http.HandlerFunc(logAPI.AllActivitiesView)))
	mux.Handle("GET /api/programs/{id}/activities-view", middleware.RequireAuth(http.HandlerFunc(logAPI.ProgramActivitiesView)))
	mux.Handle("GET /api/programs/{id}/activities/download", middleware.RequireAuth(middleware.RequireProgramManager(http.HandlerFunc(logAPI.DownloadProgramActivities))))
	mux.Handle("POST /api/programs/{id}/activities", middleware.RequireAuth(http.HandlerFunc(logAPI.CreateActivity)))
	mux.Handle("POST /api/activities/{id}/evidence", middleware.RequireAuth(http.HandlerFunc(logAPI.AddEvidence)))
	mux.Handle("PUT /api/activities/{id}", middleware.RequireAuth(http.HandlerFunc(logAPI.UpdateActivity)))
	mux.Handle("DELETE /api/activities/{id}", middleware.RequireAuth(http.HandlerFunc(logAPI.DeleteActivity)))
	mux.Handle("GET /api/uploads/{evidenceId}", middleware.RequireAuth(http.HandlerFunc(logAPI.Upload)))
	mux.Handle("GET /api/uploads/{evidenceId}/{filename}", middleware.RequireAuth(http.HandlerFunc(logAPI.Upload)))
	mux.Handle("DELETE /api/uploads/{evidenceId}", middleware.RequireAuth(http.HandlerFunc(logAPI.DeleteEvidence)))
	mux.Handle("GET /api/uploads/download-all/info", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(logAPI.DownloadAllUploadsInfo))))
	mux.Handle("GET /api/uploads/download-all", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(logAPI.DownloadAllUploads))))

	// Corrective actions: any signed-in staff may add, edit, close and reopen; only an admin may delete a
	// finding (plan/corrective-action-plan.md). Photos live under the same upload root as other evidence.
	correctiveActionAPI := &correctiveaction.API{Store: &correctiveaction.Store{DB: database}, UploadDir: cfg.UploadDir}
	correctiveActionAPI.Register(mux, middleware.RequireAuth, func(h http.Handler) http.Handler {
		return middleware.RequireAuth(middleware.RequireAdmin(h))
	})

	mux.Handle("GET /api/documents", middleware.RequireAuth(http.HandlerFunc(logAPI.ListDocuments)))
	mux.Handle("GET /api/documents/{id}/file", middleware.RequireAuth(http.HandlerFunc(logAPI.DocumentFile)))
	mux.Handle("GET /api/documents/{id}/{filename}", middleware.RequireAuth(http.HandlerFunc(logAPI.DocumentFile)))
	mux.Handle("POST /api/documents", middleware.RequireAuth(middleware.RequireProgramManager(http.HandlerFunc(logAPI.CreateDocument))))
	mux.Handle("PUT /api/documents/{id}", middleware.RequireAuth(middleware.RequireProgramManager(http.HandlerFunc(logAPI.UpdateDocument))))
	mux.Handle("DELETE /api/documents/{id}", middleware.RequireAuth(middleware.RequireProgramManager(http.HandlerFunc(logAPI.DeleteDocument))))
	// Reference tab on the medical record page: /api/reference/... (reading needs sign-in,
	// every change is admin only; the permission wrappers are inside RegisterReferenceRoutes).
	logAPI.RegisterReferenceRoutes(mux)

	mux.Handle("GET /api/staff", middleware.RequireAuth(http.HandlerFunc(staffAPI.ListStaff)))
	mux.Handle("PUT /api/staff/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(staffAPI.UpdateStaff))))
	mux.Handle("POST /api/staff/{id}/signing-certificate", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(staffAPI.CreateSigningCertificate))))
	mux.Handle("DELETE /api/staff/{id}/signing-certificate", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(staffAPI.DeleteSigningCertificate))))

	mux.Handle("GET /api/supervisors", middleware.RequireAuth(http.HandlerFunc(logAPI.ListSupervisors)))
	mux.Handle("POST /api/supervisors", middleware.RequireAuth(middleware.RequireProgramManager(http.HandlerFunc(logAPI.CreateSupervisor))))
	mux.Handle("PUT /api/supervisors/{id}", middleware.RequireAuth(middleware.RequireProgramManager(http.HandlerFunc(logAPI.UpdateSupervisor))))
	mux.Handle("DELETE /api/supervisors/{id}", middleware.RequireAuth(middleware.RequireProgramManager(http.HandlerFunc(logAPI.DeleteSupervisor))))

	mux.Handle("GET /api/position-roles", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(staffAPI.ListPositionRoles))))
	mux.Handle("POST /api/position-roles", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(staffAPI.CreatePositionRole))))
	mux.Handle("PUT /api/position-roles/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(staffAPI.UpdatePositionRole))))
	mux.Handle("DELETE /api/position-roles/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(staffAPI.DeletePositionRole))))

	// Reads are RequireAuth-only (not admin-gated): MCU/patient/EMR forms
	// across every module need the company picker, not just the admin
	// Settings page. Writes are admin-only, matching position-roles above —
	// per-user request, CompanyRelation add/edit also lives in Settings,
	// admin-gated the same way.
	mux.Handle("GET /api/entity-types", middleware.RequireAuth(http.HandlerFunc(companyAPI.ListEntityTypes)))
	mux.Handle("GET /api/companies", middleware.RequireAuth(http.HandlerFunc(companyAPI.ListCompanies)))
	mux.Handle("POST /api/companies", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(companyAPI.CreateCompany))))
	mux.Handle("PUT /api/companies/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(companyAPI.UpdateCompany))))
	mux.Handle("DELETE /api/companies/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(companyAPI.DeleteCompany))))

	mux.Handle("GET /api/company-relations", middleware.RequireAuth(http.HandlerFunc(companyAPI.ListCompanyRelations)))
	mux.Handle("POST /api/company-relations", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(companyAPI.CreateCompanyRelation))))
	mux.Handle("PUT /api/company-relations/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(companyAPI.UpdateCompanyRelation))))
	mux.Handle("DELETE /api/company-relations/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(companyAPI.DeleteCompanyRelation))))

	// ProjectTable read is RequireAuth-only (the relation forms need it);
	// writes RequireAdmin (Settings-managed), same shape as nationalities above.
	mux.Handle("GET /api/projects", middleware.RequireAuth(http.HandlerFunc(companyAPI.ListProjects)))
	mux.Handle("POST /api/projects", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(companyAPI.CreateProject))))
	mux.Handle("PUT /api/projects/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(companyAPI.UpdateProject))))
	mux.Handle("DELETE /api/projects/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(companyAPI.DeleteProject))))

	// NationalityTable read is RequireAuth-only (patient form needs it);
	// writes RequireAdmin (Settings-managed), same shape as entity-types above.
	mux.Handle("GET /api/nationalities", middleware.RequireAuth(http.HandlerFunc(patientAPI.ListNationalities)))
	mux.Handle("POST /api/nationalities", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(patientAPI.CreateNationality))))
	mux.Handle("PUT /api/nationalities/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(patientAPI.UpdateNationality))))
	mux.Handle("PUT /api/nationalities/{id}/status", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(patientAPI.SetNationalityStatus))))
	mux.Handle("DELETE /api/nationalities/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(patientAPI.DeleteNationality))))

	// Patient CRUD is RequireAuth-only, not admin-gated: any staff member
	// enters checkups and can add a new patient inline from that form.
	mux.Handle("GET /api/patients", middleware.RequireAuth(http.HandlerFunc(patientAPI.ListPatients)))
	mux.Handle("POST /api/patients", middleware.RequireAuth(http.HandlerFunc(patientAPI.CreatePatient)))
	mux.Handle("PUT /api/patients/{id}", middleware.RequireAuth(http.HandlerFunc(patientAPI.UpdatePatient)))
	mux.Handle("DELETE /api/patients/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(patientAPI.DeletePatient))))

	// Other databases (entry permit, and later callers) read identity plus the
	// latest Fit / Fit with note review. Token auth, not the staff session.
	// Offline-form uploads: no RequireAuth, because these handlers check their own bearer token.
	screeningAPI := &screening.API{DB: database, AuthStore: store}
	mux.HandleFunc("OPTIONS /api/screening/login", screening.Preflight)
	mux.HandleFunc("OPTIONS /api/screening/drug-alcohol", screening.Preflight)
	mux.HandleFunc("OPTIONS /api/screening/fatigue", screening.Preflight)
	mux.HandleFunc("POST /api/screening/login", screeningAPI.Login)
	mux.HandleFunc("POST /api/screening/drug-alcohol", screeningAPI.SubmitDrugAlcohol)
	mux.HandleFunc("POST /api/screening/fatigue", screeningAPI.SubmitFatigue)

	// The Medical Record page's alcohol and fatigue Table and Dashboard tabs.
	mux.Handle("GET /api/screening-log", middleware.RequireAuth(http.HandlerFunc(screeningAPI.LogViewList)))
	mux.Handle("GET /api/screening-log.xlsx", middleware.RequireAuth(http.HandlerFunc(screeningAPI.LogViewExport)))
	mux.Handle("POST /api/screening-log/alcohol", middleware.RequireAuth(http.HandlerFunc(screeningAPI.CreateAlcoholEntry)))
	mux.Handle("POST /api/screening-log/fatigue", middleware.RequireAuth(http.HandlerFunc(screeningAPI.CreateFatigueEntry)))
	mux.Handle("GET /api/screening-log/summary", middleware.RequireAuth(http.HandlerFunc(screeningAPI.LogViewSummary)))
	mux.Handle("GET /api/screening/submissions", middleware.RequireAuth(http.HandlerFunc(screeningAPI.ListAlcoholSubmissions)))

	// Region lookup for the cascading Regency/District/Village dropdowns.
	regionAPI := &region.API{DB: database}
	mux.Handle("GET /api/regions", middleware.RequireAuth(http.HandlerFunc(regionAPI.List)))

	// Malaria screening: entered on the Medical Record page by any signed-in staff.
	emrAPI := &emr.API{DB: database}
	mux.Handle("GET /api/malaria-screenings", middleware.RequireAuth(http.HandlerFunc(emrAPI.List)))
	mux.Handle("POST /api/malaria-screenings", middleware.RequireAuth(http.HandlerFunc(emrAPI.Create)))
	mux.Handle("GET /api/malaria-screenings.xlsx", middleware.RequireAuth(http.HandlerFunc(emrAPI.Export)))
	mux.Handle("GET /api/malaria-screenings/summary", middleware.RequireAuth(http.HandlerFunc(emrAPI.Summary)))
	mux.Handle("GET /api/malaria-screenings/slide-initial", middleware.RequireAuth(http.HandlerFunc(emrAPI.SlideInitialPreview)))
	mux.Handle("GET /api/malaria-screenings/patient-context", middleware.RequireAuth(http.HandlerFunc(emrAPI.PatientContext)))
	mux.Handle("PUT /api/malaria-screenings/{id}/result", middleware.RequireAuth(http.HandlerFunc(emrAPI.SetResult)))

	// Public fatigue screening for every employee on file (/sofi): its own cookie and signing key,
	// no staff session. The content after sign-in is built later.
	sofiAPI, err := sofi.New(database, cfg.SessionSecret, filepath.Join("web", "templates"))
	if err != nil {
		log.Fatal(err)
	}
	sofiAPI.TrustCloudflareHeader(os.Getenv("SOFI_TRUST_CF_HEADER") == "1")
	// The saved questionnaires, for the SOFI tab on the Medical Record page (staff only).
	mux.Handle("GET /api/sofi-log", middleware.RequireAuth(http.HandlerFunc(sofiAPI.StaffList)))
	mux.Handle("GET /api/sofi-log.xlsx", middleware.RequireAuth(http.HandlerFunc(sofiAPI.StaffExport)))
	mux.Handle("GET /api/sofi-log/summary", middleware.RequireAuth(http.HandlerFunc(sofiAPI.StaffSummary)))
	// When /sofi is open (plan/sofi-window-plan.md). Any staff may read the status and the rounds
	// for the results' filter; only an admin may list the full schedule, add a window or end one.
	mux.Handle("GET /api/sofi-windows/status", middleware.RequireAuth(http.HandlerFunc(sofiAPI.WindowStatus)))
	mux.Handle("GET /api/sofi-windows", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(sofiAPI.AdminWindows))))
	mux.Handle("POST /api/sofi-windows", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(sofiAPI.AdminCreateWindow))))
	mux.Handle("GET /api/sofi-items", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(sofiAPI.AdminItems))))
	mux.Handle("PUT /api/sofi-dimensions/{key}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(sofiAPI.AdminPutDimension))))
	mux.Handle("PUT /api/sofi-items/{number}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(sofiAPI.AdminPutItem))))
	mux.Handle("POST /api/sofi-preview", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(sofiAPI.AdminPreview))))
	mux.Handle("POST /api/sofi-windows/{id}/end", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(sofiAPI.AdminEndWindow))))
	// The doctor's note of a test period, printed on the PDF report. Any signed-in staff may read
	// it; PutNote checks for a doctor, the Chief Medical Officer or the administrator itself.
	mux.Handle("GET /api/sofi-windows/{id}/note", middleware.RequireAuth(http.HandlerFunc(sofiAPI.GetNote)))
	mux.Handle("PUT /api/sofi-windows/{id}/note", middleware.RequireAuth(http.HandlerFunc(sofiAPI.PutNote)))
	// Cloudflare Turnstile, the same keys as hse-register's SOC page. Without keys the page is
	// only served when SOFI_DEV_STUBS=1 (a fake check); otherwise /sofi is left off, because
	// a sign-in that anyone could script must not be open.
	turnstileKey, turnstileSecret := os.Getenv("TURNSTILE_SITEKEY"), os.Getenv("TURNSTILE_SECRET")
	switch {
	case turnstileKey != "" && turnstileSecret != "":
		sofiAPI.SetTurnstile(turnstile.NewCloudflareVerifier(turnstileSecret), turnstileKey, false)
		sofiAPI.Register(mux)
	case os.Getenv("SOFI_DEV_STUBS") == "1":
		sofiAPI.SetTurnstile(turnstile.DevVerifier{}, "", true)
		sofiAPI.Register(mux)
		log.Print("SOFI: using a FAKE Cloudflare check (SOFI_DEV_STUBS=1)")
	default:
		log.Print("SOFI (/sofi) is off: set TURNSTILE_SITEKEY and TURNSTILE_SECRET (or SOFI_DEV_STUBS=1 for development).")
	}

	// Public medical checkup submission portal (/mcu-submit): its own accounts, cookie and look.
	mcuFiles := openMcuFiles()
	startMcuPortal(database, cfg.SessionSecret, mux, mcuFiles, portalCertificates{api: entrymcuAPI, cfg: certConfig})
	registerMcuStaff(mux, clinician, database, mcuFiles)

	directoryAPI := &directory.API{DB: database, Token: cfg.DirectoryAPIToken}
	mux.HandleFunc("GET /api/directory/employees", directoryAPI.List)
	mux.HandleFunc("GET /api/directory/fitness", directoryAPI.Fitness)
	mux.HandleFunc("GET /api/directory/checkups", directoryAPI.Checkups)
	mux.HandleFunc("GET /api/directory/latest-checkups", directoryAPI.LatestCheckups)
	mux.HandleFunc("GET /api/directory/activities", directoryAPI.Activities)
	mux.HandleFunc("GET /api/directory/subheaders", directoryAPI.SubHeaders)
	mux.HandleFunc("GET /api/directory/regions", directoryAPI.Regions)
	mux.HandleFunc("GET /api/directory/companies", directoryAPI.Companies)
	mux.HandleFunc("GET /api/directory/activities/{id}/files", directoryAPI.ActivityFiles)
	mux.HandleFunc("GET /api/directory/evidence/{id}", directoryAPI.EvidenceFile)
	mux.HandleFunc("GET /api/directory/evidence/{id}/{filename}", directoryAPI.EvidenceFile)
	mux.HandleFunc("GET /api/directory/documents", directoryAPI.Documents)
	mux.HandleFunc("GET /api/directory/documents/{id}", directoryAPI.DocumentFile)
	mux.HandleFunc("GET /api/directory/documents/{id}/{filename}", directoryAPI.DocumentFile)
	mux.Handle("GET /api/directory/token", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(directoryAPI.GetToken))))
	mux.Handle("POST /api/directory/token", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(directoryAPI.RotateToken))))
	mux.Handle("POST /api/directory/full-token", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(directoryAPI.RotateToken))))

	mux.Handle("GET /api/checkup-criteria", middleware.RequireAuth(http.HandlerFunc(entrymcuAPI.ListCheckUpCriteria)))
	mux.Handle("POST /api/checkup-criteria", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.CreateCheckUpCriteria))))
	mux.Handle("PUT /api/checkup-criteria/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.UpdateCheckUpCriteria))))
	mux.Handle("PUT /api/checkup-criteria/{id}/status", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.SetCheckUpCriteriaStatus))))
	mux.Handle("DELETE /api/checkup-criteria/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.DeleteCheckUpCriteria))))

	mux.Handle("GET /api/review-result-criteria", middleware.RequireAuth(http.HandlerFunc(entrymcuAPI.ListReviewResultCriteria)))
	mux.Handle("POST /api/review-result-criteria", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.CreateReviewResultCriteria))))
	mux.Handle("PUT /api/review-result-criteria/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.UpdateReviewResultCriteria))))
	mux.Handle("PUT /api/review-result-criteria/{id}/status", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.SetReviewResultCriteriaStatus))))
	mux.Handle("DELETE /api/review-result-criteria/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.DeleteReviewResultCriteria))))

	mux.Handle("GET /api/work-locations", middleware.RequireAuth(http.HandlerFunc(entrymcuAPI.ListWorkLocations)))
	mux.Handle("POST /api/work-locations", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.CreateWorkLocation))))
	mux.Handle("PUT /api/work-locations/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.UpdateWorkLocation))))
	mux.Handle("PUT /api/work-locations/{id}/status", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.SetWorkLocationStatus))))
	mux.Handle("DELETE /api/work-locations/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.DeleteWorkLocation))))

	mux.Handle("GET /api/task-categories", middleware.RequireAuth(http.HandlerFunc(entrymcuAPI.ListTaskCategories)))
	mux.Handle("POST /api/task-categories", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.CreateTaskCategory))))
	mux.Handle("PUT /api/task-categories/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.UpdateTaskCategory))))
	mux.Handle("PUT /api/task-categories/{id}/status", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.SetTaskCategoryStatus))))
	mux.Handle("DELETE /api/task-categories/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.DeleteTaskCategory))))

	// Medical checkup records are for a doctor, the Chief Medical Officer, or the Administrator.
	mux.Handle("GET /api/mcu-records", clinician(http.HandlerFunc(entrymcuAPI.ListMcuRecords)))
	mux.Handle("POST /api/mcu-records", clinician(http.HandlerFunc(entrymcuAPI.CreateMcuRecord)))
	mux.Handle("POST /api/mcu-records/new-patient", clinician(http.HandlerFunc(entrymcuAPI.CreateNewPatientWithCheckup)))
	mux.Handle("PUT /api/mcu-records/{id}", clinician(http.HandlerFunc(entrymcuAPI.UpdateMcuRecord)))
	mux.Handle("DELETE /api/mcu-records/{id}", clinician(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.DeleteMcuRecord))))

	// Parent-record picker (renewal/follow-up linking) and the general
	// ID/name search box — ports of the legacy sheet's getOpenIDsByName and
	// searchRecords.
	mux.Handle("GET /api/mcu-records/parent-candidates", clinician(http.HandlerFunc(entrymcuAPI.ListParentCandidates)))
	mux.Handle("GET /api/mcu-records/search", clinician(http.HandlerFunc(entrymcuAPI.SearchMcuRecords)))

	// Reporting/analytics grid + chart summaries are viewable by any
	// clinician role (same checkup gate as the Employee/MCU data tabs).
	// The PDF/Excel exports stay admin-only.
	mux.Handle("GET /api/mcu-reports", clinician(http.HandlerFunc(entrymcuAPI.GetMcuReport)))
	mux.Handle("GET /api/mcu-reports/summary.pdf", clinician(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.GetMcuDashboardPDF))))
	mux.Handle("GET /api/mcu-records/browse", clinician(http.HandlerFunc(entrymcuAPI.GetMcuRecordsBrowse)))
	mux.Handle("POST /api/mcu-records/import", clinician(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.ImportMcuRecords))))
	mux.Handle("GET /api/mcu-records/export.xlsx", clinician(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.DownloadAllEntries))))
	mux.Handle("GET /api/mcu-records/mcu-data.xlsx", clinician(http.HandlerFunc(entrymcuAPI.DownloadMcuDataSheet)))
	mux.Handle("GET /api/mcu-records/export-certificates.zip", clinician(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.DownloadAllCertificates(certConfig)))))
	mux.Handle("GET /api/mcu-records/latest-certificates.zip", clinician(http.HandlerFunc(entrymcuAPI.DownloadLatestEligibleCertificates(certConfig))))
	mux.Handle("POST /api/mcu-records/clear-certificates", clinician(http.HandlerFunc(entrymcuAPI.ClearCachedCertificates(certConfig))))

	mux.Handle("GET /api/mcu-email-notification", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.GetEmailNotificationConfig))))
	mux.Handle("PUT /api/mcu-email-notification", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.UpdateEmailNotificationConfig))))
	mux.Handle("POST /api/mcu-email-notification/send-now", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(entrymcuAPI.SendNowEmailNotification))))

	// Presence: any authenticated staff can be seen/see others on a page —
	// this is a collision-avoidance nicety (see mcu.js), not admin-gated.
	mux.Handle("POST /api/presence/heartbeat", middleware.RequireAuth(http.HandlerFunc(presence.Heartbeat(presenceStore, presenceSession))))
	mux.Handle("GET /api/presence/who", middleware.RequireAuth(http.HandlerFunc(presence.Who(presenceStore, presenceSession))))

	// Health-certificate PDF — port of the legacy "Certificate Dispatcher"
	// sidebar, one record at a time instead of a pasted ID range.
	// certificate.pdf stays for old links. The named route is what MCU data
	// opens: the browser tab's save uses the last path segment as the filename.
	certPDF := clinician(http.HandlerFunc(entrymcuAPI.DownloadCertificate(certConfig)))
	mux.Handle("GET /api/mcu-records/{id}/certificate.pdf", certPDF)
	mux.Handle("GET /api/mcu-records/{id}/{filename}", certPDF)
	// Public certificate check, separate from the login page at /.
	// The QR opens /verification/{checkup id}/{seal}. The seal must match
	// before any summary is shown.
	mux.Handle("GET /verification/{checkUpID}/{seal}", http.HandlerFunc(entrymcuAPI.VerifyCertificate(certConfig)))

	mux.Handle("GET /api/users", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(staffAPI.ListUsers))))
	mux.Handle("POST /api/users", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(staffAPI.CreateUser))))
	mux.Handle("PUT /api/users/{id}/password", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(staffAPI.ResetPassword))))
	mux.Handle("DELETE /api/users/{id}", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(staffAPI.DeactivateUser))))

	mux.Handle("GET /api/admin/procedures", middleware.RequireAuth(middleware.RequireProgramManager(http.HandlerFunc(logAPI.AdminListPrograms))))
	mux.Handle("POST /api/procedures", middleware.RequireAuth(middleware.RequireProgramManager(http.HandlerFunc(logAPI.CreateProgram))))
	mux.Handle("PUT /api/procedures/{id}", middleware.RequireAuth(middleware.RequireProgramManager(http.HandlerFunc(logAPI.UpdateProgram))))
	mux.Handle("DELETE /api/procedures/{id}", middleware.RequireAuth(middleware.RequireProgramManager(http.HandlerFunc(logAPI.DeleteProgram))))

	mux.Handle("GET /api/programs/{id}/subheaders", middleware.RequireAuth(http.HandlerFunc(logAPI.ListSubHeaders)))
	mux.Handle("POST /api/programs/{id}/subheaders", middleware.RequireAuth(middleware.RequireProgramManager(http.HandlerFunc(logAPI.CreateSubHeader))))
	mux.Handle("GET /api/programs/{id}/subheader-media", middleware.RequireAuth(http.HandlerFunc(logAPI.ListSubHeaderMedia)))
	mux.Handle("PUT /api/subheaders/{id}", middleware.RequireAuth(middleware.RequireProgramManager(http.HandlerFunc(logAPI.UpdateSubHeader))))
	mux.Handle("DELETE /api/subheaders/{id}", middleware.RequireAuth(middleware.RequireProgramManager(http.HandlerFunc(logAPI.DeleteSubHeader))))
	mux.Handle("GET /api/subheaders/{id}/entries", middleware.RequireAuth(http.HandlerFunc(logAPI.ListSubHeaderEntries)))
	mux.Handle("POST /api/subheaders/{id}/entries", middleware.RequireAuth(http.HandlerFunc(logAPI.CreateSubHeaderEntry)))
	mux.Handle("PUT /api/subheader-entries/{id}", middleware.RequireAuth(http.HandlerFunc(logAPI.UpdateSubHeaderEntry)))
	mux.Handle("POST /api/subheader-entries/{id}/evidence", middleware.RequireAuth(http.HandlerFunc(logAPI.AddSubHeaderEntryEvidence)))
	mux.Handle("DELETE /api/subheader-entries/{id}", middleware.RequireAuth(http.HandlerFunc(logAPI.DeleteSubHeaderEntry)))
	// Sub-header entry evidence now lives in EvidenceTable and is served by
	// the same /api/uploads/{evidenceId} route as regular activity evidence
	// (see subheaders.go's attachEntryEvidence) — no separate route needed.
	mux.Handle("GET /api/entries/download", middleware.RequireAuth(middleware.RequireAdmin(http.HandlerFunc(logAPI.DownloadAllEntries))))

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Email notifications shelved (2026-09-25): a single To/Cc list doesn't
	// fit "many companies, no dedicated HR staff to send to" — see
	// plan/hse-medpost-plan.md's Progress Log for the reasoning and the
	// per-company-routed redesign direction. entrymcuAPI.RunEmailNotificationScheduler
	// (entrymcu/emailnotify.go) and its Settings UI are left in place, just
	// not started — uncomment this line (and the settings.html panel) to revive.
	// go entrymcuAPI.RunEmailNotificationScheduler(time.Minute)

	handler := middleware.WithSession(store)(mux)

	addr := ":" + cfg.Port
	fmt.Printf("hse-medpost listening on http://127.0.0.1%s (%s)\n", addr, cfg.DBDriver)
	log.Fatal(http.ListenAndServe(addr, handler))
}

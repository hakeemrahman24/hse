package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"hse-medpost/internal/mcusubmit"
	"hse-medpost/internal/turnstile"
)

const (
	// A request whose email link is never opened is deleted after this long (plan section 4).
	unverifiedMaxAge  = 48 * time.Hour
	unverifiedSweepIn = time.Hour

	// The reviewer digest is checked hourly; the 24/36/48 hour interval the admin chose is applied
	// inside RunDigest, so a failed send is retried an hour later and not a day later.
	digestCheckIn = time.Hour

	// Files past their 2 years and long-inactive accounts are cleaned once a day (plan section 5).
	retentionEvery = 24 * time.Hour

	// Expiry reminders go out once a day at this hour in Jakarta (WIB), never in the small hours.
	reminderHourWIB = 8
)

// startMcuPortal wires the medical checkup submission portal (/mcu-submit) into mux. Like /sofi, it
// stays off unless everything it needs is configured, because a sign-up form that anyone could
// script, or one that cannot send its mails, must not be open:
//
//	MCU_PORTAL_BASE_URL            public address used in mail links, e.g. https://medpost.example.com
//	TURNSTILE_SITEKEY / _SECRET    the same Cloudflare keys as /sofi (or MCU_PORTAL_DEV_STUBS=1: fake check)
//	MCU_SMTP_HOST/_PORT/_USER/_PASS/_FROM   optional fallback mail login (Brevo's smtp-relay.brevo.com:587);
//	                               the page-edited login in the database wins when it is set
//	                               (MCU_PORTAL_DEV_MAIL=1: write mails to the log, development only)
//	MCU_PORTAL_ADMIN_PASSWORD      password of the hidden built-in portal admin, set on the first start
//	MCU_PORTAL_ADMIN_RESET=1       replace that password once (the admin has no email for "forgot")
//	MCU_PORTAL_TRUST_CF_HEADER=1   read the visitor address from CF-Connecting-IP (tunnel-only port)
//	MCU_BREVO_WEBHOOK_SECRET       optional; turns on POST /mcu-submit/webhooks/brevo. Put the same value in
//	                               Brevo's webhook settings as an "Authorization: Bearer <secret>" header
//	MCU_STAFF_BASE_URL             optional, the staff app's address; the reviewer digest links to its
//	                               /mcu-submissions page when set, e.g. https://emr.example.com
//	MCU_PORTAL_FILES_DIR           where the rebuilt PDFs are kept, default data/mcu-files (needs Ghostscript;
//	                               uploads stay off without it, the rest of the portal still works)
//
// A failure here is logged and leaves the portal off; it never stops the staff app.
func startMcuPortal(database *sql.DB, sessionSecret string, mux *http.ServeMux, files mcusubmit.FileStore) {
	ctx := context.Background()
	store := mcusubmit.NewStore(database)

	if os.Getenv("MCU_PORTAL_ADMIN_RESET") == "1" {
		if err := store.ResetBuiltInAdmin(ctx, os.Getenv("MCU_PORTAL_ADMIN_PASSWORD")); err != nil {
			log.Printf("MCU portal is off: cannot reset the admin password: %v", err)
			return
		}
		log.Print("MCU portal: admin password reset. Remove MCU_PORTAL_ADMIN_RESET now, or the next start resets it again.")
	} else if created, err := store.EnsureBuiltInAdmin(ctx, os.Getenv("MCU_PORTAL_ADMIN_PASSWORD")); err != nil {
		log.Printf("MCU portal is off: cannot create the admin account: %v", err)
		return
	} else if created {
		log.Print("MCU portal: built-in admin account created.")
	}

	var verifier turnstile.Verifier
	var sitekey string
	var devCheck bool
	switch {
	case os.Getenv("TURNSTILE_SITEKEY") != "" && os.Getenv("TURNSTILE_SECRET") != "":
		verifier, sitekey = turnstile.NewCloudflareVerifier(os.Getenv("TURNSTILE_SECRET")), os.Getenv("TURNSTILE_SITEKEY")
	case os.Getenv("MCU_PORTAL_DEV_STUBS") == "1":
		verifier, devCheck = turnstile.DevVerifier{}, true
		log.Print("MCU portal: using a FAKE Cloudflare check (MCU_PORTAL_DEV_STUBS=1)")
	default:
		log.Print("MCU portal (/mcu-submit) is off: set TURNSTILE_SITEKEY and TURNSTILE_SECRET (or MCU_PORTAL_DEV_STUBS=1 for development).")
		return
	}

	// The mail login normally lives in the database, set on the portal admin's Mail settings page.
	// The MCU_SMTP_* variables only fill in until someone has used that page.
	// Without either, the portal still starts so the admin can sign in and set mail up, but
	// sign-up and "forgot password" stay closed.
	envMailer := mcuEnvMailer()
	if envMailer == nil {
		log.Print("MCU portal: no MCU_SMTP_* variables. Sign-up stays closed until the portal admin sets the mail login on the Mail settings page (/mcu-submit/admin/mail).")
	}
	mailer := &mcusubmit.ConfigMailer{Store: store, Key: mcusubmit.MailKey(sessionSecret), Fallback: envMailer}
	baseURL := os.Getenv("MCU_PORTAL_BASE_URL")
	if baseURL == "" {
		log.Print("MCU portal (/mcu-submit) is off: set MCU_PORTAL_BASE_URL, the public address that mail links point at.")
		return
	}

	api, err := mcusubmit.New(store, sessionSecret, filepath.Join("web", "templates"))
	if err != nil {
		log.Printf("MCU portal is off: %v", err)
		return
	}
	startMcuUploads(ctx, api, files)
	api.SetTurnstile(verifier, sitekey, devCheck)
	api.SetMailer(mailer, baseURL)
	api.SetMailConfig(mailer)
	api.SetBounceSecret(os.Getenv("MCU_BREVO_WEBHOOK_SECRET"))
	api.TrustCloudflareHeader(os.Getenv("MCU_PORTAL_TRUST_CF_HEADER") == "1")
	api.Register(mux)

	go func() {
		for {
			if n, err := store.PurgeUnverified(ctx, unverifiedMaxAge); err != nil {
				log.Printf("MCU portal: purge unverified requests: %v", err)
			} else if n > 0 {
				log.Printf("MCU portal: purged %d unverified requests older than %s", n, unverifiedMaxAge)
			}
			time.Sleep(unverifiedSweepIn)
		}
	}()
	go runRetention(ctx, store, files)
	go runReminders(ctx, store, mailer, baseURL)
	go runReviewerDigest(ctx, store, mailer, os.Getenv("MCU_STAFF_BASE_URL"))
	log.Print("MCU portal (/mcu-submit) is on.")
}

// openMcuFiles opens the folder where the rebuilt PDFs are kept. The portal's upload pages and the
// staff review list share it, so it is opened once at start-up. A nil result means the folder
// cannot be used; both sides then run without files (uploads answer 503, the staff list shows
// no file links).
func openMcuFiles() mcusubmit.FileStore {
	dir := os.Getenv("MCU_PORTAL_FILES_DIR")
	if dir == "" {
		dir = filepath.Join("data", "mcu-files")
	}
	store, err := mcusubmit.NewLocalDiskStore(dir)
	if err != nil {
		log.Printf("MCU files: cannot use %s, uploads and file links are off: %v", dir, err)
		return nil
	}
	return store
}

// startMcuUploads switches the upload pages on when Ghostscript is installed and the file folder
// is usable. Either failure is logged and leaves uploads off, because a portal that cannot rebuild
// PDFs must not accept them (the pages answer 503). There is no virus scanner: the rebuild is the
// protection (plan section 5), and mcusubmit.Pipeline.Scanner stays nil.
func startMcuUploads(ctx context.Context, api *mcusubmit.API, files mcusubmit.FileStore) {
	if files == nil {
		return
	}
	rebuilder, err := mcusubmit.NewRebuilder(ctx)
	if err != nil {
		log.Printf("MCU portal: uploads are off: %v", err)
		return
	}
	api.SetPipeline(&mcusubmit.Pipeline{Store: files, Rebuilder: rebuilder})
	log.Print("MCU portal: uploads are on.")
}

// runReviewerDigest mails the reviewers the list of documents still waiting for review. It wakes
// every digestCheckIn; RunDigest decides whether the admin's interval has passed and whether
// anything is waiting.
func runReviewerDigest(ctx context.Context, store *mcusubmit.Store, mailer mcusubmit.Mailer, staffBase string) {
	link := ""
	if staffBase != "" {
		link = strings.TrimRight(staffBase, "/") + "/mcu-submissions"
	}
	for {
		if n, err := store.RunDigest(ctx, mailer, link); err != nil {
			log.Printf("MCU portal: reviewer digest: %v", err)
		} else if n > 0 {
			log.Printf("MCU portal: reviewer digest sent to %d reviewers", n)
		}
		time.Sleep(digestCheckIn)
	}
}

// runRetention deletes stored PDFs past their 2 years and blanks accounts that have been inactive
// that long. It runs at start-up and then daily; every run only picks rows that still need work,
// so a restart or a missed day does no harm. files may be nil (uploads off): then only accounts are
// considered, and none holds a file anyway.
func runRetention(ctx context.Context, store *mcusubmit.Store, files mcusubmit.FileStore) {
	for {
		expired, erased, err := store.RunRetention(ctx, files)
		if err != nil {
			log.Printf("MCU portal: retention: %v", err)
		}
		if expired > 0 || erased > 0 {
			log.Printf("MCU portal: retention removed %d files and erased %d accounts", expired, erased)
		}
		time.Sleep(retentionEvery)
	}
}

// nextReminderRun is the next 08:00 in Jakarta after now.
func nextReminderRun(now time.Time) time.Time {
	wib := time.FixedZone("WIB", 7*60*60)
	t := now.In(wib)
	next := time.Date(t.Year(), t.Month(), t.Day(), reminderHourWIB, 0, 0, 0, wib)
	if !next.After(t) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// runReminders mails the expiring-checkup reminders once a day. RunReminders does nothing while
// the admin's master switch is off, and skips anyone mailed within the repeat period, so running
// it daily is safe.
func runReminders(ctx context.Context, store *mcusubmit.Store, mailer mcusubmit.Mailer, baseURL string) {
	for {
		time.Sleep(time.Until(nextReminderRun(time.Now())))
		subs, workers, err := store.RunReminders(ctx, mailer, baseURL)
		if err != nil {
			log.Printf("MCU portal: reminders: %v", err)
		}
		if subs > 0 {
			log.Printf("MCU portal: sent expiry reminders to %d submitters about %d workers", subs, workers)
		}
	}
}

// mcuEnvMailer is the mail login from the environment, or the log-only mailer in development, or
// nil. It is the fallback of the login stored on the portal admin's Mail settings page.
func mcuEnvMailer() mcusubmit.Mailer {
	switch {
	case os.Getenv("MCU_SMTP_HOST") != "":
		port, _ := strconv.Atoi(os.Getenv("MCU_SMTP_PORT"))
		return &mcusubmit.SMTPMailer{
			Host: os.Getenv("MCU_SMTP_HOST"), Port: port, Username: os.Getenv("MCU_SMTP_USER"),
			Password: os.Getenv("MCU_SMTP_PASS"), From: os.Getenv("MCU_SMTP_FROM"),
		}
	case os.Getenv("MCU_PORTAL_DEV_MAIL") == "1":
		log.Print("MCU portal: mails are written to the log, NOT sent (MCU_PORTAL_DEV_MAIL=1)")
		return mcusubmit.LogMailer{}
	}
	return nil
}

package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"hse-medpost/internal/mcusubmit"
	"hse-medpost/internal/turnstile"
)

const (
	// A request whose email link is never opened is deleted after this long (plan section 4).
	unverifiedMaxAge  = 48 * time.Hour
	unverifiedSweepIn = time.Hour
)

// startMcuPortal wires the medical checkup submission portal (/mcu-submit) into mux. Like /sofi, it
// stays off unless everything it needs is configured, because a sign-up form that anyone could
// script, or one that cannot send its mails, must not be open:
//
//	MCU_PORTAL_BASE_URL            public address used in mail links, e.g. https://medpost.example.com
//	TURNSTILE_SITEKEY / _SECRET    the same Cloudflare keys as /sofi (or MCU_PORTAL_DEV_STUBS=1: fake check)
//	MCU_SMTP_HOST/_PORT/_USER/_PASS/_FROM   the mail relay, Brevo's smtp-relay.brevo.com:587 (or
//	                               MCU_PORTAL_DEV_MAIL=1: write mails to the log, development only)
//	MCU_PORTAL_ADMIN_PASSWORD      password of the hidden built-in portal admin, set on the first start
//	MCU_PORTAL_ADMIN_RESET=1       replace that password once (the admin has no email for "forgot")
//	MCU_PORTAL_TRUST_CF_HEADER=1   read the visitor address from CF-Connecting-IP (tunnel-only port)
//	MCU_PORTAL_FILES_DIR           where the rebuilt PDFs are kept, default data/mcu-files (needs Ghostscript;
//	                               uploads stay off without it, the rest of the portal still works)
//
// A failure here is logged and leaves the portal off; it never stops the staff app.
func startMcuPortal(database *sql.DB, sessionSecret string, mux *http.ServeMux) {
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

	var mailer mcusubmit.Mailer
	switch {
	case os.Getenv("MCU_SMTP_HOST") != "":
		port, _ := strconv.Atoi(os.Getenv("MCU_SMTP_PORT"))
		mailer = &mcusubmit.SMTPMailer{
			Host: os.Getenv("MCU_SMTP_HOST"), Port: port, Username: os.Getenv("MCU_SMTP_USER"),
			Password: os.Getenv("MCU_SMTP_PASS"), From: os.Getenv("MCU_SMTP_FROM"),
		}
	case os.Getenv("MCU_PORTAL_DEV_MAIL") == "1":
		mailer = mcusubmit.LogMailer{}
		log.Print("MCU portal: mails are written to the log, NOT sent (MCU_PORTAL_DEV_MAIL=1)")
	default:
		log.Print("MCU portal (/mcu-submit) is off: set MCU_SMTP_HOST, MCU_SMTP_FROM and the login (or MCU_PORTAL_DEV_MAIL=1 for development).")
		return
	}
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
	startMcuUploads(ctx, api)
	api.SetTurnstile(verifier, sitekey, devCheck)
	api.SetMailer(mailer, baseURL)
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
	log.Print("MCU portal (/mcu-submit) is on.")
}

// startMcuUploads switches the upload pages on when Ghostscript is installed and the file folder
// can be created. Either failure is logged and leaves uploads off, because a portal that cannot
// rebuild PDFs must not accept them (the pages answer 503). There is no virus scanner: the rebuild
// is the protection (plan section 5), and mcusubmit.Pipeline.Scanner stays nil.
func startMcuUploads(ctx context.Context, api *mcusubmit.API) {
	dir := os.Getenv("MCU_PORTAL_FILES_DIR")
	if dir == "" {
		dir = filepath.Join("data", "mcu-files")
	}
	store, err := mcusubmit.NewLocalDiskStore(dir)
	if err != nil {
		log.Printf("MCU portal: uploads are off, cannot use the file folder: %v", err)
		return
	}
	rebuilder, err := mcusubmit.NewRebuilder(ctx)
	if err != nil {
		log.Printf("MCU portal: uploads are off: %v", err)
		return
	}
	api.SetPipeline(&mcusubmit.Pipeline{Store: store, Rebuilder: rebuilder})
	log.Printf("MCU portal: uploads are on, files in %s", dir)
}

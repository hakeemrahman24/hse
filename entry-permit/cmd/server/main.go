// Command server is the entry-permit web application.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"entry-permit/internal/company"
	"entry-permit/internal/config"
	"entry-permit/internal/dashboard"
	"entry-permit/internal/dates"
	"entry-permit/internal/db"
	"entry-permit/internal/filestore"
	"entry-permit/internal/gateofficer"
	"entry-permit/internal/gatesync"
	"entry-permit/internal/jobs"
	"entry-permit/internal/mail"
	"entry-permit/internal/malaria"
	"entry-permit/internal/permitlog"
	"entry-permit/internal/policy"
	"entry-permit/internal/position"
	"entry-permit/internal/ratelimit"
	"entry-permit/internal/reflist"
	"entry-permit/internal/region"
	"entry-permit/internal/reviewlog"
	"entry-permit/internal/settings"
	"entry-permit/internal/staff"
	"entry-permit/internal/ticketlog"
	"entry-permit/internal/turnstile"
	"entry-permit/internal/waiverlog"
	"entry-permit/internal/web"
	"entry-permit/internal/worker"
	"entry-permit/migrations"
)

const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 15 * time.Second
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("entry-permit: %v", err)
	}
}

func run() error {
	cfg := config.Load(os.Getenv)
	if err := cfg.Validate(); err != nil {
		return err
	}
	database, err := db.Open(cfg.DBDriver, cfg.DSN())
	if err != nil {
		return err
	}
	defer database.Close()
	// Migrations run at every start, so a deploy never needs a manual SQL step.
	if err := db.Migrate(database, migrations.FS, migrations.Dir(cfg.DBDriver)); err != nil {
		return err
	}

	clock := dates.SystemClock{}
	// Settings hold the rule numbers; the code holds only their defaults.
	cfgSvc := settings.New(database, clock)
	if err := cfgSvc.Seed(context.Background()); err != nil {
		return err
	}
	if err := (&position.Store{DB: database}).Seed(context.Background()); err != nil {
		return err
	}
	lists := reflist.NewStore(database, clock)
	officers := gateofficer.New(database, clock)
	workers := workerSource(cfg)
	companies := &company.Store{DB: database, Clock: clock}
	companySrc := companySource(cfg)
	var syncer *gatesync.Syncer
	if cfg.GateURL != "" && cfg.GateSecret != "" {
		syncer = &gatesync.Syncer{DB: database, Client: &gatesync.Client{BaseURL: cfg.GateURL, Secret: cfg.GateSecret},
			Officers: officers, Lists: lists, Clock: clock, Policy: cfgSvc.Policy}
	}
	mailSender := mailer(cfg) // nil when no SMTP server is set: mail waits in the outbox
	staffStore := staff.NewStore(database, clock, policy.Default()).WithPolicy(cfgSvc.Policy)
	scheduler, runner := dailyJobs(database, clock, cfgSvc.Policy, cfg.BaseURL, mailSender, gateKick(syncer), staffStore)
	app, err := web.NewApp(cfg.WebDir, web.App{
		Store:         staffStore,
		Clock:         clock,
		Pol:           policy.Default(),
		PolicyFn:      cfgSvc.Policy,
		Settings:      cfgSvc,
		Officers:      gateofficer.New(database, clock),
		Workers:       workers,
		Permits:       &permitlog.Store{DB: database},
		Waivers:       &waiverlog.Store{DB: database},
		Tickets:       &ticketlog.Store{DB: database},
		Reviews:       &reviewlog.Store{DB: database},
		Uploads:       filestore.Store{Dir: cfg.UploadDir},
		Positions:     &position.Store{DB: database},
		MXCheck:       mailDomainCheck,
		Companies:     companies,
		CompanySource: companySrc,
		Turnstile:     turnstileVerifier(cfg),
		TurnstileSite: cfg.TurnstileSiteKey,
		TurnstileDev:  cfg.TurnstileDev,
		TrustCFIP:     cfg.TrustCFIP,
		Limit:         ratelimit.New(),
		Secret:        cfg.SessionSecret,
		Secure:        strings.HasPrefix(cfg.BaseURL, "https://"),
		BaseURL:       cfg.BaseURL,
		McuPortalURL:  cfg.McuPortalURL,
		Outbox:        &mail.Outbox{DB: database},
		Mailer:        mailSender,
		Jobs:          scheduler,
		GateKick:      gateKick(syncer),
		DB:            database,
		SQL:           database,
		Lists:         lists,
		Malaria:       &malaria.Service{Lists: lists, Regions: regionSource(cfg)},
	})
	if err != nil {
		return err
	}

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: app.Routes(), ReadHeaderTimeout: readHeaderTimeout}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go sweepOfficers(ctx, officers, workers)
	go syncCompanies(ctx, companies, companySrc)
	go scheduler.Run(ctx)
	go runner.DrainLoop(ctx)
	if syncer != nil {
		go syncer.Run(ctx)
	} else {
		log.Print("gate sync is off: GATE_WORKER_URL or GATE_SHARED_SECRET is not set")
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Printf("entry-permit listening on :%s (%s)", cfg.Port, cfg.DBDriver)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// mxTimeout bounds the DNS lookup behind the "can this address receive mail" check.
const mxTimeout = 4 * time.Second

// mailDomainCheck accepts a domain with an MX record, or, as RFC 5321 allows,
// one that only has an address record. A failed lookup refuses the address; the
// admin switch "email_mx_check" turns this off when DNS itself is down.
func mailDomainCheck(domain string) error {
	ctx, cancel := context.WithTimeout(context.Background(), mxTimeout)
	defer cancel()
	if mx, err := net.DefaultResolver.LookupMX(ctx, domain); err == nil && len(mx) > 0 {
		return nil
	}
	if hosts, err := net.DefaultResolver.LookupHost(ctx, domain); err == nil && len(hosts) > 0 {
		return nil
	}
	return fmt.Errorf("no mail server found for %s", domain)
}

// regionTTL: the regency list changes only when medpost reloads it.
const regionTTL = time.Hour

// regionSource is medpost's region API when it is configured (production),
// else the local file for development, else nil (the page says so).
func regionSource(cfg config.Config) region.Source {
	switch {
	case cfg.MedpostURL != "" && cfg.MedpostToken != "":
		return &region.Cached{Src: region.HTTPSource{BaseURL: cfg.MedpostURL, Token: cfg.MedpostToken}, TTL: regionTTL}
	case cfg.RegionTSV != "":
		return &region.Cached{Src: region.FileSource{Path: cfg.RegionTSV}, TTL: regionTTL}
	}
	return nil
}

// workerSource is medpost's employee API when it is configured, else nil (the
// public lookup then says it is not available).
func workerSource(cfg config.Config) worker.Source {
	if cfg.MedpostURL != "" && cfg.MedpostToken != "" {
		return worker.HTTPSource{BaseURL: cfg.MedpostURL, Token: cfg.MedpostToken}
	}
	return nil
}

// mailer is the SMTP relay when one is configured, else nil: e-mails are then
// stored in the outbox and wait. It returns the untyped nil on purpose, so the
// app's "is there a mailer" check is not fooled by a nil value inside an interface.
func mailer(cfg config.Config) mail.Mailer {
	s := mail.SMTP{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Password: cfg.SMTPPassword, From: cfg.MailFrom, FromName: cfg.MailFromName}
	if !s.Configured() {
		return nil
	}
	return s
}

// turnstileVerifier is Cloudflare's check when a secret is set, the dev stub
// when TURNSTILE_DEV=1 (refused in production by config.Validate), else nil.
func turnstileVerifier(cfg config.Config) turnstile.Verifier {
	switch {
	case cfg.TurnstileDev:
		return turnstile.DevVerifier{}
	case cfg.TurnstileSecret != "":
		return turnstile.NewCloudflareVerifier(cfg.TurnstileSecret, cfg.TurnstileHostnames...)
	}
	return nil
}

// officerSweepEvery: how often officers whose MCU is no longer eligible are
// archived. The sweep is safe to repeat, so a restart simply runs it again.
const officerSweepEvery = 6 * time.Hour

// sweepOfficers archives gate officers who are no longer eligible workers, once
// at start and then every officerSweepEvery. Without an employee source it does
// nothing. A failed or doubtful sweep is only logged: nobody is archived on doubt.
func sweepOfficers(ctx context.Context, officers *gateofficer.Store, workers worker.Source) {
	if workers == nil {
		return
	}
	run := func() {
		names, err := officers.ArchiveIneligible(ctx, workers.Eligible)
		switch {
		case err != nil:
			log.Printf("officer eligibility sweep skipped: %v", err)
		case len(names) > 0:
			log.Printf("officer eligibility sweep archived %d officer(s): %v", len(names), names)
		}
	}
	run()
	t := time.NewTicker(officerSweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

// companySource is medpost's company API when it is configured, else nil (the
// Companies page then says so). A nil *HTTPSource must not become a non-nil
// interface, so the nil case returns the untyped nil.
func companySource(cfg config.Config) company.Source {
	if cfg.MedpostURL != "" && cfg.MedpostToken != "" {
		return company.HTTPSource{BaseURL: cfg.MedpostURL, Token: cfg.MedpostToken}
	}
	return nil
}

// companySyncEvery: how often the company mirror is refreshed. medpost's list
// changes rarely; twice a day keeps a new company from waiting long, and an
// admin can press "Refresh now" for an urgent one.
const companySyncEvery = 12 * time.Hour

// syncCompanies refreshes the mirror at start and then every companySyncEvery.
// A failed or doubtful answer is only logged and leaves the saved list as it was.
func syncCompanies(ctx context.Context, store *company.Store, src company.Source) {
	if src == nil {
		return
	}
	run := func() {
		res, err := store.Sync(ctx, src, "system")
		switch {
		case err != nil:
			log.Printf("company sync skipped: %v", err)
		case res.Any():
			log.Printf("company sync: %d companies (%d added, %d changed, %d removed)", res.Total, res.Added, res.Changed, res.Removed)
		}
	}
	run()
	t := time.NewTicker(companySyncEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

// gateKick returns the approval hook, or nil when the gate is not configured.
// It is built here, not inline, so a nil *Syncer never becomes a non-nil func.
func gateKick(s *gatesync.Syncer) func() {
	if s == nil {
		return nil
	}
	return s.Kick
}

// dailyJobs builds the scheduler for the daily jobs and the runner that carries them out
// (the runner also owns the hourly mail drain). The order matters: permits expire first,
// so the departure check sees the locks the expiry job just set; the reviewer reminder and
// the digest come after, so they read the settled state.
// WHY the hours are functions: they come from Settings, so an admin can move a job
// without a restart.
func dailyJobs(database *sql.DB, clock dates.Clock, pol func() policy.Policy, baseURL string, m mail.Mailer, kick func(), who jobs.Recipients) (*jobs.Scheduler, *jobs.Runner) {
	runner := &jobs.Runner{
		Tickets:  ticketlog.Store{DB: database},
		Permits:  permitlog.Store{DB: database},
		Waivers:  waiverlog.Store{DB: database},
		Reviews:  reviewlog.Store{DB: database},
		Dash:     dashboard.Store{DB: database},
		Staff:    who,
		Outbox:   mail.Outbox{DB: database},
		Mailer:   m,
		Clock:    clock,
		Policy:   pol,
		BaseURL:  baseURL,
		GateKick: kick,
	}
	return &jobs.Scheduler{
		DB: database, Clock: clock, Policy: pol,
		Jobs: []jobs.Job{
			{Name: "permit_expiry", Hour: func(p policy.Policy) int { return p.JobHourExpiry }, Run: runner.ExpiryCheck},
			{Name: "departure_check", Hour: func(p policy.Policy) int { return p.JobHourDeparture }, Run: runner.DepartureCheck},
			{Name: "stale_reminder", Hour: func(p policy.Policy) int { return p.JobHourStale }, Run: runner.StaleReminder},
			{Name: "admin_digest", Hour: func(p policy.Policy) int { return p.JobHourDigest }, Run: runner.AdminDigest},
		},
	}, runner
}

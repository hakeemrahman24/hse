package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"
)

// startSOCListener starts the public listener that serves only the SOC pages (plan section
// 8.1b). The Cloudflare tunnel points at it; it is a separate listener from the staff pages, so
// the staff routes do not exist at that address at all.
//
// Each outside service is real when its settings are present and a development stub only when
// SOC_DEV_STUBS=1 is also set. If a service has neither, the listener stays off and says why,
// rather than starting a sign-in page that cannot verify anyone.
//
//	Settings > Medpost API, or MEDPOST_URL + MEDPOST_DIRECTORY_TOKEN   hse-medpost employee directory (real)
//	TURNSTILE_SITEKEY, TURNSTILE_SECRET    Cloudflare Turnstile (real)
//	SOC_DEV_STUBS=1                        fake employees / fake check for whichever is missing
func startSOCListener(db *sql.DB, store *SessionStore, evidenceDir string, ref *refData, companies *companyCache, medpost *medpostConn) {
	dev := os.Getenv("SOC_DEV_STUBS") == "1"

	var dir employeeDirectory
	switch {
	case medpost.Get() != nil:
		// Every call uses the connection configured at that moment, so an administrator can change
		// the address or token in Settings > Medpost API without a restart.
		dir = liveDirectory{conn: medpost}
	case dev:
		dir = newDevDirectory()
		log.Print("SOC: using FAKE employees (SOC_DEV_STUBS=1)")
	default:
		log.Print("SOC public listener is off: set the Medpost API in Settings, or MEDPOST_URL and MEDPOST_DIRECTORY_TOKEN (or SOC_DEV_STUBS=1 for development). Restart after setting it.")
		return
	}

	var tv turnstileVerifier
	sitekey := os.Getenv("TURNSTILE_SITEKEY")
	switch {
	case sitekey != "" && os.Getenv("TURNSTILE_SECRET") != "":
		tv = newCloudflareTurnstile(os.Getenv("TURNSTILE_SECRET"))
	case dev:
		tv = devTurnstile{}
		log.Print("SOC: using a FAKE Cloudflare check (SOC_DEV_STUBS=1)")
	default:
		log.Print("SOC public listener is off: set TURNSTILE_SITEKEY and TURNSTILE_SECRET (or SOC_DEV_STUBS=1 for development).")
		return
	}

	h := newSOCHandlers(store, tv, dir, &observationSink{db: db, dir: evidenceDir})
	h.sitekey = sitekey
	// The page shows the development button only when the fake check is what verifies it.
	_, fakeCheck := tv.(devTurnstile)
	h.devStubs = fakeCheck
	h.evidenceDir = evidenceDir
	h.ref, h.companies = ref, companies
	h.trustCF = os.Getenv("SOC_TRUST_CF_HEADER") == "1"
	h.secureCookie = os.Getenv("SOC_COOKIE_SECURE") == "1"

	addr := envOrDefault("SOC_PUBLIC_ADDR", "127.0.0.1:8112")
	static := http.StripPrefix("/static/", http.FileServer(http.Dir("web/static")))
	log.Printf("SOC public listener on %s", addr)
	go func() {
		if err := http.ListenAndServe(addr, h.routes(static)); err != nil {
			log.Fatalf("SOC listener: %v", err)
		}
	}()
}

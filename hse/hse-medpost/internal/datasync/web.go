package datasync

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// regencyCodeRe is the shape of a regency code in the Region table.
var regencyCodeRe = regexp.MustCompile(`^\d{2}\.\d{2}$`)

// CheckEvery is how often the server asks whether a refresh is due. Hourly is plenty for a
// schedule counted in months, and a server that was off at the due moment catches up on start.
const CheckEvery = time.Hour

// runTimeout bounds one refresh (the region file is about 8 MB).
const runTimeout = 10 * time.Minute

// Service ties the two refreshes to the settings, the scheduler and the admin routes.
type Service struct {
	Store      *Store
	Region     *RegionSync
	Endemicity *EndemicitySync
	running    sync.Mutex // one refresh at a time: both write tables and hit the network
}

// New builds a Service that fetches from the real hosts.
func New(store *Store) *Service {
	return &Service{
		Store:      store,
		Region:     &RegionSync{Store: store, Fetcher: HTTPFetcher{AllowedHosts: RegionHosts}},
		Endemicity: &EndemicitySync{Store: store, Fetcher: HTTPFetcher{AllowedHosts: EndemicityHosts}},
	}
}

// runSource runs one source's refresh. The second result is false when another refresh is running.
func (s *Service) runSource(ctx context.Context, source string) (string, bool, error) {
	if !validSource(source) {
		return "", true, ErrBadSetting
	}
	if !s.running.TryLock() {
		return "", false, nil
	}
	defer s.running.Unlock()
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	if source == SourceRegion {
		msg, err := s.Region.Run(ctx)
		return msg, true, err
	}
	msg, err := s.Endemicity.Run(ctx)
	return msg, true, err
}

// RunDue runs every source whose interval has passed. Failures are logged and recorded; they never
// stop the server.
func (s *Service) RunDue(ctx context.Context) {
	for _, source := range []string{SourceRegion, SourceEndemicity} {
		st, err := s.Store.Get(ctx, source)
		if err != nil {
			log.Printf("datasync: %v (is migration 031/047 applied?)", err)
			continue
		}
		if !st.Due(s.Store.now()) {
			continue
		}
		msg, ran, err := s.runSource(ctx, source)
		switch {
		case !ran:
			log.Printf("datasync: %s skipped, another refresh is running", source)
		case err != nil:
			log.Printf("datasync: %s refresh failed: %v", source, err)
		default:
			log.Printf("datasync: %s: %s", source, msg)
		}
	}
}

// Run checks at start and then every CheckEvery until ctx ends. Start it with go.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(CheckEvery)
	defer t.Stop()
	s.RunDue(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.RunDue(ctx)
		}
	}
}

// --- admin routes (main.go wraps each in RequireAuth + RequireAdmin) ---

type sourceView struct {
	Source         string `json:"source"`
	Enabled        bool   `json:"enabled"`
	IntervalMonths int    `json:"intervalMonths"`
	LastRunAt      string `json:"lastRunAt"`
	LastAttemptAt  string `json:"lastAttemptAt"`
	LastStatus     string `json:"lastStatus"`
	LastMessage    string `json:"lastMessage"`
	AppliedAt      string `json:"appliedAt"`
	NextRun        string `json:"nextRun"` // "" = never run, due now
}

type changeView struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Province string `json:"province"`
	Old      string `json:"old"`
	New      string `json:"new"`
	OldYear  int    `json:"oldYear"`
	NewYear  int    `json:"newYear"`
}

type overview struct {
	Intervals  []int          `json:"intervals"`
	Region     sourceView     `json:"region"`
	Endemicity sourceView     `json:"endemicity"`
	Pending    int            `json:"pending"`
	Changes    []changeView   `json:"changes"`
	Live       int            `json:"live"`
	Counts     map[string]int `json:"counts"`
	Fallback   int            `json:"fallbackRows"` // live rows that come from an older map year
	Labels     []string       `json:"labels"`
}

func view(st Setting) sourceView {
	v := sourceView{
		Source: st.Source, Enabled: st.Enabled, IntervalMonths: st.IntervalMonths, LastRunAt: st.LastRunAt,
		LastAttemptAt: st.LastAttemptAt, LastStatus: st.LastStatus, LastMessage: st.LastMessage, AppliedAt: st.AppliedAt,
	}
	if next := st.NextRun(); !next.IsZero() {
		v.NextRun = next.Format(timestampLayout)
	}
	return v
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

const notMigrated = "The data-source settings are not available. Has the database migration been run?"

// Overview answers GET /api/data-sync.
func (s *Service) Overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	region, err := s.Store.Get(ctx, SourceRegion)
	if err != nil {
		log.Printf("datasync overview: %v", err)
		fail(w, http.StatusInternalServerError, notMigrated)
		return
	}
	endemicity, err := s.Store.Get(ctx, SourceEndemicity)
	if err != nil {
		log.Printf("datasync overview: %v", err)
		fail(w, http.StatusInternalServerError, notMigrated)
		return
	}
	pending, err := s.Store.Pending(ctx)
	if err != nil {
		log.Printf("datasync pending: %v", err)
		fail(w, http.StatusInternalServerError, "db error")
		return
	}
	live, err := s.Store.Live(ctx)
	if err != nil {
		log.Printf("datasync live: %v", err)
		fail(w, http.StatusInternalServerError, "db error")
		return
	}
	out := overview{
		Intervals: Intervals, Region: view(region), Endemicity: view(endemicity), Pending: pending.Rows,
		Changes: []changeView{}, Live: len(live), Counts: map[string]int{}, Labels: EndemicityLabels,
	}
	for _, c := range pending.Changes {
		out.Changes = append(out.Changes, changeView(c))
	}
	newest := 0
	for _, row := range live {
		out.Counts[row.Label]++
		newest = max(newest, row.DataYear)
	}
	for _, row := range live {
		if row.DataYear < newest {
			out.Fallback++
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type appliedRow struct {
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	Province   string  `json:"province"`
	Label      string  `json:"label"`
	Status     string  `json:"status"`
	DataYear   int     `json:"dataYear"`
	Cases      int     `json:"cases"`
	Population int     `json:"population"`
	Per1000    float64 `json:"per1000"`
}

// AppliedRows answers GET /api/data-sync/endemicity: the applied list, one row per regency, for the
// table on the Settings page. About 500 small rows, so it is sent whole and filtered in the browser.
func (s *Service) AppliedRows(w http.ResponseWriter, r *http.Request) {
	live, err := s.Store.Live(r.Context())
	if err != nil {
		log.Printf("datasync applied rows: %v", err)
		fail(w, http.StatusInternalServerError, "db error")
		return
	}
	out := make([]appliedRow, 0, len(live))
	for _, row := range live {
		out = append(out, appliedRow{
			Code: row.Code, Name: row.Name, Province: row.Province, Label: row.Label, Status: Status(row.Label),
			DataYear: row.DataYear, Cases: row.Cases, Population: row.Population, Per1000: math.Round(row.Per1000()*100) / 100,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// Schedule answers PUT /api/data-sync/{source}: the on/off switch and the interval.
func (s *Service) Schedule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled        bool `json:"enabled"`
		IntervalMonths int  `json:"intervalMonths"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "The form could not be read.")
		return
	}
	if err := s.Store.SaveSchedule(r.Context(), r.PathValue("source"), in.Enabled, in.IntervalMonths); err != nil {
		if errors.Is(err, ErrBadSetting) {
			fail(w, http.StatusBadRequest, "Choose 1, 3, 6 or 12 months.")
			return
		}
		log.Printf("datasync schedule: %v", err)
		fail(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// RunNow answers POST /api/data-sync/{source}/run.
func (s *Service) RunNow(w http.ResponseWriter, r *http.Request) {
	source := r.PathValue("source")
	// Detached from the request: a browser that gives up must not leave a half-loaded table.
	msg, ran, err := s.runSource(context.WithoutCancel(r.Context()), source)
	switch {
	case errors.Is(err, ErrBadSetting):
		fail(w, http.StatusNotFound, "Unknown data source.")
	case !ran:
		fail(w, http.StatusConflict, "Another refresh is running. Try again in a minute.")
	case err != nil:
		log.Printf("datasync run %s: %v", source, err)
		fail(w, http.StatusBadGateway, err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]string{"message": msg})
	}
}

// Apply answers POST /api/data-sync/endemicity/apply.
func (s *Service) Apply(w http.ResponseWriter, r *http.Request) {
	switch err := s.Store.ApplyStaged(r.Context()); {
	case errors.Is(err, ErrNothingStaged):
		fail(w, http.StatusConflict, "There is no scraped data waiting to be applied.")
	case err != nil:
		log.Printf("datasync apply: %v", err)
		fail(w, http.StatusInternalServerError, "db error")
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// Discard answers POST /api/data-sync/endemicity/discard.
func (s *Service) Discard(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DiscardStaged(r.Context()); err != nil {
		log.Printf("datasync discard: %v", err)
		fail(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type endemicityView struct {
	Known    bool    `json:"known"`
	Code     string  `json:"code"`
	Name     string  `json:"name,omitempty"`
	Province string  `json:"province,omitempty"`
	Status   string  `json:"status"`          // free, low, medium, high or unknown
	Label    string  `json:"label,omitempty"` // as published by the map
	Free     bool    `json:"free"`            // the map's "Eliminasi": free of malaria
	DataYear int     `json:"dataYear,omitempty"`
	Per1000  float64 `json:"per1000,omitempty"`
}

// RegencyEndemicity answers GET /api/endemicity/{code} for the screening form: what the applied Kemenkes
// list says about one regency (code like "62.12", the same code /api/regions uses). A regency that
// is not on the list, or no list applied yet, is {known:false}, not an error, so the form simply
// shows nothing.
func (s *Service) RegencyEndemicity(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !regencyCodeRe.MatchString(code) {
		fail(w, http.StatusBadRequest, "invalid regency code")
		return
	}
	row, ok, err := s.Store.One(r.Context(), code)
	if err != nil {
		log.Printf("datasync endemicity %s: %v", code, err)
		fail(w, http.StatusInternalServerError, "db error")
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, endemicityView{Code: code, Status: StatusUnknown})
		return
	}
	writeJSON(w, http.StatusOK, endemicityView{
		Known: true, Code: code, Name: row.Name, Province: row.Province, Status: Status(row.Label), Label: row.Label,
		Free: row.Label == "Eliminasi", DataYear: row.DataYear, Per1000: row.Per1000(),
	})
}

// RegisterRead adds the read-only route that any signed-in staff may use; auth wraps it.
func (s *Service) RegisterRead(mux *http.ServeMux, auth func(http.Handler) http.Handler) {
	mux.Handle("GET /api/endemicity/{code}", auth(http.HandlerFunc(s.RegencyEndemicity)))
}

// Register adds the routes; admin wraps each handler with the admin check.
func (s *Service) Register(mux *http.ServeMux, admin func(http.Handler) http.Handler) {
	mux.Handle("GET /api/data-sync", admin(http.HandlerFunc(s.Overview)))
	mux.Handle("GET /api/data-sync/endemicity", admin(http.HandlerFunc(s.AppliedRows)))
	mux.Handle("PUT /api/data-sync/{source}", admin(http.HandlerFunc(s.Schedule)))
	mux.Handle("POST /api/data-sync/{source}/run", admin(http.HandlerFunc(s.RunNow)))
	mux.Handle("POST /api/data-sync/endemicity/apply", admin(http.HandlerFunc(s.Apply)))
	mux.Handle("POST /api/data-sync/endemicity/discard", admin(http.HandlerFunc(s.Discard)))
}

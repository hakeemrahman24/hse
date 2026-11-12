package gatesync

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const testSecret = "test-secret"

// fakeWorker is an in-memory stand-in for the gate Worker's sync routes. It
// follows the same rules the real one does (pull never marks, ack does; an empty
// reference list is skipped; the gate's own ticket fields survive a push) and can
// be told to fail, so the Pi's crash and retry behaviour is tested without Cloudflare.
type fakeWorker struct {
	mu  sync.Mutex
	srv *httptest.Server

	permits map[string]json.RawMessage // lep -> last pushed body
	tickets map[string]map[string]any  // ticket_id -> D1-style row
	refs    map[string]any
	skipped []string

	logs   []map[string]any // gate_log rows, each with "id" and "synced"
	states []map[string]any
	tix    []map[string]any // ticket rows waiting to be pulled

	deactivated map[string][]string // status -> leps, in call order

	pushCalls      []pushBody
	deactCalls     int
	failPushOnCall int  // 1-based push call number that returns 500 (0 = never)
	down           bool // every call returns 503
	dropAcks       bool // acks answer success but record nothing (a lost ack)
	pushNumber     int
	lastAuth       string
}

type pushBody struct {
	Permits      []map[string]any `json:"permits"`
	TripTickets  []map[string]any `json:"tripTickets"`
	References   map[string]any   `json:"references"`
	SecretInBody string           `json:"secret"`
}

func newFakeWorker(t *testing.T) *fakeWorker {
	t.Helper()
	f := &fakeWorker{permits: map[string]json.RawMessage{}, tickets: map[string]map[string]any{}, deactivated: map[string][]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeWorker) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastAuth = r.Header.Get("Authorization")
	if f.down {
		reply(w, 503, map[string]any{"error": "down"})
		return
	}
	if f.lastAuth != "Bearer "+testSecret {
		reply(w, 401, map[string]any{"success": false, "msg": "Unauthorized"})
		return
	}
	raw := map[string]json.RawMessage{}
	_ = json.NewDecoder(r.Body).Decode(&raw)

	switch strings.TrimPrefix(r.URL.Path, "/api/") {
	case "sync":
		f.push(w, raw)
	case "permit/deactivate":
		var body struct {
			Status string   `json:"status"`
			Leps   []string `json:"leps"`
		}
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &body)
		f.deactCalls++
		f.deactivated[body.Status] = append(f.deactivated[body.Status], body.Leps...)
		reply(w, 200, map[string]any{"success": true, "deactivated": len(body.Leps), "status": body.Status})
	case "sync/pull-gatelog":
		reply(w, 200, map[string]any{"success": true, "rows": f.pending(f.logs, limitOf(raw))})
	case "sync/pull-gatestate":
		reply(w, 200, map[string]any{"success": true, "rows": f.pending(f.states, limitOf(raw))})
	case "sync/pull-tickets":
		reply(w, 200, map[string]any{"success": true, "rows": f.pending(f.tix, limitOf(raw))})
	case "sync/ack-gatelog":
		f.ack(w, raw, "ids", f.logs, "id")
	case "sync/ack-gatestate":
		f.ack(w, raw, "leps", f.states, "lep")
	case "sync/ack-tickets":
		f.ack(w, raw, "ticketIds", f.tix, "ticket_id")
	default:
		reply(w, 404, map[string]any{"error": "Not found"})
	}
}

func limitOf(raw map[string]json.RawMessage) int {
	var n int
	_ = json.Unmarshal(raw["limit"], &n)
	if n <= 0 {
		return 100
	}
	if n > 200 {
		return 200
	}
	return n
}

func (f *fakeWorker) pending(rows []map[string]any, limit int) []map[string]any {
	out := []map[string]any{}
	for _, r := range rows {
		if r["synced"] != true && len(out) < limit {
			c := map[string]any{}
			for k, v := range r {
				if k != "synced" {
					c[k] = v
				}
			}
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeWorker) ack(w http.ResponseWriter, raw map[string]json.RawMessage, field string, rows []map[string]any, key string) {
	var keys []any
	_ = json.Unmarshal(raw[field], &keys)
	if !f.dropAcks {
		for _, k := range keys {
			for _, r := range rows {
				if r[key] == k {
					r["synced"] = true
				}
			}
		}
	}
	reply(w, 200, map[string]any{"success": true, "acked": len(keys)})
}

func (f *fakeWorker) push(w http.ResponseWriter, raw map[string]json.RawMessage) {
	f.pushNumber++
	var body pushBody
	b, _ := json.Marshal(raw)
	_ = json.Unmarshal(b, &body)
	f.pushCalls = append(f.pushCalls, body)
	if f.failPushOnCall == f.pushNumber {
		reply(w, 500, map[string]any{"success": false, "msg": "D1 exploded"})
		return
	}
	for _, p := range body.Permits {
		lep, _ := p["lep"].(string)
		pb, _ := json.Marshal(p)
		f.permits[lep] = pb
	}
	for _, t := range body.TripTickets {
		id, _ := t["ticketId"].(string)
		f.mergeTicket(id, t)
	}
	skipped := []string{}
	if refs := body.References; refs != nil {
		if f.refs == nil {
			f.refs = map[string]any{}
		}
		for _, k := range []string{"officers", "gates", "reasons", "completingReasons", "settings"} {
			if v, ok := refs[k]; ok {
				if l, isList := v.([]any); isList && len(l) == 0 {
					skipped = append(skipped, k) // the real Worker skips an empty list
					continue
				}
				f.refs[k] = v
			}
		}
	}
	f.skipped = skipped
	reply(w, 200, map[string]any{"success": true, "permits": len(body.Permits), "tripTickets": len(body.TripTickets), "skipped": skipped})
}

// mergeTicket is the Worker's rule: a push never undoes what the gate recorded.
func (f *fakeWorker) mergeTicket(id string, t map[string]any) {
	cur := f.tickets[id]
	if cur == nil {
		f.tickets[id] = t
		return
	}
	merged := map[string]any{}
	for k, v := range t {
		merged[k] = v
	}
	if rs, _ := cur["recordStart"].(string); rs != "" {
		merged["recordStart"] = rs
	}
	if cur["status"] == "COMPLETED" || (cur["status"] == "ACTIVE" && t["status"] == "PLANNED") {
		merged["status"] = cur["status"]
	}
	f.tickets[id] = merged
}

// ── helpers the tests use to play the gate ───────────────────────────────────

func (f *fakeWorker) addLog(id int, lep, action, at string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs = append(f.logs, map[string]any{"id": float64(id), "timestamp": at, "lep": lep, "name": "Budi", "company": "PT Maju", "action": action,
		"reason": "", "gate_officer": "Sari Gate", "session_id": "S1", "date_only": at[:10], "gate_name": "Gate 1"})
}

func (f *fakeWorker) addState(lep, status, updated string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states = append(f.states, map[string]any{"lep": lep, "current_status": status, "last_action": "checkin", "last_action_time": updated,
		"last_gate_officer": "Sari Gate", "gate_name": "Gate 1", "first_checkin": updated, "last_checkout": "", "reason": "", "updated_at": updated})
}

func (f *fakeWorker) addTicketChange(id, lep, status, recordStart, closedAt, closeReason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tix = append(f.tix, map[string]any{"ticket_id": id, "lep": lep, "status": status, "record_start": recordStart, "closed_at": closedAt, "close_reason": closeReason})
}

func (f *fakeWorker) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *fakeWorker) pushedLeps() map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]bool{}
	for k := range f.permits {
		out[k] = true
	}
	return out
}

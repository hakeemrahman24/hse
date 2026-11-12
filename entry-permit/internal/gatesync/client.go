// Package gatesync keeps the Pi and the gate Worker (Cloudflare Worker + D1) in step.
//
// The Pi starts every sync; nothing on the Pi is exposed to the Worker. The Pi owns
// permits, ticket plans and the reference lists and PUSHES them. The gate owns
// scans, who is inside, and a ticket's first check-in and closing, and the Pi
// PULLS those. See plan section 4.2 and gate-worker/README.md.
package gatesync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	maxResponseBytes = 8 << 20
	maxMessageLength = 200
	callTimeout      = 30 * time.Second
	// jakartaOffset: Jakarta is UTC+7 all year (no daylight saving).
	jakartaOffset = 7 * time.Hour
)

// ── what the Worker is sent ──────────────────────────────────────────────────

// WirePermit is one permit or waiver as the gate needs it. A waiver has an empty
// mcu: no MCU rule applies to visitors.
type WirePermit struct {
	Lep              string `json:"lep"`
	Name             string `json:"name"`
	Company          string `json:"company"`
	Arrival          string `json:"arrival"`
	Departure        string `json:"departure"`
	Mcu              string `json:"mcu"`
	Status           string `json:"status"`
	PermitValidUntil string `json:"permitValidUntil"`
	Location         string `json:"location,omitempty"`
}

// WireTicket is one ticket in the gate's three states (PLANNED, ACTIVE, COMPLETED).
type WireTicket struct {
	TicketID         string `json:"ticketId"`
	Lep              string `json:"lep"`
	Name             string `json:"name"`
	Company          string `json:"company"`
	PlannedArrival   string `json:"plannedArrival"`
	PlannedDeparture string `json:"plannedDeparture"`
	SubmittedAt      string `json:"submittedAt"`
	Status           string `json:"status"`
	RecordStart      string `json:"recordStart"`
	ClosedAt         string `json:"closedAt"`
	CloseReason      string `json:"closeReason"`
	WeeksOnSite      int    `json:"weeksOnSite"`
	MedDeclaration   string `json:"medDeclaration"`
	MedDetails       string `json:"medDetails"`
	LepLocked        bool   `json:"lepLocked"`
}

// WireOfficer is an officer's name and PIN verifier. The PIN itself never leaves the Pi.
type WireOfficer struct {
	Name     string `json:"name"`
	Verifier string `json:"verifier"`
}

// References are the lists the gate page needs. An empty list is left out (nil
// slice, omitted), never sent: the Worker would skip it and the Pi would believe it was sent.
type References struct {
	Officers          []WireOfficer  `json:"officers,omitempty"`
	Gates             []string       `json:"gates,omitempty"`
	Reasons           []string       `json:"reasons,omitempty"`
	CompletingReasons []string       `json:"completingReasons,omitempty"`
	Settings          map[string]int `json:"settings,omitempty"`
}

// PushBody is one /api/sync call.
type PushBody struct {
	Permits     []WirePermit `json:"permits,omitempty"`
	TripTickets []WireTicket `json:"tripTickets,omitempty"`
	References  *References  `json:"references,omitempty"`
}

// PushResult is what the Worker says about a push.
type PushResult struct {
	Skipped []string `json:"skipped"`
}

// ── what the Worker returns ──────────────────────────────────────────────────

// LogRow is one scan, as the Worker stores it.
type LogRow struct {
	ID          int64  `json:"id"`
	Timestamp   string `json:"timestamp"`
	Lep         string `json:"lep"`
	Name        string `json:"name"`
	Company     string `json:"company"`
	Action      string `json:"action"`
	Reason      string `json:"reason"`
	GateOfficer string `json:"gate_officer"`
	SessionID   string `json:"session_id"`
	DateOnly    string `json:"date_only"`
	GateName    string `json:"gate_name"`
}

// StateRow is where one worker is now.
type StateRow struct {
	Lep             string `json:"lep"`
	CurrentStatus   string `json:"current_status"`
	LastAction      string `json:"last_action"`
	LastActionTime  string `json:"last_action_time"`
	LastGateOfficer string `json:"last_gate_officer"`
	FirstCheckin    string `json:"first_checkin"`
	LastCheckout    string `json:"last_checkout"`
	Reason          string `json:"reason"`
	GateName        string `json:"gate_name"`
	UpdatedAt       string `json:"updated_at"`
}

// TicketRow is a ticket as the gate has it; only its gate-owned fields are used.
type TicketRow struct {
	TicketID    string `json:"ticket_id"`
	Lep         string `json:"lep"`
	Status      string `json:"status"`
	RecordStart string `json:"record_start"`
	ClosedAt    string `json:"closed_at"`
	CloseReason string `json:"close_reason"`
}

// ── the client ───────────────────────────────────────────────────────────────

// Client talks to the Worker's sync routes. The secret goes in the Authorization
// header only, and no error or log line contains it.
type Client struct {
	BaseURL string
	Secret  string
	HTTP    *http.Client
}

type envelope struct {
	Success bool   `json:"success"`
	Msg     string `json:"msg"`
}

func (c *Client) call(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("gate worker %s: encode: %w", path, err)
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("gate worker %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("gate worker %s: %w", path, errors.Unwrap(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("gate worker %s: read: %w", path, err)
	}
	var env envelope
	_ = json.Unmarshal(raw, &env)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gate worker %s: HTTP %d %s", path, resp.StatusCode, clip(env.Msg, maxMessageLength))
	}
	if !env.Success {
		return fmt.Errorf("gate worker %s: refused: %s", path, clip(env.Msg, maxMessageLength))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("gate worker %s: bad answer: %w", path, err)
		}
	}
	return nil
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// Push sends one chunk. The Worker applies a chunk as one atomic batch.
func (c *Client) Push(ctx context.Context, b PushBody) (PushResult, error) {
	var res PushResult
	return res, c.call(ctx, "/api/sync", b, &res)
}

// Deactivate marks permits Resigned, Expired or Renewed on the gate.
func (c *Client) Deactivate(ctx context.Context, status string, leps []string) error {
	return c.call(ctx, "/api/permit/deactivate", map[string]any{"status": status, "leps": leps}, nil)
}

type rows[T any] struct {
	Rows []T `json:"rows"`
}

func pull[T any](ctx context.Context, c *Client, path string, limit int) ([]T, error) {
	var r rows[T]
	if err := c.call(ctx, path, map[string]any{"limit": limit}, &r); err != nil {
		return nil, err
	}
	return r.Rows, nil
}

// PullLog, PullState and PullTickets return rows not yet acked. Pulling never
// marks a row, so a row whose ack was lost simply comes back.
func (c *Client) PullLog(ctx context.Context, limit int) ([]LogRow, error) {
	return pull[LogRow](ctx, c, "/api/sync/pull-gatelog", limit)
}

func (c *Client) PullState(ctx context.Context, limit int) ([]StateRow, error) {
	return pull[StateRow](ctx, c, "/api/sync/pull-gatestate", limit)
}

func (c *Client) PullTickets(ctx context.Context, limit int) ([]TicketRow, error) {
	return pull[TicketRow](ctx, c, "/api/sync/pull-tickets", limit)
}

func (c *Client) AckLog(ctx context.Context, ids []int64) error {
	return c.call(ctx, "/api/sync/ack-gatelog", map[string]any{"ids": ids}, nil)
}

func (c *Client) AckState(ctx context.Context, leps []string) error {
	return c.call(ctx, "/api/sync/ack-gatestate", map[string]any{"leps": leps}, nil)
}

func (c *Client) AckTickets(ctx context.Context, ids []string) error {
	return c.call(ctx, "/api/sync/ack-tickets", map[string]any{"ticketIds": ids}, nil)
}

// ── time between the two sides ───────────────────────────────────────────────

// jakartaFromISO turns the Worker's UTC time ("2026-11-10T03:00:00.000Z") into a
// Jakarta moment ("2026-11-10 10:00:00"), or "" when it is not a time.
func jakartaFromISO(s string) string {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return ""
	}
	return t.UTC().Add(jakartaOffset).Format("2006-01-02 15:04:05")
}

// isoFromJakarta is the reverse, for what the Pi sends to the Worker.
func isoFromJakarta(s string) string {
	t, err := time.Parse("2006-01-02 15:04:05", strings.TrimSpace(s))
	if err != nil {
		return ""
	}
	return t.Add(-jakartaOffset).Format("2006-01-02T15:04:05.000Z")
}

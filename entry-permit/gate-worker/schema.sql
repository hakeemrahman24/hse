-- gate-db schema (prod + revamped app). Apply with:
--   wrangler d1 execute entrypermitrevamp --remote --file=schema.sql

CREATE TABLE IF NOT EXISTS permits (
  lep TEXT PRIMARY KEY,
  name TEXT,
  company TEXT,
  arrival TEXT,
  departure TEXT,
  mcu TEXT,
  status TEXT,
  is_active INTEGER DEFAULT 1,
  synced_at TEXT,
  gate_status TEXT DEFAULT 'OUTSIDE',
  last_checkin TEXT,
  last_checkout TEXT,
  check_in_date TEXT,
  check_out_date TEXT,
  total_actual_days INTEGER DEFAULT 0,
  location TEXT,
  request_type TEXT,
  purpose TEXT,
  total_days TEXT,
  malaria_status TEXT,
  permit_valid_until TEXT,
  id_photo_url TEXT
);

CREATE TABLE IF NOT EXISTS gate_state (
  lep TEXT PRIMARY KEY,
  name TEXT,
  company TEXT,
  arrival TEXT,
  departure TEXT,
  mcu TEXT,
  current_status TEXT DEFAULT 'OUTSIDE',
  last_action TEXT,
  last_action_time TEXT,
  last_gate_officer TEXT,
  last_session_id TEXT,
  first_checkin TEXT,
  last_checkout TEXT,
  last_action_hash TEXT,
  last_action_hash_time TEXT,
  reason TEXT,
  gate_name TEXT,
  is_active INTEGER DEFAULT 1,
  synced_to_sheet INTEGER DEFAULT 0,
  updated_at TEXT
);

CREATE TABLE IF NOT EXISTS gate_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp TEXT,
  lep TEXT,
  name TEXT,
  company TEXT,
  action TEXT,
  reason TEXT,
  gate_officer TEXT,
  session_id TEXT,
  date_only TEXT,
  gate_name TEXT,
  synced_to_sheet INTEGER DEFAULT 0,
  created_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS reference (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  type TEXT,
  code TEXT,
  value TEXT,
  extra TEXT
);

-- Trip / rotation ticket (plan + on-site monitoring). Source: Ticket.html → Apps Script → D1.
CREATE TABLE IF NOT EXISTS trip_tickets (
  ticket_id TEXT PRIMARY KEY,
  lep TEXT NOT NULL,
  name TEXT,
  company TEXT,
  planned_arrival TEXT,
  planned_departure TEXT,
  submitted_at TEXT,
  submitted_by TEXT,
  status TEXT DEFAULT 'PLANNED',
  record_start TEXT,
  closed_at TEXT,
  close_reason TEXT,
  weeks_on_site INTEGER DEFAULT 0,
  med_declaration TEXT,
  med_details TEXT,
  medicine_photo_url TEXT,
  lep_locked INTEGER DEFAULT 0,
  synced_to_sheet INTEGER DEFAULT 0,
  created_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_trip_tickets_lep ON trip_tickets(lep);
CREATE INDEX IF NOT EXISTS idx_trip_tickets_lep_status ON trip_tickets(lep, status);

-- Wrong-PIN counter per officer name (slice 5a). WHY a table: a Worker keeps no
-- memory between requests, and the PIN is now checked on the server, so guessing
-- needs a limit that survives restarts.
CREATE TABLE IF NOT EXISTS officer_attempts (
  key TEXT PRIMARY KEY,        -- lower-cased officer name
  fails INTEGER DEFAULT 0,
  locked_until TEXT,           -- ISO moment; empty or past means not locked
  updated_at TEXT
);

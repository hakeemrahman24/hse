// The scanner side of the Worker: lookup, action, references and officer login.
// The rules themselves are in rules.js; this file reads D1, calls them and writes.

import { checkOfficer } from './officers.js';
import {
  DEFAULT_COMPLETING_REASONS, DEFAULT_DUPLICATE_SECONDS, completesTicket, evaluate, isDuplicate,
} from './rules.js';
import { isWaiver, jakartaYmd, json, makeSessionId, nowOf, normalizeLep } from './util.js';

const MIN_REASON_LENGTH = 3;

function serializeTicket(t) {
  if (!t) return null;
  return {
    ticketId: t.ticket_id, status: t.status, plannedArrival: t.planned_arrival,
    plannedDeparture: t.planned_departure, submittedAt: t.submitted_at || null,
  };
}

function extras(permit) {
  const url = permit && permit.id_photo_url ? String(permit.id_photo_url).trim() : '';
  return { idPhotoUrl: url || null, isWaiver: isWaiver(permit && permit.lep) };
}

async function openTickets(env, lep) {
  const { results } = await env.gate_db
    .prepare("SELECT * FROM trip_tickets WHERE lep = ? AND status IN ('PLANNED','ACTIVE') ORDER BY planned_arrival ASC, submitted_at ASC")
    .bind(lep)
    .all();
  return results || [];
}

/** Gate settings pushed from the Pi, with the decided defaults when none were pushed yet. */
async function gateSettings(env) {
  const { results } = await env.gate_db
    .prepare("SELECT type, code, value FROM reference WHERE type IN ('completing_reason', 'setting')")
    .all();
  const rows = results || [];
  const completing = rows.filter((r) => r.type === 'completing_reason').map((r) => r.value);
  const dup = Number(rows.find((r) => r.type === 'setting' && r.code === 'gate_duplicate_scan_seconds')?.value);
  return {
    completing: completing.length ? completing : DEFAULT_COMPLETING_REASONS,
    duplicateSeconds: Number.isFinite(dup) && dup >= 0 ? dup : DEFAULT_DUPLICATE_SECONDS,
  };
}

// GET /api/lookup?lep=
export async function handleLookup(request, env) {
  const lep = normalizeLep(new URL(request.url).searchParams.get('lep'));
  if (!lep) return json({ success: false, msg: 'LEP is required.' });

  const permit = await env.gate_db.prepare('SELECT * FROM permits WHERE lep = ?').bind(lep).first();
  if (!permit) return json({ success: false, msg: 'LEP Not Found' });
  const state = await env.gate_db.prepare('SELECT * FROM gate_state WHERE lep = ?').bind(lep).first();
  const tickets = await openTickets(env, lep);
  const today = jakartaYmd(nowOf(env));

  const ev = evaluate({ permit, state, tickets, today });
  if (!ev.ok) {
    return json({
      success: false, msg: ev.msg, lep: permit.lep, name: permit.name, company: permit.company,
      arrival: ev.arrival, departure: ev.departure, mcu: permit.mcu, permitValidUntil: ev.permitValidUntil, ...extras(permit),
    });
  }
  const inGate = ev.currentStatus === 'INSIDE' || ev.currentStatus === 'OVERDUE';
  return json({
    success: true, lep: permit.lep, name: permit.name, company: permit.company,
    arrival: ev.arrival, departure: ev.departure, mcu: permit.mcu, malariaStatus: permit.malaria_status,
    permitValidUntil: ev.permitValidUntil, departureRaw: ev.departure, arrivalRaw: ev.arrival, mcuRaw: permit.mcu,
    checkinRaw: state ? state.first_checkin : null,
    checkinGate: inGate ? String((state && state.gate_name) || '').trim() || null : null,
    earlyArrival: ev.earlyArrival, daysBeforeEarliest: ev.daysBeforeEarliest,
    status: ev.currentStatus, statusClass: ev.currentStatus.toLowerCase(),
    mode: ev.mode, isOverdue: ev.isOverdue, forceEligible: ev.forceEligible, resigned: ev.resigned,
    ...extras(permit), tripTicket: serializeTicket(ev.ticket), availableTickets: tickets.map(serializeTicket),
  });
}

// GET /api/references: names only. Codes and verifiers never leave the Worker.
export async function handleReferences(request, env) {
  const { results } = await env.gate_db.prepare('SELECT type, code, value FROM reference ORDER BY type, value').all();
  const rows = results || [];
  const of = (type) => rows.filter((r) => r.type === type).map((r) => r.value);
  return json({
    gates: of('gate'),
    officers: of('officer').map((name) => ({ name })),
    reasons: of('checkout_reason'),
    completingReasons: of('completing_reason'),
  });
}

// POST /api/officer/login { name, code }
export async function handleOfficerLogin(request, env) {
  let body;
  try { body = await request.json(); } catch { return json({ success: false, msg: 'Invalid JSON.' }); }
  const res = await checkOfficer(env, body.name, body.code);
  return res.ok ? json({ success: true, name: res.name }) : json({ success: false, msg: res.msg });
}

const REFUSALS = {
  NO_TICKET: 'No valid trip ticket — submit rotation plan first.',
  LOCKED: 'LEP locked — maximum on-site period reached.',
};

// POST /api/action { lep, action, gateName, officerName, officerCode, reason, sessionId }
export async function handleAction(request, env) {
  let body;
  try { body = await request.json(); } catch { return json({ success: false, msg: 'Invalid JSON.' }); }

  const lep = normalizeLep(body.lep);
  const action = String(body.action || '').trim().toLowerCase();
  const gateName = String(body.gateName || '').trim();
  const officerName = String(body.officerName || '').trim();
  const officerCode = String(body.officerCode || '').trim();
  const reason = String(body.reason || '').trim();
  const sessionId = body.sessionId || makeSessionId(env);

  if (!lep) return json({ success: false, msg: 'LEP is required.' });
  if (!['checkin', 'checkout', 'force'].includes(action)) return json({ success: false, msg: 'Invalid action.' });
  if (!gateName) return json({ success: false, msg: 'Gate name is required.' });
  if (!officerName || !officerCode) return json({ success: false, msg: 'Officer is required.' });

  const officer = await checkOfficer(env, officerName, officerCode);
  if (!officer.ok) return json({ success: false, msg: officer.msg });

  const db = env.gate_db;
  const gate = await db.prepare("SELECT value FROM reference WHERE type = 'gate' AND value = ?").bind(gateName).first();
  if (!gate) return json({ success: false, msg: 'Gate name is required.' });
  const permit = await db.prepare('SELECT * FROM permits WHERE lep = ?').bind(lep).first();
  if (!permit) return json({ success: false, msg: 'LEP Not Found' });
  const state = await db.prepare('SELECT * FROM gate_state WHERE lep = ?').bind(lep).first();
  const tickets = await openTickets(env, lep);
  const settings = await gateSettings(env);

  const now = nowOf(env);
  const nowIso = now.toISOString();
  const today = jakartaYmd(now);

  // The same decision the lookup makes. Only a worker who is not inside can be refused here;
  // someone inside always gets through to check-out.
  const ev = evaluate({ permit, state, tickets, today });
  if (!ev.ok) return json({ success: false, msg: REFUSALS[ev.code] || ev.msg });

  const key = [lep, action, officerName.toLowerCase()].join('|');
  if (isDuplicate(state, key, now.getTime(), settings.duplicateSeconds)) {
    return json({ success: false, msg: 'Duplicate scan blocked. Please wait a few seconds.' });
  }

  const officerLabel = officer.name;
  const stateWrite = (status, act, extra) => db
    .prepare(`UPDATE gate_state SET current_status = ?, last_action = ?, last_action_time = ?, last_gate_officer = ?, last_session_id = ?,
              ${extra.checkout ? 'last_checkout = ?, reason = ?,' : ''} gate_name = ?, last_action_hash = ?, last_action_hash_time = ?,
              synced_to_sheet = 0, updated_at = ? WHERE lep = ?`)
    .bind(status, act, nowIso, officerLabel, sessionId, ...(extra.checkout ? [nowIso, extra.reason] : []), gateName, key, nowIso, nowIso, lep);
  const permitWrite = (gateStatus, col) => db.prepare(`UPDATE permits SET gate_status = ?, ${col} = ? WHERE lep = ?`).bind(gateStatus, nowIso, lep);
  const logWrite = (logReason) => db
    .prepare(`INSERT INTO gate_log (timestamp, lep, name, company, action, reason, gate_officer, session_id, date_only, gate_name, synced_to_sheet, created_at)
              VALUES (?,?,?,?,?,?,?,?,?,?,0,?)`)
    .bind(nowIso, lep, permit.name, permit.company, action, logReason, officerLabel, sessionId, today, gateName, nowIso);

  let stmts;
  let msg;
  if (action === 'checkin') {
    if (ev.currentStatus === 'INSIDE') {
      return json({ success: false, msg: 'Already checked in — another gate or officer may have just completed this. Refresh to see the latest status.' });
    }
    if (ev.currentStatus === 'OVERDUE') {
      return json({ success: false, msg: 'Cannot check in — worker is overdue on site. Complete check out first (regular or force if required).' });
    }
    const firstCheckIn = state && state.first_checkin ? state.first_checkin : nowIso;
    stmts = [];
    if (ev.ticket && ev.ticket.status === 'PLANNED') {
      stmts.push(db.prepare("UPDATE trip_tickets SET status = 'ACTIVE', record_start = ?, synced_to_sheet = 0 WHERE ticket_id = ?").bind(nowIso, ev.ticket.ticket_id));
    }
    stmts.push(
      db.prepare(`INSERT INTO gate_state (lep, name, company, arrival, departure, mcu, current_status, last_action, last_action_time,
                    last_gate_officer, last_session_id, first_checkin, last_action_hash, last_action_hash_time, gate_name, is_active, synced_to_sheet, updated_at)
                  VALUES (?,?,?,?,?,?, 'INSIDE', 'checkin', ?,?,?,?,?,?,?,1,0,?)
                  ON CONFLICT(lep) DO UPDATE SET current_status = 'INSIDE', last_action = 'checkin', last_action_time = excluded.last_action_time,
                    last_gate_officer = excluded.last_gate_officer, last_session_id = excluded.last_session_id,
                    first_checkin = CASE WHEN first_checkin IS NULL THEN excluded.first_checkin ELSE first_checkin END,
                    last_action_hash = excluded.last_action_hash, last_action_hash_time = excluded.last_action_hash_time,
                    gate_name = excluded.gate_name, synced_to_sheet = 0, updated_at = excluded.updated_at`)
        .bind(lep, permit.name, permit.company, permit.arrival, permit.departure, permit.mcu, nowIso, officerLabel, sessionId,
          firstCheckIn, key, nowIso, gateName, nowIso),
      db.prepare("UPDATE permits SET gate_status = 'INSIDE', last_checkin = ? WHERE lep = ?").bind(nowIso, lep),
      logWrite(reason),
    );
    msg = 'CHECK-IN SUCCESS';
  } else if (action === 'checkout') {
    if (ev.currentStatus !== 'INSIDE') {
      return json({ success: false, msg: 'Not marked inside — status may have changed (another checkout, or refresh needed). Refresh and try again if unsure.' });
    }
    // Everything is checked BEFORE anything is written, then written in one batch:
    // a refused check-out must never leave a ticket closed (gate rules 3.1.2).
    if (ev.forceEligible) {
      return json({ success: false, msg: 'Regular check-out is not available — use force check-out (more than 3 days past scheduled departure, or the LEP validity has ended).' });
    }
    if (reason.length < MIN_REASON_LENGTH) {
      return json({ success: false, msg: 'Check-out reason is required (minimum 3 characters).' });
    }
    stmts = [];
    if (ev.ticket && completesTicket(reason, settings.completing)) {
      stmts.push(db.prepare("UPDATE trip_tickets SET status = 'COMPLETED', closed_at = ?, close_reason = ?, synced_to_sheet = 0 WHERE ticket_id = ?").bind(nowIso, reason, ev.ticket.ticket_id));
    }
    stmts.push(stateWrite('OUTSIDE', 'checkout', { checkout: true, reason }), permitWrite('OUTSIDE', 'last_checkout'), logWrite(reason));
    msg = 'CHECK-OUT SUCCESS';
  } else {
    if (ev.currentStatus !== 'INSIDE') return json({ success: false, msg: 'Worker is not currently inside.' });
    if (!ev.forceEligible) {
      return json({ success: false, msg: 'Force check-out is only used after more than 3 days past scheduled departure, or when the LEP validity has ended — use regular check-out.' });
    }
    const forceReason = reason || 'Force check-out';
    stmts = [stateWrite('OUTSIDE', 'force', { checkout: true, reason: forceReason }), permitWrite('OUTSIDE', 'last_checkout'), logWrite(forceReason)];
    msg = 'FORCE-CHECKOUT RECORDED';
  }

  await db.batch(stmts);
  return json({ success: true, msg, officer: officerLabel });
}



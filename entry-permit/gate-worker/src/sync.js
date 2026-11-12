// The sync side of the Worker: what the Pi calls every few minutes.
// The Pi owns permits, tickets' plans and the reference lists; the gate owns gate
// log, gate state and a ticket's first check-in and closing (gate rules section 5).

import { batchChunks, formatDate, json, nowISO, normalizeLep, safeEqual } from './util.js';

const PUSH_CHUNK = 40; // statements per D1 batch
const ACK_CHUNK = 80;
const DEFAULT_PULL = 100;
const MAX_PULL = 200;

function pullLimit(body) {
  const n = Number(body && body.limit);
  return Number.isFinite(n) && n > 0 ? Math.min(MAX_PULL, Math.floor(n)) : DEFAULT_PULL;
}

/**
 * The secret comes as "Authorization: Bearer <secret>" or, during the cut-over,
 * as "secret" in the body. A Worker with no SYNC_SECRET set refuses everything:
 * the old check compared two undefined values and let any caller in.
 */
async function authorized(request, body, env) {
  const secret = env.SYNC_SECRET;
  if (!secret) return false;
  const header = request.headers.get('Authorization') || '';
  const bearer = header.startsWith('Bearer ') ? header.slice(7).trim() : '';
  if (bearer && (await safeEqual(bearer, secret))) return true;
  const fromBody = body && typeof body.secret === 'string' ? body.secret : '';
  return !!fromBody && (await safeEqual(fromBody, secret));
}

function guarded(handler) {
  return async (request, env) => {
    let body;
    try { body = await request.json(); } catch { return json({ success: false, msg: 'Invalid JSON.' }); }
    if (!(await authorized(request, body, env))) return json({ success: false, msg: 'Unauthorized' }, 401);
    try {
      return await handler(body || {}, env);
    } catch (err) {
      console.error('sync error:', err);
      return json({ success: false, msg: String((err && err.message) || err) }, 500);
    }
  };
}

const PERMIT_UPSERT = `
  INSERT INTO permits (lep, name, company, arrival, departure, mcu, status, malaria_status,
    gate_status, check_in_date, check_out_date, total_actual_days, location, request_type,
    purpose, total_days, is_active, synced_at, permit_valid_until, id_photo_url)
  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,?,?,?)
  ON CONFLICT(lep) DO UPDATE SET
    name = excluded.name, company = excluded.company, arrival = excluded.arrival, departure = excluded.departure,
    mcu = excluded.mcu, status = excluded.status, malaria_status = excluded.malaria_status,
    gate_status = excluded.gate_status, check_in_date = excluded.check_in_date, check_out_date = excluded.check_out_date,
    total_actual_days = excluded.total_actual_days, location = excluded.location, request_type = excluded.request_type,
    purpose = excluded.purpose, total_days = excluded.total_days, permit_valid_until = excluded.permit_valid_until,
    id_photo_url = excluded.id_photo_url, is_active = 1, synced_at = excluded.synced_at`;

const TICKET_UPSERT = `
  INSERT INTO trip_tickets (ticket_id, lep, name, company, planned_arrival, planned_departure,
    submitted_at, submitted_by, status, record_start, closed_at, close_reason,
    weeks_on_site, med_declaration, med_details, medicine_photo_url, lep_locked, synced_to_sheet, created_at)
  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,?)
  ON CONFLICT(ticket_id) DO UPDATE SET
    lep = excluded.lep, name = excluded.name, company = excluded.company,
    planned_arrival = excluded.planned_arrival, planned_departure = excluded.planned_departure,
    submitted_at = excluded.submitted_at,
    -- WHY: the gate owns a ticket's first check-in and its closing; the Pi owns the plan.
    -- A push must never undo a scan, so the gate's values win when it has set them.
    status = CASE WHEN trip_tickets.status = 'COMPLETED' THEN 'COMPLETED'
                  WHEN trip_tickets.status = 'ACTIVE' AND excluded.status = 'PLANNED' THEN 'ACTIVE'
                  ELSE excluded.status END,
    record_start = COALESCE(NULLIF(trip_tickets.record_start, ''), excluded.record_start),
    closed_at = CASE WHEN trip_tickets.status = 'COMPLETED' THEN trip_tickets.closed_at ELSE excluded.closed_at END,
    close_reason = CASE WHEN trip_tickets.status = 'COMPLETED' THEN trip_tickets.close_reason ELSE excluded.close_reason END,
    weeks_on_site = excluded.weeks_on_site,
    med_declaration = excluded.med_declaration, med_details = excluded.med_details,
    medicine_photo_url = excluded.medicine_photo_url, lep_locked = excluded.lep_locked`;

// POST /api/sync { permits, tripTickets, references }
const handleSync = guarded(async (body, env) => {
  const db = env.gate_db;
  const refs = body.references || {};
  const stmts = [];
  let permitCount = 0;
  let ticketCount = 0;

  for (const p of Array.isArray(body.permits) ? body.permits : []) {
    const lep = normalizeLep(p.lep);
    if (!lep) continue;
    stmts.push(db.prepare(PERMIT_UPSERT).bind(
      lep, p.name, p.company, formatDate(p.arrival), formatDate(p.departure), formatDate(p.mcu),
      p.status || 'Approved', p.malariaStatus || null, p.gateStatus || 'OUTSIDE',
      p.checkInDate || null, p.checkOutDate || null, p.totalActualDays || 0,
      p.location || null, p.requestType || null, p.purpose || null, p.totalDays || null,
      nowISO(env), formatDate(p.permitValidUntil) || formatDate(p.mcu), p.idPhotoUrl || p.id_photo_url || null,
    ));
    permitCount++;
  }

  for (const t of Array.isArray(body.tripTickets) ? body.tripTickets : []) {
    const ticketId = String(t.ticketId || t.ticket_id || '').trim();
    const lep = normalizeLep(t.lep);
    if (!ticketId || !lep) continue;
    const now = nowISO(env);
    stmts.push(db.prepare(TICKET_UPSERT).bind(
      ticketId, lep, t.name || '', t.company || '', formatDate(t.plannedArrival), formatDate(t.plannedDeparture),
      t.submittedAt || now, t.submittedBy || '', t.status || 'PLANNED', t.recordStart || null, t.closedAt || null,
      t.closeReason || null, Number(t.weeksOnSite) || 0, t.medDeclaration || 'No', t.medDetails || '',
      t.medicinePhotoUrl || '', t.lepLocked ? 1 : 0, now,
    ));
    // A planned or active ticket also sets the permit's schedule shown at the gate.
    if (t.status === 'PLANNED' || t.status === 'ACTIVE' || !t.status) {
      stmts.push(db.prepare('UPDATE permits SET arrival = ?, departure = ?, synced_at = ? WHERE lep = ?')
        .bind(formatDate(t.plannedArrival), formatDate(t.plannedDeparture), now, lep));
    }
    ticketCount++;
  }
  await batchChunks(db, stmts, PUSH_CHUNK);

  const skipped = [];
  let refCount = 0;
  const ins = (type, code, value, extra) => db.prepare('INSERT INTO reference (type, code, value, extra) VALUES (?, ?, ?, ?)').bind(type, code, value, extra ?? null);
  const list = async (type, label, items) => {
    if (!Array.isArray(items)) return;
    if (!items.length) { skipped.push(label); return; }
    await db.batch([db.prepare('DELETE FROM reference WHERE type = ?').bind(type), ...items]);
    refCount += items.length;
  };
  await list('officer', 'officers', Array.isArray(refs.officers)
    ? refs.officers.filter((o) => (o.verifier || o.code) && o.name).map((o) => ins('officer', o.verifier || o.code, o.name, o.officer))
    : refs.officers);
  await list('gate', 'gates', Array.isArray(refs.gates) ? refs.gates.filter(Boolean).map((g) => ins('gate', g, g)) : refs.gates);
  await list('checkout_reason', 'reasons', Array.isArray(refs.reasons) ? refs.reasons.filter(Boolean).map((r) => ins('checkout_reason', r, r)) : refs.reasons);
  await list('completing_reason', 'completingReasons', Array.isArray(refs.completingReasons) ? refs.completingReasons.filter(Boolean).map((r) => ins('completing_reason', r, r)) : refs.completingReasons);
  if (refs.settings && typeof refs.settings === 'object') {
    const stmts2 = [];
    for (const [k, v] of Object.entries(refs.settings)) {
      stmts2.push(db.prepare("DELETE FROM reference WHERE type = 'setting' AND code = ?").bind(k), ins('setting', k, String(v)));
    }
    if (stmts2.length) await db.batch(stmts2);
  }

  return json({ success: true, permits: permitCount, tripTickets: ticketCount, references: refCount, skipped, synced_at: nowISO(env) });
});

// Pull never marks a row; the caller acks after it has saved the rows (at least once).
const pull = (table, order) => guarded(async (body, env) => {
  const { results } = await env.gate_db
    .prepare(`SELECT * FROM ${table} WHERE synced_to_sheet = 0 ORDER BY ${order} ASC LIMIT ?`)
    .bind(pullLimit(body))
    .all();
  return json({ success: true, rows: results });
});

const ack = (table, key, field, clean) => guarded(async (body, env) => {
  const keys = (Array.isArray(body[field]) ? body[field] : []).map(clean).filter((k) => k !== null && k !== '');
  if (!keys.length) return json({ success: true, acked: 0 });
  await batchChunks(env.gate_db, keys.map((k) => env.gate_db.prepare(`UPDATE ${table} SET synced_to_sheet = 1 WHERE ${key} = ?`).bind(k)), ACK_CHUNK);
  return json({ success: true, acked: keys.length });
});

const positiveInt = (v) => {
  const n = Number(v);
  return Number.isInteger(n) && n > 0 ? n : null;
};
const upper = (v) => String(v || '').trim().toUpperCase();

export const handlePullGatelog = pull('gate_log', 'created_at');
export const handleAckGatelog = ack('gate_log', 'id', 'ids', positiveInt);
export const handlePullGatestate = pull('gate_state', 'updated_at');
export const handleAckGatestate = ack('gate_state', 'lep', 'leps', upper);
export const handlePullTickets = pull('trip_tickets', 'created_at');
export const handleAckTickets = ack('trip_tickets', 'ticket_id', 'ticketIds', (v) => String(v || '').trim());

// POST /api/permit/deactivate { status, leps }
// A worker still inside stays INSIDE so check-out remains possible.
export const handlePermitDeactivate = guarded(async (body, env) => {
  const status = String(body.status || 'Expired').trim() || 'Expired';
  const leps = (Array.isArray(body.leps) ? body.leps : []).map(upper).filter(Boolean);
  if (!leps.length) return json({ success: true, deactivated: 0 });
  const db = env.gate_db;
  const now = nowISO(env);
  for (const lep of leps) {
    const p = await db.prepare('SELECT gate_status FROM permits WHERE lep = ?').bind(lep).first();
    const inside = p && String(p.gate_status || '').toUpperCase() === 'INSIDE';
    if (inside) {
      await db.prepare('UPDATE permits SET status = ?, is_active = 0, synced_at = ? WHERE lep = ?').bind(status, now, lep).run();
    } else {
      await db.batch([
        db.prepare("UPDATE permits SET status = ?, is_active = 0, gate_status = 'OUTSIDE', synced_at = ? WHERE lep = ?").bind(status, now, lep),
        db.prepare('UPDATE gate_state SET is_active = 0, synced_to_sheet = 0, updated_at = ? WHERE lep = ?').bind(now, lep),
      ]);
    }
  }
  return json({ success: true, deactivated: leps.length, status });
});

// POST /api/sync/reset-sync-flags { scope }: recovery, rows are pulled again.
export const handleResetSyncFlags = guarded(async (body, env) => {
  const scope = String(body.scope || 'all').toLowerCase();
  const reset = { gatelog: 0, gatestate: 0 };
  if (scope === 'gatelog' || scope === 'all') {
    reset.gatelog = (await env.gate_db.prepare('UPDATE gate_log SET synced_to_sheet = 0 WHERE synced_to_sheet = 1').run()).meta?.changes ?? 0;
  }
  if (scope === 'gatestate' || scope === 'all') {
    reset.gatestate = (await env.gate_db.prepare('UPDATE gate_state SET synced_to_sheet = 0 WHERE synced_to_sheet = 1').run()).meta?.changes ?? 0;
  }
  return json({ success: true, reset });
});

export { handleSync };

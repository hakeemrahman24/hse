// The gate rules as pure functions: no D1, no clock, no Response.
// lookup and action both call evaluate(), so a scan page left open or a direct
// API call can never get past a check the lookup would have made (gate rules 3.1.1).

import { addDays, cmpYmd, daysBetween, isResigned, ymdKey } from './util.js';

/** Days after the planned departure that a worker may still enter or leave normally. */
export const GRACE_DAYS = 3;
/** A worker may arrive this many days before the planned arrival without a warning. */
export const EARLY_DAYS = 3;

export const DEFAULT_COMPLETING_REASONS = ['Sesuai jadwal keluar', 'Selesai awal'];
export const DEFAULT_DUPLICATE_SECONDS = 5;

function ticketCoversDay(ticket, day) {
  const arr = ymdKey(ticket && ticket.planned_arrival);
  const dep = ymdKey(ticket && ticket.planned_departure);
  if (!arr || !dep || !day) return false;
  return cmpYmd(day, arr) >= 0 && cmpYmd(day, dep) <= 0;
}

/**
 * The ticket a scan uses (tickets doc 4). Future rotations stay listed but are
 * not used for the scan.
 */
export function pickGateTicket(tickets, day, onSite) {
  if (!tickets || !tickets.length) return null;
  const covering = tickets.filter((t) => ticketCoversDay(t, day));
  if (covering.length) {
    covering.sort((a, b) => {
      if (a.status === 'ACTIVE' && b.status !== 'ACTIVE') return -1;
      if (b.status === 'ACTIVE' && a.status !== 'ACTIVE') return 1;
      return cmpYmd(ymdKey(a.planned_arrival), ymdKey(b.planned_arrival));
    });
    return covering[0];
  }
  if (onSite) {
    const started = tickets.filter((t) => {
      const arr = ymdKey(t.planned_arrival);
      return arr && cmpYmd(day, arr) >= 0;
    });
    if (started.length) {
      started.sort((a, b) => cmpYmd(ymdKey(b.planned_departure), ymdKey(a.planned_departure)));
      return started[0];
    }
  }
  const notEnded = tickets.filter((t) => {
    const dep = ymdKey(t.planned_departure);
    return !dep || cmpYmd(day, dep) <= 0;
  });
  if (notEnded.length) {
    notEnded.sort((a, b) => cmpYmd(ymdKey(a.planned_arrival), ymdKey(b.planned_arrival)));
    return notEnded[0];
  }
  return tickets[0];
}

export function scheduleOf(permit, ticket) {
  if (ticket && ticket.planned_arrival) {
    return { arrival: ticket.planned_arrival, departure: ticket.planned_departure || permit.departure };
  }
  return { arrival: permit.arrival, departure: permit.departure };
}

/**
 * Decide what the gate may do for one permit on one day.
 *
 * Returns { ok: false, code, msg, ... } when check-in is refused, or
 * { ok: true, mode, forceEligible, ... } with what the scanner needs.
 * Codes: RESIGNED, INACTIVE, NO_TICKET, LOCKED, EXPIRED.
 *
 * The LEP valid-until (permit_valid_until) is what ends a worker's right to
 * enter, not the raw MCU date (decided 2026-11-06): it is the earliest of the
 * MCU date less the grace, the contract, the visa and the passport.
 */
export function evaluate({ permit, state, tickets, today }) {
  const currentStatus = state ? state.current_status : 'OUTSIDE';
  const onSite = permit.gate_status === 'INSIDE' || currentStatus === 'INSIDE' || currentStatus === 'OVERDUE';
  const resigned = isResigned(permit.status);
  const validUntil = ymdKey(permit.permit_valid_until) || ymdKey(permit.mcu) || null;

  const ticket = pickGateTicket(tickets, today, onSite);
  const sched = scheduleOf(permit, ticket);
  const arrival = ymdKey(sched.arrival);
  const departure = ymdKey(sched.departure);
  const info = {
    onSite, resigned, currentStatus, ticket, sched, validUntil,
    lep: permit.lep, name: permit.name, company: permit.company,
    arrival: sched.arrival, departure: sched.departure, mcu: permit.mcu, permitValidUntil: validUntil,
  };
  const refuse = (code, msg) => ({ ok: false, code, msg, ...info });

  // A worker already inside may always be checked out, whatever happened to the
  // permit since (resigned, expired, deactivated): refusing them would trap them
  // on site. Everyone else needs an active, Approved permit.
  if (!onSite) {
    if (resigned) return refuse('RESIGNED', 'Worker has resigned — check-in not allowed.');
    if (!permit.is_active || permit.status !== 'Approved') return refuse('INACTIVE', 'LEP is inactive.');
    if (!ticket) return refuse('NO_TICKET', 'NO TRIP TICKET');
    if (ticket.lep_locked) return refuse('LOCKED', 'LEP LOCKED');
    if (departure && daysBetween(departure, today) > GRACE_DAYS) return refuse('EXPIRED', 'LEP EXPIRED');
    if (validUntil && today > validUntil) return refuse('EXPIRED', 'LEP EXPIRED');
  }

  let earlyArrival = false;
  let daysBeforeEarliest = 0;
  if (arrival && !onSite) {
    const earliest = addDays(arrival, -EARLY_DAYS);
    if (today < earliest) {
      earlyArrival = true;
      daysBeforeEarliest = daysBetween(today, earliest);
    }
  }

  const inside = currentStatus === 'INSIDE';
  const pastDeparture = departure ? daysBetween(departure, today) : 0;
  const isOverdue = !!departure && today > departure && inside;
  const forceEligible = inside && (pastDeparture > GRACE_DAYS || (!!validUntil && today > validUntil));
  let mode = 'checkin';
  if (inside) mode = 'checkout';
  if (isOverdue) mode = 'force';
  if (forceEligible) mode = 'force';

  return { ok: true, ...info, earlyArrival, daysBeforeEarliest, isOverdue, forceEligible, mode };
}

/** Does this check-out reason complete the ticket? Exact text from the list, any case. */
export function completesTicket(reason, completing) {
  const r = String(reason || '').trim().toLowerCase();
  if (!r) return false;
  const list = completing && completing.length ? completing : DEFAULT_COMPLETING_REASONS;
  return list.some((x) => String(x).trim().toLowerCase() === r);
}

/** Same scan repeated inside the window is a double tap, not a second scan. */
export function isDuplicate(state, key, nowMs, seconds) {
  if (!state || state.last_action_hash !== key || !state.last_action_hash_time) return false;
  const then = Date.parse(state.last_action_hash_time);
  return Number.isFinite(then) && nowMs - then < seconds * 1000;
}

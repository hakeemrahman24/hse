// Small helpers shared by the gate Worker. No D1 access here.

export const CORS_HEADERS = {
  'Access-Control-Allow-Origin': '*',
  'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
  'Access-Control-Allow-Headers': 'Content-Type, Authorization',
};

export function json(data, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { ...CORS_HEADERS, 'Content-Type': 'application/json' },
  });
}

/**
 * The current moment. WHY env.NOW: tests pin the clock by setting it; the
 * deployed Worker never has it, so production always uses the real time.
 */
export function nowOf(env) {
  return env && env.NOW ? new Date(env.NOW) : new Date();
}

export function nowISO(env) {
  return nowOf(env).toISOString();
}

/**
 * The calendar day in Jakarta, "YYYY-MM-DD".
 * WHY one function everywhere: the old Worker mixed the Jakarta day with the
 * server's UTC day, so between 00:00 and 07:00 Jakarta time "today" was
 * yesterday for the departure and validity tests (gate rules 3.1.4).
 */
export function jakartaYmd(date) {
  return date.toLocaleDateString('en-CA', { timeZone: 'Asia/Jakarta' });
}

const YMD = /^\d{4}-\d{2}-\d{2}/;

/** A stored day as "YYYY-MM-DD", or null. A date-time is read as a Jakarta day. */
export function ymdKey(val) {
  if (!val) return null;
  const s = String(val);
  if (YMD.test(s)) return s.slice(0, 10);
  const d = new Date(val);
  return Number.isNaN(d.getTime()) ? null : jakartaYmd(d);
}

/** Pushed dates keep their calendar day exactly; nothing shifts them by a zone. */
export function formatDate(val) {
  return ymdKey(val);
}

export function addDays(ymd, n) {
  const d = new Date(ymd + 'T00:00:00Z');
  d.setUTCDate(d.getUTCDate() + n);
  return d.toISOString().slice(0, 10);
}

/** Whole days from a to b (b later gives a positive number). */
export function daysBetween(a, b) {
  return Math.round((Date.parse(b + 'T00:00:00Z') - Date.parse(a + 'T00:00:00Z')) / 86400000);
}

export function cmpYmd(a, b) {
  if (!a && !b) return 0;
  if (!a) return 1;
  if (!b) return -1;
  return a < b ? -1 : a > b ? 1 : 0;
}

/** LEP-XXXXX / WVR-XXXXX, or a bare 5-character code (taken as LEP-). */
export function normalizeLep(lep) {
  const s = String(lep || '').trim().toUpperCase().replace(/\s/g, '');
  if (!s) return '';
  if (/^(LEP|WVR)-[A-Z0-9]{5}$/.test(s)) return s;
  if (/^[A-Z0-9]{5}$/.test(s)) return 'LEP-' + s;
  return s;
}

export function isResigned(status) {
  return String(status || '').replace(/\s+/g, '').toLowerCase() === 'resigned';
}

export function isWaiver(lep) {
  return /^WVR-/.test(String(lep || '').trim().toUpperCase());
}

export function makeSessionId(env) {
  return 'S' + nowOf(env).getTime().toString(36).toUpperCase() + Math.random().toString(36).slice(2, 6).toUpperCase();
}

/** Run D1 statements in chunks; each chunk is one atomic batch. */
export async function batchChunks(db, statements, chunkSize) {
  const size = Math.max(1, Number(chunkSize) || 50);
  for (let i = 0; i < statements.length; i += size) {
    await db.batch(statements.slice(i, i + size));
  }
}

/** Equal-length digests are compared in constant time, so timing leaks nothing about a secret. */
export async function safeEqual(a, b) {
  const enc = new TextEncoder();
  const [da, db] = await Promise.all([
    crypto.subtle.digest('SHA-256', enc.encode(String(a))),
    crypto.subtle.digest('SHA-256', enc.encode(String(b))),
  ]);
  const x = new Uint8Array(da);
  const y = new Uint8Array(db);
  let diff = 0;
  for (let i = 0; i < x.length; i++) diff |= x[i] ^ y[i];
  return diff === 0;
}

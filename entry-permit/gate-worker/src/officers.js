// Gate officers: checking a name and PIN on the server.
//
// WHY here and not in the page: the old scanner page held every officer's PIN
// in plain text (served by a public endpoint) and compared them in the browser.
// Now the Worker stores only a salted PBKDF2-SHA256 verifier (the Go app makes it,
// internal/gateofficer) and the PIN is checked here, with a lockout.

import { nowISO, nowOf, safeEqual } from './util.js';

export const SCHEME = 'pbkdf2-sha256';
export const MAX_TRIES = 5;
export const LOCK_MINUTES = 15;
// Workers allow at most 100,000 PBKDF2 iterations; a bigger stored count would fail every login.
const MAX_ITERATIONS = 100000;

function fromHex(hex) {
  if (!/^([0-9a-f]{2})+$/i.test(hex)) return null;
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  return out;
}

/** True when the stored value is a verifier, not a legacy plain-text code. */
export function isVerifier(stored) {
  return String(stored || '').startsWith(SCHEME + '$');
}

/** Check a PIN against "pbkdf2-sha256$iterations$salt-hex$key-hex". */
export async function verifyPin(stored, pin) {
  const parts = String(stored || '').split('$');
  if (parts.length !== 4 || parts[0] !== SCHEME) return false;
  const iterations = Number(parts[1]);
  const salt = fromHex(parts[2]);
  const want = fromHex(parts[3]);
  if (!Number.isInteger(iterations) || iterations < 1 || iterations > MAX_ITERATIONS || !salt || !want) return false;
  const key = await crypto.subtle.importKey('raw', new TextEncoder().encode(pin), 'PBKDF2', false, ['deriveBits']);
  const bits = new Uint8Array(await crypto.subtle.deriveBits({ name: 'PBKDF2', hash: 'SHA-256', salt, iterations }, key, want.length * 8));
  let diff = bits.length ^ want.length;
  for (let i = 0; i < bits.length && i < want.length; i++) diff |= bits[i] ^ want[i];
  return diff === 0;
}

/**
 * Check one officer by name and PIN.
 * Returns { ok: true, name } or { ok: false, msg, locked }.
 * After MAX_TRIES wrong PINs for a name, that name is locked for LOCK_MINUTES.
 * The message never says whether the name or the PIN was wrong.
 */
export async function checkOfficer(env, name, pin) {
  const nameKey = String(name || '').trim().toLowerCase();
  const code = String(pin || '').trim();
  const denied = { ok: false, msg: 'Access Denied', locked: false };
  if (!nameKey || !code) return denied;

  const now = nowOf(env);
  const row = await env.gate_db.prepare('SELECT fails, locked_until FROM officer_attempts WHERE key = ?').bind(nameKey).first();
  if (row && row.locked_until && Date.parse(row.locked_until) > now.getTime()) {
    return { ok: false, msg: 'Too many wrong codes. Try again later.', locked: true };
  }

  const { results } = await env.gate_db
    .prepare("SELECT code, value FROM reference WHERE type = 'officer' AND lower(value) = ?")
    .bind(nameKey)
    .all();
  let matched = null;
  for (const o of results || []) {
    // Verifier: exact PIN. Legacy plain code (cut-over only): same letters in any case.
    const good = isVerifier(o.code) ? await verifyPin(o.code, code) : await safeEqual(String(o.code || '').toUpperCase(), code.toUpperCase());
    if (good) {
      matched = o;
      break;
    }
  }

  if (matched) {
    if (row) await env.gate_db.prepare('DELETE FROM officer_attempts WHERE key = ?').bind(nameKey).run();
    return { ok: true, name: matched.value };
  }

  const fails = ((row && row.fails) || 0) + 1;
  const lockedUntil = fails >= MAX_TRIES ? new Date(now.getTime() + LOCK_MINUTES * 60000).toISOString() : null;
  await env.gate_db
    .prepare(
      `INSERT INTO officer_attempts (key, fails, locked_until, updated_at) VALUES (?, ?, ?, ?)
       ON CONFLICT(key) DO UPDATE SET fails = excluded.fails, locked_until = excluded.locked_until, updated_at = excluded.updated_at`
    )
    .bind(nameKey, lockedUntil ? 0 : fails, lockedUntil, nowISO(env))
    .run();
  return denied;
}

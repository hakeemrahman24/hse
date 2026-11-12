// A stand-in for Cloudflare D1 over an in-memory SQLite database, so the Worker's
// real SQL runs in tests against the real schema.sql. Same calls the Worker uses:
// prepare().bind().first() / all() / run(), and batch() as one transaction.

import { readFileSync } from 'node:fs';
import { DatabaseSync } from 'node:sqlite';

const SCHEMA = readFileSync(new URL('../schema.sql', import.meta.url), 'utf8');

function statement(db, sql, args) {
  const bound = args.map((a) => (a === undefined ? null : a));
  return {
    bind: (...next) => statement(db, sql, next),
    first: async () => db.prepare(sql).get(...bound) ?? null,
    all: async () => ({ results: db.prepare(sql).all(...bound) }),
    run: async () => {
      const r = db.prepare(sql).run(...bound);
      return { meta: { changes: Number(r.changes) } };
    },
  };
}

export function makeD1() {
  const db = new DatabaseSync(':memory:');
  db.exec(SCHEMA);
  return {
    raw: db,
    prepare: (sql) => statement(db, sql, []),
    // D1 runs a batch atomically: all statements succeed or none do.
    batch: async (stmts) => {
      db.exec('BEGIN');
      try {
        const out = [];
        for (const s of stmts) out.push(await s.run());
        db.exec('COMMIT');
        return out;
      } catch (err) {
        db.exec('ROLLBACK');
        throw err;
      }
    },
  };
}

/** An env with a fresh database, a secret and a pinned clock. */
export function makeEnv(now = '2026-11-10T03:00:00.000Z') {
  return { gate_db: makeD1(), SYNC_SECRET: 'test-secret', NOW: now };
}

/** Build a verifier exactly as the Go app does: pbkdf2-sha256$iterations$salt-hex$key-hex. */
export async function makeVerifier(pin, iterations = 1000) {
  const salt = new Uint8Array(16).map((_, i) => i + 1);
  const key = await crypto.subtle.importKey('raw', new TextEncoder().encode(pin), 'PBKDF2', false, ['deriveBits']);
  const bits = new Uint8Array(await crypto.subtle.deriveBits({ name: 'PBKDF2', hash: 'SHA-256', salt, iterations }, key, 256));
  const hex = (u) => Array.from(u, (b) => b.toString(16).padStart(2, '0')).join('');
  return `pbkdf2-sha256$${iterations}$${hex(salt)}$${hex(bits)}`;
}

export function call(handler, env, body, { method = 'POST', headers = {}, url = 'https://gate.example/x' } = {}) {
  const init = { method, headers: { 'Content-Type': 'application/json', ...headers } };
  if (method !== 'GET') init.body = JSON.stringify(body);
  return handler(new Request(url, init), env);
}

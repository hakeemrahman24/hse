import test from 'node:test';
import assert from 'node:assert/strict';
import worker from '../src/index.js';
import { makeEnv } from './d1shim.js';

const post = async (env, path, body, headers = {}) => {
  const res = await worker.fetch(new Request('https://gate.example' + path, {
    method: 'POST', headers: { 'Content-Type': 'application/json', ...headers }, body: JSON.stringify(body),
  }), env);
  return { status: res.status, body: await res.json() };
};
const push = (env, body) => post(env, '/api/sync', { secret: 'test-secret', ...body });
const all = (env, sql, ...args) => env.gate_db.raw.prepare(sql).all(...args).map((r) => ({ ...r }));
const one = (env, sql, ...args) => env.gate_db.raw.prepare(sql).get(...args);

const permit = (lep = 'LEP-ABCDE', over = {}) => ({ lep, name: 'Budi', company: 'PT Maju', arrival: '2026-11-10', departure: '2026-12-01', mcu: '2027-03-01', permitValidUntil: '2027-01-17', ...over });
const refs = { officers: [{ code: 'pbkdf2-sha256$1000$aa$bb', name: 'Sari Gate' }], gates: ['Gate 1'], reasons: ['Tugas luar'] };

// ── who may call ─────────────────────────────────────────────────────────────

test('every sync route needs the secret, in the body or as a Bearer header', async () => {
  const env = makeEnv();
  const routes = ['/api/sync', '/api/sync/pull-gatelog', '/api/sync/ack-gatelog', '/api/sync/pull-gatestate', '/api/sync/ack-gatestate',
    '/api/sync/pull-tickets', '/api/sync/ack-tickets', '/api/sync/reset-sync-flags', '/api/permit/deactivate'];
  for (const r of routes) {
    assert.equal((await post(env, r, {})).status, 401, r + ' without a secret');
    assert.equal((await post(env, r, { secret: 'nope' })).status, 401, r + ' with a wrong secret');
    assert.equal((await post(env, r, {}, { Authorization: 'Bearer nope' })).status, 401, r + ' with a wrong header');
    assert.equal((await post(env, r, { secret: 'test-secret' })).status, 200, r + ' with the body secret');
    assert.equal((await post(env, r, {}, { Authorization: 'Bearer test-secret' })).status, 200, r + ' with the header secret');
  }
});

test('a Worker with no SYNC_SECRET set refuses everything instead of accepting an empty one', async () => {
  const env = makeEnv();
  delete env.SYNC_SECRET;
  assert.equal((await post(env, '/api/sync', {})).status, 401);
  assert.equal((await post(env, '/api/sync', { secret: '' })).status, 401);
  assert.equal((await post(env, '/api/sync', { secret: undefined }, { Authorization: 'Bearer ' })).status, 401);
});

test('the old ticket-submit route is gone, and unknown routes give 404', async () => {
  const env = makeEnv();
  assert.equal((await post(env, '/api/ticket/submit', { secret: 'test-secret', lep: 'LEP-ABCDE' })).status, 404);
  assert.equal((await post(env, '/nope', {})).status, 404);
  const pre = await worker.fetch(new Request('https://gate.example/api/sync', { method: 'OPTIONS' }), env);
  assert.match(pre.headers.get('Access-Control-Allow-Headers'), /Authorization/);
});

// ── push ─────────────────────────────────────────────────────────────────────

test('push stores permits, tickets and references, and a ticket rewrites the permit dates', async () => {
  const env = makeEnv();
  const r = await push(env, {
    permits: [permit()], references: refs,
    tripTickets: [{ ticketId: 'TKT-1', lep: 'lep-abcde', plannedArrival: '2026-12-10', plannedDeparture: '2027-01-02', status: 'PLANNED', lepLocked: true }],
  });
  assert.deepEqual([r.status, r.body.success, r.body.permits, r.body.tripTickets], [200, true, 1, 1]);
  const p = one(env, 'SELECT * FROM permits');
  assert.deepEqual([p.lep, p.status, p.is_active, p.permit_valid_until, p.arrival, p.departure], ['LEP-ABCDE', 'Approved', 1, '2027-01-17', '2026-12-10', '2027-01-02']);
  const t = one(env, 'SELECT * FROM trip_tickets');
  assert.deepEqual([t.lep, t.lep_locked, t.synced_to_sheet], ['LEP-ABCDE', 1, 0]);
  assert.deepEqual(all(env, "SELECT code, value FROM reference WHERE type = 'officer'"), [{ code: 'pbkdf2-sha256$1000$aa$bb', value: 'Sari Gate' }]);
});

test('push keeps a calendar day exactly, with no time zone shift', async () => {
  const env = makeEnv();
  await push(env, { permits: [permit('LEP-ABCDE', { arrival: '2026-11-10', permitValidUntil: '2027-01-01' })] });
  const p = one(env, 'SELECT arrival, permit_valid_until FROM permits');
  assert.deepEqual([p.arrival, p.permit_valid_until], ['2026-11-10', '2027-01-01']);
});

test('an empty officer, gate or reason list never wipes the copy the gate already has', async () => {
  const env = makeEnv();
  await push(env, { references: refs });
  const r = await push(env, { references: { officers: [], gates: [], reasons: [] } });
  assert.equal(r.body.success, true);
  assert.deepEqual(r.body.skipped.sort(), ['gates', 'officers', 'reasons']);
  assert.equal(one(env, "SELECT COUNT(*) AS n FROM reference WHERE type = 'officer'").n, 1);
  assert.equal(one(env, "SELECT COUNT(*) AS n FROM reference WHERE type = 'gate'").n, 1);
  assert.equal(one(env, "SELECT COUNT(*) AS n FROM reference WHERE type = 'checkout_reason'").n, 1);
  // A real list replaces the old one.
  await push(env, { references: { officers: [{ code: 'pbkdf2-sha256$1000$cc$dd', name: 'Dewi Gate' }], gates: ['Gate 2'], reasons: ['Pindah area'] } });
  assert.deepEqual(all(env, "SELECT value FROM reference WHERE type = 'officer'").map((x) => x.value), ['Dewi Gate']);
  assert.deepEqual(all(env, "SELECT value FROM reference WHERE type = 'gate'").map((x) => x.value), ['Gate 2']);
});

test('push stores the gate settings: reasons that complete a ticket and the duplicate window', async () => {
  const env = makeEnv();
  await push(env, { references: { completingReasons: ['Sesuai jadwal keluar', 'Selesai awal'], settings: { gate_duplicate_scan_seconds: 7 } } });
  assert.deepEqual(all(env, "SELECT value FROM reference WHERE type = 'completing_reason' ORDER BY value").map((x) => x.value), ['Selesai awal', 'Sesuai jadwal keluar']);
  assert.equal(one(env, "SELECT value FROM reference WHERE type = 'setting' AND code = 'gate_duplicate_scan_seconds'").value, '7');
});

test('a half-failed push changes nothing in that chunk', async () => {
  const env = makeEnv();
  env.gate_db.raw.exec('DROP TABLE trip_tickets'); // a ticket statement will fail
  const r = await push(env, { permits: [permit()], tripTickets: [{ ticketId: 'T', lep: 'LEP-ABCDE', plannedArrival: '2026-12-10', plannedDeparture: '2027-01-02' }] });
  assert.equal(r.status, 500);
  assert.equal(r.body.success, false);
  assert.equal(one(env, 'SELECT COUNT(*) AS n FROM permits').n, 0);
});

// ── pull and ack: at least once ──────────────────────────────────────────────

function addLog(env, n) {
  for (let i = 0; i < n; i++) {
    env.gate_db.raw.prepare("INSERT INTO gate_log (timestamp, lep, action, synced_to_sheet, created_at) VALUES (?,?,?,0,?)")
      .run('2026-11-10T03:00:00Z', 'LEP-ABCDE', 'checkin', `2026-11-10T03:00:${String(i % 60).padStart(2, '0')}Z`);
  }
}

test('pull returns unacked rows again and again until they are acked', async () => {
  const env = makeEnv();
  addLog(env, 3);
  const a = (await post(env, '/api/sync/pull-gatelog', { secret: 'test-secret' })).body;
  const b = (await post(env, '/api/sync/pull-gatelog', { secret: 'test-secret' })).body;
  assert.equal(a.rows.length, 3);
  assert.deepEqual(a.rows.map((r) => r.id), b.rows.map((r) => r.id)); // a lost ack means the same rows come back
  const ack = (await post(env, '/api/sync/ack-gatelog', { secret: 'test-secret', ids: [a.rows[0].id, a.rows[1].id, 'junk', -4] })).body;
  assert.equal(ack.acked, 2);
  assert.equal((await post(env, '/api/sync/pull-gatelog', { secret: 'test-secret' })).body.rows.length, 1);
  // Acking the same ids twice is harmless.
  assert.equal((await post(env, '/api/sync/ack-gatelog', { secret: 'test-secret', ids: [a.rows[0].id] })).body.success, true);
});

test('pull is capped at 200 rows and defaults to 100', async () => {
  const env = makeEnv();
  addLog(env, 250);
  assert.equal((await post(env, '/api/sync/pull-gatelog', { secret: 'test-secret' })).body.rows.length, 100);
  assert.equal((await post(env, '/api/sync/pull-gatelog', { secret: 'test-secret', limit: 5000 })).body.rows.length, 200);
  assert.equal((await post(env, '/api/sync/pull-gatelog', { secret: 'test-secret', limit: 7 })).body.rows.length, 7);
});

test('gate state and ticket changes are pulled and acked by their own keys', async () => {
  const env = makeEnv();
  await push(env, { permits: [permit()], tripTickets: [{ ticketId: 'TKT-1', lep: 'LEP-ABCDE', plannedArrival: '2026-11-10', plannedDeparture: '2026-12-01' }] });
  env.gate_db.raw.prepare("INSERT INTO gate_state (lep, current_status, synced_to_sheet, updated_at) VALUES ('LEP-ABCDE','INSIDE',0,'2026-11-10T03:00:00Z')").run();
  const tickets = (await post(env, '/api/sync/pull-tickets', { secret: 'test-secret' })).body.rows;
  const states = (await post(env, '/api/sync/pull-gatestate', { secret: 'test-secret' })).body.rows;
  assert.deepEqual([tickets.length, states.length], [1, 1]);
  await post(env, '/api/sync/ack-tickets', { secret: 'test-secret', ticketIds: ['TKT-1'] });
  await post(env, '/api/sync/ack-gatestate', { secret: 'test-secret', leps: ['lep-abcde'] });
  assert.equal((await post(env, '/api/sync/pull-tickets', { secret: 'test-secret' })).body.rows.length, 0);
  assert.equal((await post(env, '/api/sync/pull-gatestate', { secret: 'test-secret' })).body.rows.length, 0);
});

test('reset-sync-flags makes acked rows come back once more', async () => {
  const env = makeEnv();
  addLog(env, 2);
  await post(env, '/api/sync/ack-gatelog', { secret: 'test-secret', ids: [1, 2] });
  const r = (await post(env, '/api/sync/reset-sync-flags', { secret: 'test-secret', scope: 'gatelog' })).body;
  assert.equal(r.reset.gatelog, 2);
  assert.equal((await post(env, '/api/sync/pull-gatelog', { secret: 'test-secret' })).body.rows.length, 2);
});

// ── deactivate ───────────────────────────────────────────────────────────────

test('deactivating a worker who is still inside keeps them inside so they can check out', async () => {
  const env = makeEnv();
  await push(env, { permits: [permit('LEP-INSID'), permit('LEP-OUTSD')] });
  env.gate_db.raw.exec("UPDATE permits SET gate_status = 'INSIDE' WHERE lep = 'LEP-INSID'");
  env.gate_db.raw.exec("INSERT INTO gate_state (lep, current_status, is_active, synced_to_sheet) VALUES ('LEP-OUTSD','OUTSIDE',1,1)");
  const r = (await post(env, '/api/permit/deactivate', { secret: 'test-secret', status: 'Resigned', leps: ['lep-insid', 'LEP-OUTSD'] })).body;
  assert.deepEqual([r.success, r.deactivated, r.status], [true, 2, 'Resigned']);
  const inside = one(env, "SELECT status, is_active, gate_status FROM permits WHERE lep = 'LEP-INSID'");
  assert.deepEqual([inside.status, inside.is_active, inside.gate_status], ['Resigned', 0, 'INSIDE']);
  const out = one(env, "SELECT status, is_active, gate_status FROM permits WHERE lep = 'LEP-OUTSD'");
  assert.deepEqual([out.status, out.is_active, out.gate_status], ['Resigned', 0, 'OUTSIDE']);
  const st = one(env, "SELECT is_active, synced_to_sheet FROM gate_state WHERE lep = 'LEP-OUTSD'");
  assert.deepEqual([st.is_active, st.synced_to_sheet], [0, 0]);
});

// ── a push never undoes what the gate owns on a ticket ───────────────────────

const ticket = (over = {}) => ({ ticketId: 'TKT-1', lep: 'LEP-ABCDE', name: 'Budi', company: 'PT Maju', plannedArrival: '2026-11-10T01:00:00.000Z',
  plannedDeparture: '2026-11-10T09:00:00.000Z', submittedAt: '2026-11-09T01:00:00.000Z', status: 'PLANNED', recordStart: '', closedAt: '', closeReason: '',
  weeksOnSite: 0, medDeclaration: 'No', medDetails: '', lepLocked: false, ...over });
const tk = (env) => one(env, 'SELECT status, record_start, closed_at, close_reason, planned_departure FROM trip_tickets WHERE ticket_id = ?', 'TKT-1');

test('a stale PLANNED push keeps the gate first check-in and ACTIVE status, but takes the new plan', async () => {
  const env = makeEnv();
  await push(env, { permits: [permit()], tripTickets: [ticket()] });
  env.gate_db.raw.prepare("UPDATE trip_tickets SET status='ACTIVE', record_start='2026-11-10T02:00:00.000Z' WHERE ticket_id='TKT-1'").run();
  await push(env, { tripTickets: [ticket({ plannedDeparture: '2026-11-11T09:00:00.000Z' })] });
  const row = tk(env);
  assert.equal(row.status, 'ACTIVE');
  assert.equal(row.record_start, '2026-11-10T02:00:00.000Z');
  assert.equal(row.planned_departure, '2026-11-11'); // stored as a calendar day
});

test('a push never reopens a ticket the gate completed, and keeps its closing', async () => {
  const env = makeEnv();
  await push(env, { permits: [permit()], tripTickets: [ticket()] });
  env.gate_db.raw.prepare("UPDATE trip_tickets SET status='COMPLETED', record_start='2026-11-10T02:00:00.000Z', closed_at='2026-11-10T08:00:00.000Z', close_reason='Sesuai jadwal keluar' WHERE ticket_id='TKT-1'").run();
  await push(env, { tripTickets: [ticket({ status: 'ACTIVE', recordStart: '2026-11-10T02:00:00.000Z' })] });
  const row = tk(env);
  assert.equal(row.status, 'COMPLETED');
  assert.equal(row.closed_at, '2026-11-10T08:00:00.000Z');
  assert.equal(row.close_reason, 'Sesuai jadwal keluar');
});

test('the Pi can still close a ticket the gate has not closed', async () => {
  const env = makeEnv();
  await push(env, { permits: [permit()], tripTickets: [ticket()] });
  await push(env, { tripTickets: [ticket({ status: 'COMPLETED', closedAt: '2026-11-10T08:00:00.000Z', closeReason: 'Selesai awal' })] });
  const row = tk(env);
  assert.equal(row.status, 'COMPLETED');
  assert.equal(row.close_reason, 'Selesai awal');
});

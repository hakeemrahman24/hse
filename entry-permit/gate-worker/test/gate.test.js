import test from 'node:test';
import assert from 'node:assert/strict';
import { handleAction, handleLookup, handleOfficerLogin, handleReferences } from '../src/gate.js';
import { call, makeEnv, makeVerifier } from './d1shim.js';

const PIN = 'AB12';

async function setup(over = {}) {
  const env = makeEnv(over.now);
  const d = env.gate_db.raw;
  const p = { lep: 'LEP-ABCDE', name: 'Budi', company: 'PT Maju', arrival: '2026-11-10', departure: '2026-12-01', mcu: '2027-03-01',
    status: 'Approved', is_active: 1, gate_status: 'OUTSIDE', permit_valid_until: '2027-01-17', ...over.permit };
  d.prepare(`INSERT INTO permits (lep, name, company, arrival, departure, mcu, status, is_active, gate_status, permit_valid_until)
             VALUES (?,?,?,?,?,?,?,?,?,?)`).run(p.lep, p.name, p.company, p.arrival, p.departure, p.mcu, p.status, p.is_active, p.gate_status, p.permit_valid_until);
  if (over.ticket !== false) {
    const t = { id: 'TKT-1', status: 'PLANNED', arr: '2026-11-10', dep: '2026-12-01', locked: 0, ...over.ticket };
    d.prepare(`INSERT INTO trip_tickets (ticket_id, lep, planned_arrival, planned_departure, status, lep_locked) VALUES (?,?,?,?,?,?)`)
      .run(t.id, p.lep, t.arr, t.dep, t.status, t.locked);
  }
  const ref = d.prepare('INSERT INTO reference (type, code, value) VALUES (?,?,?)');
  ref.run('officer', await makeVerifier(PIN), 'Sari Gate');
  ref.run('officer', 'OLDPIN', 'Old Guy');
  ref.run('gate', 'Gate 1', 'Gate 1');
  ref.run('checkout_reason', 'Tugas luar', 'Tugas luar');
  ref.run('checkout_reason', 'Sesuai jadwal keluar', 'Sesuai jadwal keluar');
  return env;
}

const act = async (env, over = {}) => {
  const body = { lep: 'LEP-ABCDE', action: 'checkin', gateName: 'Gate 1', officerName: 'Sari Gate', officerCode: PIN, reason: '', ...over };
  return (await call(handleAction, env, body)).json();
};
const row = (env, sql, ...args) => env.gate_db.raw.prepare(sql).get(...args);
const lookup = async (env, lep = 'LEP-ABCDE') => (await call(handleLookup, env, null, { method: 'GET', url: 'https://gate.example/api/lookup?lep=' + lep })).json();

// ── lookup ───────────────────────────────────────────────────────────────────

test('lookup: unknown number, then a good permit offers check-in', async () => {
  const env = await setup();
  assert.equal((await lookup(env, 'LEP-ZZZZZ')).msg, 'LEP Not Found');
  const r = await lookup(env);
  assert.deepEqual([r.success, r.mode, r.status, r.permitValidUntil], [true, 'checkin', 'OUTSIDE', '2027-01-17']);
  assert.equal(r.tripTicket.ticketId, 'TKT-1');
});

test('lookup: no ticket, locked ticket, resigned and inactive permits are refused with their own message', async () => {
  assert.equal((await lookup(await setup({ ticket: false }))).msg, 'NO TRIP TICKET');
  assert.equal((await lookup(await setup({ ticket: { locked: 1 } }))).msg, 'LEP LOCKED');
  assert.match((await lookup(await setup({ permit: { status: 'Resigned' } }))).msg, /resigned/);
  assert.equal((await lookup(await setup({ permit: { is_active: 0 } }))).msg, 'LEP is inactive.');
});

test('lookup: the gate ends at the LEP date, on the Jakarta calendar', async () => {
  // 01:00 on 10 Nov in Jakarta is still 9 Nov in UTC; the LEP ended on the 9th.
  const env = await setup({ now: '2026-11-09T18:00:00.000Z', permit: { permit_valid_until: '2026-11-09' } });
  assert.equal((await lookup(env)).msg, 'LEP EXPIRED');
});

// ── references and officer login ─────────────────────────────────────────────

test('references list gates, officer names and reasons, and never a code or verifier', async () => {
  const env = await setup();
  const text = await (await call(handleReferences, env, null, { method: 'GET' })).text();
  const r = JSON.parse(text);
  assert.deepEqual(r.gates, ['Gate 1']);
  assert.deepEqual(r.officers.map((o) => o.name).sort(), ['Old Guy', 'Sari Gate']);
  assert.deepEqual(r.reasons.sort(), ['Sesuai jadwal keluar', 'Tugas luar']);
  for (const secret of ['pbkdf2', 'OLDPIN', PIN]) assert.equal(text.includes(secret), false, `leaked ${secret}`);
  assert.equal(r.officers.every((o) => !('code' in o)), true);
});

test('officer login checks the PIN on the server and says the same thing for a wrong name or PIN', async () => {
  const env = await setup();
  const login = async (name, code) => (await call(handleOfficerLogin, env, { name, code })).json();
  assert.deepEqual(await login('sari gate', PIN), { success: true, name: 'Sari Gate' }); // any case of the name
  assert.equal((await login('Sari Gate', 'ab12')).success, false); // a verifier PIN is case-sensitive
  const wrongPin = await login('Sari Gate', 'ZZZZ');
  const wrongName = await login('Nobody', PIN);
  assert.deepEqual([wrongPin.success, wrongPin.msg], [false, 'Access Denied']);
  assert.deepEqual(wrongName, wrongPin);
  assert.equal((await login('Old Guy', 'oldpin')).success, true); // legacy plain code, cut-over only
});

test('five wrong PINs lock a name for 15 minutes, even for the right PIN; a good login clears the count', async () => {
  const env = await setup();
  const login = async (code) => (await call(handleOfficerLogin, env, { name: 'Sari Gate', code })).json();
  for (let i = 0; i < 4; i++) await login('ZZZZ');
  assert.equal((await login(PIN)).success, true); // 4 wrong, then right: cleared
  for (let i = 0; i < 5; i++) await login('ZZZZ');
  const locked = await login(PIN);
  assert.deepEqual([locked.success, locked.msg], [false, 'Too many wrong codes. Try again later.']);
  env.NOW = '2026-11-10T03:14:00.000Z';
  assert.equal((await login(PIN)).success, false);
  env.NOW = '2026-11-10T03:16:00.000Z';
  assert.equal((await login(PIN)).success, true);
});

// ── check-in ─────────────────────────────────────────────────────────────────

test('check-in writes state, permit, ticket and log together, in the officer\'s name', async () => {
  const env = await setup();
  const r = await act(env);
  assert.deepEqual([r.success, r.msg, r.officer], [true, 'CHECK-IN SUCCESS', 'Sari Gate']);
  const state = row(env, 'SELECT * FROM gate_state WHERE lep = ?', 'LEP-ABCDE');
  assert.deepEqual([state.current_status, state.gate_name, state.last_gate_officer], ['INSIDE', 'Gate 1', 'Sari Gate']);
  assert.equal(row(env, 'SELECT gate_status FROM permits').gate_status, 'INSIDE');
  const t = row(env, 'SELECT * FROM trip_tickets');
  assert.deepEqual([t.status, t.record_start, t.synced_to_sheet], ['ACTIVE', '2026-11-10T03:00:00.000Z', 0]);
  const log = row(env, 'SELECT * FROM gate_log');
  assert.deepEqual([log.action, log.gate_officer, log.date_only, log.synced_to_sheet], ['checkin', 'Sari Gate', '2026-11-10', 0]);
});

test('check-in refusals write nothing: bad PIN, no officer name, unknown gate, unknown permit', async () => {
  const env = await setup();
  assert.equal((await act(env, { officerCode: 'ZZZZ' })).msg, 'Access Denied');
  assert.equal((await act(env, { officerName: '' })).msg, 'Officer is required.');
  assert.equal((await act(env, { gateName: 'Gate 9' })).msg, 'Gate name is required.');
  assert.equal((await act(env, { lep: 'LEP-ZZZZZ' })).msg, 'LEP Not Found');
  assert.equal(row(env, 'SELECT COUNT(*) AS n FROM gate_log').n, 0);
  assert.equal(row(env, 'SELECT COUNT(*) AS n FROM gate_state').n, 0);
});

test('the action re-checks expiry, so a scan page left open cannot check in an expired worker', async () => {
  const env = await setup({ now: '2026-12-05T03:00:00.000Z' }); // departure 12-01 + 3 days has passed
  const r = await act(env);
  assert.deepEqual([r.success, r.msg], [false, 'LEP EXPIRED']);
  assert.equal(row(env, 'SELECT COUNT(*) AS n FROM gate_log').n, 0);
  const lapsed = await setup({ permit: { permit_valid_until: '2026-11-09' } });
  assert.equal((await act(lapsed)).msg, 'LEP EXPIRED');
});

test('the action refuses no ticket, a locked ticket and a resigned worker', async () => {
  assert.match((await act(await setup({ ticket: false }))).msg, /No valid trip ticket/);
  assert.match((await act(await setup({ ticket: { locked: 1 } }))).msg, /LEP locked/);
  assert.match((await act(await setup({ permit: { status: 'Resigned' } }))).msg, /resigned/);
});

test('a repeat inside 5 seconds is blocked as a double tap; after that it is "already checked in"', async () => {
  const env = await setup();
  assert.equal((await act(env)).success, true);
  env.NOW = '2026-11-10T03:00:04.000Z';
  assert.match((await act(env)).msg, /Duplicate scan blocked/);
  env.NOW = '2026-11-10T03:00:05.500Z'; // a different 5-second bucket would have let this through before; the window is time, not a bucket
  assert.match((await act(env)).msg, /Already checked in/);
});

// ── check-out and force ──────────────────────────────────────────────────────

async function inside(over = {}) {
  const env = await setup(over);
  assert.equal((await act(env)).success, true);
  return env;
}

test('check-out with a completing reason closes the ticket in the same step', async () => {
  const env = await inside();
  env.NOW = '2026-11-28T03:00:00.000Z';
  const r = await act(env, { action: 'checkout', reason: 'Sesuai jadwal keluar' });
  assert.deepEqual([r.success, r.msg], [true, 'CHECK-OUT SUCCESS']);
  const t = row(env, 'SELECT * FROM trip_tickets');
  assert.deepEqual([t.status, t.close_reason, t.closed_at, t.synced_to_sheet], ['COMPLETED', 'Sesuai jadwal keluar', '2026-11-28T03:00:00.000Z', 0]);
  assert.equal(row(env, 'SELECT current_status FROM gate_state').current_status, 'OUTSIDE');
  assert.equal(row(env, 'SELECT gate_status FROM permits').gate_status, 'OUTSIDE');
  assert.equal(row(env, "SELECT COUNT(*) AS n FROM gate_log WHERE action = 'checkout'").n, 1);
});

test('check-out with another reason leaves the ticket open', async () => {
  const env = await inside();
  env.NOW = '2026-11-12T03:00:00.000Z';
  assert.equal((await act(env, { action: 'checkout', reason: 'Tugas luar' })).success, true);
  assert.equal(row(env, 'SELECT status FROM trip_tickets').status, 'ACTIVE');
});

test('a refused check-out completes nothing (the old code closed the ticket first)', async () => {
  const env = await inside();
  env.NOW = '2026-11-12T03:00:00.000Z';
  const short = await act(env, { action: 'checkout', reason: 'ab' });
  assert.match(short.msg, /reason is required/);
  assert.equal(row(env, 'SELECT status FROM trip_tickets').status, 'ACTIVE');

  env.NOW = '2026-12-06T03:00:00.000Z'; // more than 3 days past departure
  const late = await act(env, { action: 'checkout', reason: 'Sesuai jadwal keluar' });
  assert.match(late.msg, /Regular check-out is not available/);
  const t = row(env, 'SELECT status, closed_at FROM trip_tickets');
  assert.deepEqual([t.status, t.closed_at], ['ACTIVE', null]);
  assert.equal(row(env, 'SELECT current_status FROM gate_state').current_status, 'INSIDE');
});

test('force check-out only after 3 days past departure or past the LEP date', async () => {
  const env = await inside();
  env.NOW = '2026-12-02T03:00:00.000Z';
  assert.match((await act(env, { action: 'force' })).msg, /Force check-out is only used/);
  env.NOW = '2026-12-06T03:00:00.000Z';
  const r = await act(env, { action: 'force' });
  assert.deepEqual([r.success, r.msg], [true, 'FORCE-CHECKOUT RECORDED']);
  assert.equal(row(env, "SELECT reason FROM gate_log WHERE action = 'force'").reason, 'Force check-out');
  assert.equal(row(env, 'SELECT current_status FROM gate_state').current_status, 'OUTSIDE');
});

test('a worker who resigned while inside can still be checked out', async () => {
  const env = await inside();
  env.gate_db.raw.prepare("UPDATE permits SET status = 'Resigned', is_active = 0").run();
  env.NOW = '2026-11-12T03:00:00.000Z';
  assert.equal((await act(env, { action: 'checkout', reason: 'Tugas luar' })).success, true);
  assert.match((await act(env, { action: 'checkin' })).msg, /resigned/);
});

test('check-out of someone who is not inside is refused', async () => {
  const env = await setup();
  assert.match((await act(env, { action: 'checkout', reason: 'Tugas luar' })).msg, /Not marked inside/);
});

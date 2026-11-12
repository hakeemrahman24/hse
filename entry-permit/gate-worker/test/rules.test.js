import test from 'node:test';
import assert from 'node:assert/strict';
import { addDays, daysBetween, jakartaYmd, normalizeLep, ymdKey } from '../src/util.js';
import { completesTicket, evaluate, isDuplicate, pickGateTicket } from '../src/rules.js';

const permit = (over = {}) => ({
  lep: 'LEP-ABCDE', name: 'Budi', company: 'PT Maju', status: 'Approved', is_active: 1, gate_status: 'OUTSIDE',
  arrival: '2026-11-10', departure: '2026-12-01', mcu: '2027-03-01', permit_valid_until: '2027-01-17', ...over,
});
const ticket = (over = {}) => ({ ticket_id: 'TKT-1', status: 'PLANNED', planned_arrival: '2026-11-10', planned_departure: '2026-12-01', lep_locked: 0, ...over });
const eval_ = (over = {}) => evaluate({ permit: permit(), state: null, tickets: [ticket()], today: '2026-11-10', ...over });

test('Jakarta day: 00:30 Jakarta is still the new day, though UTC is the day before', () => {
  assert.equal(jakartaYmd(new Date('2026-11-09T17:30:00Z')), '2026-11-10');
  assert.equal(jakartaYmd(new Date('2026-11-10T16:59:00Z')), '2026-11-10');
});

test('day arithmetic works on calendar days only', () => {
  assert.equal(addDays('2026-12-30', 3), '2027-01-02');
  assert.equal(daysBetween('2026-11-10', '2026-11-13'), 3);
  assert.equal(ymdKey('2026-11-10T08:00:00Z'), '2026-11-10');
  assert.equal(ymdKey(''), null);
});

test('normalizeLep accepts the three shapes and leaves junk alone', () => {
  assert.equal(normalizeLep(' lep-abcde '), 'LEP-ABCDE');
  assert.equal(normalizeLep('wvr-12345'), 'WVR-12345');
  assert.equal(normalizeLep('abcde'), 'LEP-ABCDE');
  assert.equal(normalizeLep(''), '');
});

test('a worker with a ticket window around today may check in', () => {
  const r = eval_();
  assert.equal(r.ok, true);
  assert.equal(r.mode, 'checkin');
  assert.equal(r.earlyArrival, false);
});

test('refusals come in the documented order', () => {
  assert.equal(eval_({ permit: permit({ status: 'Resigned' }) }).code, 'RESIGNED');
  assert.equal(eval_({ permit: permit({ is_active: 0 }) }).code, 'INACTIVE');
  assert.equal(eval_({ permit: permit({ status: 'Expired' }) }).code, 'INACTIVE');
  assert.equal(eval_({ tickets: [] }).code, 'NO_TICKET');
  assert.equal(eval_({ tickets: [ticket({ lep_locked: 1 })] }).code, 'LOCKED');
});

test('past departure plus 3 days refuses a new check-in, day 3 itself is allowed', () => {
  assert.equal(eval_({ today: '2026-12-04' }).ok, true);
  const r = eval_({ today: '2026-12-05' });
  assert.deepEqual([r.ok, r.code, r.msg], [false, 'EXPIRED', 'LEP EXPIRED']);
});

test('the gate follows the LEP valid-until, not the MCU date', () => {
  const p = permit({ permit_valid_until: '2026-11-20', mcu: '2027-03-01' });
  assert.equal(eval_({ permit: p, today: '2026-11-20' }).ok, true);
  assert.equal(eval_({ permit: p, today: '2026-11-21' }).code, 'EXPIRED');
});

test('with no LEP date the MCU date is the fallback', () => {
  const p = permit({ permit_valid_until: null, mcu: '2026-11-15' });
  assert.equal(eval_({ permit: p, today: '2026-11-16' }).code, 'EXPIRED');
});

test('early arrival is a warning with the days left, never a refusal', () => {
  const r = eval_({ today: '2026-11-04' }); // arrival 11-10, earliest 11-07
  assert.equal(r.ok, true);
  assert.equal(r.earlyArrival, true);
  assert.equal(r.daysBeforeEarliest, 3);
  assert.equal(eval_({ today: '2026-11-07' }).earlyArrival, false);
});

test('a worker inside is checked out, never refused, even after the permit ended', () => {
  const inside = { current_status: 'INSIDE' };
  for (const over of [{ status: 'Resigned' }, { status: 'Expired', is_active: 0 }, { permit_valid_until: '2026-11-01' }]) {
    const r = eval_({ permit: permit({ gate_status: 'INSIDE', ...over }), state: inside });
    assert.equal(r.ok, true, JSON.stringify(over));
    assert.notEqual(r.mode, 'checkin');
  }
});

test('regular check-out becomes force check-out after 3 days past departure or past the LEP date', () => {
  const inside = { current_status: 'INSIDE' };
  const at = (today, over = {}) => eval_({ permit: permit({ gate_status: 'INSIDE', ...over }), state: inside, today });
  assert.deepEqual([at('2026-12-01').mode, at('2026-12-01').forceEligible], ['checkout', false]);
  assert.deepEqual([at('2026-12-02').mode, at('2026-12-02').isOverdue, at('2026-12-02').forceEligible], ['force', true, false]);
  assert.equal(at('2026-12-04').forceEligible, false);
  assert.equal(at('2026-12-05').forceEligible, true);
  assert.equal(at('2026-11-21', { permit_valid_until: '2026-11-20' }).forceEligible, true);
});

test('ticket choice: window around today, ACTIVE first; then the last started; then the next one', () => {
  const a = ticket({ ticket_id: 'A', status: 'PLANNED', planned_arrival: '2026-11-01', planned_departure: '2026-11-30' });
  const b = ticket({ ticket_id: 'B', status: 'ACTIVE', planned_arrival: '2026-11-05', planned_departure: '2026-11-20' });
  assert.equal(pickGateTicket([a, b], '2026-11-10', false).ticket_id, 'B');
  const c = ticket({ ticket_id: 'C', planned_arrival: '2026-12-10', planned_departure: '2026-12-30' });
  assert.equal(pickGateTicket([c], '2026-11-10', false).ticket_id, 'C');
  const done1 = ticket({ ticket_id: 'D1', planned_arrival: '2026-09-01', planned_departure: '2026-09-20' });
  const done2 = ticket({ ticket_id: 'D2', planned_arrival: '2026-10-01', planned_departure: '2026-10-20' });
  assert.equal(pickGateTicket([done1, done2], '2026-11-10', true).ticket_id, 'D2');
  assert.equal(pickGateTicket([], '2026-11-10', false), null);
});

test('only the listed reasons complete a ticket, matched as whole text', () => {
  assert.equal(completesTicket('Sesuai jadwal keluar'), true);
  assert.equal(completesTicket(' selesai awal '), true);
  assert.equal(completesTicket('Tugas luar'), false);
  assert.equal(completesTicket('selesai jadwal kerja'), false); // the old hard-coded phrase
  assert.equal(completesTicket('Tugas luar', ['Tugas luar']), true);
  assert.equal(completesTicket(''), false);
});

test('a repeat inside the window is a duplicate; the next window is not', () => {
  const state = { last_action_hash: 'k', last_action_hash_time: '2026-11-10T03:00:00.000Z' };
  const t = Date.parse('2026-11-10T03:00:00.000Z');
  assert.equal(isDuplicate(state, 'k', t + 4999, 5), true);
  assert.equal(isDuplicate(state, 'k', t + 5000, 5), false);
  assert.equal(isDuplicate(state, 'other', t + 1000, 5), false);
  assert.equal(isDuplicate(null, 'k', t, 5), false);
});

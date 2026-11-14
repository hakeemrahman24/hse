// Run with: node --test web/jstest/   (no install needed)
// The form helpers are pure functions, so they are tested here without a browser.
const test = require('node:test');
const assert = require('node:assert/strict');
const I = require('../static/inputs.js');

test('formatDate builds dd/mm/yyyy as digits are typed', () => {
  const cases = {
    '': '', '2': '2', '24': '24', '240': '24/0', '2407': '24/07', '24071': '24/07/1',
    '24071983': '24/07/1983',
    '240719831234': '24/07/1983', // extra digits are dropped
    '24/071983': '24/07/1983',    // the slash was forgotten
    '24/': '24/',                  // a typed slash is kept, so Backspace can remove it
    '1/': '1/', '1/2/2020': '1/2/2020',
    '/': '', '24//07': '24/07',
    '24-07-1983': '24/07/1983', '24.07.1983': '24/07/1983', '24 07 1983': '24/07/1983',
    'ab24c07': '24/07',
  };
  for (const [raw, want] of Object.entries(cases)) assert.equal(I.formatDate(raw), want, JSON.stringify(raw));
});

test('maskRest shows the blanks still to fill in, after what is typed', () => {
  const cases = {
    '': '__/__/____', '1': '_/__/____', '24': '/__/____', '24/': '__/____', '24/0': '_/____',
    '24/07': '/____', '24/07/': '____', '24/07/19': '__', '24/07/1983': '',
    '1/2/2020': '', // a short day and month, finished: nothing left to fill
  };
  for (const [typed, want] of Object.entries(cases)) assert.equal(I.maskRest(typed), want, JSON.stringify(typed));
});

test('tidyDate pads a short day or month and says whether the date is real', () => {
  assert.deepEqual(I.tidyDate('1/2/2020'), { text: '01/02/2020', ok: true });
  assert.deepEqual(I.tidyDate('24/07/1983'), { text: '24/07/1983', ok: true });
  assert.equal(I.tidyDate('31/04/2020').ok, false);   // April has 30 days
  assert.equal(I.tidyDate('29/02/2020').ok, true);    // leap year
  assert.equal(I.tidyDate('29/02/2021').ok, false);
  assert.equal(I.tidyDate('24/07').ok, false);        // not finished
  assert.equal(I.tidyDate('24/07/198').ok, false);    // year needs four digits
  assert.equal(I.tidyDate('00/01/2020').ok, false);
  assert.deepEqual(I.tidyDate(''), { text: '', ok: true }); // blank is for "required" to judge
});

test('toISO and fromISO convert for the date picker', () => {
  assert.equal(I.toISO('24/07/1983'), '1983-07-24');
  assert.equal(I.toISO('1/2/2020'), '2020-02-01');
  assert.equal(I.toISO('31/04/2020'), '');
  assert.equal(I.toISO('nonsense'), '');
  assert.equal(I.fromISO('1983-07-24'), '24/07/1983');
  assert.equal(I.fromISO(''), '');
  assert.equal(I.fromISO('1983-7'), '');
});

test('isFuture compares a typed date with today', () => {
  assert.equal(I.isFuture('24/07/2030', '2026-11-08'), true);
  assert.equal(I.isFuture('08/11/2026', '2026-11-08'), false); // today is not the future
  assert.equal(I.isFuture('24/07/1983', '2026-11-08'), false);
  assert.equal(I.isFuture('24/07', '2026-11-08'), false);       // unfinished: not judged here
});

test('cleanPhone keeps digits and one leading plus', () => {
  assert.equal(I.cleanPhone('+62 812-3456 (789)'), '+628123456789');
  assert.equal(I.cleanPhone('0812 3456 789'), '08123456789');
  assert.equal(I.cleanPhone('++62'), '+62');
  assert.equal(I.cleanPhone('62+812'), '62812');
  assert.equal(I.cleanPhone('  '), '');
});

test('idRule follows the nationality like medpost: Indonesia is a NIK, others a passport', () => {
  assert.deepEqual(I.idRule('Indonesia'), { kind: 'nik', label: 'NIK (16 digits)', maxlength: 16, numeric: true });
  assert.equal(I.idRule('  indonesia ').kind, 'nik');
  const p = I.idRule('Vietnam');
  assert.deepEqual(p, { kind: 'passport', label: 'Passport number (7 to 9 letters or numbers)', maxlength: 9, numeric: false });
  assert.equal(I.idRule('').kind, 'any'); // nationality not chosen yet: no restriction, a neutral label
  assert.equal(I.idRule('').label, 'NIK or passport number');
});

test('cleanId keeps only what the kind of id allows', () => {
  assert.equal(I.cleanId('nik', '3201 0203-0490 0001'), '3201020304900001');
  assert.equal(I.cleanId('nik', '3201A02030490000199'), '3201020304900001'); // letters dropped, cut at 16
  assert.equal(I.cleanId('passport', 'ab-123 45'), 'AB12345');
  assert.equal(I.cleanId('passport', 'AB1234567890'), 'AB1234567'); // cut at 9
  assert.equal(I.cleanId('any', 'ab-12 x'), 'AB12X');
});

test('digitsOnly and tidyText', () => {
  assert.equal(I.digitsOnly('0001 2345-67'), '0001234567');
  assert.equal(I.tidyText('  Budi   Santoso '), 'Budi Santoso');
  assert.equal(I.tidyText('a\tb\n c'), 'a b c');
  assert.equal(I.tidyEmail('  Rina@Example.COM '), 'rina@example.com');
});

test('tidyEmail cleans what was pasted', () => {
  assert.equal(I.tidyEmail('mailto:Rina @ Example.com.'), 'rina@example.com');
  assert.equal(I.tidyEmail('<rina@example.com>'), 'rina@example.com');
  assert.equal(I.tidyEmail('rina​＠example。com'), 'rina@example.com');
});

test('tidyName cleans and title-styles a person name', () => {
  assert.equal(I.tidyName('  BUDI   santoso '), 'Budi Santoso');
  assert.equal(I.tidyName('Budi123 Santoso!'), 'Budi Santoso');
  assert.equal(I.tidyName('abdul rahman BIN ahmad'), 'Abdul Rahman bin Ahmad');
  assert.equal(I.tidyName('anne-marie o’neil'), "Anne-Marie O'Neil");
  assert.equal(I.tidyName('john a. smith iii'), 'John A. Smith III');
  assert.equal(I.tidyName('McDonald'), 'McDonald');
  assert.equal(I.tidyName('1234'), '');
});

// Run with: node --test web/jstest/   (the step arithmetic is pure, so no browser is needed)
const test = require('node:test');
const assert = require('node:assert/strict');
const W = require('../static/wizard.js');

test('clamp keeps a step inside the first and last section', () => {
  assert.equal(W.clamp(-1, 8), 0);
  assert.equal(W.clamp(3, 8), 3);
  assert.equal(W.clamp(8, 8), 7);
  assert.equal(W.clamp(99, 8), 7);
});

test('label names the step out of the total', () => {
  assert.equal(W.label(0, 8, 'Who is asking'), 'Step 1 of 8: Who is asking');
  assert.equal(W.label(7, 8, ''), 'Step 8 of 8');
});

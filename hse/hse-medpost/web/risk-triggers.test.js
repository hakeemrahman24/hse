const { readFileSync } = require("fs");
const { test } = require("node:test");
const assert = require("node:assert/strict");
const vm = require("vm");

const context = {};
vm.createContext(context);
vm.runInContext(readFileSync("web/static/risk-triggers.js", "utf8"), context);
const { triggerMatchesPosition, matchedRiskIds, newlyMatchedRiskIds } = context;

const risks = [
  { taskCategoryId: 1, name: "Food Handler", status: "Active", triggerWords: "food handler, cook" },
  { taskCategoryId: 2, name: "Professional Drivers", status: "Active", triggerWords: "driver, drivers" },
  { taskCategoryId: 3, name: "Work in Extreme Temperature", status: "Active", triggerWords: "extreme temperature" },
  { taskCategoryId: 4, name: "Old risk", status: "Archived", triggerWords: "welder" },
];

test("a job title matches a whole trigger word", () => {
  assert.equal(triggerMatchesPosition("Welder 3G", "welder"), true);
  assert.equal(triggerMatchesPosition("Senior Food Handler", "food handler"), true);
  assert.equal(triggerMatchesPosition("screwdriver", "driver"), false);
  assert.equal(triggerMatchesPosition("Helper", "welder"), false);
});

function ids(list) {
  return Array.from(list || []).join(",");
}

test("matching risks are the active boxes whose words appear", () => {
  assert.equal(ids(matchedRiskIds("Cook", risks)), "1");
  assert.equal(ids(matchedRiskIds("Professional Driver", risks)), "2");
  assert.equal(ids(matchedRiskIds("Welder 3G", risks)), "");
});

test("a new word ticks a box and removing it does not untick", () => {
  const first = newlyMatchedRiskIds("Cook", risks, []);
  assert.equal(ids(first.tick), "1");
  const still = newlyMatchedRiskIds("Cook", risks, first.matched);
  assert.equal(ids(still.tick), "");
  const cleared = newlyMatchedRiskIds("Helper", risks, still.matched);
  assert.equal(ids(cleared.tick), "");
  assert.equal(ids(cleared.matched), "");
  const again = newlyMatchedRiskIds("Cook", risks, cleared.matched);
  assert.equal(ids(again.tick), "1");
});

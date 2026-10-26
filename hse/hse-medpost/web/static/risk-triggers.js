// Whole-word match of a work position against an occupational-health-risk
// trigger. "Welder 3G" matches "welder". "driver" does not match inside
// "screwdriver", and "driver" does not match "drivers" — seed both forms
// when both should tick the box.
function triggerMatchesPosition(position, trigger) {
  const needles = String(trigger || "").toLowerCase().match(/[a-z0-9]+/g) || [];
  if (!needles.length) return false;
  const hay = String(position || "").toLowerCase().match(/[a-z0-9]+/g) || [];
  for (let i = 0; i <= hay.length - needles.length; i++) {
    let found = true;
    for (let j = 0; j < needles.length; j++) {
      if (hay[i + j] !== needles[j]) {
        found = false;
        break;
      }
    }
    if (found) return true;
  }
  return false;
}

function matchedRiskIds(position, categories) {
  const ids = [];
  for (const cat of categories || []) {
    if (cat.status === "Archived") continue;
    const words = String(cat.triggerWords || "").split(",");
    for (const word of words) {
      if (triggerMatchesPosition(position, word)) {
        ids.push(cat.taskCategoryId);
        break;
      }
    }
  }
  return ids;
}

// Tick only risks that newly match. A box the staff unticked stays off
// while the same word is still in the position, and a box they ticked by
// hand is left alone when the word is removed.
function newlyMatchedRiskIds(position, categories, alreadyMatched) {
  const matched = matchedRiskIds(position, categories);
  const prev = new Set(alreadyMatched || []);
  return {
    matched,
    tick: matched.filter((id) => !prev.has(id)),
  };
}

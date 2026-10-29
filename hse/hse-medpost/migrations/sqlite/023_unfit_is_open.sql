-- Unfit is an open result (owner, 2026-10-29): only Fit and Fit with note close a checkup. New and
-- edited records already save this way (isTerminalResult in internal/entrymcu/validity.go); this
-- brings the Unfit records saved before that into line, so counts by status and the renewal and
-- expiry filters that read IsTerminal agree with the rule.
--
-- Safe to run at every start: it only touches Unfit rows that still say Close or terminal.
UPDATE McuRecord SET CheckUpStatus = 'Open', IsTerminal = 0
WHERE ResultCriteria = 'Unfit' AND (CheckUpStatus IS NULL OR CheckUpStatus <> 'Open' OR IsTerminal <> 0);

-- 016 ticket daily-job state (slice 6a). Moments are CHAR(19) 'yyyy-mm-dd hh:mm:ss' (Jakarta).
--
-- WHY these live on the ticket: the daily departure job (ticket rules section 5) must be
-- safe to run again. Each step is guarded by one of these "done" fields, so a second run
-- on the same day, or a restart in the middle of a run, never sends a reminder twice.
--   lep_locked         the gate refuses a check-in for this ticket (permit expired or renewed)
--   weeks_on_site      whole weeks since the first check-in, refreshed daily
--   reminder_notified  the next-rotation reminder was queued
--   fatigue_flag       an early return is required (flag only, no e-mail)
--   week_n1_sent_at    when the "one week before the cap" mail was queued
--   week_n0_locked_at  when the "cap reached" mail was queued
ALTER TABLE ticket ADD COLUMN lep_locked INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ticket ADD COLUMN weeks_on_site INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ticket ADD COLUMN reminder_notified INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ticket ADD COLUMN fatigue_flag INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ticket ADD COLUMN week_n1_sent_at CHAR(19) NOT NULL DEFAULT '';
ALTER TABLE ticket ADD COLUMN week_n0_locked_at CHAR(19) NOT NULL DEFAULT '';

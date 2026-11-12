-- 016 ticket daily-job state (MariaDB). Keep in step with migrations/sqlite/016_ticket_daily.sql.
-- Run by hand after a mysqldump. Moments are CHAR(19) (Jakarta).
ALTER TABLE ticket ADD COLUMN lep_locked INT NOT NULL DEFAULT 0;
ALTER TABLE ticket ADD COLUMN weeks_on_site INT NOT NULL DEFAULT 0;
ALTER TABLE ticket ADD COLUMN reminder_notified INT NOT NULL DEFAULT 0;
ALTER TABLE ticket ADD COLUMN fatigue_flag INT NOT NULL DEFAULT 0;
ALTER TABLE ticket ADD COLUMN week_n1_sent_at CHAR(19) NOT NULL DEFAULT '';
ALTER TABLE ticket ADD COLUMN week_n0_locked_at CHAR(19) NOT NULL DEFAULT '';

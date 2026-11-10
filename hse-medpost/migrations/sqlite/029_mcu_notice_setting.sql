-- Medical checkup portal: how often submitters are told about reviewed and rejected documents, and
-- whether the reviewers are copied (owner, 2026-11-10). Single row (id 1). IntervalHours is checked
-- in Go (6, 12, 24 or 48), not here, so the file stays valid on both engines. LastRunAt is when the
-- last run finished without an error (UTC, YYYY-MM-DDTHH:MM:SS). Runs happen every IntervalHours
-- counted from StartHour in Timezone, so the default (12 hours from 08:00 WIB) runs at 08:00 and 20:00.
CREATE TABLE IF NOT EXISTS McuNoticeSetting (
    McuNoticeSettingID INTEGER PRIMARY KEY,
    IntervalHours      INTEGER NOT NULL DEFAULT 12,
    StartHour          INTEGER NOT NULL DEFAULT 8,         -- 0-23, in the zone below
    Timezone           TEXT NOT NULL DEFAULT 'WIB',        -- WIB, WITA, WIT or UTC, checked in Go
    CopyReviewers      INTEGER NOT NULL DEFAULT 1,
    LastRunAt          TEXT NULL
);
INSERT OR IGNORE INTO McuNoticeSetting (McuNoticeSettingID) VALUES (1);

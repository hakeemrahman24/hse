-- Medical checkup portal: the reviewers' mail list and the digest interval.
-- Design: plan/mcu-submission-plan.md section 8, changed by the owner on 2026-10-29: reviewers get
-- one digest of every document still waiting for review, every 24, 36 or 48 hours (not one mail
-- per batch). Timestamps are UTC, YYYY-MM-DDTHH:MM:SS.

-- The people who receive the digest. One plain list managed by the portal admin; nobody here has
-- an account in the portal or in the staff app.
CREATE TABLE IF NOT EXISTS McuReviewerEmail (
    ReviewerEmailID INTEGER PRIMARY KEY AUTOINCREMENT,
    Email           TEXT NOT NULL UNIQUE,              -- lower-cased
    Name            TEXT NOT NULL DEFAULT '',
    Active          INTEGER NOT NULL DEFAULT 1,
    EmailBouncedAt  TEXT NULL,                         -- set by the Brevo webhook; skipped while set
    CreatedAt       TEXT NOT NULL
);

-- Single row (id 1). IntervalHours is checked in Go (24, 36 or 48), not here, so the file stays
-- valid on both engines. LastSentAt is when the last digest went out.
CREATE TABLE IF NOT EXISTS McuDigestSetting (
    McuDigestSettingID INTEGER PRIMARY KEY,
    IntervalHours      INTEGER NOT NULL DEFAULT 24,
    LastSentAt         TEXT NULL
);
INSERT OR IGNORE INTO McuDigestSetting (McuDigestSettingID) VALUES (1);

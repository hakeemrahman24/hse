-- 012 ticket (MariaDB). Keep in step with migrations/sqlite/012_ticket.sql.
-- Run by hand after a mysqldump. Days are CHAR(10) 'yyyy-mm-dd', moments CHAR(19) (Jakarta).
ALTER TABLE permit ADD COLUMN dob CHAR(10) NOT NULL DEFAULT '';

CREATE TABLE ticket (
    ticket_id          VARCHAR(12)  NOT NULL PRIMARY KEY,   -- TKT-xxxxx
    lep_no             VARCHAR(12)  NOT NULL,               -- LEP-xxxxx, or WVR-xxxxx for a waiver ticket
    is_work            INT          NOT NULL,               -- 0 for a waiver ticket
    status             VARCHAR(20)  NOT NULL,               -- PendingDoctor / Approved / Rejected / Closed / Cancelled
    arrival            CHAR(10)     NOT NULL,
    departure          CHAR(10)     NOT NULL,
    location           VARCHAR(10)  NOT NULL,               -- Onshore / Offshore, chosen per ticket
    pattern            VARCHAR(30)  NOT NULL DEFAULT '',    -- Normal, Consecutive, ShortBreak, ...Extended
    rotation_no        INT          NOT NULL,
    fatigue_due        CHAR(10)     NOT NULL DEFAULT '',
    gap_days           INT          NOT NULL DEFAULT 0,
    short_break        INT          NOT NULL DEFAULT 0,
    rotation_extension INT          NOT NULL DEFAULT 0,
    fatigue_consent    INT          NOT NULL DEFAULT 0,
    doctor_review      VARCHAR(20)  NOT NULL DEFAULT '',
    doctor_note        VARCHAR(80)  NOT NULL DEFAULT '',
    mcu_result         VARCHAR(20)  NOT NULL DEFAULT '',   -- copied from the permit
    med_answer         CHAR(1)      NOT NULL DEFAULT '',   -- Y / N for "Fit with note"
    med_details        VARCHAR(255) NOT NULL DEFAULT '',
    med_photo          VARCHAR(80)  NOT NULL DEFAULT '',
    requester_email    VARCHAR(160) NOT NULL DEFAULT '',   -- reminders go here (jobs, later)
    record_start       CHAR(19)     NOT NULL DEFAULT '',   -- set by gate check-in
    closed_at          CHAR(19)     NOT NULL DEFAULT '',
    closed_reason      VARCHAR(60)  NOT NULL DEFAULT '',
    submitted_at       CHAR(19)     NOT NULL
);

CREATE INDEX idx_ticket_lep ON ticket (lep_no, arrival);

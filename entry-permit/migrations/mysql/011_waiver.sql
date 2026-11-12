-- 011 waiver (MariaDB). Keep in step with migrations/sqlite/011_waiver.sql.
-- Run by hand after a mysqldump. Days are CHAR(10) 'yyyy-mm-dd', moments CHAR(19) (Jakarta).
CREATE TABLE waiver (
    request_id      VARCHAR(12)  NOT NULL PRIMARY KEY,   -- WVR-xxxxx
    status          VARCHAR(40)  NOT NULL,
    applicant_type  VARCHAR(20)  NOT NULL,               -- Government / VisitorCompany
    company_id      BIGINT       NOT NULL,               -- the medpost company mirror id at submit
    company         VARCHAR(160) NOT NULL,               -- snapshot of its name
    visitor_name    VARCHAR(120) NOT NULL,
    gender          VARCHAR(10)  NOT NULL,
    dob             CHAR(10)     NOT NULL,
    nationality     VARCHAR(60)  NOT NULL,
    nik             VARCHAR(20)  NOT NULL,               -- cleaned NIK / passport number
    position        VARCHAR(120) NOT NULL,
    department      VARCHAR(120) NOT NULL,
    visit_location  VARCHAR(120) NOT NULL,
    purpose         VARCHAR(255) NOT NULL,
    visit_from      CHAR(10)     NOT NULL,
    visit_to        CHAR(10)     NOT NULL,
    days            INT          NOT NULL,               -- inclusive, derived from the dates
    requester_name  VARCHAR(120) NOT NULL,
    requester_email VARCHAR(160) NOT NULL,
    phone           VARCHAR(20)  NOT NULL,
    risk            VARCHAR(500) NOT NULL,
    precaution      VARCHAR(500) NOT NULL,
    prevent         VARCHAR(500) NOT NULL,
    takes_medicine  INT          NOT NULL DEFAULT 0,
    medicine1       VARCHAR(120) NOT NULL DEFAULT '',
    dosage1         VARCHAR(120) NOT NULL DEFAULT '',
    passport_until  CHAR(10)     NOT NULL DEFAULT '',
    visa_until      CHAR(10)     NOT NULL DEFAULT '',
    id_doc          VARCHAR(80)  NOT NULL DEFAULT '',
    id_photo        VARCHAR(80)  NOT NULL DEFAULT '',
    letter_doc      VARCHAR(80)  NOT NULL DEFAULT '',
    visa_doc        VARCHAR(80)  NOT NULL DEFAULT '',
    declared_at     CHAR(19)     NOT NULL,
    created_at      CHAR(19)     NOT NULL
);

CREATE INDEX idx_waiver_nik     ON waiver (nik);
CREATE INDEX idx_waiver_visit   ON waiver (visit_to);
CREATE INDEX idx_waiver_company ON waiver (company, visit_from);

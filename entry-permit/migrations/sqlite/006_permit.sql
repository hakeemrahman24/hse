-- 006 permit (work entry permit), part 1 columns: who, which MCU, what state.
-- Slice 3c adds the rest (address, uploads, review fields) with ALTER TABLE.
-- Days are CHAR(10) 'yyyy-mm-dd', moments CHAR(19) 'yyyy-mm-dd hh:mm:ss' (Jakarta).
CREATE TABLE permit (
    request_id       VARCHAR(12)  NOT NULL PRIMARY KEY,   -- WEP-xxxxx
    lep_no           VARCHAR(12)  NOT NULL DEFAULT '',    -- LEP-xxxxx, set at Final approval
    status           VARCHAR(40)  NOT NULL,
    patient_id       BIGINT       NOT NULL,               -- hse-medpost patientId
    personal_id      VARCHAR(30)  NOT NULL DEFAULT '',    -- NIK / passport when medpost supplies it
    worker_name      VARCHAR(120) NOT NULL,
    company          VARCHAR(160) NOT NULL DEFAULT '',
    nationality      VARCHAR(60)  NOT NULL DEFAULT '',
    requester_name   VARCHAR(120) NOT NULL,
    requester_email  VARCHAR(160) NOT NULL,
    work_location    VARCHAR(10)  NOT NULL,               -- Onshore / Offshore
    mcu_checkup_id   VARCHAR(40)  NOT NULL DEFAULT '',
    mcu_valid_until  CHAR(10)     NOT NULL DEFAULT '',    -- snapshot at submit
    visa_expiry      CHAR(10)     NOT NULL DEFAULT '',
    lep_valid_until  CHAR(10)     NOT NULL DEFAULT '',
    created_at       CHAR(19)     NOT NULL
);

CREATE INDEX idx_permit_patient  ON permit (patient_id, created_at);
CREATE INDEX idx_permit_personal ON permit (personal_id, created_at);

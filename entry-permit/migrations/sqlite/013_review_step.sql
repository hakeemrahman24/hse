-- 013 review_step (slice 4a): one row per decision a reviewer made on a request.
-- Replaces the per-stage columns of the old sheet (reviewer, time, note for each stage).
-- Moments are CHAR(19) 'yyyy-mm-dd hh:mm:ss' (Jakarta).
--
-- WHY the key is (subject_id, stage): a stage can be decided once. The status guard in
-- reviewlog stops a second decision, and this key is the last guard if that ever failed.
CREATE TABLE review_step (
    subject_id VARCHAR(12)  NOT NULL,               -- WEP-xxxxx or WVR-xxxxx
    stage      VARCHAR(10)  NOT NULL,               -- doctor / stage1 / final
    reviewer   VARCHAR(80)  NOT NULL,               -- display name of the signed-in reviewer
    decision   VARCHAR(10)  NOT NULL,               -- approved / rejected
    note       VARCHAR(500) NOT NULL DEFAULT '',
    at         CHAR(19)     NOT NULL,
    PRIMARY KEY (subject_id, stage)
);

-- 013 review_step (MariaDB). Keep in step with migrations/sqlite/013_review_step.sql.
-- Run by hand after a mysqldump. Moments are CHAR(19) 'yyyy-mm-dd hh:mm:ss' (Jakarta).
CREATE TABLE review_step (
    subject_id VARCHAR(12)  NOT NULL,               -- WEP-xxxxx or WVR-xxxxx
    stage      VARCHAR(10)  NOT NULL,               -- doctor / stage1 / final
    reviewer   VARCHAR(80)  NOT NULL,               -- display name of the signed-in reviewer
    decision   VARCHAR(10)  NOT NULL,               -- approved / rejected
    note       VARCHAR(500) NOT NULL DEFAULT '',
    at         CHAR(19)     NOT NULL,
    PRIMARY KEY (subject_id, stage)
);

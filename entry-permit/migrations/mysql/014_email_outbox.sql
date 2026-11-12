-- 014 email_outbox (MariaDB). Keep in step with migrations/sqlite/014_email_outbox.sql.
-- Run by hand after a mysqldump. Moments are CHAR(19) 'yyyy-mm-dd hh:mm:ss' (Jakarta).
CREATE TABLE email_outbox (
    id           BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    queued_at    CHAR(19)     NOT NULL,
    status       VARCHAR(10)  NOT NULL,               -- Pending / Sent / Failed
    kind         VARCHAR(30)  NOT NULL,
    source_key   VARCHAR(20)  NOT NULL DEFAULT '',
    to_addrs     VARCHAR(500) NOT NULL,
    cc_addrs     VARCHAR(500) NOT NULL DEFAULT '',
    subject      VARCHAR(300) NOT NULL,
    text_body    MEDIUMTEXT   NOT NULL,
    html_body    MEDIUMTEXT   NOT NULL,
    attempts     INTEGER      NOT NULL DEFAULT 0,
    next_attempt CHAR(19)     NOT NULL,
    last_error   VARCHAR(300) NOT NULL DEFAULT '',
    sent_at      CHAR(19)     NOT NULL DEFAULT ''
);

CREATE INDEX idx_outbox_due ON email_outbox (status, next_attempt);

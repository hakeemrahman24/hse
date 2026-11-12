-- 014 email_outbox (slice 4c): every e-mail is stored first, then sent.
-- Moments are CHAR(19) 'yyyy-mm-dd hh:mm:ss' (Jakarta).
--
-- WHY store before sending: a mail server that is down must not lose an approval
-- notice. The row stays Pending and the drain retries it; after outbox_max_attempts
-- it becomes Failed and shows on the admin page until someone requeues it.
-- WHY next_attempt is moved forward before each send: two drains (or a drain and
-- the immediate send) can then never both take the same row.
CREATE TABLE email_outbox (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    queued_at    CHAR(19)     NOT NULL,
    status       VARCHAR(10)  NOT NULL,               -- Pending / Sent / Failed
    kind         VARCHAR(30)  NOT NULL,               -- wep-approved, wep-rejected, ...
    source_key   VARCHAR(20)  NOT NULL DEFAULT '',    -- the LEP / WEP / WVR number it is about
    to_addrs     VARCHAR(500) NOT NULL,               -- comma list, lower-case
    cc_addrs     VARCHAR(500) NOT NULL DEFAULT '',
    subject      VARCHAR(300) NOT NULL,
    text_body    TEXT         NOT NULL,
    html_body    TEXT         NOT NULL,
    attempts     INTEGER      NOT NULL DEFAULT 0,
    next_attempt CHAR(19)     NOT NULL,
    last_error   VARCHAR(300) NOT NULL DEFAULT '',
    sent_at      CHAR(19)     NOT NULL DEFAULT ''
);

CREATE INDEX idx_outbox_due ON email_outbox (status, next_attempt);

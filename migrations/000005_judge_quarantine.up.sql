CREATE TABLE judge_message_quarantine (
    stream_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    payload_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    reason VARCHAR(32) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (stream_id, payload_digest),
    CONSTRAINT ck_judge_quarantine_reason CHECK (reason IN ('invalid_envelope','unresolvable_envelope'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

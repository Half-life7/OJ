ALTER TABLE submissions DROP CHECK ck_submissions_language,
 ADD CONSTRAINT ck_submissions_language CHECK (language IN ('cpp17','go','python3','java','javascript','text'));
ALTER TABLE global_board_cache DROP PRIMARY KEY,
 ADD COLUMN period VARCHAR(10) NOT NULL DEFAULT 'all',
 ADD PRIMARY KEY (kind,period),
 ADD CONSTRAINT ck_board_period CHECK (period IN ('all','week','month'));
CREATE TABLE rejudge_requests (
 actor_id BIGINT UNSIGNED NOT NULL,
 request_key VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 request_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 tasks JSON NOT NULL,
 created_at DATETIME(6) NOT NULL,
 PRIMARY KEY (actor_id,request_key),
 FOREIGN KEY (actor_id) REFERENCES users(id)
) ENGINE=InnoDB;

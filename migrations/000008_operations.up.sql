CREATE TABLE contest_freezes (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
 contest_id BIGINT UNSIGNED NOT NULL,
 standings JSON NOT NULL,
 created_at DATETIME(6) NOT NULL,
 ended_at DATETIME(6) NULL,
 KEY idx_freeze_current (contest_id,ended_at,id),
 FOREIGN KEY (contest_id) REFERENCES contests(id)
) ENGINE=InnoDB;
CREATE TABLE contest_imports (
 token CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
 contest_id BIGINT UNSIGNED NOT NULL,
 actor_id BIGINT UNSIGNED NOT NULL,
 csv_data MEDIUMTEXT NOT NULL,
 preview JSON NOT NULL,
 expires_at DATETIME(6) NOT NULL,
 applied_at DATETIME(6) NULL,
 FOREIGN KEY (contest_id) REFERENCES contests(id),
 FOREIGN KEY (actor_id) REFERENCES users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

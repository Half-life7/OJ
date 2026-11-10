CREATE TABLE community_documents (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
 owner_id BIGINT UNSIGNED NOT NULL,
 kind VARCHAR(20) NOT NULL,
 title VARCHAR(200) NOT NULL,
 body MEDIUMTEXT NOT NULL,
 visibility VARCHAR(10) NOT NULL,
 review_status VARCHAR(10) NOT NULL DEFAULT 'pending',
 problem_id BIGINT UNSIGNED NULL,
 contest_id BIGINT UNSIGNED NULL,
 parent_id BIGINT UNSIGNED NULL,
 revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
 created_at DATETIME(6) NOT NULL,
 updated_at DATETIME(6) NOT NULL,
 published_at DATETIME(6) NULL,
 deleted_at DATETIME(6) NULL,
 KEY idx_community_kind (kind,id),
 KEY idx_community_owner (owner_id,kind,id),
 KEY idx_community_problem (problem_id,kind,id),
 KEY idx_community_contest (contest_id,kind,id),
 KEY idx_community_parent (parent_id,id),
 FOREIGN KEY (owner_id) REFERENCES users(id),
 FOREIGN KEY (problem_id) REFERENCES problems(id),
 FOREIGN KEY (contest_id) REFERENCES contests(id),
 FOREIGN KEY (parent_id) REFERENCES community_documents(id),
 CONSTRAINT ck_document_kind CHECK (kind IN ('list','discussion','solution')),
 CONSTRAINT ck_document_visibility CHECK (visibility IN ('public','private')),
 CONSTRAINT ck_document_review CHECK (review_status IN ('pending','approved','rejected')),
 CONSTRAINT ck_document_size CHECK (OCTET_LENGTH(body)<=65536),
 CONSTRAINT ck_document_context CHECK ((kind='list' AND problem_id IS NULL AND contest_id IS NULL AND parent_id IS NULL) OR (kind='solution' AND problem_id IS NOT NULL AND contest_id IS NULL AND parent_id IS NULL) OR (kind='discussion' AND ((problem_id IS NOT NULL AND contest_id IS NULL) OR (problem_id IS NULL AND contest_id IS NOT NULL))))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE problem_list_items (
 document_id BIGINT UNSIGNED NOT NULL,
 problem_id BIGINT UNSIGNED NOT NULL,
 position INT UNSIGNED NOT NULL,
 PRIMARY KEY (document_id,problem_id),
 UNIQUE KEY uq_list_position (document_id,position),
 FOREIGN KEY (document_id) REFERENCES community_documents(id),
 FOREIGN KEY (problem_id) REFERENCES problems(id)
) ENGINE=InnoDB;
CREATE TABLE problem_list_favorites (
 document_id BIGINT UNSIGNED NOT NULL,
 user_id BIGINT UNSIGNED NOT NULL,
 created_at DATETIME(6) NOT NULL,
 PRIMARY KEY (document_id,user_id),
 FOREIGN KEY (document_id) REFERENCES community_documents(id),
 FOREIGN KEY (user_id) REFERENCES users(id)
) ENGINE=InnoDB;
CREATE TABLE discussion_likes (
 document_id BIGINT UNSIGNED NOT NULL,
 user_id BIGINT UNSIGNED NOT NULL,
 created_at DATETIME(6) NOT NULL,
 PRIMARY KEY (document_id,user_id),
 FOREIGN KEY (document_id) REFERENCES community_documents(id),
 FOREIGN KEY (user_id) REFERENCES users(id)
) ENGINE=InnoDB;
CREATE TABLE community_write_limits (
 user_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
 window_start DATETIME(6) NOT NULL,
 writes INT UNSIGNED NOT NULL,
 FOREIGN KEY (user_id) REFERENCES users(id)
) ENGINE=InnoDB;
CREATE TABLE global_board_cache (
 kind VARCHAR(20) NOT NULL PRIMARY KEY,
 snapshot_json JSON NOT NULL,
 built_at DATETIME(6) NOT NULL,
 CONSTRAINT ck_board_kind CHECK (kind IN ('practice','contest','solutions'))
) ENGINE=InnoDB;

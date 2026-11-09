CREATE TABLE problems (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    title VARCHAR(200) NOT NULL,
    difficulty VARCHAR(20) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'draft',
    visibility VARCHAR(20) NOT NULL DEFAULT 'hidden',
    snapshot_json JSON NOT NULL,
    revision BIGINT UNSIGNED NOT NULL,
    statement_version BIGINT UNSIGNED NULL,
    data_version BIGINT UNSIGNED NULL,
    creator_id BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_problems_public (status, visibility, id),
    KEY idx_problems_difficulty (difficulty, id),
    CONSTRAINT fk_problems_creator FOREIGN KEY (creator_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT ck_problems_difficulty CHECK (difficulty IN ('easy','medium','hard')),
    CONSTRAINT ck_problems_status CHECK (status IN ('draft','published','archived')),
    CONSTRAINT ck_problems_visibility CHECK (visibility IN ('public','hidden','contest_only')),
    CONSTRAINT ck_problems_revision CHECK (revision > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE problem_versions (
    problem_id BIGINT UNSIGNED NOT NULL,
    version BIGINT UNSIGNED NOT NULL,
    snapshot_json JSON NOT NULL,
    created_by BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (problem_id, version),
    CONSTRAINT fk_problem_versions_problem FOREIGN KEY (problem_id) REFERENCES problems(id) ON DELETE RESTRICT,
    CONSTRAINT fk_problem_versions_creator FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT ck_problem_versions_version CHECK (version > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE problem_data_versions (
    problem_id BIGINT UNSIGNED NOT NULL,
    data_version BIGINT UNSIGNED NOT NULL,
    storage_key CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    case_count INT UNSIGNED NOT NULL,
    total_bytes BIGINT NOT NULL,
    total_score BIGINT NOT NULL,
    revision BIGINT UNSIGNED NOT NULL,
    created_by BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (problem_id, data_version),
    UNIQUE KEY uq_problem_data_storage_key (storage_key),
    CONSTRAINT fk_problem_data_problem FOREIGN KEY (problem_id) REFERENCES problems(id) ON DELETE RESTRICT,
    CONSTRAINT fk_problem_data_creator FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT ck_problem_data_version CHECK (data_version > 0),
    CONSTRAINT ck_problem_data_case_count CHECK (case_count BETWEEN 1 AND 512),
    CONSTRAINT ck_problem_data_total_bytes CHECK (total_bytes BETWEEN 0 AND 67108864),
    CONSTRAINT ck_problem_data_total_score CHECK (total_score > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE testcases (
    problem_id BIGINT UNSIGNED NOT NULL,
    data_version BIGINT UNSIGNED NOT NULL,
    case_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    score BIGINT NOT NULL,
    input_bytes BIGINT NOT NULL,
    output_bytes BIGINT NOT NULL,
    PRIMARY KEY (problem_id, data_version, case_id),
    CONSTRAINT fk_testcases_data FOREIGN KEY (problem_id, data_version) REFERENCES problem_data_versions(problem_id, data_version) ON DELETE RESTRICT,
    CONSTRAINT ck_testcases_score CHECK (score >= 0),
    CONSTRAINT ck_testcases_input_bytes CHECK (input_bytes BETWEEN 0 AND 16777216),
    CONSTRAINT ck_testcases_output_bytes CHECK (output_bytes BETWEEN 0 AND 16777216)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE tags (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    name VARCHAR(32) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_tags_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE problem_tags (
    problem_id BIGINT UNSIGNED NOT NULL,
    tag_id BIGINT UNSIGNED NOT NULL,
    PRIMARY KEY (problem_id, tag_id),
    KEY idx_problem_tags_tag (tag_id, problem_id),
    CONSTRAINT fk_problem_tags_problem FOREIGN KEY (problem_id) REFERENCES problems(id) ON DELETE RESTRICT,
    CONSTRAINT fk_problem_tags_tag FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- Pointers are nullable only while a new object is being assembled inside a
-- transaction; composite keys prevent referencing another problem's version.
ALTER TABLE problems
    ADD CONSTRAINT fk_problems_statement FOREIGN KEY (id, statement_version) REFERENCES problem_versions(problem_id, version) ON DELETE RESTRICT,
    ADD CONSTRAINT fk_problems_data FOREIGN KEY (id, data_version) REFERENCES problem_data_versions(problem_id, data_version) ON DELETE RESTRICT;

-- This row serializes all admissions, so concurrent API requests cannot pass
-- an identical capacity check and grow the pending queue beyond its budget.
CREATE TABLE judge_admission (
    id TINYINT UNSIGNED NOT NULL,
    PRIMARY KEY (id),
    CONSTRAINT ck_judge_admission_singleton CHECK (id = 1)
) ENGINE=InnoDB;
INSERT INTO judge_admission (id) VALUES (1);

CREATE TABLE submissions (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    user_id BIGINT UNSIGNED NOT NULL,
    problem_id BIGINT UNSIGNED NOT NULL,
    statement_version BIGINT UNSIGNED NOT NULL,
    data_version BIGINT UNSIGNED NULL,
    contest_id BIGINT UNSIGNED NULL,
    contest_team_id BIGINT UNSIGNED NULL,
    kind VARCHAR(10) NOT NULL,
    language VARCHAR(20) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    source_code MEDIUMTEXT NOT NULL,
    stdin MEDIUMBLOB NULL,
    generation BIGINT UNSIGNED NOT NULL DEFAULT 1,
    status VARCHAR(32) NOT NULL DEFAULT 'Pending',
    score BIGINT NOT NULL DEFAULT 0,
    execution_time_ms BIGINT NOT NULL DEFAULT 0,
    memory_usage_kb BIGINT NOT NULL DEFAULT 0,
    passed_cases INT UNSIGNED NOT NULL DEFAULT 0,
    total_cases INT UNSIGNED NOT NULL,
    output MEDIUMBLOB NOT NULL,
    error_message MEDIUMTEXT NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_submissions_user (user_id, id),
    KEY idx_submissions_user_problem (user_id, problem_id, id),
    KEY idx_submissions_user_kind (user_id, kind, id),
    KEY idx_submissions_problem (problem_id, id),
    CONSTRAINT fk_submissions_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT fk_submissions_problem FOREIGN KEY (problem_id) REFERENCES problems(id) ON DELETE RESTRICT,
    CONSTRAINT fk_submissions_statement FOREIGN KEY (problem_id, statement_version) REFERENCES problem_versions(problem_id, version) ON DELETE RESTRICT,
    CONSTRAINT fk_submissions_data FOREIGN KEY (problem_id, data_version) REFERENCES problem_data_versions(problem_id, data_version) ON DELETE RESTRICT,
    CONSTRAINT ck_submissions_kind CHECK (kind IN ('Run','Submit')),
    CONSTRAINT ck_submissions_language CHECK (language IN ('cpp17','go','python3','java')),
    CONSTRAINT ck_submissions_generation CHECK (generation > 0),
    CONSTRAINT ck_submissions_status CHECK (status IN ('Pending','Queued','Compiling','Running','Accepted','WrongAnswer','TimeLimitExceeded','MemoryLimitExceeded','RuntimeError','CompileError','SystemError','Cancelled')),
    CONSTRAINT ck_submissions_resources CHECK (score >= 0 AND execution_time_ms >= 0 AND memory_usage_kb >= 0 AND passed_cases <= total_cases),
    CONSTRAINT ck_submissions_source_size CHECK (OCTET_LENGTH(source_code) BETWEEN 1 AND 65536),
    CONSTRAINT ck_submissions_stdin_size CHECK (stdin IS NULL OR OCTET_LENGTH(stdin) <= 65536),
    CONSTRAINT ck_submissions_data_required CHECK (kind = 'Run' OR data_version IS NOT NULL),
    CONSTRAINT ck_submissions_run_unscored CHECK (kind = 'Submit' OR score = 0),
    CONSTRAINT ck_submissions_practice CHECK (contest_id IS NULL AND contest_team_id IS NULL)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE judge_tasks (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    submission_id BIGINT UNSIGNED NOT NULL,
    generation BIGINT UNSIGNED NOT NULL,
    attempt INT UNSIGNED NOT NULL DEFAULT 1,
    status VARCHAR(32) NOT NULL DEFAULT 'Pending',
    lease_token CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL,
    lease_expires_at DATETIME(6) NULL,
    error_message MEDIUMTEXT NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_judge_tasks_generation (submission_id, generation),
    UNIQUE KEY uq_judge_tasks_envelope (id, submission_id, generation),
    KEY idx_judge_tasks_status (status, id),
    KEY idx_judge_tasks_lease (status, lease_expires_at),
    CONSTRAINT fk_judge_tasks_submission FOREIGN KEY (submission_id) REFERENCES submissions(id) ON DELETE RESTRICT,
    CONSTRAINT ck_judge_tasks_generation CHECK (generation > 0),
    CONSTRAINT ck_judge_tasks_attempt CHECK (attempt BETWEEN 1 AND 4),
    CONSTRAINT ck_judge_tasks_status CHECK (status IN ('Pending','Queued','Compiling','Running','Accepted','WrongAnswer','TimeLimitExceeded','MemoryLimitExceeded','RuntimeError','CompileError','SystemError','Cancelled')),
    CONSTRAINT ck_judge_tasks_lease CHECK ((lease_token IS NULL AND lease_expires_at IS NULL) OR (lease_token IS NOT NULL AND lease_expires_at IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE judge_task_outbox (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    task_id BIGINT UNSIGNED NOT NULL,
    submission_id BIGINT UNSIGNED NOT NULL,
    generation BIGINT UNSIGNED NOT NULL,
    attempt INT UNSIGNED NOT NULL,
    dispatch_attempts INT UNSIGNED NOT NULL DEFAULT 0,
    dispatched_at DATETIME(6) NULL,
    stream_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_judge_outbox_attempt (task_id, attempt),
    KEY idx_judge_outbox_pending (dispatched_at, id),
    CONSTRAINT fk_judge_outbox_task FOREIGN KEY (task_id, submission_id, generation) REFERENCES judge_tasks(id, submission_id, generation) ON DELETE RESTRICT,
    CONSTRAINT ck_judge_outbox_attempt CHECK (attempt BETWEEN 1 AND 4),
    CONSTRAINT ck_judge_outbox_dispatch CHECK ((dispatched_at IS NULL AND stream_id IS NULL) OR (dispatched_at IS NOT NULL AND stream_id IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- One authoritative result per generation; a later generation preserves this
-- row and appends another. attempt identifies the accepted execution result.
CREATE TABLE submission_results (
    submission_id BIGINT UNSIGNED NOT NULL,
    generation BIGINT UNSIGNED NOT NULL,
    attempt INT UNSIGNED NOT NULL,
    status VARCHAR(32) NOT NULL,
    score BIGINT NOT NULL,
    execution_time_ms BIGINT NOT NULL,
    memory_usage_kb BIGINT NOT NULL,
    passed_cases INT UNSIGNED NOT NULL,
    total_cases INT UNSIGNED NOT NULL,
    output MEDIUMBLOB NOT NULL,
    error_message MEDIUMTEXT NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (submission_id, generation),
    CONSTRAINT fk_submission_results_task FOREIGN KEY (submission_id, generation) REFERENCES judge_tasks(submission_id, generation) ON DELETE RESTRICT,
    CONSTRAINT ck_submission_results_attempt CHECK (attempt BETWEEN 1 AND 4),
    CONSTRAINT ck_submission_results_status CHECK (status IN ('Accepted','WrongAnswer','TimeLimitExceeded','MemoryLimitExceeded','RuntimeError','CompileError','SystemError','Cancelled')),
    CONSTRAINT ck_submission_results_resources CHECK (score >= 0 AND execution_time_ms >= 0 AND memory_usage_kb >= 0 AND passed_cases <= total_cases)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE submission_case_results (
    submission_id BIGINT UNSIGNED NOT NULL,
    generation BIGINT UNSIGNED NOT NULL,
    case_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    status VARCHAR(32) NOT NULL,
    score BIGINT NOT NULL,
    execution_time_ms BIGINT NOT NULL,
    memory_usage_kb BIGINT NOT NULL,
    PRIMARY KEY (submission_id, generation, case_id),
    CONSTRAINT fk_submission_case_result FOREIGN KEY (submission_id, generation) REFERENCES submission_results(submission_id, generation) ON DELETE RESTRICT,
    CONSTRAINT ck_submission_case_status CHECK (status IN ('Accepted','WrongAnswer','TimeLimitExceeded','MemoryLimitExceeded','RuntimeError','CompileError','SystemError','Cancelled')),
    CONSTRAINT ck_submission_case_resources CHECK (score >= 0 AND execution_time_ms >= 0 AND memory_usage_kb >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

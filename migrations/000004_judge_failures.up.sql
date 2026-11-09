-- Initial submissions use the default; retry intents provide a bounded delay.
ALTER TABLE judge_task_outbox
    ADD COLUMN available_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    ADD KEY idx_judge_outbox_due (dispatched_at, available_at, id);

CREATE TABLE judge_attempt_failures (
    task_id BIGINT UNSIGNED NOT NULL,
    submission_id BIGINT UNSIGNED NOT NULL,
    generation BIGINT UNSIGNED NOT NULL,
    attempt INT UNSIGNED NOT NULL,
    reason VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    disposition VARCHAR(10) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (task_id, attempt),
    UNIQUE KEY uq_judge_failure_envelope (task_id, submission_id, generation, attempt),
    CONSTRAINT fk_judge_failure_task FOREIGN KEY (task_id, submission_id, generation) REFERENCES judge_tasks(id, submission_id, generation) ON DELETE RESTRICT,
    CONSTRAINT ck_judge_failure_reason CHECK (reason IN ('runner_unavailable','runner_protocol','runner_system_error','data_unavailable','execution_interrupted')),
    CONSTRAINT ck_judge_failure_disposition CHECK ((attempt BETWEEN 1 AND 3 AND disposition = 'retry') OR (attempt = 4 AND disposition = 'dead'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- A durable dead-letter record also serves as its transactional outbox.
CREATE TABLE judge_dead_letters (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    task_id BIGINT UNSIGNED NOT NULL,
    submission_id BIGINT UNSIGNED NOT NULL,
    generation BIGINT UNSIGNED NOT NULL,
    attempt INT UNSIGNED NOT NULL,
    reason VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    dispatch_attempts INT UNSIGNED NOT NULL DEFAULT 0,
    dispatched_at DATETIME(6) NULL,
    stream_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_judge_dead_task (task_id),
    KEY idx_judge_dead_pending (dispatched_at, id),
    CONSTRAINT fk_judge_dead_failure FOREIGN KEY (task_id, submission_id, generation, attempt) REFERENCES judge_attempt_failures(task_id, submission_id, generation, attempt) ON DELETE RESTRICT,
    CONSTRAINT ck_judge_dead_attempt CHECK (attempt = 4),
    CONSTRAINT ck_judge_dead_reason CHECK (reason IN ('runner_unavailable','runner_protocol','runner_system_error','data_unavailable','execution_interrupted')),
    CONSTRAINT ck_judge_dead_dispatch CHECK ((dispatched_at IS NULL AND stream_id IS NULL) OR (dispatched_at IS NOT NULL AND stream_id IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

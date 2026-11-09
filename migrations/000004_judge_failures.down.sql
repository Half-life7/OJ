DROP TABLE judge_dead_letters;
DROP TABLE judge_attempt_failures;
ALTER TABLE judge_task_outbox
    DROP KEY idx_judge_outbox_due,
    DROP COLUMN available_at;

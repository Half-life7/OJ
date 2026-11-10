-- Refuse silent downgrading of immutable SPJ/output-only snapshots to a build
-- that would interpret them as ordinary programming/token problems.
ALTER TABLE problem_versions ADD CONSTRAINT ck_v9_rollback_guard CHECK (
 COALESCE(JSON_UNQUOTE(JSON_EXTRACT(snapshot_json,'$.checker_mode')),'') <> 'spj'
 AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(snapshot_json,'$.problem_type')),'') <> 'output_only'
);
ALTER TABLE problem_versions DROP CHECK ck_v9_rollback_guard;
ALTER TABLE submissions DROP CHECK ck_submissions_language,
 ADD CONSTRAINT ck_submissions_language CHECK (language IN ('cpp17','go','python3','java'));
DROP TABLE rejudge_requests;
DELETE FROM global_board_cache;
ALTER TABLE global_board_cache DROP PRIMARY KEY, DROP CHECK ck_board_period, DROP COLUMN period, ADD PRIMARY KEY (kind);

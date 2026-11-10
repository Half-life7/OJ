-- Deliberately fail if contest submissions exist rather than deleting history.
ALTER TABLE submissions ADD CONSTRAINT ck_submissions_practice CHECK (contest_id IS NULL AND contest_team_id IS NULL);
ALTER TABLE submissions DROP FOREIGN KEY fk_submission_contest_team, DROP CHECK ck_submission_contest_pair, DROP INDEX idx_submissions_contest;
DROP TABLE contest_points;
DROP TABLE contest_snapshots;
DROP TABLE contest_team_members;
DROP TABLE contest_teams;
DROP TABLE contest_problems;
DROP TABLE contest_admins;
DROP TABLE contests;
ALTER TABLE persons DROP COLUMN organization, DROP COLUMN revision;

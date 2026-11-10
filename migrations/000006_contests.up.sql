ALTER TABLE persons ADD COLUMN organization VARCHAR(200) NOT NULL DEFAULT '', ADD COLUMN revision BIGINT UNSIGNED NOT NULL DEFAULT 1;

CREATE TABLE contests (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
 title VARCHAR(200) NOT NULL,
 scoring_mode VARCHAR(10) NOT NULL,
 enrollment_policy VARCHAR(20) NOT NULL,
 approval_required BOOLEAN NOT NULL,
 team_mode BOOLEAN NOT NULL,
 starts_at DATETIME(6) NOT NULL,
 ends_at DATETIME(6) NOT NULL,
 revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
 created_by BIGINT UNSIGNED NOT NULL,
 created_at DATETIME(6) NOT NULL,
 settled_at DATETIME(6) NULL,
 CONSTRAINT fk_contest_creator FOREIGN KEY (created_by) REFERENCES users(id),
 CONSTRAINT ck_contest_mode CHECK (scoring_mode IN ('OI','ACM','IOI')),
 CONSTRAINT ck_contest_enrollment CHECK (enrollment_policy IN ('open','admin_add')),
 CONSTRAINT ck_contest_time CHECK (starts_at < ends_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE contest_admins (
 contest_id BIGINT UNSIGNED NOT NULL,
 user_id BIGINT UNSIGNED NOT NULL,
 PRIMARY KEY (contest_id,user_id),
 FOREIGN KEY (contest_id) REFERENCES contests(id),
 FOREIGN KEY (user_id) REFERENCES users(id)
) ENGINE=InnoDB;
CREATE TABLE contest_problems (
 contest_id BIGINT UNSIGNED NOT NULL,
 problem_id BIGINT UNSIGNED NOT NULL,
 statement_version BIGINT UNSIGNED NOT NULL,
 data_version BIGINT UNSIGNED NOT NULL,
 PRIMARY KEY (contest_id,problem_id),
 FOREIGN KEY (contest_id) REFERENCES contests(id),
 FOREIGN KEY (problem_id,statement_version) REFERENCES problem_versions(problem_id,version),
 FOREIGN KEY (problem_id,data_version) REFERENCES problem_data_versions(problem_id,data_version)
) ENGINE=InnoDB;
CREATE TABLE contest_teams (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
 contest_id BIGINT UNSIGNED NOT NULL,
 name VARCHAR(100) NOT NULL,
 participant_type VARCHAR(20) NOT NULL DEFAULT 'official',
 status VARCHAR(20) NOT NULL,
 created_at DATETIME(6) NOT NULL,
 UNIQUE KEY uq_contest_team (contest_id,id),
 FOREIGN KEY (contest_id) REFERENCES contests(id),
 CONSTRAINT ck_team_type CHECK (participant_type IN ('official','star')),
 CONSTRAINT ck_team_status CHECK (status IN ('pending','active','withdrawn'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE contest_team_members (
 contest_id BIGINT UNSIGNED NOT NULL,
 team_id BIGINT UNSIGNED NOT NULL,
 user_id BIGINT UNSIGNED NOT NULL,
 person_id BIGINT UNSIGNED NOT NULL,
 identity_snapshot JSON NOT NULL,
 PRIMARY KEY (contest_id,user_id),
 UNIQUE KEY uq_contest_person (contest_id,person_id),
 FOREIGN KEY (contest_id,team_id) REFERENCES contest_teams(contest_id,id),
 FOREIGN KEY (user_id) REFERENCES users(id),
 FOREIGN KEY (person_id) REFERENCES persons(id)
) ENGINE=InnoDB;
CREATE TABLE contest_snapshots (
 contest_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
 standings JSON NOT NULL,
 created_at DATETIME(6) NOT NULL,
 FOREIGN KEY (contest_id) REFERENCES contests(id)
) ENGINE=InnoDB;
CREATE TABLE contest_points (
 contest_id BIGINT UNSIGNED NOT NULL,
 person_id BIGINT UNSIGNED NOT NULL,
 team_id BIGINT UNSIGNED NOT NULL,
 points INT NOT NULL,
 PRIMARY KEY (contest_id,person_id),
 FOREIGN KEY (contest_id,team_id) REFERENCES contest_teams(contest_id,id),
 FOREIGN KEY (person_id) REFERENCES persons(id),
 CONSTRAINT ck_points CHECK (points BETWEEN 0 AND 100)
) ENGINE=InnoDB;
ALTER TABLE submissions DROP CHECK ck_submissions_practice,
 ADD CONSTRAINT ck_submission_contest_pair CHECK ((contest_id IS NULL AND contest_team_id IS NULL) OR (contest_id IS NOT NULL AND contest_team_id IS NOT NULL)),
 ADD CONSTRAINT fk_submission_contest_team FOREIGN KEY (contest_id,contest_team_id) REFERENCES contest_teams(contest_id,id),
 ADD KEY idx_submissions_contest (contest_id,id);

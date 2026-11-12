-- Keep in step with migrations/sqlite/019_waiver_questions.sql. Run by hand after a mysqldump.
-- 019 the rest of the visitor form (the old system's cards): the Risk Exposure and Health
-- Declaration answers (one letter per question: N = No, A = N/A, Y = Yes, in the order of
-- internal/waiver/questions.go), medicines 2 and 3 and the visitor's own notes.
-- Rows made before this migration have '' answers, shown to reviewers as "not recorded".
ALTER TABLE waiver ADD COLUMN risk_answers   VARCHAR(20)  NOT NULL DEFAULT '';
ALTER TABLE waiver ADD COLUMN health_answers VARCHAR(30)  NOT NULL DEFAULT '';
ALTER TABLE waiver ADD COLUMN medicine2      VARCHAR(120) NOT NULL DEFAULT '';
ALTER TABLE waiver ADD COLUMN dosage2        VARCHAR(120) NOT NULL DEFAULT '';
ALTER TABLE waiver ADD COLUMN medicine3      VARCHAR(120) NOT NULL DEFAULT '';
ALTER TABLE waiver ADD COLUMN dosage3        VARCHAR(120) NOT NULL DEFAULT '';
ALTER TABLE waiver ADD COLUMN medicine_notes VARCHAR(500) NOT NULL DEFAULT '';

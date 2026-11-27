-- Rerunning a run that stopped with an updated plan (#68).
--
-- denials_json holds the tool calls a step run's latest turn was refused, as
-- its runtime reported them (domain.PermissionDenial). Each turn replaces
-- them, so once the step ends they are what it was still refused: a denial
-- the person granted is gone after the retry. A failed run's detail shows
-- them, and the fixes it suggests come from them.
ALTER TABLE runs ADD COLUMN denials_json TEXT NOT NULL DEFAULT '';

-- rerun_of names the run a run started over from: the same task, a fresh
-- workspace, every step run again. The run it names stays as it was.
ALTER TABLE executions ADD COLUMN rerun_of TEXT NOT NULL DEFAULT '';

-- A resume can run under a plan the person edited. replaced_plan_json is the
-- plan the run ran under before that round, empty for a resume under the
-- approved plan, so the rounds before it keep the plan they ran.
-- edited_steps is a JSON list of the steps the person edited, which the
-- round runs again with everything after them even where they had
-- completed: like a round's feedback targets, they tell which steps a round
-- was to run when the run's history is read back.
ALTER TABLE resumes ADD COLUMN replaced_plan_json TEXT NOT NULL DEFAULT '';
ALTER TABLE resumes ADD COLUMN edited_steps TEXT NOT NULL DEFAULT '';

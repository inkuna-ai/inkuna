import { useState } from "react";
import { setPlanTitle } from "../bridge";
import type { BranchPlan, TaskSpec } from "../types";
import { Alert, Button, inputClass } from "./common";
import { TaskDetails } from "./TaskDetails";

/**
 * A planned task's title, which can change until the plan is approved. Go
 * checks the new title and names the planned branch again from it.
 */
export function PlanTitle({
  workflowID,
  task,
  disabled = false,
  onChanged,
}: {
  workflowID: string;
  task: TaskSpec;
  disabled?: boolean;
  /** The task with its new title, and the branch Go named for it. */
  onChanged(task: TaskSpec, branching?: BranchPlan): void;
}) {
  const saved = task.title ?? "";
  // The draft belongs to the title it was started from, so a new plan, or a
  // title Go cleaned, starts it over.
  const [draft, setDraft] = useState({ from: saved, value: saved });
  const value = draft.from === saved ? draft.value : saved;
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const changed = value.trim() !== saved;
  async function save() {
    setSaving(true);
    setError("");
    try {
      const result = await setPlanTitle(workflowID, value);
      onChanged(result.task, result.branching);
    } catch (reason) {
      setError(String(reason));
    } finally {
      setSaving(false);
    }
  }
  return (
    <div aria-label="Task" className="mb-4 grid min-w-0 gap-2">
      <form
        className="flex min-w-0 flex-wrap items-center gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          if (changed && value.trim()) void save();
        }}
      >
        <input
          className={`${inputClass} min-w-0 flex-1 text-base font-semibold`}
          aria-label="Task title"
          value={value}
          disabled={disabled || saving}
          onChange={(event) =>
            setDraft({ from: saved, value: event.target.value })
          }
        />
        {changed && (
          <>
            <Button
              type="submit"
              disabled={disabled || saving || !value.trim()}
            >
              {saving ? "Saving…" : "Save title"}
            </Button>
            <Button
              variant="ghost"
              disabled={saving}
              onClick={() => setDraft({ from: saved, value: saved })}
            >
              Cancel
            </Button>
          </>
        )}
      </form>
      {error && <Alert flush>{error}</Alert>}
      <TaskDetails task={task} />
    </div>
  );
}

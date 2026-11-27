import { isTerminal, type WorkflowRun } from "../../types";

/** Which runs the Tasks list shows, by how they ended or whether their pull request merged. */
export type StateFilter =
  | "all"
  | "active"
  | "completed"
  | "failed"
  | "cancelled"
  | "interrupted"
  | "merged";

/** How long ago a run must have ended, or started if it has not, to be shown. */
export type AgeFilter = "any" | "1d" | "7d" | "30d" | "90d";

/** The filters the Tasks list applies together. */
export interface TaskFilter {
  state: StateFilter;
  /** A project's id, or "" for every project. */
  project: string;
  age: AgeFilter;
}

/** The filter that shows every run. */
export const everyTask: TaskFilter = { state: "all", project: "", age: "any" };

/** The state filter's choices, in the order the list offers them. */
export const stateFilters: Array<{ id: StateFilter; label: string }> = [
  { id: "all", label: "All states" },
  { id: "active", label: "Not ended" },
  { id: "completed", label: "Completed" },
  { id: "failed", label: "Failed" },
  { id: "cancelled", label: "Cancelled" },
  { id: "interrupted", label: "Interrupted" },
  { id: "merged", label: "Pull request merged" },
];

/** The age filter's choices, each with the days a run must be older than. */
export const ageFilters: Array<{ id: AgeFilter; label: string; days: number }> =
  [
    { id: "any", label: "Any age", days: 0 },
    { id: "1d", label: "Older than a day", days: 1 },
    { id: "7d", label: "Older than a week", days: 7 },
    { id: "30d", label: "Older than 30 days", days: 30 },
    { id: "90d", label: "Older than 90 days", days: 90 },
  ];

const day = 24 * 60 * 60 * 1000;

/** The runs the filter shows, in the order given. */
export function filterRuns(
  runs: WorkflowRun[],
  filter: TaskFilter,
  now = Date.now(),
): WorkflowRun[] {
  const days = ageFilters.find((age) => age.id === filter.age)?.days ?? 0;
  return runs.filter((run) => {
    if (filter.project && run.projectId !== filter.project) return false;
    if (!inState(run, filter.state)) return false;
    if (days > 0) {
      const when = Date.parse(run.finishedAt ?? run.startedAt);
      if (Number.isNaN(when) || now - when < days * day) return false;
    }
    return true;
  });
}

function inState(run: WorkflowRun, state: StateFilter): boolean {
  switch (state) {
    case "all":
      return true;
    case "active":
      return !isTerminal(run.state);
    // Whatever state the run ended in, as the last check found its branch.
    case "merged":
      return run.pullRequest?.state === "MERGED";
    default:
      return run.state === state;
  }
}

/** Whether filter narrows the list at all. */
export function filtering(filter: TaskFilter): boolean {
  return (
    filter.state !== everyTask.state ||
    filter.project !== everyTask.project ||
    filter.age !== everyTask.age
  );
}

/**
 * The tasks of the runs that Go would let go now, once each, in list order.
 * A task with a run Go refuses is left out even when another of its runs is
 * listed without a reason, since the reason is the task's.
 */
export function deletableTasks(runs: WorkflowRun[]): string[] {
  const refused = new Set(
    runs.filter((run) => run.deleteRefused).map((run) => run.taskId),
  );
  const tasks: string[] = [];
  for (const run of runs) {
    if (refused.has(run.taskId) || tasks.includes(run.taskId)) continue;
    tasks.push(run.taskId);
  }
  return tasks;
}

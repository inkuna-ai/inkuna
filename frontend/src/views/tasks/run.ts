import type { WorkflowRun } from "../../types";

export type Tab =
  | "timeline"
  | "context"
  | "changes"
  | "messages"
  | "artifacts"
  | "cost";

/** "agent · runtime · model" for a run, or a placeholder before routing. */
export function runtimeLine(run: WorkflowRun) {
  return (
    [run.agent, run.runtime, run.model].filter(Boolean).join(" · ") ||
    "runtime pending"
  );
}

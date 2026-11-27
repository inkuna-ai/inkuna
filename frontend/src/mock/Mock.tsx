import { useState } from "react";
import type { WorkflowRun } from "../types";
import { RunDetail } from "../views/tasks/RunDetail";
import type { Tab } from "../views/tasks/run";
import { roundEvents, roundSteps } from "./fixtures";

const run: WorkflowRun = {
  id: "run-58",
  workflowId: "workflow-58",
  taskId: "task-58",
  state: "completed",
  title: "Files changed view: a GitHub-style diff on the run detail",
  agent: "implementer",
  runtime: "claude",
  model: "sonnet",
  branch: "feature/58-files-changed-view",
  startedAt: "2026-10-25T09:00:00Z",
  finishedAt: "2026-10-25T10:05:00Z",
  round: 2,
  steps: roundSteps,
  events: roundEvents,
  checks: { passed: 1, failed: 1, running: 1, total: 4 },
};

/**
 * The run detail on the Changes tab, in the same columns as the app, for a
 * run in its second round that can be sent back for a third.
 */
export function Mock() {
  const [tab, setTab] = useState<Tab>("changes");
  return (
    <div className="grid min-h-screen grid-cols-[236px_minmax(0,1fr)] max-[900px]:grid-cols-[72px_minmax(0,1fr)]">
      <aside className="border-r border-line bg-sidebar" />
      <main className="min-w-0 px-[clamp(28px,5vw,72px)] pt-10 pb-20 max-[600px]:px-[18px]">
        <div className="grid grid-cols-[minmax(240px,300px)_minmax(0,1fr)] items-start gap-4 max-[900px]:grid-cols-1">
          <div className="rounded-md border border-l-2 border-accent bg-raised p-3.5 text-[13px] font-semibold">
            {run.title}
          </div>
          <div className="min-w-0">
            <RunDetail
              run={run}
              tab={tab}
              onTab={setTab}
              onCancel={() => {}}
              onRemoveWorktree={async () => {}}
            />
          </div>
        </div>
      </main>
    </div>
  );
}

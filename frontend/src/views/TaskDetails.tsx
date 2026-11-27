import { useEffect, useState } from "react";
import { getRunTask } from "../bridge";
import type { TaskSpec } from "../types";
import { Disclosure } from "./common";
import { InlineAlert } from "./tasks/shared";

/**
 * What a task asks for beyond its title, folded until it is opened: the
 * details, acceptance criteria, and constraints.
 */
export function TaskDetails({ task }: { task: TaskSpec }) {
  return (
    <Disclosure label="Details">
      <TaskDetailsBody task={task} />
    </Disclosure>
  );
}

/** A run's task details, read from Go when they are first opened. */
export function RunTaskDetails({ runID }: { runID: string }) {
  return (
    <Disclosure label="Details">
      <LoadedTaskDetails runID={runID} />
    </Disclosure>
  );
}

function LoadedTaskDetails({ runID }: { runID: string }) {
  const [loaded, setLoaded] = useState<{
    runID: string;
    task?: TaskSpec;
    error?: string;
  } | null>(null);
  useEffect(() => {
    let active = true;
    getRunTask(runID).then(
      (task) => active && setLoaded({ runID, task }),
      (reason: unknown) =>
        active && setLoaded({ runID, error: String(reason) }),
    );
    return () => {
      active = false;
    };
  }, [runID]);
  if (loaded?.runID !== runID)
    return <p className="m-0 text-xs text-muted">Loading the details…</p>;
  if (loaded.error) return <InlineAlert>{loaded.error}</InlineAlert>;
  return <TaskDetailsBody task={loaded.task!} />;
}

function TaskDetailsBody({ task }: { task: TaskSpec }) {
  const criteria = task.acceptanceCriteria ?? [];
  const constraints = task.constraints ?? [];
  return (
    <div className="grid min-w-0 gap-3 text-xs leading-relaxed text-ink">
      <p className="m-0 wrap-anywhere whitespace-pre-wrap">{task.objective}</p>
      {criteria.length > 0 && (
        <List label="Acceptance criteria" items={criteria} />
      )}
      {constraints.length > 0 && (
        <List label="Constraints" items={constraints} />
      )}
    </div>
  );
}

function List({ label, items }: { label: string; items: string[] }) {
  return (
    <div className="min-w-0">
      <b className="text-[11px] font-semibold text-ink-soft">{label}</b>
      <ul className="m-0 mt-1 grid gap-0.5 pl-4">
        {items.map((item, index) => (
          <li key={index} className="wrap-anywhere">
            {item}
          </li>
        ))}
      </ul>
    </div>
  );
}

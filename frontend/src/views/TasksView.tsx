import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  cancelWorkflowRun,
  checkPullRequests,
  getWorkflowRun,
  listWorkflowRuns,
  removeRunWorktree,
} from "../bridge";
import { useInkunaEvent } from "../events";
import {
  isTerminal,
  type DeleteReport,
  type PlanSuggestion,
  type Project,
  type PullRequestCheck,
  type WorkflowRun,
} from "../types";
import { formatTime } from "../format";
import { Alert, Button, Chip, Empty, Header, StateChip } from "./common";
import { bestCostLabel, costBreakdown } from "./cost";
import { LinkButton } from "./setup/shared";
import { DeleteDialog, type DeleteTarget } from "./tasks/DeleteDialog";
import {
  ageFilters,
  deletableTasks,
  everyTask,
  filterRuns,
  filtering,
  stateFilters,
  type AgeFilter,
  type StateFilter,
  type TaskFilter,
} from "./tasks/history";
import { RunDetail } from "./tasks/RunDetail";
import { checksTone } from "./tasks/checks";
import { runtimeLine, type Tab } from "./tasks/run";

const pullTones = { MERGED: "green", OPEN: "accent", CLOSED: "muted" } as const;

const filterClass =
  "w-full min-w-0 rounded border border-line-strong bg-field px-1.5 py-1.5 text-[11px] text-ink-strong";

export function TasksView({
  selectedRunID,
  onSelectRun,
  onOpenThread,
  onRerun,
  projects = [],
}: {
  selectedRunID: string | null;
  onSelectRun(runID: string | null): void;
  onOpenThread?(taskID: string): void;
  /** Opens the approval screen to rerun a stopped run, with fixes made. */
  onRerun?(run: WorkflowRun, suggestions: PlanSuggestion[]): void;
  /** The projects the list can be filtered by. */
  projects?: Project[];
}) {
  const [runs, setRuns] = useState<WorkflowRun[]>([]);
  const [selected, setSelected] = useState<WorkflowRun | null>(null);
  const [error, setError] = useState("");
  const [tab, setTab] = useState<Tab>("timeline");
  const [filter, setFilter] = useState<TaskFilter>(everyTask);
  const [selecting, setSelecting] = useState(false);
  const [picked, setPicked] = useState<Set<string>>(() => new Set());
  const [deleting, setDeleting] = useState<DeleteTarget | null>(null);
  const [checkingPulls, setCheckingPulls] = useState(false);
  const [pullCheck, setPullCheck] = useState<PullRequestCheck | null>(null);

  const shown = useMemo(() => filterRuns(runs, filter), [runs, filter]);
  const offered = useMemo(() => deletableTasks(shown), [shown]);
  // Only tasks still shown and still deletable count, so a filter changed
  // or a run started since the pick never widens a delete.
  const chosen = offered.filter((taskID) => picked.has(taskID));

  // Detail responses are only applied while the selection they were requested
  // for is still current, so a slow response cannot overwrite a newer pick.
  const requested = useRef<string | null>(null);
  const refresh = useCallback(async () => {
    const target = selectedRunID;
    requested.current = target;
    try {
      const list = await listWorkflowRuns();
      setRuns(list);
      if (target) {
        const detail = await getWorkflowRun(target);
        if (requested.current === target) setSelected(detail);
      }
      setError("");
    } catch (reason) {
      // A refresh for a selection that has since changed, such as a run
      // that was just deleted, has nothing to report.
      if (requested.current === target) setError(String(reason));
    }
  }, [selectedRunID]);

  useEffect(() => {
    void refresh();
    return () => {
      requested.current = null;
    };
  }, [refresh]);
  useInkunaEvent("inkuna:runs", refresh);

  // Go reads each project's pull requests at most every few minutes unless
  // asked, and sends inkuna:runs once it has, which refreshes the list.
  const checkPulls = useCallback(async (force: boolean) => {
    setCheckingPulls(true);
    try {
      setPullCheck(await checkPullRequests(force));
    } catch (reason) {
      setPullCheck({
        checked: 0,
        linked: 0,
        removed: [],
        problems: [String(reason)],
      });
    } finally {
      setCheckingPulls(false);
    }
  }, []);
  useEffect(() => {
    void checkPulls(false);
  }, [checkPulls]);
  useInkunaEvent("inkuna:inbox", refresh);
  // A task's cost grows as each call is stored, which can be after its run
  // ends.
  useInkunaEvent("inkuna:usage", refresh);

  // A run paused for review changes only when someone answers it, which
  // sends inkuna:runs, so it is not polled for however long it waits.
  const moving = (state: string) => !isTerminal(state) && state !== "reviewing";
  const anyActive =
    runs.some((run) => moving(run.state)) ||
    (selected !== null && moving(selected.state));
  useEffect(() => {
    if (!anyActive) return;
    const interval = window.setInterval(() => void refresh(), 1500);
    return () => window.clearInterval(interval);
  }, [anyActive, refresh]);

  async function cancel(runID: string) {
    setError("");
    try {
      const next = await cancelWorkflowRun(runID);
      setSelected(next);
      setRuns((current) =>
        current.map((run) => (run.id === next.id ? { ...run, ...next } : run)),
      );
    } catch (reason) {
      setError(String(reason));
    }
  }

  // The review bar shows its own errors; a run it moved is shown at once.
  function reviewed(next: WorkflowRun) {
    setSelected(next);
    setRuns((current) =>
      current.map((run) => (run.id === next.id ? { ...run, ...next } : run)),
    );
    void refresh();
  }

  function toggle(taskID: string) {
    setPicked((current) => {
      const next = new Set(current);
      if (next.has(taskID)) next.delete(taskID);
      else next.add(taskID);
      return next;
    });
  }

  // The dialog stays open on Go's report; the list and the detail move on
  // at once, so nothing deleted stays selected.
  function deleted(report: DeleteReport) {
    const gone = (run: WorkflowRun) =>
      report.tasks.some((task) =>
        task.runId ? task.runId === run.id : task.taskId === run.taskId,
      );
    setPicked(new Set());
    if (selected && gone(selected)) {
      setSelected(null);
      onSelectRun(null);
    }
    setRuns((current) => current.filter((run) => !gone(run)));
    void refresh();
  }

  // Errors reach the run's own workspace section rather than the page.
  async function removeWorktree(runID: string) {
    const next = await removeRunWorktree(runID);
    setSelected(next);
    setRuns((current) =>
      current.map((run) => (run.id === next.id ? { ...run, ...next } : run)),
    );
  }

  return (
    <section>
      <Header
        eyebrow="Execution"
        title="Tasks"
        actions={
          <>
            <Button
              aria-pressed={selecting}
              onClick={() => {
                setSelecting((value) => !value);
                setPicked(new Set());
              }}
            >
              {selecting ? "Done selecting" : "Select"}
            </Button>
            <Button
              disabled={checkingPulls}
              onClick={() => void checkPulls(true)}
            >
              {checkingPulls
                ? "Checking pull requests…"
                : "Check pull requests"}
            </Button>
            <Button onClick={() => void refresh()} aria-label="Refresh tasks">
              Refresh
            </Button>
          </>
        }
      />
      {error && <Alert>{error}</Alert>}
      {(pullCheck?.problems?.length ?? 0) > 0 && (
        <Alert>
          Some pull requests could not be checked:
          <ul className="m-0 mt-1.5 grid gap-1 pl-4">
            {pullCheck?.problems?.map((problem) => (
              <li key={problem}>{problem}</li>
            ))}
          </ul>
        </Alert>
      )}
      {(pullCheck?.removed.length ?? 0) > 0 && (
        <p role="status" className="mt-0 mb-3 text-xs text-muted">
          Removed {pullCheck?.removed.length} worktree
          {pullCheck?.removed.length === 1 ? "" : "s"} whose pull request
          merged; the branches are kept.
        </p>
      )}
      <div className="grid grid-cols-[minmax(240px,300px)_minmax(0,1fr)] items-start gap-4 max-[900px]:grid-cols-1">
        <div className="flex min-w-0 flex-col gap-2 min-[901px]:sticky min-[901px]:top-4 min-[901px]:max-h-[calc(100vh-2rem)]">
          <div
            className="grid grid-cols-3 gap-1.5 px-1 pt-1"
            role="group"
            aria-label="Filter tasks"
          >
            <select
              className={filterClass}
              aria-label="Filter by state"
              value={filter.state}
              onChange={(event) =>
                setFilter({
                  ...filter,
                  state: event.target.value as StateFilter,
                })
              }
            >
              {stateFilters.map((state) => (
                <option key={state.id} value={state.id}>
                  {state.label}
                </option>
              ))}
            </select>
            <select
              className={filterClass}
              aria-label="Filter by project"
              value={filter.project}
              onChange={(event) =>
                setFilter({ ...filter, project: event.target.value })
              }
            >
              <option value="">All projects</option>
              {projects.map((project) => (
                <option key={project.id} value={project.id}>
                  {project.name}
                </option>
              ))}
            </select>
            <select
              className={filterClass}
              aria-label="Filter by age"
              value={filter.age}
              onChange={(event) =>
                setFilter({ ...filter, age: event.target.value as AgeFilter })
              }
            >
              {ageFilters.map((age) => (
                <option key={age.id} value={age.id}>
                  {age.label}
                </option>
              ))}
            </select>
          </div>
          {selecting && (
            <div
              role="toolbar"
              aria-label="Selected tasks"
              className="mx-1 flex flex-wrap items-center gap-x-3 gap-y-1.5 border border-line bg-panel p-2 text-[11px]"
            >
              <span className="text-muted" aria-live="polite">
                {chosen.length} selected
              </span>
              <LinkButton
                disabled={offered.length === 0}
                onClick={() => setPicked(new Set(offered))}
              >
                Select all finished
              </LinkButton>
              {chosen.length > 0 && (
                <LinkButton onClick={() => setPicked(new Set())}>
                  Clear
                </LinkButton>
              )}
              <Button
                variant="danger"
                className="ml-auto px-2.5 py-1 text-xs"
                disabled={chosen.length === 0}
                onClick={() => setDeleting({ kind: "tasks", taskIds: chosen })}
              >
                Delete…
              </Button>
            </div>
          )}
          <div
            className="grid min-h-0 min-w-0 flex-1 content-start gap-2 overflow-y-auto p-1 max-[900px]:max-h-[40vh]"
            role="group"
            aria-label="Task runs"
          >
            {runs.length === 0 && (
              <Empty title="No tasks yet">
                <p>Approve a planned workflow to start its first run.</p>
              </Empty>
            )}
            {runs.length > 0 && shown.length === 0 && (
              <Empty title="No tasks match">
                <p>No run has the state, project, and age chosen.</p>
                {filtering(filter) && (
                  <Button onClick={() => setFilter(everyTask)}>
                    Show every task
                  </Button>
                )}
              </Empty>
            )}
            {shown.map((run) => {
              const current = selectedRunID === run.id;
              return (
                <div key={run.id} className="flex min-w-0 items-start gap-2">
                  {selecting &&
                    (run.deleteRefused ? (
                      <span
                        role="img"
                        title={run.deleteRefused}
                        aria-label={`Cannot delete: ${run.deleteRefused}`}
                        className="mt-3.5 grid size-4 flex-none cursor-help place-items-center text-xs text-muted"
                      >
                        –
                      </span>
                    ) : (
                      <input
                        type="checkbox"
                        aria-label={`Select task ${run.title}`}
                        className="mt-3.5 size-4 flex-none accent-accent"
                        checked={picked.has(run.taskId)}
                        onChange={() => toggle(run.taskId)}
                      />
                    ))}
                  <button
                    type="button"
                    aria-current={current ? "true" : undefined}
                    className={`grid w-full min-w-0 flex-1 cursor-pointer gap-1.5 rounded-md border border-l-2 p-3.5 text-left ${
                      current
                        ? "border-accent bg-raised"
                        : "border-line bg-panel hover:border-line-strong"
                    }`}
                    onClick={() => {
                      onSelectRun(run.id);
                      setTab("timeline");
                    }}
                  >
                    <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                      <StateChip state={run.state} />
                      {run.state === "reviewing" && (
                        <Chip tone="accent">awaiting review</Chip>
                      )}
                      {(run.round ?? 1) > 1 && <Chip>round {run.round}</Chip>}
                      {(run.pendingInputs ?? 0) > 0 && (
                        <Chip tone="accent">input needed</Chip>
                      )}
                      {run.checks && (
                        <Chip tone={checksTone(run.checks)}>
                          {run.checks.passed}/{run.checks.total} checks
                        </Chip>
                      )}
                      {run.orchestrated && (
                        <Chip tone="green">orchestrated</Chip>
                      )}
                      {run.pullRequest && (
                        <Chip
                          tone={
                            pullTones[
                              run.pullRequest.state as keyof typeof pullTones
                            ] ?? "muted"
                          }
                        >
                          PR #{run.pullRequest.number}{" "}
                          {run.pullRequest.state.toLowerCase()}
                        </Chip>
                      )}
                    </div>
                    <b
                      className="min-w-0 truncate text-[13px] font-semibold text-ink-strong"
                      title={run.title}
                    >
                      {run.title}
                    </b>
                    <small className="min-w-0 text-[11px] leading-snug wrap-anywhere text-muted">
                      {runtimeLine(run)}
                      {run.ruleName ? ` · rule ${run.ruleName}` : ""}
                    </small>
                    <small className="min-w-0 text-[11px] leading-snug text-muted">
                      {formatTime(run.startedAt)}
                      {run.finishedAt ? ` → ${formatTime(run.finishedAt)}` : ""}
                    </small>
                    {run.taskCost && (
                      <small
                        className="min-w-0 text-[11px] leading-snug wrap-anywhere text-muted"
                        title={costBreakdown(run.taskCost)}
                      >
                        Task cost{" "}
                        <span className="font-mono text-ink">
                          {bestCostLabel(run.taskCost)}
                        </span>
                        {run.taskCost.subscriptionCents > 0 &&
                          " · API-equivalent"}
                      </small>
                    )}
                  </button>
                </div>
              );
            })}
          </div>
        </div>
        <div className="min-w-0">
          {selected ? (
            <RunDetail
              run={selected}
              tab={tab}
              onTab={setTab}
              onCancel={() => cancel(selected.id)}
              onRemoveWorktree={() => removeWorktree(selected.id)}
              onDelete={() =>
                setDeleting({
                  kind: "run",
                  runId: selected.id,
                  taskId: selected.taskId,
                })
              }
              onReviewed={reviewed}
              onOpenThread={onOpenThread}
              onRerun={
                onRerun
                  ? (suggestions) => onRerun(selected, suggestions)
                  : undefined
              }
            />
          ) : (
            <Empty title="Select a task">
              <p>
                See each agent&apos;s context, the latest commit, and the A2A
                conversation.
              </p>
            </Empty>
          )}
        </div>
      </div>
      {deleting && (
        <DeleteDialog
          target={deleting}
          projects={projects}
          onClose={() => setDeleting(null)}
          onDeleted={deleted}
        />
      )}
    </section>
  );
}

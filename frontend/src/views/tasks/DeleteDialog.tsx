import { useEffect, useRef, useState } from "react";
import {
  deleteRun,
  deleteTasks,
  previewDeleteRun,
  previewDeleteTasks,
} from "../../bridge";
import { formatBytes } from "../../format";
import type {
  BranchPreview,
  DeleteOptions,
  DeleteReport,
  Project,
  TaskDeletePreview,
} from "../../types";
import { Alert, Button, Modal } from "../common";
import { CheckRow } from "../setup/shared";

/** What a delete was asked for: tasks from the list, or a run from its detail. */
export type DeleteTarget =
  | { kind: "tasks"; taskIds: string[] }
  | { kind: "run"; runId: string; taskId: string };

const plural = (count: number, one: string, many = `${one}s`) =>
  `${count} ${count === 1 ? one : many}`;

const sectionHeading =
  "mt-5 mb-1.5 font-mono text-[9px] font-normal tracking-widest text-muted uppercase";
const itemClass =
  "grid min-w-0 gap-0.5 border-b border-line py-1.5 text-xs last:border-b-0";

// Go lists a worktree or branch once per task, so one that two of the tasks
// share would otherwise be listed, counted, and confirmed twice.
function uniqueBy<T>(items: T[], key: (item: T) => string): T[] {
  const seen = new Set<string>();
  return items.filter((item) => {
    const id = key(item);
    if (seen.has(id)) return false;
    seen.add(id);
    return true;
  });
}

// A branch name is only unique within its repository, so two projects can
// each have a branch of the same name.
const branchKey = (branch: BranchPreview) =>
  JSON.stringify([branch.projectId, branch.name]);

/**
 * Confirms a delete with what Go says it would remove and leave, offers the
 * two clean-ups unticked, and then shows Go's report of what it did.
 */
export function DeleteDialog({
  target,
  projects = [],
  onClose,
  onDeleted,
}: {
  target: DeleteTarget;
  /** Names the project of each branch when a delete spans projects. */
  projects?: Project[];
  onClose(): void;
  /** Called with Go's report as soon as the delete succeeds. */
  onDeleted(report: DeleteReport): void;
}) {
  const [previews, setPreviews] = useState<{
    tasks: TaskDeletePreview[];
    run?: TaskDeletePreview;
  } | null>(null);
  const [loadError, setLoadError] = useState("");
  const [scope, setScope] = useState<"run" | "task">("run");
  const [removeWorktrees, setRemoveWorktrees] = useState(false);
  const [deleteBranches, setDeleteBranches] = useState(false);
  const [unmergedPicked, setUnmergedPicked] = useState<Set<string>>(
    () => new Set(),
  );
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [report, setReport] = useState<DeleteReport | null>(null);
  // The ref refuses a second confirmation before the pending state renders.
  const deleting = useRef(false);

  useEffect(() => {
    let active = true;
    const load =
      target.kind === "tasks"
        ? previewDeleteTasks(target.taskIds).then((tasks) => ({ tasks }))
        : Promise.all([
            previewDeleteTasks([target.taskId]),
            previewDeleteRun(target.runId),
          ]).then(([tasks, run]) => ({ tasks, run }));
    load
      .then((value) => {
        if (active) setPreviews(value);
      })
      .catch((reason: unknown) => {
        if (active) setLoadError(String(reason));
      });
    return () => {
      active = false;
    };
  }, [target]);

  // From a run's detail, a task with other runs can lose just this one; a
  // task's only run goes with its task.
  const taskRuns = previews?.tasks[0]?.runs.length ?? 0;
  const choosing = target.kind === "run" && taskRuns > 1;
  const runOnly = choosing && scope === "run";
  const shown = previews
    ? runOnly && previews.run
      ? [previews.run]
      : previews.tasks
    : [];
  const refused = shown.flatMap((preview) =>
    preview.refused ? [preview.refused] : [],
  );
  const runs = shown.flatMap((preview) => preview.runs);
  const worktrees = uniqueBy(
    runs.flatMap((run) => (run.worktree ? [run.worktree] : [])),
    (tree) => tree.path,
  );
  const removable = worktrees.filter((tree) => tree.removable);
  const branches = uniqueBy(
    runs.flatMap((run) => (run.branch ? [run.branch] : [])),
    branchKey,
  );
  const merged = branches.filter(
    (branch) => branch.deletable && !branch.unique,
  );
  const unmerged = branches.filter(
    (branch) => branch.deletable && branch.unique > 0,
  );
  // Across projects, each branch says whose it is, since two can each have
  // a branch of one name.
  const spansProjects =
    new Set(branches.map((branch) => branch.projectId)).size > 1;
  const projectOf = (branch: BranchPreview) =>
    projects.find((project) => project.id === branch.projectId)?.name ??
    branch.projectId;
  const bytes = (trees: typeof worktrees) =>
    trees.reduce((total, tree) => total + tree.bytes, 0);

  function options(): DeleteOptions {
    if (!deleteBranches) return { removeWorktrees };
    const chosen = [
      ...merged,
      ...unmerged.filter((branch) => unmergedPicked.has(branchKey(branch))),
    ];
    const confirmed: Record<string, Record<string, string>> = {};
    for (const branch of chosen) {
      (confirmed[branch.projectId] ??= {})[branch.name] = branch.commit;
    }
    return { removeWorktrees, deleteBranches: confirmed };
  }

  async function confirm() {
    if (deleting.current) return;
    deleting.current = true;
    setPending(true);
    setError("");
    try {
      const done =
        runOnly && target.kind === "run"
          ? await deleteRun(target.runId, options())
          : await deleteTasks(
              shown.map((preview) => preview.taskId),
              options(),
            );
      setReport(done);
      onDeleted(done);
    } catch (reason) {
      setError(String(reason));
    } finally {
      deleting.current = false;
      setPending(false);
    }
  }

  const title = report
    ? "Deleted"
    : target.kind === "tasks"
      ? `Delete ${plural(target.taskIds.length, "task")}?`
      : runOnly
        ? "Delete this run?"
        : "Delete this task?";

  return (
    <Modal wide title={title} onClose={() => deleting.current || onClose()}>
      {report ? (
        <DeleteSummary report={report} />
      ) : loadError ? (
        <Alert flush>{loadError}</Alert>
      ) : !previews ? (
        <p role="status" className="m-0 text-sm text-muted">
          Reading what would be removed…
        </p>
      ) : (
        <div className="min-w-0 text-sm">
          {choosing && (
            <fieldset className="m-0 mb-4 grid gap-1.5 border-0 p-0 text-xs text-ink-soft">
              <legend className="mb-1.5 text-muted">What to delete</legend>
              <label className="flex items-start gap-2">
                <input
                  type="radio"
                  name="delete-scope"
                  className="mt-0.5 accent-accent"
                  checked={scope === "run"}
                  onChange={() => setScope("run")}
                />
                <span>
                  Only this run; the task and its other{" "}
                  {plural(taskRuns - 1, "run")} stay
                </span>
              </label>
              <label className="flex items-start gap-2">
                <input
                  type="radio"
                  name="delete-scope"
                  className="mt-0.5 accent-accent"
                  checked={scope === "task"}
                  onChange={() => setScope("task")}
                />
                <span>The whole task and all {taskRuns} of its runs</span>
              </label>
            </fieldset>
          )}
          {refused.length > 0 && (
            <div className="mb-4">
              <Alert flush>
                Nothing can be deleted while{" "}
                {refused.length === 1 ? "this stands" : "these stand"}:
                <ul className="m-0 mt-1.5 grid gap-1 pl-4">
                  {refused.map((reason) => (
                    <li key={reason}>{reason}</li>
                  ))}
                </ul>
              </Alert>
            </div>
          )}
          <p className="m-0 text-ink" aria-label="What goes">
            {runOnly
              ? "Removes 1 run from Inkuna's history; its task, plan, and other runs stay."
              : `Removes ${plural(shown.length, "task")} and ${plural(runs.length, "run")} from Inkuna's history.`}
          </p>
          <p className="mt-1.5 mb-0 text-xs text-muted">
            Plans, timelines, messages, usage and cost records go, and so do
            artifacts no other task uses. Nothing changes in GitHub or Linear.
          </p>

          <h3 className={sectionHeading}>
            Worktrees
            {worktrees.length > 0 &&
              ` · ${plural(worktrees.length, "worktree")} · ${formatBytes(bytes(worktrees))}`}
          </h3>
          {worktrees.length === 0 ? (
            <p className="m-0 text-xs text-muted">None left on disk.</p>
          ) : (
            <ul aria-label="Worktrees" className="m-0 list-none p-0">
              {worktrees.map((tree) => (
                <li key={tree.path} className={itemClass}>
                  <span className="min-w-0 font-mono text-[11px] wrap-anywhere">
                    {tree.path}
                  </span>
                  <small className="text-[11px] wrap-anywhere text-muted">
                    {formatBytes(tree.bytes)}
                    {tree.removable ? "" : ` · stays: ${tree.reason ?? ""}`}
                  </small>
                </li>
              ))}
            </ul>
          )}
          <CheckRow
            checked={removeWorktrees}
            disabled={removable.length === 0}
            onChange={(event) => setRemoveWorktrees(event.target.checked)}
          >
            Also remove worktrees
            {removable.length > 0 &&
              `: ${plural(removable.length, "worktree")} without uncommitted changes, ${formatBytes(bytes(removable))}`}
          </CheckRow>

          <h3 className={sectionHeading}>
            Branches
            {branches.length > 0 &&
              ` · ${plural(branches.length, "branch", "branches")}`}
          </h3>
          {branches.length === 0 ? (
            <p className="m-0 text-xs text-muted">None left.</p>
          ) : (
            <ul aria-label="Branches" className="m-0 list-none p-0">
              {branches.map((branch) => (
                <li key={branchKey(branch)} className={itemClass}>
                  <span className="min-w-0 font-mono text-[11px] wrap-anywhere">
                    {branch.name}
                  </span>
                  <small className="text-[11px] wrap-anywhere text-muted">
                    {spansProjects && `${projectOf(branch)} · `}
                    {branchLine(branch)}
                  </small>
                </li>
              ))}
            </ul>
          )}
          <CheckRow
            checked={deleteBranches}
            disabled={merged.length + unmerged.length === 0}
            onChange={(event) => setDeleteBranches(event.target.checked)}
          >
            Also delete branches
            {merged.length > 0 &&
              `: ${plural(merged.length, "branch", "branches")} whose commits are all on other branches`}
            . A branch&apos;s clean worktree goes with it.
          </CheckRow>
          {deleteBranches && unmerged.length > 0 && (
            <div
              aria-label="Branches with unmerged commits"
              className="-mt-2 border-l-2 border-danger-line pl-3"
            >
              <p className="m-0 text-xs text-muted">
                These have commits no other branch has, and stay unless you tick
                them:
              </p>
              {unmerged.map((branch) => (
                <CheckRow
                  key={branchKey(branch)}
                  checked={unmergedPicked.has(branchKey(branch))}
                  onChange={(event) =>
                    setUnmergedPicked((current) => {
                      const next = new Set(current);
                      if (event.target.checked) next.add(branchKey(branch));
                      else next.delete(branchKey(branch));
                      return next;
                    })
                  }
                >
                  Delete {branch.name}
                  {spansProjects && ` in ${projectOf(branch)}`} and lose{" "}
                  {plural(branch.unique, "commit")}
                </CheckRow>
              ))}
            </div>
          )}
          {error && <Alert flush>{error}</Alert>}
        </div>
      )}
      <div className="mt-6 flex justify-end gap-2">
        {report ? (
          <Button variant="primary" onClick={onClose}>
            Done
          </Button>
        ) : (
          <>
            <Button disabled={pending} onClick={onClose}>
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={pending || !previews || refused.length > 0}
              onClick={() => void confirm()}
            >
              {pending
                ? "Deleting…"
                : runOnly
                  ? "Delete run"
                  : `Delete ${plural(shown.length || (target.kind === "tasks" ? target.taskIds.length : 1), "task")}`}
            </Button>
          </>
        )}
      </div>
    </Modal>
  );
}

function branchLine(branch: BranchPreview): string {
  const commits =
    branch.unique > 0
      ? `${plural(branch.unique, "commit")} only on this branch`
      : "every commit is on another branch";
  return branch.deletable
    ? commits
    : `${commits} · stays: ${branch.reason ?? ""}`;
}

/** Go's report of a delete: what went, and what stayed and why. */
export function DeleteSummary({ report }: { report: DeleteReport }) {
  const tasks = report.tasks;
  const oneRun = tasks.length === 1 && Boolean(tasks[0].runId);
  const rows = tasks.reduce(
    (total, task) =>
      total + task.rows.reduce((sum, table) => sum + table.rows, 0),
    0,
  );
  const blobs = tasks.reduce((total, task) => total + task.blobs, 0);
  const blobBytes = tasks.reduce((total, task) => total + task.blobBytes, 0);
  const keptBlobs = tasks.reduce((total, task) => total + task.keptBlobs, 0);
  const worktrees = tasks.flatMap((task) => task.worktrees);
  const removedTrees = worktrees.filter((tree) => tree.removed);
  const keptTrees = worktrees.filter((tree) => !tree.removed);
  const branches = tasks.flatMap((task) => task.branches);
  const deletedBranches = branches.filter((branch) => branch.deleted);
  const keptBranches = branches.filter((branch) => !branch.deleted);
  const links = tasks.flatMap((task) => task.links);
  const problems = tasks.flatMap((task) => task.problems ?? []);
  return (
    <div role="status" aria-label="Delete report" className="min-w-0 text-sm">
      <p className="m-0 text-ink">
        {oneRun ? "Deleted the run" : `Deleted ${plural(tasks.length, "task")}`}
        : {plural(rows, "row")} of history.
      </p>
      <p className="mt-1.5 mb-0 text-xs text-muted">
        Freed {plural(blobs, "artifact")} ({formatBytes(blobBytes)}).
        {keptBlobs > 0 &&
          ` ${plural(keptBlobs, "artifact")} another task still uses stayed.`}
      </p>
      {problems.length > 0 && (
        <div className="mt-3">
          <Alert flush>
            <ul className="m-0 grid gap-1 pl-4">
              {problems.map((problem) => (
                <li key={problem}>{problem}</li>
              ))}
            </ul>
          </Alert>
        </div>
      )}
      {worktrees.length > 0 && (
        <>
          <h3 className={sectionHeading}>Worktrees</h3>
          <ul aria-label="Worktrees" className="m-0 list-none p-0">
            {removedTrees.map((tree) => (
              <li key={tree.path} className={itemClass}>
                <span className="font-mono text-[11px] wrap-anywhere">
                  {tree.path}
                </span>
                <small className="text-[11px] text-ok">Removed</small>
              </li>
            ))}
            {keptTrees.map((tree) => (
              <li key={tree.path} className={itemClass}>
                <span className="font-mono text-[11px] wrap-anywhere">
                  {tree.path}
                </span>
                <small className="text-[11px] wrap-anywhere text-muted">
                  Kept: {tree.reason}
                </small>
              </li>
            ))}
          </ul>
          {keptTrees.length > 0 && (
            <p className="mt-1.5 mb-0 text-xs text-muted">
              Settings lists the worktrees a delete kept under Disk usage, where
              you can remove them later.
            </p>
          )}
        </>
      )}
      {branches.length > 0 && (
        <>
          <h3 className={sectionHeading}>Branches</h3>
          <ul aria-label="Branches" className="m-0 list-none p-0">
            {deletedBranches.map((branch) => (
              <li key={branch.name} className={itemClass}>
                <span className="font-mono text-[11px] wrap-anywhere">
                  {branch.name}
                </span>
                <small className="text-[11px] text-ok">
                  Deleted at {branch.commit?.slice(0, 12)}
                  {branch.unique > 0 &&
                    `, with ${plural(branch.unique, "commit")} only it had`}
                </small>
              </li>
            ))}
            {keptBranches.map((branch) => (
              <li key={branch.name} className={itemClass}>
                <span className="font-mono text-[11px] wrap-anywhere">
                  {branch.name}
                </span>
                <small className="text-[11px] wrap-anywhere text-muted">
                  Kept: {branch.reason}
                </small>
              </li>
            ))}
          </ul>
        </>
      )}
      {links.length > 0 && (
        <p className="mt-4 mb-0 text-xs text-muted">
          Inkuna no longer links{" "}
          {links.map((link) => `${link.tracker} ${link.id}`).join(", ")};
          nothing changed there.
        </p>
      )}
    </div>
  );
}

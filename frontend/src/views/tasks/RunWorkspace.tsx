import { useRef, useState } from "react";
import { openExternal } from "../../bridge";
import { formatTime } from "../../format";
import type { WorkflowRun } from "../../types";
import { canRemoveWorktree, cleanupWords } from "../branching";
import { Button, Modal } from "../common";
import { InlineAlert, Summary } from "./shared";

function worktreeState(run: WorkflowRun): string {
  if (run.cleanup === "checkout") return "in the checkout";
  if (run.worktreeRemovedAt)
    return `removed ${formatTime(run.worktreeRemovedAt)}, branch kept`;
  return cleanupWords(run.cleanup);
}

/**
 * The run's branch and working directory, what cleanup did, and, for a
 * finished run whose worktree is still there, a confirmed way to remove it.
 */
export function RunWorkspace({
  run,
  onRemoveWorktree,
}: {
  run: WorkflowRun;
  onRemoveWorktree(): Promise<void>;
}) {
  const [confirming, setConfirming] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  // The ref refuses a second confirmation before the pending state renders.
  const removing = useRef(false);
  if (!run.worktree && !run.branch) return null;

  async function remove() {
    if (removing.current) return;
    removing.current = true;
    setPending(true);
    setError("");
    try {
      await onRemoveWorktree();
      setConfirming(false);
    } catch (reason) {
      setError(String(reason));
      setConfirming(false);
    } finally {
      removing.current = false;
      setPending(false);
    }
  }

  return (
    <div className="mt-4 grid min-w-0 gap-2" aria-label="Run workspace">
      <Summary
        label={run.branch ? `Branch · ${run.branch}` : "Workspace"}
        meta={worktreeState(run)}
      >
        {run.worktree ?? "No worktree yet"}
      </Summary>
      {run.pullRequest && (
        <small
          className="text-[11px] wrap-anywhere text-muted"
          aria-label="Pull request"
        >
          Pull request #{run.pullRequest.number}{" "}
          {run.pullRequest.mergedAt
            ? `merged ${formatTime(run.pullRequest.mergedAt)}`
            : run.pullRequest.state.toLowerCase()}{" "}
          ·{" "}
          <button
            type="button"
            className="cursor-pointer border-0 bg-transparent p-0 text-accent underline"
            onClick={() => openExternal(run.pullRequest?.url ?? "")}
          >
            Open #{run.pullRequest.number}
          </button>
        </small>
      )}
      {run.workspaceNotes?.map((note) => (
        <small className="text-[11px] wrap-anywhere text-muted" key={note}>
          {note}
        </small>
      ))}
      {error && <InlineAlert>{error}</InlineAlert>}
      {canRemoveWorktree(run) && (
        <div>
          <Button disabled={pending} onClick={() => setConfirming(true)}>
            Remove worktree
          </Button>
        </div>
      )}
      {confirming && (
        <Modal
          title="Remove this run's worktree?"
          onClose={() => removing.current || setConfirming(false)}
        >
          <p className="m-0 text-sm wrap-anywhere text-muted">
            This deletes {run.worktree}.
            {run.branch
              ? ` The branch ${run.branch} and its commits stay.`
              : ""}{" "}
            A worktree with uncommitted changes is kept.
          </p>
          <div className="mt-6 flex justify-end gap-2">
            <Button disabled={pending} onClick={() => setConfirming(false)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={pending}
              onClick={() => void remove()}
            >
              Remove worktree
            </Button>
          </div>
        </Modal>
      )}
    </div>
  );
}

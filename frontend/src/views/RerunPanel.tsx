import type {
  RerunRevision,
  ResumeStep,
  RunResume,
  WorkflowPlan,
} from "../types";
import { Button, StateChip } from "./common";

/** How the person chose to rerun a stopped run. */
export interface RerunChoice {
  /** The round the run stopped in, which the rerun names. */
  round: number;
  startOver: boolean;
  /** Throw away what the stopped steps left uncommitted (resume only). */
  discard: boolean;
  /** The uncommitted paths the person saw, which a discard must match. */
  changes: string[];
}

/**
 * What the approval screen does differently for a rerun of a stopped run:
 * Go rebuilds each edit against the plan the run ran under and says what
 * resuming under it would do, and approving resumes the run or starts it
 * over instead of starting a first run.
 */
export interface RerunControls {
  /** What resuming under the plan as first shown would do. */
  resume: RunResume;
  revise(workflowJSON: string): Promise<RerunRevision>;
  onRerun(
    workflow: WorkflowPlan["workflow"],
    choice: RerunChoice,
  ): Promise<void>;
  /**
   * Opens the Rules editor on the run's rule with the plan's access changes
   * made, unsaved; the rerun keeps the plan and what resuming it would do.
   */
  saveToRule?(
    workflow: WorkflowPlan["workflow"],
    resume: RunResume,
  ): Promise<void>;
}

/** Step ids with their states, in a sentence. */
function stepList(steps: ResumeStep[]) {
  return steps.map((step, index) => (
    <span key={step.id} className="inline-flex items-center gap-1">
      {index > 0 && <span className="text-muted">, </span>}
      <span className="text-ink">{step.id}</span>
      {step.state !== "completed" && step.state !== "pending" && (
        <StateChip state={step.state} />
      )}
    </span>
  ));
}

/**
 * The rerun's two ways, as Go said: resuming keeps the steps that finished
 * and runs the rest, with the edited ones, on the run's worktree, or Go's
 * reason it cannot; starting over runs every step in a fresh one. What the
 * stopped steps left uncommitted is kept or discarded for a resume. The
 * changes are this run's; saving them to the rule is a separate choice.
 */
export function RerunPanel({
  resume,
  discard,
  onDiscard,
  onSaveToRule,
  disabled,
}: {
  resume: RunResume;
  discard: boolean;
  onDiscard(discard: boolean): void;
  onSaveToRule?(): void;
  disabled: boolean;
}) {
  return (
    <section
      aria-label="Rerun"
      className="mb-4 grid min-w-0 gap-2.5 border-l-2 border-accent bg-raised p-4 text-xs"
    >
      <b className="text-sm text-ink">
        Rerun the run that stopped in round {resume.round}
      </b>
      {resume.refusal ? (
        <p className="m-0 text-muted wrap-anywhere">
          It cannot be resumed: {resume.refusal}. Start over runs every step
          again in a fresh worktree.
        </p>
      ) : (
        <>
          <p className="m-0 min-w-0 wrap-anywhere text-muted">
            Resume runs {stepList(resume.rerun)} again on the run&apos;s branch
            and worktree
            {resume.kept.length > 0 && (
              <>, keeping what {stepList(resume.kept)} did</>
            )}
            . A step you edit runs again, with every step after it.
          </p>
          <p className="m-0 text-muted">
            Start over runs every step again in a fresh worktree, as a new run
            of the task; this run stays as it is.
          </p>
        </>
      )}
      {!resume.refusal && resume.changes.length > 0 && (
        <fieldset className="m-0 grid min-w-0 gap-2 border-0 p-0">
          <legend className="mb-1.5 p-0 text-ink wrap-anywhere">
            Left uncommitted in {resume.workspace}, for a resume
          </legend>
          <ul className="m-0 grid list-none gap-1 p-0 font-mono">
            {resume.changes.map((change) => (
              <li key={change.path} className="wrap-anywhere">
                <span className="text-muted">{change.status}</span>{" "}
                {change.path}
              </li>
            ))}
          </ul>
          <label className="flex items-start gap-2">
            <input
              type="radio"
              name="rerun-leftovers"
              checked={!discard}
              disabled={disabled}
              onChange={() => onDiscard(false)}
            />
            <span>Keep them for the steps that run again.</span>
          </label>
          <label className="flex items-start gap-2">
            <input
              type="radio"
              name="rerun-leftovers"
              checked={discard}
              disabled={disabled}
              onChange={() => onDiscard(true)}
            />
            <span>Discard them for good before resuming.</span>
          </label>
        </fieldset>
      )}
      {onSaveToRule && (
        <div className="flex min-w-0 flex-wrap items-center justify-end gap-2">
          <span className="min-w-0 flex-1 text-muted">
            The changes you make here are for this run only. To start every plan
            from the rule with them, review them in the Rules editor and save
            them there.
          </span>
          <Button disabled={disabled} onClick={onSaveToRule}>
            Save these changes to the rule…
          </Button>
        </div>
      )}
    </section>
  );
}

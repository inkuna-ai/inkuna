import { useEffect, useState } from "react";
import { getRunResume, resumeWorkflowRun } from "../../bridge";
import type { ResumeStep, RunResume, WorkflowRun } from "../../types";
import { Button, StateChip } from "../common";
import { InlineAlert } from "./shared";

/**
 * Resume a run that stopped before it finished, where it stopped: the steps
 * that stopped or never started run again on the run's worktree, and the
 * rest keep their results. Go says what a resume would run and keep and what
 * is left uncommitted, or why the run cannot be resumed; the bar shows that
 * and sends the person's choice about the uncommitted changes back to Go.
 */
export function ResumeBar({
  run,
  onResumed,
}: {
  run: WorkflowRun;
  /** The run as Go returned it once resumed. */
  onResumed(run: WorkflowRun): void;
}) {
  // What Go offered, remembered with the run, state, round, and workspace it
  // was asked for, so the bar never shows another moment's choices.
  const key = [
    run.id,
    run.state,
    run.round ?? 1,
    run.worktreeRemovedAt ?? "",
    run.lastCommit?.hash ?? "",
  ].join(":");
  // A worktree can change without the run changing, so Go is asked again
  // when the confirmation opens, after a refusal, and on request.
  const [checks, setChecks] = useState(0);
  const checkAgain = () => setChecks((count) => count + 1);
  const [loaded, setLoaded] = useState<{
    key: string;
    resume: RunResume;
  } | null>(null);
  const [loadError, setLoadError] = useState<{
    key: string;
    message: string;
  } | null>(null);
  useEffect(() => {
    let active = true;
    getRunResume(run.id)
      .then((resume) => {
        if (active) setLoaded({ key, resume });
      })
      .catch((reason: unknown) => {
        if (active) setLoadError({ key, message: String(reason) });
      });
    return () => {
      active = false;
    };
  }, [run.id, key, checks]);
  const resume = loaded?.key === key ? loaded.resume : null;

  const [open, setOpen] = useState(false);
  const [discard, setDiscard] = useState(false);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState("");
  // Another moment's offer starts the bar over.
  const [shownFor, setShownFor] = useState("");
  if (resume && shownFor !== key) {
    setShownFor(key);
    setOpen(false);
    setDiscard(false);
    setError("");
  }

  const failure = loadError?.key === key ? loadError.message : "";
  if (!resume) {
    return failure ? (
      <div className="mt-4">
        <InlineAlert>{failure}</InlineAlert>
      </div>
    ) : null;
  }
  if (!resume.stopped) return null;
  const changes = resume.changes;

  async function send(current: RunResume) {
    setSending(true);
    setError("");
    try {
      onResumed(
        await resumeWorkflowRun(run.id, {
          round: current.round,
          discard: discard && current.changes.length > 0,
          changes: current.changes.map((change) => change.path),
        }),
      );
    } catch (reason) {
      setError(String(reason));
      checkAgain();
    } finally {
      setSending(false);
    }
  }

  return (
    <section
      aria-label="Resume"
      className="mt-5 grid min-w-0 gap-3 border-l-2 border-accent bg-raised p-4 text-xs"
    >
      <div className="flex min-w-0 flex-wrap items-center gap-2.5">
        <b className="min-w-0 flex-1 text-sm text-ink">
          Resume from round {resume.round}
        </b>
        {!resume.refusal && !open && (
          <Button
            onClick={() => {
              setOpen(true);
              checkAgain();
            }}
          >
            Resume…
          </Button>
        )}
      </div>
      {resume.refusal ? (
        <div className="flex min-w-0 flex-wrap items-center gap-2.5">
          <p className="m-0 min-w-0 flex-1 text-muted wrap-anywhere">
            This run cannot be resumed: {resume.refusal}
          </p>
          <Button variant="ghost" onClick={checkAgain}>
            Check again
          </Button>
        </div>
      ) : (
        <p className="m-0 text-muted">
          Runs {stepNames(resume.rerun)} again on the same branch
          {resume.kept.length > 0
            ? `, keeping what ${stepNames(resume.kept)} did`
            : ""}
          .
        </p>
      )}
      {error && <InlineAlert>{error}</InlineAlert>}
      {open && !resume.refusal && (
        <form
          aria-label="Confirm resume"
          className="grid min-w-0 gap-3"
          onSubmit={(event) => {
            event.preventDefault();
            void send(resume);
          }}
        >
          <ul className="m-0 grid list-none gap-1.5 p-0">
            {resume.rerun.map((step) => (
              <li
                key={step.id}
                className="flex min-w-0 flex-wrap items-center gap-2"
              >
                <StateChip state={step.state} />
                <span className="wrap-anywhere text-ink">
                  {step.id}
                  {step.title && step.title !== step.id
                    ? ` · ${step.title}`
                    : ""}
                </span>
              </li>
            ))}
          </ul>
          {changes.length > 0 && (
            <fieldset className="m-0 grid min-w-0 gap-2 border-0 p-0">
              <legend className="mb-1.5 p-0 text-ink wrap-anywhere">
                Left uncommitted in {resume.workspace}
              </legend>
              <ul className="m-0 grid list-none gap-1 p-0 font-mono">
                {changes.map((change) => (
                  <li key={change.path} className="wrap-anywhere">
                    <span className="text-muted">{change.status}</span>{" "}
                    {change.path}
                  </li>
                ))}
              </ul>
              <label className="flex items-start gap-2">
                <input
                  type="radio"
                  name="leftovers"
                  checked={!discard}
                  onChange={() => setDiscard(false)}
                />
                <span>
                  Keep them: the steps that stopped finish from them, and their
                  commit takes them.
                </span>
              </label>
              <label className="flex items-start gap-2">
                <input
                  type="radio"
                  name="leftovers"
                  checked={discard}
                  onChange={() => setDiscard(true)}
                />
                <span>
                  Discard them for good, and start those steps from the last
                  commit.
                </span>
              </label>
            </fieldset>
          )}
          <div className="flex min-w-0 flex-wrap items-center justify-end gap-2">
            <Button variant="ghost" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button
              type="submit"
              variant={discard && changes.length > 0 ? "danger" : "primary"}
              disabled={sending}
            >
              {sending
                ? "Resuming…"
                : discard && changes.length > 0
                  ? "Discard and resume"
                  : "Resume"}
            </Button>
          </div>
        </form>
      )}
    </section>
  );
}

/** Step ids in a sentence: "a", "a and b", "a, b, and c". */
function stepNames(steps: ResumeStep[]): string {
  const ids = steps.map((step) => step.id);
  if (ids.length <= 2) return ids.join(" and ");
  return `${ids.slice(0, -1).join(", ")}, and ${ids[ids.length - 1]}`;
}

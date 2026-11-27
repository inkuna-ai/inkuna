import { useEffect, useState } from "react";
import { getRunFailure } from "../../bridge";
import type {
  Denial,
  PlanSuggestion,
  RunFailure,
  WorkflowRun,
} from "../../types";
import { Button, StateChip } from "../common";
import { InlineAlert } from "./shared";

/** What a denial means for the run, in a few words. */
function denialNote(denial: Denial): string {
  if (denial.write) return "a write; only the step's permission allows it";
  if (denial.grantable) return "asked in the inbox; not allowed";
  return "cannot be allowed for one run";
}

/**
 * Why a run that stopped did not finish, next to the step it happened at:
 * the step's error, what its last turn was refused, and its failed checks,
 * all as Go recorded them. Go's suggested fixes can be ticked, and Rerun
 * opens the approval screen with the ticked ones already made, for the
 * person to review; nothing changes until they approve the rerun there.
 */
export function RunFailurePanel({
  run,
  onRerun,
}: {
  run: WorkflowRun;
  onRerun?(suggestions: PlanSuggestion[]): void;
}) {
  // What Go said, remembered with the run, state, and round it was asked
  // for, so the panel never shows another moment's reasons.
  const key = [run.id, run.state, run.round ?? 1].join(":");
  const [loaded, setLoaded] = useState<{
    key: string;
    failure?: RunFailure;
    error?: string;
  } | null>(null);
  const [accepted, setAccepted] = useState<{
    key: string;
    ids: Set<string>;
  }>({ key, ids: new Set() });
  useEffect(() => {
    let active = true;
    getRunFailure(run.id)
      .then((failure) => {
        if (active) setLoaded({ key, failure });
      })
      .catch((reason: unknown) => {
        if (active) setLoaded({ key, error: String(reason) });
      });
    return () => {
      active = false;
    };
  }, [run.id, key]);
  const shown = loaded?.key === key ? loaded : null;
  if (shown?.error)
    return (
      <div className="mt-4">
        <InlineAlert>{shown.error}</InlineAlert>
      </div>
    );
  const failure = shown?.failure;
  if (!failure?.stopped) return null;
  const ticked = accepted.key === key ? accepted.ids : new Set<string>();
  const toggle = (id: string) => {
    const next = new Set(ticked);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setAccepted({ key, ids: next });
  };
  return (
    <section
      aria-label="Why it stopped"
      className="mt-5 grid min-w-0 gap-3 border-l-2 border-danger-line bg-raised p-4 text-xs"
    >
      <b className="text-sm text-ink">Why it stopped</b>
      {failure.steps.length === 0 && (
        <p className="m-0 text-muted">No step recorded why it stopped.</p>
      )}
      {failure.steps.map((step) => (
        <div key={step.stepId} className="grid min-w-0 gap-1.5">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <StateChip state={step.state} />
            <span className="wrap-anywhere text-ink">
              {step.stepId}
              {step.title && step.title !== step.stepId
                ? ` · ${step.title}`
                : ""}
            </span>
          </div>
          {step.error && (
            <p className="m-0 text-ink-soft wrap-anywhere">{step.error}</p>
          )}
          {step.denials.length > 0 && (
            <ul
              aria-label={`What ${step.stepId} was refused`}
              className="m-0 grid list-none gap-1 p-0"
            >
              {step.denials.map((denial, index) => (
                <li key={index} className="min-w-0 wrap-anywhere">
                  Refused <b className="font-mono">{denial.tool}</b>
                  {denial.command && (
                    <span className="font-mono"> {denial.command}</span>
                  )}
                  {/* The detail is the call's input; beside a command it
                      says what else the call carried. */}
                  {denial.detail &&
                    denial.detail !== denial.command &&
                    (denial.command ? (
                      <span className="text-ink-soft"> ({denial.detail})</span>
                    ) : (
                      <span className="font-mono"> {denial.detail}</span>
                    ))}
                  <span className="text-muted"> · {denialNote(denial)}</span>
                </li>
              ))}
            </ul>
          )}
          {step.checks.length > 0 && (
            <ul
              aria-label={`Checks of ${step.stepId} that failed`}
              className="m-0 grid list-none gap-1 p-0"
            >
              {step.checks.map((check) => (
                <li key={check.checkId} className="min-w-0 wrap-anywhere">
                  <span className="font-mono">{check.argv.join(" ")}</span>
                  <span className="text-muted">
                    {" · "}
                    {check.error ||
                      (check.exitCode !== undefined
                        ? `exited ${check.exitCode}`
                        : "failed")}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </div>
      ))}
      {failure.suggestions.length > 0 && (
        <fieldset className="m-0 grid min-w-0 gap-1.5 border-0 p-0">
          <legend className="mb-1.5 p-0 text-ink">Suggested fixes</legend>
          {failure.suggestions.map((suggestion) => (
            <label key={suggestion.id} className="flex items-start gap-2">
              <input
                type="checkbox"
                checked={ticked.has(suggestion.id)}
                onChange={() => toggle(suggestion.id)}
              />
              <span className="wrap-anywhere">{suggestion.text}</span>
            </label>
          ))}
        </fieldset>
      )}
      {onRerun && (
        <div className="flex min-w-0 flex-wrap items-center justify-end gap-2">
          <span className="min-w-0 flex-1 text-muted">
            Rerun opens the plan to approve again, with the ticked fixes made.
          </span>
          <Button
            onClick={() =>
              onRerun(
                failure.suggestions.filter((suggestion) =>
                  ticked.has(suggestion.id),
                ),
              )
            }
          >
            Rerun…
          </Button>
        </div>
      )}
    </section>
  );
}

import { useState } from "react";
import { openExternal, retryTrackerChange } from "../../bridge";
import type { TrackerChange } from "../../types";
import { Button, StateChip } from "../common";
import { InlineAlert } from "./shared";

/** The issue a change acts on, as its tracker names it: ENG-12, or #12. */
function issueLabel(change: TrackerChange): string {
  if (!change.issue) return "";
  return change.tracker === "github" ? `#${change.issue}` : change.issue;
}

/** The text a change would post in full, which its summary cuts to a line. */
function fullText(change: TrackerChange): string {
  // A new issue's summary is its title alone, so its description is never
  // in it.
  if (change.kind === "create") return change.change.description ?? "";
  const text = change.change.body ?? change.change.description ?? "";
  return text.includes("\n") || text.length > 120 ? text : "";
}

/**
 * Tracker changes the run's agents asked for, each with how it stands as Go
 * says it. With retry, a change Go offers to try again has Retry, and what
 * Go answers replaces it here until the run is shown again.
 */
export function TrackerChanges({
  runId,
  changes,
  label,
  retry = false,
}: {
  runId: string;
  changes: TrackerChange[];
  label: string;
  retry?: boolean;
}) {
  const [retrying, setRetrying] = useState("");
  const [answered, setAnswered] = useState<Record<string, TrackerChange>>({});
  const [error, setError] = useState("");
  if (changes.length === 0) return null;

  async function retryChange(id: string) {
    if (retrying) return;
    setRetrying(id);
    setError("");
    try {
      const change = await retryTrackerChange(runId, id);
      setAnswered((current) => ({ ...current, [id]: change }));
    } catch (reason) {
      setError(String(reason));
    } finally {
      setRetrying("");
    }
  }

  return (
    <section aria-label={label} className="grid min-w-0 gap-2 text-xs">
      <b className="min-w-0 text-ink">{label}</b>
      <ul className="m-0 grid min-w-0 list-none gap-1.5 p-0">
        {changes.map((listed) => {
          // Go's answer to a retry is newer than the run it came with, until
          // the run itself says the change is no longer what it was.
          const shown = answered[listed.id];
          const change =
            shown && shown.updatedAt >= listed.updatedAt ? shown : listed;
          const issue = issueLabel(change);
          const text = fullText(change);
          // Nor are a new issue's labels.
          const labels =
            change.kind === "create" ? (change.change.addLabels ?? []) : [];
          return (
            <li
              key={change.id}
              aria-label={`${change.kind} change`}
              className="grid min-w-0 gap-1 bg-field p-2.5"
            >
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <StateChip state={change.state} />
                <span className="font-mono text-[10px] text-muted">
                  {change.stepId}
                </span>
                <span className="min-w-0 flex-1 wrap-anywhere">
                  {issue && <b className="text-ink">{issue} </b>}
                  {change.summary}
                </span>
                {change.url && (
                  <button
                    type="button"
                    className="cursor-pointer border-0 bg-transparent p-0 text-accent underline"
                    onClick={() => openExternal(change.url!)}
                  >
                    Open
                  </button>
                )}
                {retry && change.retry && (
                  <Button
                    disabled={retrying !== ""}
                    onClick={() => void retryChange(change.id)}
                  >
                    {retrying === change.id ? "Retrying…" : "Retry"}
                  </Button>
                )}
              </div>
              {labels.length > 0 && (
                <p className="m-0 min-w-0 text-ink-soft wrap-anywhere">
                  Labels: {labels.join(", ")}
                </p>
              )}
              {text && (
                <p className="m-0 max-h-40 min-w-0 overflow-auto whitespace-pre-wrap text-ink-soft wrap-anywhere">
                  {text}
                </p>
              )}
              {change.reason && (
                <small className="min-w-0 text-[11px] wrap-anywhere text-muted">
                  {change.reason}
                </small>
              )}
            </li>
          );
        })}
      </ul>
      {error && <InlineAlert>{error}</InlineAlert>}
    </section>
  );
}

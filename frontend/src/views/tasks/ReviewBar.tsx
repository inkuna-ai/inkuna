import { useEffect, useState } from "react";
import { approveReview, getRunReview, requestChanges } from "../../bridge";
import type { RunReview, WorkflowRun } from "../../types";
import { Button, inputClass } from "../common";
import { GateLinks } from "../messages/GateLinks";
import { InlineAlert } from "./shared";
import { TrackerChanges } from "./TrackerChanges";

interface Row {
  key: number;
  target: string;
  text: string;
}

/**
 * Approve a run paused at a review gate, or send it back with what to
 * change: a row per thing to fix, each for the stage that should fix it.
 * Go says what the bar may offer and checks what it sends; the bar shows
 * Go's reason when it refuses. At a gate the form is open; on an ended run
 * it opens from a button.
 */
export function ReviewBar({
  run,
  commentsVersion = 0,
  onChanged,
  onOpenThread,
}: {
  run: WorkflowRun;
  /** Changes when a comment is written, so the bar counts the drafts again. */
  commentsVersion?: number;
  /** The run as Go returned it after an approval or a request. */
  onChanged(run: WorkflowRun): void;
  onOpenThread?(): void;
}) {
  const reviewing = run.state === "reviewing";
  const round = run.round ?? 1;
  // What Go offered, remembered with the run, state, round, and workspace it
  // was asked for, so a bar never shows another moment's choices.
  const key = [
    run.id,
    run.state,
    round,
    run.worktreeRemovedAt ?? "",
    run.lastCommit?.hash ?? "",
  ].join(":");
  // A worktree can change without the run changing, so Go is asked again
  // when the form opens, after a refusal, and on request, keeping the form.
  const [checks, setChecks] = useState(0);
  const checkAgain = () => setChecks((count) => count + 1);
  const [loaded, setLoaded] = useState<{
    key: string;
    review: RunReview;
  } | null>(null);
  const [loadError, setLoadError] = useState<{
    key: string;
    message: string;
  } | null>(null);
  useEffect(() => {
    let active = true;
    getRunReview(run.id)
      .then((review) => {
        if (active) setLoaded({ key, review });
      })
      .catch((reason: unknown) => {
        if (active) setLoadError({ key, message: String(reason) });
      });
    return () => {
      active = false;
    };
  }, [run.id, key, checks, commentsVersion]);
  const review = loaded?.key === key ? loaded.review : null;

  const [open, setOpen] = useState(false);
  // Rows keep their own keys, so removing one never moves another's text.
  const [form, setForm] = useState<{ next: number; rows: Row[] }>({
    next: 1,
    rows: [],
  });
  const rows = form.rows;
  const [sending, setSending] = useState(false);
  const [error, setError] = useState("");
  const defaultTarget = review?.target || review?.targets[0]?.id || "";
  const setRows = (change: (rows: Row[]) => Row[]) =>
    setForm((current) => ({ ...current, rows: change(current.rows) }));
  const addRow = () =>
    setForm((current) => ({
      next: current.next + 1,
      rows: [
        ...current.rows,
        { key: current.next, target: defaultTarget, text: "" },
      ],
    }));
  // A review that offers new choices starts the form over.
  const [formFor, setFormFor] = useState("");
  if (review && formFor !== key) {
    setFormFor(key);
    setForm({ next: 2, rows: [{ key: 1, target: defaultTarget, text: "" }] });
    setOpen(false);
    setError("");
  }

  const items = rows
    .map((current) => ({ target: current.target, text: current.text.trim() }))
    .filter((item) => item.text);
  async function act(action: () => Promise<WorkflowRun>) {
    setSending(true);
    setError("");
    try {
      onChanged(await action());
    } catch (reason) {
      setError(String(reason));
      checkAgain();
    } finally {
      setSending(false);
    }
  }
  const update = (key: number, change: Partial<Row>) =>
    setRows((current) =>
      current.map((entry) =>
        entry.key === key ? { ...entry, ...change } : entry,
      ),
    );

  const failure = loadError?.key === key ? loadError.message : "";
  if (!review) {
    return failure ? (
      <div className="mt-4">
        <InlineAlert>{failure}</InlineAlert>
      </div>
    ) : null;
  }
  const canRequest = !review.refusal && review.targets.length > 0;
  const showForm = canRequest && (reviewing || open);
  const drafts = review.drafts ?? 0;
  const draftWords = `${drafts} draft ${drafts === 1 ? "comment" : "comments"}`;

  return (
    <section
      aria-label="Review"
      className="mt-5 grid min-w-0 gap-3 border-l-2 border-accent bg-raised p-4 text-xs"
    >
      <div className="flex min-w-0 flex-wrap items-center gap-2.5">
        <b className="min-w-0 flex-1 text-sm text-ink">
          {reviewing
            ? `Waiting for your review of round ${review.round}`
            : `Round ${review.round} ${run.state}`}
        </b>
        {reviewing && onOpenThread && (
          <Button variant="ghost" onClick={onOpenThread}>
            Open in Messages
          </Button>
        )}
        {!reviewing && canRequest && !open && (
          <Button
            onClick={() => {
              setOpen(true);
              checkAgain();
            }}
          >
            Send back for changes
          </Button>
        )}
        {review.approve && (
          <Button
            variant="primary"
            disabled={sending}
            onClick={() => void act(() => approveReview(run.id))}
          >
            Approve
          </Button>
        )}
      </div>
      {/* What each paused gate follows, so nothing is approved unread. */}
      {reviewing &&
        (run.steps ?? [])
          .filter((step) => step.state === "reviewing")
          .map((step) => (
            <GateLinks key={step.id} runId={run.id} gateId={step.id} />
          ))}
      {/* What Approve makes in the tracker, seen before it is made. */}
      {reviewing && (review.trackerChanges?.length ?? 0) > 0 && (
        <TrackerChanges
          runId={run.id}
          changes={review.trackerChanges ?? []}
          label="Approve also makes these tracker changes"
        />
      )}
      {review.refusal && (
        <div className="flex min-w-0 flex-wrap items-center gap-2.5">
          <p className="m-0 min-w-0 flex-1 text-muted wrap-anywhere">
            {reviewing ? "" : "Changes cannot be sent: "}
            {review.refusal}
          </p>
          <Button variant="ghost" onClick={checkAgain}>
            Check again
          </Button>
        </div>
      )}
      {error && <InlineAlert>{error}</InlineAlert>}
      {showForm && (
        <form
          className="grid min-w-0 gap-2.5"
          aria-label="Request changes"
          onSubmit={(event) => {
            event.preventDefault();
            void act(() =>
              requestChanges(run.id, { round: review.round, items }),
            );
          }}
        >
          {rows.map((entry, index) => (
            <div
              key={entry.key}
              className="grid min-w-0 grid-cols-[minmax(120px,200px)_minmax(0,1fr)_auto] items-start gap-2 max-[640px]:grid-cols-1"
            >
              <select
                aria-label={`Stage for item ${index + 1}`}
                className={`${inputClass} py-1.5 text-xs`}
                value={entry.target}
                onChange={(event) =>
                  update(entry.key, { target: event.target.value })
                }
              >
                {review.targets.map((target) => (
                  <option key={target.id} value={target.id}>
                    {target.id} · {target.agent}
                  </option>
                ))}
              </select>
              <textarea
                aria-label={`Item ${index + 1}`}
                className={`${inputClass} min-h-16 resize-y text-xs`}
                placeholder={`What ${entry.target} should change…`}
                value={entry.text}
                onChange={(event) =>
                  update(entry.key, { text: event.target.value })
                }
              />
              <Button
                variant="ghost"
                aria-label={`Remove item ${index + 1}`}
                title="Remove this item"
                disabled={rows.length === 1}
                onClick={() =>
                  setRows((current) =>
                    current.filter((other) => other.key !== entry.key),
                  )
                }
              >
                ✕
              </Button>
            </div>
          ))}
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <Button variant="ghost" onClick={addRow}>
              + Add another item
            </Button>
            <span className="flex-1" />
            {!reviewing && (
              <Button variant="ghost" onClick={() => setOpen(false)}>
                Cancel
              </Button>
            )}
            <Button
              type="submit"
              disabled={sending || (items.length === 0 && drafts === 0)}
            >
              {sending ? "Sending…" : "Request changes"}
            </Button>
          </div>
          {drafts > 0 && (
            <p className="m-0 text-muted">
              Your {draftWords} on the changes {drafts === 1 ? "goes" : "go"}{" "}
              with this request
              {review.approve ? "; Approve leaves them as drafts" : ""}.
            </p>
          )}
        </form>
      )}
    </section>
  );
}

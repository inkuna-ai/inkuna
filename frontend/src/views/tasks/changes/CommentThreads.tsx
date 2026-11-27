import { useEffect, useState } from "react";
import {
  deleteComment,
  getCommentTarget,
  resolveThread,
  saveComment,
} from "../../../bridge";
import type { CommentThread, LineComment, ReviewTarget } from "../../../types";
import { Button, Chip, Code, inputClass } from "../../common";
import { InlineAlert } from "../shared";
import {
  canComment,
  currentComposer,
  linesLabel,
  useCommenting,
  type Composer,
} from "./commenting";

const replyWords: Record<string, { text: string; tone: string }> = {
  fixed: { text: "fixed it", tone: "text-ok" },
  declined: { text: "declined", tone: "text-warn" },
  question: { text: "asked", tone: "text-accent" },
  "no-reply": { text: "did not reply", tone: "text-muted" },
};

/** One comment of a thread and, once it was sent, its target's reply. */
function CommentEntry({
  comment,
  canEdit,
  onEdit,
  onDelete,
  busy,
}: {
  comment: LineComment;
  canEdit: boolean;
  onEdit(): void;
  onDelete(): void;
  busy: boolean;
}) {
  const reply = comment.status ? replyWords[comment.status] : undefined;
  return (
    <article
      aria-label={
        comment.state === "draft" ? "Draft comment" : `Comment ${comment.item}`
      }
      className="grid min-w-0 gap-1"
    >
      <div className="flex min-w-0 flex-wrap items-center gap-2 text-[11px] text-muted">
        <b className="font-medium text-ink-strong">You</b>
        <span>for {comment.target}</span>
        {comment.state === "draft" ? (
          <Chip tone="warn">draft</Chip>
        ) : (
          <span className="font-mono text-[10px] text-accent">
            {comment.item} · round {comment.round}
          </span>
        )}
        {comment.state === "draft" && (
          <span className="ml-auto flex gap-1">
            {canEdit && (
              <Button
                variant="ghost"
                className="px-1.5 py-0.5 text-[11px]"
                disabled={busy}
                onClick={onEdit}
              >
                Edit
              </Button>
            )}
            <Button
              variant="ghost"
              className="px-1.5 py-0.5 text-[11px] text-danger"
              disabled={busy}
              onClick={onDelete}
            >
              Delete
            </Button>
          </span>
        )}
      </div>
      <p className="m-0 min-w-0 wrap-anywhere whitespace-pre-wrap text-ink">
        {comment.body}
      </p>
      {comment.state === "sent" && (
        <p className="m-0 min-w-0 border-l-2 border-accent-line pl-2 wrap-anywhere whitespace-pre-wrap text-ink-soft">
          <b className="font-medium text-ink-strong">{comment.target}</b>{" "}
          {reply ? (
            <span className={reply.tone}>{reply.text}</span>
          ) : (
            <span className="text-muted">has not replied yet</span>
          )}
          {comment.note ? `: ${comment.note}` : ""}
        </p>
      )}
    </article>
  );
}

/**
 * A comment form: what to change, and the stage to change it. Go checks
 * both when it saves.
 */
function CommentForm({
  label,
  body,
  target,
  targets,
  submit,
  onChange,
  onSubmit,
  onCancel,
}: {
  label: string;
  body: string;
  target: string;
  targets: ReviewTarget[];
  submit: string;
  onChange(change: { body?: string; target?: string }): void;
  onSubmit(): Promise<void>;
  onCancel(): void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  return (
    <form
      aria-label={label}
      className="grid min-w-0 gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        setBusy(true);
        setError("");
        onSubmit()
          .catch((reason: unknown) => setError(String(reason)))
          .finally(() => setBusy(false));
      }}
    >
      <textarea
        aria-label="Comment"
        autoFocus
        className={`${inputClass} min-h-16 resize-y font-sans text-xs`}
        placeholder="What should change here…"
        value={body}
        onChange={(event) => onChange({ body: event.target.value })}
        onKeyDown={(event) => {
          if (event.key === "Escape") onCancel();
        }}
      />
      {error && <InlineAlert>{error}</InlineAlert>}
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <label className="flex items-center gap-1.5 text-[11px] text-ink-soft">
          For
          <select
            aria-label="Stage for the comment"
            className={`${inputClass} w-auto py-1 text-xs`}
            value={target}
            onChange={(event) => onChange({ target: event.target.value })}
          >
            {!target && <option value="">Choosing…</option>}
            {targets.map((option) => (
              <option key={option.id} value={option.id}>
                {option.id} · {option.agent}
              </option>
            ))}
          </select>
        </label>
        <span className="flex-1" />
        <Button
          variant="ghost"
          className="px-2.5 py-1 text-xs"
          onClick={onCancel}
        >
          Cancel
        </Button>
        <Button
          type="submit"
          className="px-2.5 py-1 text-xs"
          disabled={busy || !body.trim() || !target}
        >
          {submit}
        </Button>
      </div>
    </form>
  );
}

/** The form for a new thread on the lines the person picked. */
export function NewComment({ composer }: { composer: Composer }) {
  const commenting = useCommenting()!;
  const { runID, targets, setComposer, reload } = commenting;
  // The default stage comes from Go: the one that last changed the line.
  // A stage the person picked first is kept.
  const { path, side, endLine } = composer;
  const chosen = Boolean(composer.target);
  useEffect(() => {
    if (chosen) return;
    let active = true;
    const pick = (target: string) => {
      const fallback = targets[targets.length - 1]?.id ?? "";
      const stage = targets.some((option) => option.id === target)
        ? target
        : fallback;
      if (active && stage)
        setComposer((current) =>
          current && !current.target ? { ...current, target: stage } : current,
        );
    };
    getCommentTarget(runID, path, side, endLine).then(pick, () => pick(""));
    return () => {
      active = false;
    };
  }, [runID, path, side, endLine, chosen, targets, setComposer]);
  return (
    <div className="border-y border-accent-line bg-raised px-3 py-2.5 font-sans">
      <p className="m-0 mb-2 text-[11px] text-muted">
        Comment on {linesLabel(composer.side, composer.line, composer.endLine)}.
        It stays a draft until you request changes.
      </p>
      <CommentForm
        label="New comment"
        body={composer.body}
        target={composer.target}
        targets={targets}
        submit="Add comment"
        onChange={(change) =>
          setComposer((current) =>
            current ? { ...current, ...change } : current,
          )
        }
        onCancel={() => setComposer(null)}
        onSubmit={async () => {
          await saveComment(runID, {
            path: composer.path,
            side: composer.side,
            line: composer.line,
            endLine: composer.endLine,
            commit: composer.commit,
            body: composer.body,
            target: composer.target,
          });
          setComposer(null);
          reload();
        }}
      />
    </div>
  );
}

/**
 * A thread: each comment with its reply, and what can be done with it.
 * Resolved threads fold to a line. A thread shown away from its lines
 * names them, and an outdated one shows the diff its last comment was sent
 * with, since its lines are gone.
 */
export function ThreadBox({
  thread,
  showPlace = false,
}: {
  thread: CommentThread;
  showPlace?: boolean;
}) {
  const commenting = useCommenting()!;
  const { runID, targets, reload } = commenting;
  const writable = canComment(commenting);
  const resolved = thread.state === "resolved";
  const [open, setOpen] = useState(!resolved);
  // A draft being edited has its id; a reply being written has none.
  const [editing, setEditing] = useState<{
    id?: string;
    body: string;
    target: string;
  } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const draft = thread.comments.find((comment) => comment.state === "draft");
  const last = thread.comments[thread.comments.length - 1];
  const excerpt = [...thread.comments]
    .reverse()
    .find((comment) => comment.excerpt)?.excerpt;
  async function act(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await action();
      reload();
    } catch (reason) {
      setError(String(reason));
    } finally {
      setBusy(false);
    }
  }
  // Resolving folds the thread; reopening unfolds it to reply.
  const toggleResolved = () => {
    setOpen(resolved);
    void act(() => resolveThread(runID, thread.id, !resolved));
  };
  const place = `${thread.path} ${linesLabel(thread.side, thread.line, thread.endLine)}`;
  const resolver = thread.resolvedBy === "human" ? "you" : last?.target;
  return (
    <section
      aria-label={`Comments on ${place}`}
      className="grid min-w-0 gap-2 border-y border-line bg-raised px-3 py-2.5 font-sans text-xs leading-relaxed"
    >
      {(showPlace || resolved || thread.outdated || thread.unplaced) && (
        <div className="flex min-w-0 flex-wrap items-center gap-2 text-[11px] text-muted">
          {showPlace && (
            <span className="min-w-0 font-mono wrap-anywhere text-ink-soft">
              {place}
            </span>
          )}
          {resolved && <Chip tone="green">resolved by {resolver}</Chip>}
          {thread.outdated && (
            <Chip tone="muted">
              outdated
              {thread.outdatedRound
                ? ` after round ${thread.outdatedRound}`
                : ""}
            </Chip>
          )}
          {thread.unplaced && (
            <Chip tone="muted">written on newer changes; refresh</Chip>
          )}
          {resolved && (
            <Button
              variant="ghost"
              className="px-1.5 py-0.5 text-[11px]"
              aria-expanded={open}
              onClick={() => setOpen((value) => !value)}
            >
              {open
                ? "Hide"
                : `Show ${thread.comments.length} ${thread.comments.length === 1 ? "comment" : "comments"}`}
            </Button>
          )}
          {resolved && last?.state === "sent" && (
            <Button
              variant="ghost"
              className="px-1.5 py-0.5 text-[11px]"
              disabled={busy}
              onClick={toggleResolved}
            >
              Reopen
            </Button>
          )}
        </div>
      )}
      {open && (
        <>
          {showPlace && excerpt && <Code>{excerpt}</Code>}
          {thread.comments.map((comment) =>
            editing?.id === comment.id ? (
              <CommentForm
                key={comment.id}
                label="Edit comment"
                body={editing.body}
                target={editing.target}
                targets={targets}
                submit="Save"
                onChange={(change) => setEditing({ ...editing, ...change })}
                onCancel={() => setEditing(null)}
                onSubmit={async () => {
                  await saveComment(runID, {
                    id: comment.id,
                    body: editing.body,
                    target: editing.target,
                  });
                  setEditing(null);
                  reload();
                }}
              />
            ) : (
              <CommentEntry
                key={comment.id}
                comment={comment}
                canEdit={writable}
                busy={busy}
                onEdit={() =>
                  setEditing({
                    id: comment.id,
                    body: comment.body,
                    target: comment.target,
                  })
                }
                onDelete={() =>
                  void act(() => deleteComment(runID, comment.id))
                }
              />
            ),
          )}
          {editing && !editing.id && (
            <CommentForm
              label="Reply"
              body={editing.body}
              target={editing.target}
              targets={targets}
              submit="Add reply"
              onChange={(change) => setEditing({ ...editing, ...change })}
              onCancel={() => setEditing(null)}
              onSubmit={async () => {
                await saveComment(runID, {
                  threadId: thread.id,
                  body: editing.body,
                  target: editing.target,
                });
                setEditing(null);
                reload();
              }}
            />
          )}
        </>
      )}
      {error && <InlineAlert>{error}</InlineAlert>}
      {!editing && !resolved && (
        <div className="flex min-w-0 flex-wrap items-center gap-2 empty:hidden">
          {open &&
            !resolved &&
            !thread.outdated &&
            !thread.unplaced &&
            writable &&
            !draft && (
              <Button
                className="px-2.5 py-1 text-xs"
                disabled={busy}
                onClick={() =>
                  setEditing({
                    body: "",
                    target:
                      targets.find((option) => option.id === last?.target)
                        ?.id ??
                      targets[targets.length - 1]?.id ??
                      "",
                  })
                }
              >
                Reply
              </Button>
            )}
          {!resolved && last?.state === "sent" && (
            <Button
              variant="ghost"
              className="px-2.5 py-1 text-xs"
              disabled={busy}
              onClick={toggleResolved}
            >
              Resolve
            </Button>
          )}
        </div>
      )}
    </section>
  );
}

/**
 * Threads the diff cannot show at their lines: outdated ones, whose lines
 * changed, and ones on files these changes do not show; and why no comment
 * can be written now, when Go says so.
 */
export function OtherThreads({
  threads,
  error,
}: {
  threads: CommentThread[];
  error: string;
}) {
  const commenting = useCommenting();
  const [open, setOpen] = useState(false);
  if (!commenting) return null;
  const outdated = threads.filter((thread) => thread.outdated).length;
  // A comment begun before the branch moved has lines numbered on a version
  // the diff no longer shows, so its text is kept here until the person picks
  // the lines again, which carries it over, or discards it.
  const stale =
    commenting.loaded && commenting.composer && !currentComposer(commenting)
      ? commenting.composer
      : null;
  return (
    <>
      {error && <InlineAlert>{error}</InlineAlert>}
      {stale && (
        <div
          role="status"
          className="grid min-w-0 gap-2 border-l-2 border-accent bg-raised px-3 py-2 text-xs"
        >
          <p className="m-0 text-ink-soft wrap-anywhere">
            You were writing a comment on {stale.path}{" "}
            {linesLabel(stale.side, stale.line, stale.endLine)} before the
            branch moved. Pick the lines again to carry it over.
          </p>
          {stale.body && (
            <p className="m-0 wrap-anywhere whitespace-pre-wrap text-ink">
              {stale.body}
            </p>
          )}
          <div>
            <Button
              variant="ghost"
              className="px-2.5 py-1 text-xs"
              onClick={() => commenting.setComposer(null)}
            >
              Discard
            </Button>
          </div>
        </div>
      )}
      {commenting.loaded && commenting.refusal && (
        <p className="m-0 text-[11px] wrap-anywhere text-muted">
          Comments are written while the run can be sent back for changes:{" "}
          {commenting.refusal}
        </p>
      )}
      {threads.length > 0 && (
        <div className="min-w-0 border border-line bg-field">
          <button
            type="button"
            aria-expanded={open}
            className="w-full cursor-pointer border-0 bg-transparent px-3 py-2 text-left text-xs text-ink-soft hover:text-ink-strong"
            onClick={() => setOpen((value) => !value)}
          >
            {open ? "▾" : "▸"} {threads.length}{" "}
            {threads.length === 1 ? "comment" : "comments"} not on these lines
            {outdated > 0 ? ` (${outdated} outdated)` : ""}
          </button>
          {open && (
            <div className="grid min-w-0 gap-2 p-2 pt-0">
              {threads.map((thread) => (
                <ThreadBox
                  key={`${thread.id}:${thread.state}`}
                  thread={thread}
                  showPlace
                />
              ))}
            </div>
          )}
        </div>
      )}
    </>
  );
}

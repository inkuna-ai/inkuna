import { useEffect, useState } from "react";
import { copyText } from "../../../bridge";
import { formatTime } from "../../../format";
import type { BranchCommit } from "../../../types";
import { Button, Chip } from "../../common";
import { InlineAlert } from "../shared";
import { groupCommits } from "./commits";

/**
 * The commits on the run's branch, like a pull request's Commits tab, in
 * branch order under a header for each step that made them. Choosing one
 * shows its changes; All changes shows the whole branch.
 */
export function CommitList({
  commits,
  loading,
  error,
  onRetry,
  selected,
  onSelect,
}: {
  commits: BranchCommit[];
  /** No read of the commits has finished yet. */
  loading: boolean;
  /** Why the last read failed; the commits shown, if any, are from before. */
  error: string;
  onRetry(): void;
  /** The commit the Files view shows, or undefined for all changes. */
  selected?: string;
  onSelect(hash?: string): void;
}) {
  const row =
    "flex w-full min-w-0 cursor-pointer flex-wrap items-center gap-x-3 gap-y-1 border-0 bg-transparent px-3 py-2 text-left text-xs";
  const failure = error && (
    <div className="grid gap-2">
      <InlineAlert>{error}</InlineAlert>
      <div>
        <Button className="px-2.5 py-1 text-xs" onClick={onRetry}>
          Retry
        </Button>
      </div>
    </div>
  );
  if (loading)
    return <p className="m-0 text-xs text-muted">Loading commits…</p>;
  if (commits.length === 0)
    return (
      failure || (
        <p className="m-0 text-xs text-muted">No commits on the branch yet.</p>
      )
    );
  return (
    <div className="grid min-w-0 gap-3">
      {failure}
      <button
        type="button"
        aria-current={selected === undefined ? "true" : undefined}
        className={`${row} rounded border border-line ${selected === undefined ? "bg-raised text-ink-strong shadow-[inset_2px_0_0_var(--color-accent)]" : "text-ink hover:bg-raised"}`}
        onClick={() => onSelect(undefined)}
      >
        <span className="font-semibold">All changes</span>
        <span className="text-muted">
          {commits.length} {commits.length === 1 ? "commit" : "commits"}
        </span>
      </button>
      {groupCommits(commits).map((group) => (
        <section
          key={group.key}
          aria-label={
            group.external ? "External commits" : `Step ${group.stepId}`
          }
          className="min-w-0 border border-line bg-field"
        >
          <h4
            className="m-0 flex min-w-0 flex-wrap items-center gap-2 border-b border-line bg-raised px-3 py-1.5 text-xs font-normal text-ink-soft"
            title={
              group.external
                ? "Commits Inkuna has no record of making, such as an edit made by hand in the worktree"
                : undefined
            }
          >
            {group.external ? (
              <Chip tone="warn">external</Chip>
            ) : (
              <>
                <span>
                  step{" "}
                  <b className="font-mono font-medium text-ink-strong">
                    {group.stepId}
                  </b>
                </span>
                {group.agent && (
                  <span className="text-muted">· {group.agent}</span>
                )}
              </>
            )}
          </h4>
          <ol className="m-0 list-none p-0">
            {group.commits.map((commit) => (
              <li
                key={commit.hash}
                className="flex min-w-0 items-center border-b border-line last:border-b-0"
              >
                <button
                  type="button"
                  aria-current={selected === commit.hash ? "true" : undefined}
                  className={`${row} flex-1 ${selected === commit.hash ? "bg-raised text-ink-strong shadow-[inset_2px_0_0_var(--color-accent)]" : "text-ink hover:bg-raised"}`}
                  onClick={() => onSelect(commit.hash)}
                >
                  <span className="font-mono text-accent">
                    {commit.shortHash}
                  </span>
                  <span className="min-w-0 flex-1 basis-[200px] wrap-anywhere">
                    {commit.subject}
                  </span>
                  {commit.merge && <Chip tone="muted">merge</Chip>}
                  <span className="text-muted">
                    {commit.author.replace(/\s*<[^>]*>$/, "")} ·{" "}
                    {formatTime(commit.date)}
                  </span>
                  <span className="font-mono whitespace-nowrap text-muted">
                    {commit.files} {commit.files === 1 ? "file" : "files"}{" "}
                    <span className="text-ok">+{commit.additions}</span>{" "}
                    <span className="text-danger">−{commit.deletions}</span>
                  </span>
                </button>
                <CopyHash hash={commit.hash} />
              </li>
            ))}
          </ol>
        </section>
      ))}
    </div>
  );
}

/** Copies a commit's full hash, and says so for a moment. */
export function CopyHash({ hash }: { hash: string }) {
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");
  const [reason, setReason] = useState("");
  useEffect(() => {
    if (state === "idle") return;
    const timer = window.setTimeout(() => setState("idle"), 1500);
    return () => window.clearTimeout(timer);
  }, [state]);
  return (
    <button
      type="button"
      aria-label={`Copy ${hash}`}
      title={state === "failed" ? reason : "Copy the full hash"}
      className="flex-none cursor-pointer border-0 bg-transparent px-3 py-2 font-mono text-[11px] text-ink-soft hover:text-accent"
      onClick={() =>
        copyText(hash).then(
          () => setState("copied"),
          (error: unknown) => {
            setReason(String(error));
            setState("failed");
          },
        )
      }
    >
      {state === "copied" ? "Copied" : state === "failed" ? "Failed" : "Copy"}
    </button>
  );
}

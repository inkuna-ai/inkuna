import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import {
  getRunChanges,
  getRunCommits,
  getRunViewed,
  setFileViewed,
} from "../../../bridge";
import { formatTime } from "../../../format";
import type {
  BranchCommit,
  DiffChange,
  DiffFile,
  DiffScope,
  WorkflowRun,
} from "../../../types";
import { Button, Code, Empty, inputClass, TabPanel, Tabs } from "../../common";
import { InlineAlert, Summary, TabBody } from "../shared";
import { ChangeView } from "./ChangeView";
import { ChecksView } from "./ChecksView";
import { CommentsContext, useComments } from "./commenting";
import { OtherThreads } from "./CommentThreads";
import { CommitList, CopyHash } from "./CommitList";
import { rememberMode, storedMode, type DiffMode } from "./mode";
import type { ShownRound } from "../rounds";

/** The Changes tab's views, like a pull request's tabs. */
export type ChangesView = "files" | "commits" | "checks";

/** Below this width the diff column has no room for two versions side by side. */
const splitWidth = 480;

const viewedKey = (path: string, blob: string) => `${path}\u0000${blob}`;

const plural = (count: number, one: string, many: string) =>
  `${count} ${count === 1 ? one : many}`;

/**
 * The run's changes, reviewed like a pull request: all of them, or one
 * commit's, as a file tree beside each file's diff, with the branch's
 * commits a tab away. Files marked viewed stay viewed until their content
 * changes.
 */
export function ChangesTab({
  run,
  rounds = [],
  view: shownView,
  onView,
  onCommentsChanged,
}: {
  run: WorkflowRun;
  /** The run's rounds; each after the first offers what changed since it started. */
  rounds?: ShownRound[];
  /** The view to show, when the run detail chooses it, as its checks summary does. */
  view?: ChangesView;
  onView?(view: ChangesView): void;
  /** Called when a comment is written, changed, or resolved here. */
  onCommentsChanged?(): void;
}) {
  const started = Boolean(run.branch || run.worktree);
  const [scope, setScope] = useState<DiffScope>({ kind: "branch" });
  // The round a range scope shows the changes since, which the scope alone
  // cannot tell apart when two rounds started from the same commit.
  const [sinceRound, setSinceRound] = useState<number | null>(null);
  const [ownView, setOwnView] = useState<ChangesView>("files");
  const view = shownView ?? ownView;
  const setView = (next: ChangesView) => {
    setOwnView(next);
    onView?.(next);
  };
  const [refreshes, setRefreshes] = useState(0);

  // Results remember the scope they were fetched for, so switching scope
  // never shows the previous scope's files, while a refresh keeps them up
  // until the new ones arrive.
  const [loaded, setLoaded] = useState<{
    scope: DiffScope;
    change: DiffChange;
  } | null>(null);
  const [failure, setFailure] = useState<{
    scope: DiffScope;
    message: string;
  } | null>(null);
  useEffect(() => {
    if (!started) return;
    let active = true;
    getRunChanges(run.id, scope)
      .then((change) => {
        if (!active) return;
        setLoaded({ scope, change });
        setFailure(null);
      })
      .catch((reason: unknown) => {
        if (active) setFailure({ scope, message: String(reason) });
      });
    return () => {
      active = false;
    };
  }, [run.id, scope, refreshes, started]);
  const change = loaded?.scope === scope ? loaded.change : null;
  const error = failure?.scope === scope ? failure.message : "";

  // Comments go on all of the branch's changes, placed on the commit the
  // view shows; a round, or the run pausing or ending, can change them.
  const commentable = scope.kind === "branch" && change !== null;
  const comments = useComments(
    run.id,
    commentable ? change.to : undefined,
    `${run.state}:${run.round ?? 1}`,
    onCommentsChanged,
  );
  const shownPaths = new Set(change?.files.map((file) => file.path));
  const elsewhere = commentable
    ? comments.threads.filter(
        (thread) =>
          thread.outdated || thread.unplaced || !shownPaths.has(thread.path),
      )
    : [];

  // Commits refetch when the run commits again; the change does not, so the
  // reader is told rather than having the diff move under them.
  // The Commits view tells a read still under way, or one that failed, from
  // a branch with no commits, so it remembers whether a read has succeeded
  // and why the last one failed. A later read keeps the list it has.
  const [commits, setCommits] = useState<BranchCommit[]>([]);
  const [commitsRead, setCommitsRead] = useState(false);
  const [commitsError, setCommitsError] = useState("");
  const [commitRetries, setCommitRetries] = useState(0);
  const [notice, setNotice] = useState("");
  const lastCommit = run.lastCommit?.hash;
  useEffect(() => {
    if (!started) return;
    let active = true;
    getRunCommits(run.id)
      .then((list) => {
        if (!active) return;
        setCommits(list ?? []);
        setCommitsRead(true);
        setCommitsError("");
      })
      .catch((reason: unknown) => {
        if (active) setCommitsError(String(reason));
      });
    return () => {
      active = false;
    };
  }, [run.id, lastCommit, refreshes, commitRetries, started]);

  const [viewed, setViewed] = useState<ReadonlySet<string>>(new Set());
  // Marks toggled before the stored ones arrive are newer than them, so
  // they are applied on top rather than replaced.
  const marksRead = useRef(false);
  const earlyMarks = useRef(new Map<string, boolean>());
  useEffect(() => {
    if (!started) return;
    let active = true;
    marksRead.current = false;
    getRunViewed(run.id)
      .then((marks) => {
        if (!active) return;
        const next = new Set(
          (marks ?? []).map((mark) => viewedKey(mark.path, mark.blob)),
        );
        for (const [key, on] of earlyMarks.current)
          if (on) next.add(key);
          else next.delete(key);
        earlyMarks.current.clear();
        marksRead.current = true;
        setViewed(next);
      })
      .catch((reason: unknown) => {
        if (active) setNotice(String(reason));
      });
    return () => {
      active = false;
    };
  }, [run.id, started]);
  const isViewed = useCallback(
    (file: DiffFile) =>
      Boolean(file.blob) && viewed.has(viewedKey(file.path, file.blob!)),
    [viewed],
  );

  // A file is open unless it is viewed or generated, until the reader opens
  // or closes it themselves. Their choice holds for that version of the
  // file, so one that changes again opens or closes by the rule anew.
  const [opened, setOpened] = useState<ReadonlyMap<string, boolean>>(new Map());
  const isOpen = useCallback(
    (file: DiffFile) =>
      opened.get(viewedKey(file.path, file.blob ?? "")) ??
      !(isViewed(file) || file.generated),
    [opened, isViewed],
  );
  const toggleOpen = useCallback(
    (file: DiffFile) =>
      setOpened((all) =>
        new Map(all).set(viewedKey(file.path, file.blob ?? ""), !isOpen(file)),
      ),
    [isOpen],
  );
  // One mark's writes go one after another, so they reach Go in the order
  // they were made. A failure undoes the screen only when no later toggle
  // of that mark has replaced it, and then shows the mark as Go last
  // confirmed it, since earlier writes may have failed too.
  const writes = useRef(
    new Map<
      string,
      {
        latest: number;
        pending: number;
        confirmed: boolean;
        done: Promise<void>;
      }
    >(),
  );
  const writeCount = useRef(0);
  const toggleViewed = useCallback(
    (file: DiffFile) => {
      const blob = file.blob;
      if (!blob) return;
      const key = viewedKey(file.path, blob);
      const mark = !viewed.has(key);
      const apply = (on: boolean) =>
        setViewed((all) => {
          const next = new Set(all);
          if (on) next.add(key);
          else next.delete(key);
          return next;
        });
      apply(mark);
      if (!marksRead.current) earlyMarks.current.set(key, mark);
      // Viewed decides whether the file is open from here.
      setOpened((all) => {
        const next = new Map(all);
        next.delete(key);
        return next;
      });
      setNotice("");
      const id = ++writeCount.current;
      const write = writes.current.get(key) ?? {
        latest: 0,
        pending: 0,
        confirmed: !mark,
        done: Promise.resolve(),
      };
      write.latest = id;
      const send = () =>
        setFileViewed(run.id, file.path, blob, mark)
          .then(
            () => {
              write.confirmed = mark;
            },
            (reason: unknown) => {
              if (write.latest !== id) return;
              apply(write.confirmed);
              if (!marksRead.current)
                earlyMarks.current.set(key, write.confirmed);
              setNotice(String(reason));
            },
          )
          .finally(() => {
            write.pending--;
          });
      // With nothing in flight for this mark, the write starts at once.
      write.done = write.pending > 0 ? write.done.then(send) : send();
      write.pending++;
      writes.current.set(key, write);
    },
    [run.id, viewed],
  );

  const [mode, setMode] = useState<DiffMode>(storedMode);
  const [showTree, setShowTree] = useState(true);
  const [filter, setFilter] = useState("");
  const root = useRef<HTMLDivElement>(null);
  // The diff column's width, as ChangeView measures it.
  const [width, setWidth] = useState<number | null>(null);
  const narrow = width !== null && width < splitWidth;
  const shownMode: DiffMode = narrow ? "unified" : mode;

  // Rows change height between layouts, so switching keeps the reader at
  // the file they were reading: its header where it was, or at the top of
  // the screen when they were partway through it.
  const anchor = useRef<{ path: string; top: number } | null>(null);
  const chooseMode = (next: DiffMode) => {
    for (const section of root.current?.querySelectorAll("section") ?? []) {
      const { top, bottom } = section.getBoundingClientRect();
      if (bottom > 0) {
        anchor.current = { path: section.getAttribute("aria-label")!, top };
        break;
      }
    }
    setMode(next);
    rememberMode(next);
  };
  useLayoutEffect(() => {
    const kept = anchor.current;
    anchor.current = null;
    if (!kept) return;
    const section = [...(root.current?.querySelectorAll("section") ?? [])].find(
      (candidate) => candidate.getAttribute("aria-label") === kept.path,
    );
    if (section)
      window.scrollBy?.(
        0,
        section.getBoundingClientRect().top - Math.max(kept.top, 0),
      );
  }, [shownMode]);

  // A run that has not written still has its checks, so only the Files and
  // Commits views wait for a branch.
  const nothingYet = (
    <div className="min-w-0 wrap-anywhere">
      <Empty title="No changes yet">
        <p>
          Inkuna commits on the task branch after each writable step finishes.
        </p>
      </Empty>
    </div>
  );

  const total = change?.files.length ?? 0;
  const viewedCount =
    change?.files.filter((file) => isViewed(file)).length ?? 0;
  const commit =
    scope.kind === "commit"
      ? commits.find((entry) => entry.hash === scope.commit)
      : undefined;
  const position =
    scope.kind === "commit"
      ? commits.findIndex((entry) => entry.hash === scope.commit)
      : -1;
  const previous = position > 0 ? commits[position - 1] : undefined;
  const next = position >= 0 ? commits[position + 1] : undefined;
  // Choosing a commit, or all changes, shows its files; the reader is
  // taken back up to them when they chose from further down.
  const choose = (hash?: string) => {
    setScope(hash ? { kind: "commit", commit: hash } : { kind: "branch" });
    setSinceRound(null);
    setView("files");
    if ((root.current?.getBoundingClientRect().top ?? 0) < 0)
      root.current?.scrollIntoView?.({ block: "start" });
  };
  // What changed since a round started runs from the commit it built on to
  // the branch's newest commit, as the Commits view last read it.
  const tip = commits.length > 0 ? commits[commits.length - 1].hash : "";
  const sinceRounds = rounds.filter((round) => round.round > 1 && round.since);
  const chooseSince = (round: ShownRound) => {
    setScope({ kind: "range", from: round.since!, to: tip });
    setSinceRound(round.round);
    setView("files");
  };
  const tipIndex = change
    ? commits.findIndex((entry) => entry.hash === change.to)
    : -1;
  // The changes since a round run to the newest commit; when the branch
  // moves on, the reader is told, as for all the changes, and a refresh
  // takes the range to the new tip.
  const sinceScope = scope.kind === "range" && sinceRound !== null;
  const refresh = () => {
    if (
      scope.kind === "range" &&
      sinceRound !== null &&
      tip &&
      tip !== scope.to
    )
      setScope({ ...scope, to: tip });
    setRefreshes((count) => count + 1);
  };
  const moved =
    (scope.kind === "branch" || sinceScope) &&
    change !== null &&
    commits.length > 0 &&
    commits[commits.length - 1].hash !== change.to;
  const newer = tipIndex >= 0 ? commits.length - 1 - tipIndex : 0;
  const toggle = (active: boolean) =>
    `cursor-pointer border-0 px-2.5 py-1 text-xs disabled:cursor-not-allowed disabled:opacity-45 ${
      active
        ? "bg-raised text-ink-strong"
        : "bg-transparent text-ink-soft hover:text-ink-strong"
    }`;

  return (
    <TabBody>
      {/* The browser's own scroll anchoring is off here: the diff keeps the
          screen still itself when a chunk above it is drawn, and WebKit, the
          app's webview, would not do it at all. */}
      <div ref={root} className="grid min-w-0 gap-3 [overflow-anchor:none]">
        <Tabs
          tabs={[
            {
              id: "files",
              label: change ? `Files ${change.files.length}` : "Files",
            },
            {
              id: "commits",
              label: commitsRead ? `Commits ${commits.length}` : "Commits",
            },
            {
              id: "checks",
              label: run.checks
                ? `Checks ${run.checks.passed}/${run.checks.total}`
                : "Checks",
            },
          ]}
          active={view}
          onChange={setView}
          label="Changes"
          idPrefix="changes"
        />
        {moved && (
          <div
            role="status"
            className="flex flex-wrap items-center gap-3 border-l-2 border-accent bg-raised px-3 py-2 text-xs text-ink-soft"
          >
            <span className="min-w-0 flex-1">
              {newer > 0
                ? `The branch has ${plural(newer, "new commit", "new commits")} since these changes loaded.`
                : "The branch has changed since these changes loaded."}
            </span>
            <Button className="px-2.5 py-1 text-xs" onClick={refresh}>
              Refresh
            </Button>
          </div>
        )}
        {notice && <InlineAlert>{notice}</InlineAlert>}
        <TabPanel idPrefix="changes" tab={view}>
          {view === "checks" ? (
            <ChecksView runID={run.id} />
          ) : !started ? (
            nothingYet
          ) : view === "commits" ? (
            <CommitList
              commits={commits}
              loading={!commitsRead && !commitsError}
              error={commitsError}
              onRetry={() => {
                setCommitsError("");
                setCommitRetries((count) => count + 1);
              }}
              selected={scope.kind === "commit" ? scope.commit : undefined}
              onSelect={choose}
            />
          ) : (
            <div className="grid min-w-0 gap-3">
              <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2">
                <button
                  type="button"
                  aria-pressed={showTree}
                  aria-label="File tree"
                  title={showTree ? "Hide the file tree" : "Show the file tree"}
                  className="cursor-pointer rounded border border-line-strong bg-transparent px-2 py-1 text-xs text-ink-soft hover:text-ink-strong"
                  onClick={() => setShowTree((shown) => !shown)}
                >
                  ☰
                </button>
                <h3 className="m-0 min-w-0 flex-1 text-sm font-semibold wrap-anywhere">
                  {change ? (
                    <>
                      {plural(total, "file", "files")} changed,{" "}
                      <span className="font-mono text-ok">
                        +{change.additions}
                      </span>{" "}
                      <span className="font-mono text-danger">
                        −{change.deletions}
                      </span>
                    </>
                  ) : error ? (
                    "Changes"
                  ) : (
                    "Loading changes…"
                  )}
                </h3>
                {!showTree && filter && (
                  <button
                    type="button"
                    title="Clear the filter"
                    className="cursor-pointer rounded border border-accent-line bg-transparent px-2 py-1 text-xs text-accent"
                    onClick={() => setFilter("")}
                  >
                    Filtered by “{filter}” ✕
                  </button>
                )}
                <select
                  aria-label="Changes to show"
                  className={`${inputClass} w-auto max-w-[280px] py-1 text-xs`}
                  value={
                    scope.kind === "range" && sinceRound
                      ? `round:${sinceRound}`
                      : scope.kind === "commit"
                        ? scope.commit
                        : ""
                  }
                  onChange={(event) => {
                    const value = event.target.value;
                    const round = sinceRounds.find(
                      (entry) => `round:${entry.round}` === value,
                    );
                    if (round) chooseSince(round);
                    else choose(value || undefined);
                  }}
                >
                  <option value="">All changes</option>
                  {sinceRounds.map((round) => (
                    <option
                      key={`round:${round.round}`}
                      value={`round:${round.round}`}
                      disabled={!tip}
                    >
                      Changes since round {round.round} started
                    </option>
                  ))}
                  {commits.map((entry) => (
                    <option key={entry.hash} value={entry.hash}>
                      {entry.shortHash} {entry.subject}
                      {entry.stepId
                        ? ` · ${entry.stepId}`
                        : entry.external
                          ? " · external"
                          : ""}
                    </option>
                  ))}
                </select>
                <div
                  role="group"
                  aria-label="Layout"
                  className="flex overflow-hidden rounded border border-line-strong"
                >
                  <button
                    type="button"
                    aria-pressed={shownMode === "unified"}
                    className={toggle(shownMode === "unified")}
                    onClick={() => chooseMode("unified")}
                  >
                    Unified
                  </button>
                  <button
                    type="button"
                    aria-pressed={shownMode === "split"}
                    disabled={narrow}
                    title={
                      narrow
                        ? "Split needs a wider diff; widen the window or hide the file tree"
                        : undefined
                    }
                    className={toggle(shownMode === "split")}
                    onClick={() => chooseMode("split")}
                  >
                    Split
                  </button>
                </div>
                {change && (
                  <span className="text-xs whitespace-nowrap text-muted">
                    {viewedCount} of {total} viewed
                  </span>
                )}
                <Button className="px-2.5 py-1 text-xs" onClick={refresh}>
                  Refresh
                </Button>
              </div>
              {error && <InlineAlert>{error}</InlineAlert>}
              {commit && (
                <>
                  <Summary
                    label={
                      <>
                        {commit.shortHash}
                        {commit.stepId
                          ? ` · step ${commit.stepId}`
                          : commit.external
                            ? " · external"
                            : ""}
                        {commit.agent ? ` · ${commit.agent}` : ""}
                      </>
                    }
                    meta={
                      <>
                        {commit.author} · {formatTime(commit.date)}
                      </>
                    }
                  >
                    {commit.subject}
                  </Summary>
                  <nav
                    aria-label="Commits"
                    className="flex flex-wrap items-center gap-2 text-xs text-muted"
                  >
                    <Button
                      className="px-2.5 py-1 text-xs"
                      disabled={!previous}
                      onClick={() => previous && choose(previous.hash)}
                    >
                      ← Previous
                    </Button>
                    <span>
                      Commit {position + 1} of {commits.length}
                    </span>
                    <Button
                      className="px-2.5 py-1 text-xs"
                      disabled={!next}
                      onClick={() => next && choose(next.hash)}
                    >
                      Next →
                    </Button>
                    <CopyHash hash={commit.hash} />
                  </nav>
                  {commit.body && <Code>{commit.body}</Code>}
                </>
              )}
              {scope.kind === "range" && sinceRound && change && (
                <p className="m-0 text-xs text-muted">
                  What round {sinceRound} and later rounds changed, from{" "}
                  <span className="font-mono">{change.from.slice(0, 7)}</span>{" "}
                  to <span className="font-mono">{change.to.slice(0, 7)}</span>.
                </p>
              )}
              <CommentsContext.Provider value={commentable ? comments : null}>
                <OtherThreads threads={elsewhere} error={comments.error} />
                {change && change.files.length === 0 && (
                  <p className="m-0 text-xs text-muted">
                    No files changed in this scope.
                  </p>
                )}
                {change && change.files.length > 0 && (
                  <ChangeView
                    key={`${change.from}..${change.to}`}
                    runID={run.id}
                    scope={scope}
                    change={change}
                    mode={shownMode}
                    showTree={showTree}
                    filter={filter}
                    onFilter={setFilter}
                    isViewed={isViewed}
                    isOpen={isOpen}
                    onToggleOpen={toggleOpen}
                    onToggleViewed={toggleViewed}
                    onDiffWidth={setWidth}
                  />
                )}
              </CommentsContext.Provider>
            </div>
          )}
        </TabPanel>
      </div>
    </TabBody>
  );
}

import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type {
  DiffFile,
  DiffFilePatch,
  DiffFileStatus,
  DiffLine,
  DiffSide,
} from "../../../types";
import { Button, Chip } from "../../common";
import { InlineAlert } from "../shared";
import {
  canComment,
  currentComposer,
  linesLabel,
  useCommenting,
} from "./commenting";
import { NewComment, ThreadBox } from "./CommentThreads";
import type { DiffMode } from "./mode";
import {
  buildRows,
  expandStep,
  hiddenLines,
  hunkOf,
  rowLines,
  withComments,
  type Gap,
  type LineAt,
  type Row,
} from "./rows";
import { watchNear } from "./viewport";
import { Windowed } from "./Windowed";

/** Where a file's patch is: loading, loaded, or failed with Go's reason. */
export type PatchState =
  | { state: "loading" }
  | { state: "loaded"; patch: DiffFilePatch }
  | { state: "failed"; message: string };

const statusStyles: Record<DiffFileStatus, { letter: string; tone: string }> = {
  added: { letter: "A", tone: "text-ok border-ok-line" },
  modified: { letter: "M", tone: "text-warn border-warn-line" },
  deleted: { letter: "D", tone: "text-danger border-danger-line" },
  renamed: { letter: "R", tone: "text-ink-soft border-line-strong" },
  copied: { letter: "C", tone: "text-ink-soft border-line-strong" },
};

/** A one-letter mark for how a file changed, named for screen readers. */
export function StatusBadge({ status }: { status: DiffFileStatus }) {
  const style = statusStyles[status] ?? statusStyles.modified;
  return (
    <span
      role="img"
      aria-label={status}
      title={status}
      className={`inline-grid size-4 flex-none place-items-center rounded-sm border font-mono text-[9px] leading-none ${style.tone}`}
    >
      {style.letter}
    </span>
  );
}

/** "+12 −3" in the add and delete colours. */
export function LineCounts({ file }: { file: DiffFile }) {
  return (
    <span className="flex-none font-mono text-[11px] whitespace-nowrap">
      <span className="text-ok">+{file.additions}</span>{" "}
      <span className="text-danger">−{file.deletions}</span>
    </span>
  );
}

/** Why a file has no patch to show, or "" when it has one. */
function noPatchReason(file: DiffFile): string {
  if (file.binary) return "Binary file not shown.";
  if (file.additions > 0 || file.deletions > 0) return "";
  if (file.status === "renamed") return "File renamed without changes.";
  if (file.status === "copied") return "File copied without changes.";
  if (file.oldMode && file.newMode && file.oldMode !== file.newMode)
    return `File mode changed from ${file.oldMode} to ${file.newMode}.`;
  if (file.status === "added") return "Empty file added.";
  if (file.status === "deleted") return "Empty file deleted.";
  return "No lines changed.";
}

/**
 * One file of a change: a header that sticks while its diff scrolls, with
 * collapse and Viewed, and the diff, which loads once the file is near the
 * viewport and open.
 */
export function FileDiff({
  file,
  mode,
  open,
  viewed,
  patch,
  revealed,
  total,
  sectionRef,
  onToggleOpen,
  onToggleViewed,
  onLoad,
  onReveal,
}: {
  file: DiffFile;
  mode: DiffMode;
  open: boolean;
  viewed: boolean;
  patch?: PatchState;
  revealed?: ReadonlyMap<number, DiffLine>;
  total?: number;
  sectionRef(element: HTMLElement | null): void;
  onToggleOpen(): void;
  onToggleViewed(): void;
  /** Loads the patch; anyway loads one over the soft caps. */
  onLoad(anyway: boolean): void;
  onReveal(gap: Gap, direction: "up" | "down" | "all"): Promise<void>;
}) {
  const ref = useRef<HTMLElement | null>(null);
  const [near, setNear] = useState(false);
  useEffect(() => watchNear(ref.current!, setNear), []);
  const reason = noPatchReason(file);
  useEffect(() => {
    if (near && open && !patch && !reason) onLoad(false);
  }, [near, open, patch, reason, onLoad]);
  const label = file.oldPath ? `${file.oldPath} → ${file.path}` : file.path;
  return (
    <section
      ref={(element) => {
        ref.current = element;
        sectionRef(element);
      }}
      aria-label={file.path}
      className="min-w-0 border border-line bg-field"
    >
      <div className="z-sticky sticky top-0 flex min-w-0 items-center gap-2 border-b border-line bg-raised px-2 py-1.5">
        <button
          type="button"
          aria-expanded={open}
          aria-label={`${open ? "Collapse" : "Expand"} ${file.path}`}
          className="grid size-5 flex-none cursor-pointer place-items-center border-0 bg-transparent p-0 text-ink-soft hover:text-ink-strong"
          onClick={onToggleOpen}
        >
          <span aria-hidden="true">{open ? "▾" : "▸"}</span>
        </button>
        <StatusBadge status={file.status} />
        <span
          className="min-w-0 flex-1 truncate font-mono text-xs text-ink-strong"
          title={label}
        >
          {label}
        </span>
        {file.generated && <Chip tone="neutral">generated</Chip>}
        <ThreadCount path={file.path} />
        <LineCounts file={file} />
        <label className="flex flex-none cursor-pointer items-center gap-1.5 text-[11px] text-ink-soft">
          <input
            type="checkbox"
            checked={viewed}
            disabled={!file.blob}
            onChange={onToggleViewed}
            className="accent-accent"
          />
          Viewed
        </label>
      </div>
      <FileBody
        file={file}
        mode={mode}
        open={open}
        viewed={viewed}
        patch={patch}
        reason={reason}
        revealed={revealed}
        total={total}
        onToggleOpen={onToggleOpen}
        onLoad={onLoad}
        onReveal={onReveal}
      />
    </section>
  );
}

const emptyLines: ReadonlyMap<number, DiffLine> = new Map();

/** How many comment threads are on a file's lines, for a collapsed file too. */
function ThreadCount({ path }: { path: string }) {
  const commenting = useCommenting();
  const count =
    commenting?.threads.filter(
      (thread) => thread.path === path && !thread.outdated && !thread.unplaced,
    ).length ?? 0;
  if (count === 0) return null;
  return (
    <Chip tone="accent">
      {count} {count === 1 ? "comment" : "comments"}
    </Chip>
  );
}

function FileBody({
  file,
  mode,
  open,
  viewed,
  patch,
  reason,
  revealed = emptyLines,
  total,
  onToggleOpen,
  onLoad,
  onReveal,
}: {
  file: DiffFile;
  mode: DiffMode;
  open: boolean;
  viewed: boolean;
  patch?: PatchState;
  reason: string;
  revealed?: ReadonlyMap<number, DiffLine>;
  total?: number;
  onToggleOpen(): void;
  onLoad(anyway: boolean): void;
  onReveal(gap: Gap, direction: "up" | "down" | "all"): Promise<void>;
}) {
  const loaded = patch?.state === "loaded" ? patch.patch : undefined;
  const rows = useMemo(
    () => (loaded ? buildRows(loaded, mode, revealed, total) : []),
    [loaded, mode, revealed, total],
  );
  // Threads and the form for a new comment sit under the last line they
  // are on; a thread whose lines this diff does not show goes below it.
  const commenting = useCommenting();
  const threads = useMemo(
    () =>
      commenting?.threads.filter(
        (thread) =>
          thread.path === file.path && !thread.outdated && !thread.unplaced,
      ) ?? [],
    [commenting?.threads, file.path],
  );
  const writing = currentComposer(commenting);
  const composer = writing?.path === file.path ? writing : null;
  const composeSide = composer?.side;
  const composeEnd = composer?.endLine;
  const placed = useMemo(
    () =>
      withComments(
        rows,
        threads,
        composeSide && composeEnd
          ? { side: composeSide, endLine: composeEnd }
          : null,
      ),
    [rows, threads, composeSide, composeEnd],
  );
  if (!open) {
    if (!file.generated || viewed) return null;
    return (
      <Note>
        Generated files are collapsed.{" "}
        <button type="button" className={linkButton} onClick={onToggleOpen}>
          Load diff
        </button>
      </Note>
    );
  }
  if (reason) return <Note>{reason}</Note>;
  if (!patch || patch.state === "loading") return <Note>Loading diff…</Note>;
  if (patch.state === "failed")
    return (
      <div className="grid gap-2 p-3">
        <InlineAlert>{patch.message}</InlineAlert>
        <div>
          <Button onClick={() => onLoad(false)}>Retry</Button>
        </div>
      </div>
    );
  if (loaded?.tooLarge)
    return (
      <Note>
        {loaded.reason}{" "}
        {loaded.loadable && (
          <button
            type="button"
            className={linkButton}
            onClick={() => onLoad(true)}
          >
            Load diff
          </button>
        )}
      </Note>
    );
  if (rows.length === 0) return <Note>{noLinesReason(file)}</Note>;
  return (
    <>
      <Rows
        key={mode}
        path={file.path}
        rows={placed.rows}
        mode={mode}
        digits={gutterDigits(loaded!, total)}
        onReveal={onReveal}
      />
      {placed.unplaced.map((thread) => (
        <ThreadBox
          key={`${thread.id}:${thread.state}`}
          thread={thread}
          showPlace
        />
      ))}
    </>
  );
}

function noLinesReason(file: DiffFile) {
  return noPatchReason({ ...file, additions: 0, deletions: 0 });
}

const linkButton =
  "cursor-pointer border-0 bg-transparent p-0 text-xs text-accent hover:underline";

function Note({ children }: { children: React.ReactNode }) {
  return <p className="m-0 px-3 py-2.5 text-xs text-muted">{children}</p>;
}

/** How many digits the widest line number takes, so every row lines up. */
function gutterDigits(patch: DiffFilePatch, total?: number) {
  let widest = total ?? patch.newTotal ?? 0;
  for (const hunk of patch.hunks)
    widest = Math.max(
      widest,
      hunk.oldStart + hunk.oldLines,
      hunk.newStart + hunk.newLines,
    );
  return Math.max(3, String(widest).length);
}

/** Lines picked for a comment: one side, within one hunk. */
interface Picked {
  side: DiffSide;
  start: number;
  end: number;
  hunk: number;
}

/** Whether a line is among those picked, or those a new comment is on. */
function within(picked: Picked | null, at: LineAt) {
  return (
    picked !== null &&
    picked.side === at.side &&
    at.line >= Math.min(picked.start, picked.end) &&
    at.line <= Math.max(picked.start, picked.end)
  );
}

/**
 * Picking lines to comment on: a click on a line's number picks it, a drag
 * over numbers on one side of a hunk picks a range, and a shift-click
 * stretches the open comment to that line. Comments go only on the lines a
 * patch shows, and only while Go says they can be written.
 */
function usePicking(path: string) {
  const commenting = useCommenting();
  const writable = canComment(commenting);
  const [dragging, setDragging] = useState<Picked | null>(null);
  // The mouseup listener reads the drag as last committed.
  const latest = useRef(dragging);
  useLayoutEffect(() => {
    latest.current = dragging;
  }, [dragging]);
  useEffect(() => {
    if (!dragging) return;
    const finish = () => {
      const picked = latest.current;
      setDragging(null);
      // A drag that ends on its first line is a click, which picks it. One
      // that ends elsewhere is never a click on a number: the browser sends
      // that click to an element both lines share.
      if (!picked || picked.start === picked.end || !commenting) return;
      // What was written so far moves to the new lines, whose default
      // stage Go picks again.
      commenting.setComposer((current) => ({
        path,
        side: picked.side,
        line: Math.min(picked.start, picked.end),
        endLine: Math.max(picked.start, picked.end),
        commit: commenting.commit,
        body: current?.body ?? "",
        target: "",
      }));
    };
    document.addEventListener("mouseup", finish);
    return () => document.removeEventListener("mouseup", finish);
  }, [dragging, commenting, path]);
  const writing = currentComposer(commenting);
  const composer = writing?.path === path ? writing : null;
  const shown: Picked | null =
    dragging ??
    (composer
      ? {
          side: composer.side,
          start: composer.line,
          end: composer.endLine,
          hunk: -1,
        }
      : null);
  return {
    writable,
    shown,
    start(at: LineAt, hunk: number) {
      setDragging({ side: at.side, start: at.line, end: at.line, hunk });
    },
    enter(row: Row) {
      const hunk = hunkOf(row);
      if (!dragging || hunk !== dragging.hunk) return;
      const at = rowLines(row).find((line) => line.side === dragging.side);
      if (at) setDragging({ ...dragging, end: at.line });
    },
    click(at: LineAt, stretch: boolean) {
      if (!commenting) return;
      commenting.setComposer((current) => {
        const same =
          current?.path === path &&
          current.side === at.side &&
          current.commit === commenting.commit;
        if (stretch && same)
          return {
            ...current,
            line: Math.min(current.line, at.line),
            endLine: Math.max(current.endLine, at.line),
          };
        return {
          path,
          side: at.side,
          line: at.line,
          endLine: at.line,
          commit: commenting.commit,
          body: current?.body ?? "",
          target: "",
        };
      });
    },
  };
}

type Picking = ReturnType<typeof usePicking>;

function Rows({
  path,
  rows,
  mode,
  digits,
  onReveal,
}: {
  path: string;
  rows: Row[];
  mode: DiffMode;
  digits: number;
  onReveal(gap: Gap, direction: "up" | "down" | "all"): Promise<void>;
}) {
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const picking = usePicking(path);
  const commenting = useCommenting();
  const gutter = `${digits + 2}ch`;
  const columns =
    mode === "split"
      ? `${gutter} 1.5ch minmax(0,1fr) ${gutter} 1.5ch minmax(0,1fr)`
      : `${gutter} ${gutter} 1.5ch minmax(0,1fr)`;
  async function reveal(
    key: string,
    gap: Gap,
    direction: "up" | "down" | "all",
  ) {
    setBusy(key);
    setError("");
    try {
      await onReveal(gap, direction);
    } catch (reason) {
      setError(String(reason));
    } finally {
      setBusy("");
    }
  }
  return (
    <div
      className="inkuna-diff font-mono text-[11px] leading-5"
      style={{ "--diff-columns": columns } as React.CSSProperties}
    >
      {error && (
        <div className="p-2">
          <InlineAlert>{error}</InlineAlert>
        </div>
      )}
      <Windowed
        rows={rows}
        render={(row) => {
          switch (row.kind) {
            case "hunk":
              return <HunkRow header={row.header} />;
            case "expand":
              return (
                <ExpandRow
                  gap={row.gap}
                  busy={busy === row.key}
                  onReveal={(direction) => reveal(row.key, row.gap, direction)}
                />
              );
            case "line":
              return <UnifiedRow row={row} line={row.line} picking={picking} />;
            case "split":
              return (
                <SplitRow
                  row={row}
                  left={row.left}
                  right={row.right}
                  picking={picking}
                />
              );
            case "thread":
              // A thread that is resolved or reopened folds or opens anew.
              return <ThreadBox key={row.thread.state} thread={row.thread} />;
            case "compose": {
              const composer = currentComposer(commenting);
              return composer ? <NewComment composer={composer} /> : null;
            }
          }
        }}
      />
    </div>
  );
}

const rowGrid = "grid min-w-0 [grid-template-columns:var(--diff-columns)]";
const numberCell = "px-1.5 text-right text-faint select-none";
const codeCell = "min-w-0 pr-3 whitespace-pre-wrap wrap-anywhere";

const kindClass = { context: "", added: "line-added", deleted: "line-deleted" };
const marker = { context: " ", added: "+", deleted: "−" };

function HunkRow({ header }: { header: string }) {
  return (
    <div className="border-y border-line bg-panel px-3 text-muted wrap-anywhere">
      {header}
    </div>
  );
}

function ExpandRow({
  gap,
  busy,
  onReveal,
}: {
  gap: Gap;
  busy: boolean;
  onReveal(direction: "up" | "down" | "all"): void;
}) {
  const hidden = hiddenLines(gap);
  const all = hidden !== null && hidden <= expandStep && gap.up && gap.down;
  const button =
    "cursor-pointer border-0 bg-transparent px-1.5 text-ink-soft hover:text-accent disabled:cursor-default disabled:opacity-50";
  return (
    <div className="flex items-center gap-1 bg-panel text-muted">
      {all ? (
        <button
          type="button"
          className={button}
          disabled={busy}
          aria-label={`Expand all ${hidden} hidden lines`}
          onClick={() => onReveal("all")}
        >
          ↕
        </button>
      ) : (
        <>
          {gap.up && (
            <button
              type="button"
              className={button}
              disabled={busy}
              aria-label={`Expand ${Math.min(expandStep, hidden ?? expandStep)} lines up`}
              onClick={() => onReveal("up")}
            >
              ↑
            </button>
          )}
          {gap.down && (
            <button
              type="button"
              className={button}
              disabled={busy}
              aria-label={`Expand ${Math.min(expandStep, hidden ?? expandStep)} lines down`}
              onClick={() => onReveal("down")}
            >
              ↓
            </button>
          )}
        </>
      )}
      <span className="px-1.5">
        {busy
          ? "Loading…"
          : hidden === null
            ? "More lines below"
            : `${hidden} hidden ${hidden === 1 ? "line" : "lines"}`}
      </span>
    </div>
  );
}

function LineText({ line }: { line: DiffLine }) {
  return (
    <>
      {line.spans?.length
        ? line.spans.map((span, index) => (
            <span
              key={index}
              className={
                [span.class, span.changed && "changed"]
                  .filter(Boolean)
                  .join(" ") || undefined
              }
            >
              {span.text}
            </span>
          ))
        : line.text}
      {line.noNewline && (
        <abbr
          className="ml-1 text-danger no-underline"
          title="No newline at end of file"
        >
          ⊘
        </abbr>
      )}
    </>
  );
}

/**
 * A line's number, which picks the line for a comment when it can take one:
 * a click picks it, a drag from it picks a range, and a shift-click
 * stretches the open comment to it.
 */
function LineNumber({
  number,
  at,
  row,
  picking,
  className = "",
}: {
  number?: number;
  at?: LineAt;
  row: Row;
  picking: Picking;
  className?: string;
}) {
  const hunk = hunkOf(row);
  if (!at || hunk === null || !picking.writable)
    return <span className={`${numberCell} ${className}`}>{number ?? ""}</span>;
  const label = `Comment on ${linesLabel(at.side, at.line, at.line)}`;
  return (
    <button
      type="button"
      aria-label={label}
      title={`${label}; drag to pick several`}
      className={`${numberCell} ${className} relative cursor-pointer border-0 bg-transparent font-mono hover:bg-accent/15 hover:text-accent`}
      onMouseDown={(event) => {
        if (event.button !== 0 || event.shiftKey) return;
        event.preventDefault();
        picking.start(at, hunk);
      }}
      onClick={(event) => picking.click(at, event.shiftKey)}
    >
      {number ?? ""}
    </button>
  );
}

// Picked lines take the accent over their add or delete colour, with a bar
// down their left edge.
const pickedClass = "bg-accent/15 shadow-[inset_2px_0_0_var(--color-accent)]";

function UnifiedRow({
  row,
  line,
  picking,
}: {
  row: Row;
  line: DiffLine;
  picking: Picking;
}) {
  const at = rowLines(row)[0];
  const picked = within(picking.shown, at);
  return (
    <div
      className={`${rowGrid} ${kindClass[line.kind]} ${picked ? pickedClass : ""}`}
      onMouseEnter={() => picking.enter(row)}
    >
      <LineNumber
        number={line.old}
        at={line.kind === "deleted" ? at : undefined}
        row={row}
        picking={picking}
      />
      <LineNumber
        number={line.new}
        at={line.kind === "deleted" ? undefined : at}
        row={row}
        picking={picking}
      />
      <span className="text-center text-muted select-none">
        {marker[line.kind]}
      </span>
      <span className={codeCell}>
        <LineText line={line} />
      </span>
    </div>
  );
}

function SplitSide({
  row,
  line,
  side,
  picking,
}: {
  row: Row;
  line?: DiffLine;
  side: "old" | "new";
  picking: Picking;
}) {
  if (!line) return <span className="col-span-3 bg-canvas/60" />;
  // An unchanged line is commented on as the branch has it, on the right.
  const at = rowLines(row).find((candidate) => candidate.side === side);
  const picked = at !== undefined && within(picking.shown, at);
  return (
    <>
      <LineNumber
        number={side === "old" ? line.old : line.new}
        at={at}
        row={row}
        picking={picking}
        className={`${kindClass[line.kind]} ${picked ? pickedClass : ""}`}
      />
      <span
        className={`text-center text-muted select-none ${kindClass[line.kind]} ${picked ? "bg-accent/15" : ""}`}
      >
        {marker[line.kind]}
      </span>
      <span
        className={`${codeCell} ${kindClass[line.kind]} ${picked ? "bg-accent/15" : ""}`}
      >
        <LineText line={line} />
      </span>
    </>
  );
}

function SplitRow({
  row,
  left,
  right,
  picking,
}: {
  row: Row;
  left?: DiffLine;
  right?: DiffLine;
  picking: Picking;
}) {
  return (
    <div className={rowGrid} onMouseEnter={() => picking.enter(row)}>
      <SplitSide row={row} line={left} side="old" picking={picking} />
      <SplitSide row={row} line={right} side="new" picking={picking} />
    </div>
  );
}

import type {
  CommentThread,
  DiffFilePatch,
  DiffHunk,
  DiffLine,
  DiffSide,
} from "../../../types";
import type { DiffMode } from "./mode";

/** How many hidden lines one click on an expander reveals. */
export const expandStep = 20;

/**
 * Unchanged lines a patch left out: between two hunks, above the first, or
 * below the last. Numbers are in the newer version; end is null while the
 * file's length is not known, which leaves the gap below the last hunk open.
 */
export interface Gap {
  start: number;
  end: number | null;
  /** Added to a line's newer number, gives its older one. */
  offset: number;
  /** Above the first hunk a gap only grows up, below the last only down. */
  up: boolean;
  down: boolean;
}

/** One row of a file's diff, in either layout. */
export type Row =
  | { kind: "hunk"; key: string; header: string }
  | { kind: "line"; key: string; line: DiffLine }
  | { kind: "split"; key: string; left?: DiffLine; right?: DiffLine }
  | { kind: "expand"; key: string; gap: Gap }
  | { kind: "thread"; key: string; thread: CommentThread }
  | { kind: "compose"; key: string };

/** A line a comment can be on: its side of the diff and its number there. */
export interface LineAt {
  side: DiffSide;
  line: number;
}

/**
 * The lines a row shows, as comments place them: a removed line on the
 * old side, and any other line on the new side, as the branch has it. A
 * split row of an unchanged line shows it once.
 */
export function rowLines(row: Row): LineAt[] {
  const at = (line: DiffLine): LineAt =>
    line.kind === "deleted"
      ? { side: "old", line: line.old! }
      : { side: "new", line: line.new! };
  if (row.kind === "line") return [at(row.line)];
  if (row.kind !== "split") return [];
  const lines: LineAt[] = [];
  if (row.left?.kind === "deleted") lines.push(at(row.left));
  if (row.right && row.right.kind !== "deleted") lines.push(at(row.right));
  return lines;
}

/** The hunk a row of a patch's own lines is in, or null for other rows. */
export function hunkOf(row: Row): number | null {
  const match = /^h(\d+)-/.exec(row.key);
  return match ? Number(match[1]) : null;
}

/**
 * Puts each thread after the row of its last line, and the form for a new
 * comment after the row of the lines it is on. Threads whose lines no row
 * shows come back unplaced.
 */
export function withComments(
  rows: Row[],
  threads: CommentThread[],
  composer?: { side: DiffSide; endLine: number } | null,
): { rows: Row[]; unplaced: CommentThread[] } {
  if (threads.length === 0 && !composer) return { rows, unplaced: [] };
  const pending = new Set(threads);
  let composing = Boolean(composer);
  const placed: Row[] = [];
  for (const row of rows) {
    placed.push(row);
    for (const { side, line } of rowLines(row)) {
      for (const thread of pending)
        if (thread.side === side && thread.endLine === line) {
          placed.push({ kind: "thread", key: `t-${thread.id}`, thread });
          pending.delete(thread);
        }
      if (composing && composer!.side === side && composer!.endLine === line) {
        placed.push({ kind: "compose", key: "compose" });
        composing = false;
      }
    }
  }
  return { rows: placed, unplaced: [...pending] };
}

/** What an expander asks Go for (RunFileExcerpt). */
export interface Reveal {
  oldStart: number;
  newStart: number;
  count: number;
}

/** How many lines a gap hides, or null when that is not known yet. */
export function hiddenLines(gap: Gap): number | null {
  return gap.end === null ? null : gap.end - gap.start + 1;
}

/**
 * The lines one click reveals: down grows from the hunk above, up from the
 * hunk below, and all fills the gap.
 */
export function revealFor(gap: Gap, direction: "up" | "down" | "all"): Reveal {
  let newStart = gap.start;
  let count = expandStep;
  if (gap.end !== null) {
    const hidden = gap.end - gap.start + 1;
    if (direction === "all") count = hidden;
    else if (direction === "up") {
      newStart = Math.max(gap.start, gap.end - expandStep + 1);
      count = gap.end - newStart + 1;
    } else count = Math.min(expandStep, hidden);
  }
  return { oldStart: newStart + gap.offset, newStart, count };
}

// A hunk that adds or removes every line it covers on one side has a count
// of 0 there, and its start names the line before it.
const firstNew = (hunk: DiffHunk) =>
  hunk.newLines > 0 ? hunk.newStart : hunk.newStart + 1;
const afterNew = (hunk: DiffHunk) =>
  hunk.newLines > 0 ? hunk.newStart + hunk.newLines : hunk.newStart + 1;
const firstOld = (hunk: DiffHunk) =>
  hunk.oldLines > 0 ? hunk.oldStart : hunk.oldStart + 1;
const afterOld = (hunk: DiffHunk) =>
  hunk.oldLines > 0 ? hunk.oldStart + hunk.oldLines : hunk.oldStart + 1;

/**
 * Lays a patch out as rows: each hunk's header and lines, with the context
 * lines revealed so far (by their newer number) and an expander for what is
 * still hidden. A hunk whose gap above is fully revealed joins the one
 * before, so its header goes. Split rows pair a run of deleted lines with
 * the added lines that follow it.
 */
export function buildRows(
  patch: DiffFilePatch,
  mode: DiffMode,
  revealed: ReadonlyMap<number, DiffLine>,
  knownTotal?: number,
): Row[] {
  const rows: Row[] = [];
  const hunks = patch.hunks ?? [];
  if (hunks.length === 0) return rows;
  const { status, binary } = patch.file;
  const expandable = !binary && status !== "added" && status !== "deleted";
  const total = knownTotal ?? (patch.newTotal || undefined);
  const context = (line: DiffLine): Row =>
    mode === "split"
      ? { kind: "split", key: `n${line.new}`, left: line, right: line }
      : { kind: "line", key: `n${line.new}`, line };

  // Lays out one gap; true when all of it is revealed.
  const gap = (
    start: number,
    end: number | null,
    offset: number,
    up: boolean,
    down: boolean,
    key: string,
  ): boolean => {
    if (!expandable || (end !== null && start > end)) return false;
    let top = start;
    while ((end === null || top <= end) && revealed.has(top)) {
      rows.push(context(revealed.get(top)!));
      top++;
    }
    if (end === null) {
      rows.push({
        kind: "expand",
        key,
        gap: { start: top, end: null, offset, up, down },
      });
      return false;
    }
    if (top > end) return true;
    let bottom = end;
    while (bottom >= top && revealed.has(bottom)) bottom--;
    rows.push({
      kind: "expand",
      key,
      gap: { start: top, end: bottom, offset, up, down },
    });
    for (let number = bottom + 1; number <= end; number++)
      rows.push(context(revealed.get(number)!));
    return false;
  };

  const lines = (hunk: DiffHunk, index: number) => {
    if (mode === "unified") {
      hunk.lines.forEach((line, at) =>
        rows.push({ kind: "line", key: `h${index}-${at}`, line }),
      );
      return;
    }
    let deleted: DiffLine[] = [];
    let added: DiffLine[] = [];
    let at = 0;
    const flush = () => {
      for (let pair = 0; pair < Math.max(deleted.length, added.length); pair++)
        rows.push({
          kind: "split",
          key: `h${index}-${at++}`,
          left: deleted[pair],
          right: added[pair],
        });
      deleted = [];
      added = [];
    };
    for (const line of hunk.lines) {
      if (line.kind === "context") {
        flush();
        rows.push({
          kind: "split",
          key: `h${index}-${at++}`,
          left: line,
          right: line,
        });
      } else if (line.kind === "deleted") {
        if (added.length > 0) flush();
        deleted.push(line);
      } else added.push(line);
    }
    flush();
  };

  hunks.forEach((hunk, index) => {
    const start = index === 0 ? 1 : afterNew(hunks[index - 1]);
    const end = firstNew(hunk) - 1;
    const offset = firstOld(hunk) - firstNew(hunk);
    const joined = gap(start, end, offset, true, index > 0, `g${index}`);
    if (!joined)
      rows.push({ kind: "hunk", key: `h${index}`, header: hunk.header });
    lines(hunk, index);
  });
  const last = hunks[hunks.length - 1];
  gap(
    afterNew(last),
    total ?? null,
    afterOld(last) - afterNew(last),
    false,
    true,
    `g${hunks.length}`,
  );
  return rows;
}

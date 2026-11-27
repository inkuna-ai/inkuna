import type { DiffFilePatch, DiffHunk, DiffLine } from "../../../types";
import { buildRows, revealFor, type Gap, type Row } from "./rows";

const context = (old: number, line: number): DiffLine => ({
  kind: "context",
  old,
  new: line,
  text: `line ${line}`,
});

// A 40-line file with line 10 changed and line 30 changed, three lines of
// context around each.
const hunks: DiffHunk[] = [
  {
    header: "@@ -7,7 +7,7 @@",
    oldStart: 7,
    oldLines: 7,
    newStart: 7,
    newLines: 7,
    lines: [
      context(7, 7),
      context(8, 8),
      context(9, 9),
      { kind: "deleted", old: 10, text: "old 10" },
      { kind: "added", new: 10, text: "new 10" },
      context(11, 11),
      context(12, 12),
      context(13, 13),
    ],
  },
  {
    header: "@@ -27,7 +27,8 @@",
    oldStart: 27,
    oldLines: 7,
    newStart: 27,
    newLines: 8,
    lines: [
      context(27, 27),
      context(28, 28),
      context(29, 29),
      { kind: "deleted", old: 30, text: "old 30" },
      { kind: "added", new: 30, text: "new 30" },
      { kind: "added", new: 31, text: "extra" },
      context(31, 32),
      context(32, 33),
      context(33, 34),
    ],
  },
];

const patch = (fields: Partial<DiffFilePatch> = {}): DiffFilePatch => ({
  file: { path: "a.txt", status: "modified", additions: 2, deletions: 2 },
  from: "a",
  to: "b",
  hunks,
  bytes: 0,
  lines: 0,
  newTotal: 41,
  ...fields,
});

const gaps = (rows: Row[]) =>
  rows.flatMap((row) => (row.kind === "expand" ? [row.gap] : []));

describe("diff rows", () => {
  it("puts an expander in every gap, each growing only toward the hunks it lies between", () => {
    const rows = buildRows(patch(), "unified", new Map());
    expect(gaps(rows)).toEqual<Gap[]>([
      { start: 1, end: 6, offset: 0, up: true, down: false },
      { start: 14, end: 26, offset: 0, up: true, down: true },
      { start: 35, end: 41, offset: -1, up: false, down: true },
    ]);
    expect(rows.filter((row) => row.kind === "hunk")).toHaveLength(2);
  });

  it("asks for the lines next to a hunk, numbered in both versions", () => {
    const below: Gap = {
      start: 35,
      end: 41,
      offset: -1,
      up: false,
      down: true,
    };
    expect(revealFor(below, "down")).toEqual({
      oldStart: 34,
      newStart: 35,
      count: 7,
    });
    const between: Gap = {
      start: 14,
      end: 60,
      offset: 0,
      up: true,
      down: true,
    };
    expect(revealFor(between, "up")).toEqual({
      oldStart: 41,
      newStart: 41,
      count: 20,
    });
    expect(revealFor(between, "down")).toEqual({
      oldStart: 14,
      newStart: 14,
      count: 20,
    });
    const open: Gap = {
      start: 35,
      end: null,
      offset: -1,
      up: false,
      down: true,
    };
    expect(revealFor(open, "down")).toEqual({
      oldStart: 34,
      newStart: 35,
      count: 20,
    });
  });

  it("shows revealed lines and joins two hunks once the gap between them is open", () => {
    const revealed = new Map<number, DiffLine>();
    for (let line = 14; line <= 16; line++)
      revealed.set(line, context(line, line));
    let rows = buildRows(patch(), "unified", revealed);
    expect(gaps(rows)[1]).toEqual({
      start: 17,
      end: 26,
      offset: 0,
      up: true,
      down: true,
    });
    for (let line = 17; line <= 26; line++)
      revealed.set(line, context(line, line));
    rows = buildRows(patch(), "unified", revealed);
    expect(gaps(rows)).toHaveLength(2);
    expect(rows.filter((row) => row.kind === "hunk")).toHaveLength(1);
  });

  it("leaves the gap below the last hunk open until the file's length is known", () => {
    expect(
      gaps(buildRows(patch({ newTotal: 0 }), "unified", new Map()))[2],
    ).toEqual({
      start: 35,
      end: null,
      offset: -1,
      up: false,
      down: true,
    });
    expect(
      gaps(buildRows(patch({ newTotal: 0 }), "unified", new Map(), 34)),
    ).toHaveLength(2);
  });

  it("never offers to expand an added file", () => {
    const added = patch({
      file: { path: "a.txt", status: "added", additions: 1, deletions: 0 },
      hunks: [
        {
          header: "@@ -0,0 +1 @@",
          oldStart: 0,
          oldLines: 0,
          newStart: 1,
          newLines: 1,
          lines: [{ kind: "added", new: 1, text: "only" }],
        },
      ],
      newTotal: 1,
    });
    expect(gaps(buildRows(added, "unified", new Map()))).toEqual([]);
  });

  it("pairs a run of deleted lines with the added lines after it in split rows", () => {
    const rows = buildRows(patch(), "split", new Map()).filter(
      (row) => row.kind === "split",
    );
    const second = rows.slice(7);
    expect(
      second.map((row) =>
        row.kind === "split"
          ? [row.left?.text ?? "", row.right?.text ?? ""]
          : [],
      ),
    ).toEqual([
      ["line 27", "line 27"],
      ["line 28", "line 28"],
      ["line 29", "line 29"],
      ["old 30", "new 30"],
      ["", "extra"],
      ["line 32", "line 32"],
      ["line 33", "line 33"],
      ["line 34", "line 34"],
    ]);
  });
});

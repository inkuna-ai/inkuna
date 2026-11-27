import type {
  BranchCommit,
  Check,
  CheckLog,
  DiffChange,
  DiffExcerpt,
  DiffFile,
  DiffFilePatch,
  DiffHunk,
  DiffLine,
  DiffScope,
  DiffSpan,
  FeedbackRequest,
  RunEvent,
  RunReview,
  RunRound,
  StepRun,
  ViewedFile,
} from "../types";

/**
 * Stand-ins for the Go bindings the Changes tab calls, over a made-up run:
 * ordinary edits, a 19,000-line added file and a 400-hunk file for the
 * scrolling check, generated, too-large, binary, renamed, deleted, and
 * mode-only files. Patches are built the way Go builds them, with three
 * lines of context, rough highlighting, and changed words marked.
 */

interface Edit {
  /** The 1-based old line the edit starts at; deletes that many old lines, then inserts. */
  at: number;
  remove?: number;
  insert?: string[];
}

interface Fixture {
  file: DiffFile;
  old: string[];
  updated: string[];
  hunks: DiffHunk[];
  /** Over Go's soft caps: loads only when asked. */
  tooLarge?: boolean;
}

const keywords =
  /\b(package|import|func|return|var|const|if|else|for|range|type|struct|interface|export|function|let|new)\b/;
const token =
  /(\/\/.*$|"(?:[^"\\]|\\.)*"|\b\d+\b|\b[A-Za-z_][A-Za-z0-9_]*\b|\s+|.)/g;

/** A rough stand-in for chroma: keywords, strings, numbers, comments, calls. */
function highlight(text: string, changed = false): DiffSpan[] {
  const spans: DiffSpan[] = [];
  for (const [piece] of text.matchAll(token)) {
    let className: string | undefined;
    if (piece.startsWith("//")) className = "tok-comment";
    else if (piece.startsWith('"')) className = "tok-string";
    else if (/^\d+$/.test(piece)) className = "tok-number";
    else if (keywords.test(piece) && piece.match(keywords)![0] === piece)
      className = "tok-keyword";
    else if (/^[A-Z]/.test(piece)) className = "tok-type";
    spans.push({
      text: piece,
      class: className,
      changed: changed || undefined,
    });
  }
  return spans;
}

/** Marks the words two paired lines do not share, as Go does. */
function markWords(old: string, updated: string): [DiffSpan[], DiffSpan[]] {
  const a = old.split(/(\s+)/);
  const b = updated.split(/(\s+)/);
  let prefix = 0;
  while (prefix < a.length && prefix < b.length && a[prefix] === b[prefix])
    prefix++;
  let suffix = 0;
  while (
    suffix < a.length - prefix &&
    suffix < b.length - prefix &&
    a[a.length - 1 - suffix] === b[b.length - 1 - suffix]
  )
    suffix++;
  const spans = (words: string[]) => [
    ...highlight(words.slice(0, prefix).join("")),
    ...highlight(words.slice(prefix, words.length - suffix).join(""), true),
    ...highlight(words.slice(words.length - suffix).join("")),
  ];
  return [spans(a), spans(b)];
}

/** Applies edits to old and lays the result out as hunks with three lines of context. */
function patchOf(
  old: string[],
  edits: Edit[],
): { updated: string[]; hunks: DiffHunk[] } {
  const ops: DiffLine[] = [];
  const updated: string[] = [];
  const sorted = [...edits].sort((x, y) => x.at - y.at);
  let next = 0;
  for (let line = 1; line <= old.length + 1; line++) {
    while (next < sorted.length && sorted[next].at === line) {
      const edit = sorted[next++];
      const removed = old.slice(line - 1, line - 1 + (edit.remove ?? 0));
      const deleted = removed.map((text, index) => ({
        kind: "deleted" as const,
        old: line + index,
        text,
      }));
      const added = (edit.insert ?? []).map((text) => {
        updated.push(text);
        return { kind: "added" as const, new: updated.length, text };
      });
      deleted.forEach((entry, index) => {
        const pair = added[index];
        if (pair) {
          const [left, right] = markWords(entry.text, pair.text);
          (entry as DiffLine).spans = left;
          (pair as DiffLine).spans = right;
        } else (entry as DiffLine).spans = highlight(entry.text);
      });
      added
        .slice(deleted.length)
        .forEach(
          (entry) => ((entry as DiffLine).spans = highlight(entry.text)),
        );
      ops.push(...deleted, ...added);
      line += edit.remove ?? 0;
    }
    if (line > old.length) break;
    updated.push(old[line - 1]);
    ops.push({
      kind: "context",
      old: line,
      new: updated.length,
      text: old[line - 1],
      spans: highlight(old[line - 1]),
    });
  }
  const hunks: DiffHunk[] = [];
  let index = 0;
  while (index < ops.length) {
    while (index < ops.length && ops[index].kind === "context") index++;
    if (index >= ops.length) break;
    const start = Math.max(0, index - 3);
    let end = index;
    for (;;) {
      while (end < ops.length && ops[end].kind !== "context") end++;
      let run = end;
      while (run < ops.length && ops[run].kind === "context") run++;
      if (run < ops.length && run - end <= 6) end = run;
      else {
        end = Math.min(ops.length, end + 3);
        break;
      }
    }
    const lines = ops.slice(start, end);
    const olds = lines.filter((line) => line.kind !== "added");
    const news = lines.filter((line) => line.kind !== "deleted");
    const oldStart = olds[0]?.old ?? (lines[0].new ?? 1) - 1;
    const newStart = news[0]?.new ?? (lines[0].old ?? 1) - 1;
    hunks.push({
      header: `@@ -${oldStart},${olds.length} +${newStart},${news.length} @@`,
      oldStart,
      oldLines: olds.length,
      newStart,
      newLines: news.length,
      lines,
    });
    index = end;
  }
  return { updated, hunks };
}

const blobs = new Map<string, number>();
function blobFor(path: string, version = 0) {
  const seed = [...`${path}#${version}`].reduce(
    (sum, character) => (sum * 31 + character.charCodeAt(0)) >>> 0,
    7,
  );
  blobs.set(path, seed);
  return seed.toString(16).padStart(8, "0").repeat(5);
}

function fixture(
  path: string,
  old: string[],
  edits: Edit[],
  fields: Partial<DiffFile> = {},
): Fixture {
  const { updated, hunks } = patchOf(old, edits);
  const additions = hunks.reduce(
    (sum, hunk) =>
      sum + hunk.lines.filter((line) => line.kind === "added").length,
    0,
  );
  const deletions = hunks.reduce(
    (sum, hunk) =>
      sum + hunk.lines.filter((line) => line.kind === "deleted").length,
    0,
  );
  return {
    file: {
      path,
      status: "modified",
      oldMode: "100644",
      newMode: "100644",
      additions,
      deletions,
      blob: blobFor(path),
      ...fields,
    },
    old,
    updated,
    hunks,
  };
}

function goFile(name: string, functions: number): string[] {
  const lines = [`package ${name}`, "", 'import "fmt"', ""];
  for (let index = 1; index <= functions; index++)
    lines.push(
      `// Step${index} does step ${index} of the work.`,
      `func Step${index}(count int) (string, error) {`,
      `\tif count < ${index} {`,
      `\t\treturn "", fmt.Errorf("step ${index}: need %d, have %d", ${index}, count)`,
      "\t}",
      `\treturn fmt.Sprintf("done %d", count*${index}), nil`,
      "}",
      "",
    );
  return lines;
}

function added(path: string, lines: string[], fields: Partial<DiffFile> = {}) {
  return fixture(path, [], [{ at: 1, insert: lines }], {
    status: "added",
    oldMode: undefined,
    ...fields,
  });
}

const service = goFile("changes", 40);
const big = Array.from(
  { length: 19_000 },
  (_, index) =>
    `\t{ID: ${index + 1}, Name: "row ${index + 1}", Score: ${(index * 37) % 101}}, // generated row`,
);
const many = goFile("routing", 420);

const fixtures: Fixture[] = [
  fixture("internal/app/changes.go", service, [
    {
      at: 6,
      remove: 1,
      insert: ["func Step1(count int, label string) (string, error) {"],
    },
    { at: 12, insert: ["\t// Count steps from one, as the view does."] },
    {
      at: 150,
      remove: 2,
      insert: [
        '\t\treturn "", fmt.Errorf("step 19: need %d, have %d (retry later)", 19, count)',
        "\t}",
        '\tlog.Printf("step 19 ran with %d", count)',
      ],
    },
    {
      at: 290,
      remove: 1,
      insert: ['\treturn fmt.Sprintf("finished %d", count*37), nil'],
    },
  ]),
  fixture(
    "internal/routing/policy.go",
    many,
    Array.from({ length: 400 }, (_, index) => ({
      at: 10 + index * 8,
      remove: 1,
      insert: [
        `func Step${index + 1}(count int) (string, error) { // reviewed`,
      ],
    })),
  ),
  added("internal/storage/table.go", [
    "package storage",
    "",
    "var rows = []Row{",
    ...big,
    "}",
  ]),
  added("frontend/src/views/tasks/changes/tree.ts", [
    'import type { DiffFile } from "../../../types";',
    "",
    "export function matchesFilter(file: DiffFile, filter: string) {",
    "  const words = filter.toLowerCase().split(/\\s+/);",
    "  return words.every((word) => file.path.includes(word));",
    "}",
  ]),
  fixture(
    "go.sum",
    ["a v1.0.0 h1:abc=", "b v2.0.0 h1:def="],
    [{ at: 3, insert: ["c v3.1.0 h1:ghi=", "c v3.1.0/go.mod h1:jkl="] }],
    { generated: true },
  ),
  {
    ...added(
      "testdata/huge.json",
      Array.from({ length: 25_000 }, (_, index) => `  {"id": ${index}},`),
    ),
    tooLarge: true,
  },
  {
    file: {
      path: "assets/logo.png",
      status: "modified",
      binary: true,
      additions: 0,
      deletions: 0,
      blob: blobFor("assets/logo.png"),
    },
    old: [],
    updated: [],
    hunks: [],
  },
  {
    file: {
      path: "docs/review.md",
      oldPath: "docs/diff-review.md",
      status: "renamed",
      similarity: 100,
      additions: 0,
      deletions: 0,
      blob: blobFor("docs/review.md"),
    },
    old: [],
    updated: [],
    hunks: [],
  },
  {
    ...fixture(
      "scripts/release.sh",
      ["#!/bin/sh", "echo release"],
      [{ at: 1, remove: 2 }],
    ),
    file: {
      path: "scripts/release.sh",
      status: "deleted",
      oldMode: "100755",
      additions: 0,
      deletions: 2,
      blob: blobFor("scripts/release.sh"),
    },
  },
  {
    file: {
      path: "scripts/check.sh",
      status: "modified",
      oldMode: "100644",
      newMode: "100755",
      additions: 0,
      deletions: 0,
      blob: blobFor("scripts/check.sh"),
    },
    old: [],
    updated: [],
    hunks: [],
  },
];

const hash = (seed: string) =>
  [...seed]
    .reduce((sum, character) => (sum * 33 + character.charCodeAt(0)) >>> 0, 5)
    .toString(16)
    .padStart(8, "0")
    .repeat(5);

const base = hash("base");
const commit = (
  seed: string,
  subject: string,
  date: string,
  fields: Partial<BranchCommit> = {},
): BranchCommit => ({
  hash: hash(seed),
  shortHash: hash(seed).slice(0, 7),
  subject,
  author: "Inkuna <inkuna@localhost>",
  date,
  files: 0,
  additions: 0,
  deletions: 0,
  ...fields,
});
const commits: BranchCommit[] = [
  commit(
    "research",
    "inkuna(research): Map the diff model's bindings",
    "2026-10-25T09:05:00Z",
    { stepId: "research", agent: "researcher" },
  ),
  commit(
    "implement",
    "inkuna(implement): Add the Changes tab",
    "2026-10-25T09:40:00Z",
    {
      stepId: "implement",
      agent: "implementer",
      body: "Agent: implementer\nTask: task-58\nRun: run-58",
    },
  ),
  commit(
    "implement again",
    "inkuna(implement): Load patches near the viewport",
    "2026-10-25T09:52:00Z",
    { stepId: "implement", agent: "implementer" },
  ),
  commit("by hand", "Fix a typo by hand", "2026-10-25T10:02:00Z", {
    author: "Reviewer <reviewer@example.com>",
    external: true,
  }),
];
const moved = new URLSearchParams(window.location.search).has("moved");
const listed = moved
  ? [
      ...commits,
      commit(
        "later",
        "inkuna(verify): Run the checks",
        "2026-10-25T10:30:00Z",
        {
          stepId: "verify",
          agent: "verifier",
        },
      ),
    ]
  : commits;
const tip = commits[commits.length - 1].hash;

const later = <T>(value: T, milliseconds = 120): Promise<T> =>
  new Promise((resolve) =>
    window.setTimeout(() => resolve(value), milliseconds),
  );

// Each commit scope shows a few of the files, so switching scope visibly
// changes the list.
const commitFiles: Record<string, string[]> = {
  [commits[0].hash]: [
    "docs/review.md",
    "frontend/src/views/tasks/changes/tree.ts",
  ],
  [commits[1].hash]: ["internal/app/changes.go", "go.sum", "assets/logo.png"],
  [commits[2].hash]: ["internal/routing/policy.go"],
  [commits[3].hash]: ["scripts/check.sh"],
};
// Each commit's counts are those of the files its scope shows.
for (const entry of commits) {
  const list = fixtures.filter((candidate) =>
    commitFiles[entry.hash]?.includes(candidate.file.path),
  );
  entry.files = list.length;
  entry.additions = list.reduce((sum, item) => sum + item.file.additions, 0);
  entry.deletions = list.reduce((sum, item) => sum + item.file.deletions, 0);
}

function scoped(scope: DiffScope) {
  if (scope.kind === "commit") {
    const index = commits.findIndex((entry) => entry.hash === scope.commit);
    return {
      from: index > 0 ? commits[index - 1].hash : base,
      to: scope.commit,
      list: fixtures.filter((entry) =>
        commitFiles[scope.commit]?.includes(entry.file.path),
      ),
    };
  }
  if (scope.kind === "range" && scope.from !== base) {
    // A range from a commit shows the files of the commits after it.
    const after = commits.slice(
      commits.findIndex((entry) => entry.hash === scope.from) + 1,
    );
    return {
      from: scope.from,
      to: scope.to,
      list: fixtures.filter((entry) =>
        after.some((commit) =>
          commitFiles[commit.hash]?.includes(entry.file.path),
        ),
      ),
    };
  }
  const to = scope.kind === "range" ? scope.to : tip;
  return {
    from: scope.kind === "range" ? scope.from : base,
    to,
    list: fixtures,
  };
}

// Two rounds: round 1 researched and implemented; the reviewer sent
// implement back, and round 2 re-ran it and verify, keeping research.
const event = (
  runId: string,
  sequence: number,
  at: string,
  kind: string,
  summary: string,
): RunEvent => ({
  version: 1,
  runId,
  sequence,
  timestamp: `2026-10-25T${at}Z`,
  kind,
  summary,
});
const step = (
  id: string,
  title: string,
  kind: string,
  round: number,
  summary: string,
  agent?: string,
): StepRun => ({ id, title, kind, agent, state: "completed", round, summary });
export const roundSteps: StepRun[] = [
  step(
    "research",
    "Map the bindings",
    "agent",
    1,
    "Found the diff model's bindings",
    "researcher",
  ),
  step(
    "implement",
    "Implement",
    "agent",
    2,
    "Loaded patches near the viewport (round 2)",
    "implementer",
  ),
  step("verify", "Verify", "evaluation", 2, "2 of 4 checks passed"),
];
export const roundEvents: RunEvent[] = [
  event(
    "run-58.r2.implement",
    1,
    "09:45:00",
    "lifecycle",
    "Round 2 resumes the session of implement's last run",
  ),
  event(
    "run-58.r2.implement",
    2,
    "09:52:00",
    "completion",
    "inkuna(implement): Load patches near the viewport (round 2)",
  ),
  event("run-58.r2.verify", 1, "09:55:00", "warning", "task test failed"),
];
const rounds: RunRound[] = [
  {
    round: 1,
    startedAt: "2026-10-25T09:00:00Z",
    finishedAt: "2026-10-25T09:41:00Z",
    steps: [
      roundSteps[0],
      step(
        "implement",
        "Implement",
        "agent",
        1,
        "Added the Changes tab",
        "implementer",
      ),
      step("verify", "Verify", "evaluation", 1, "4 of 4 checks passed"),
    ],
    events: [
      event(
        "run-58-research",
        1,
        "09:01:00",
        "lifecycle",
        "Researcher started",
      ),
      event(
        "run-58-research",
        2,
        "09:05:00",
        "completion",
        "inkuna(research): Map the diff model's bindings",
      ),
      event(
        "run-58-implement",
        1,
        "09:40:00",
        "completion",
        "inkuna(implement): Add the Changes tab",
      ),
    ],
    feedback: [],
  },
  {
    round: 2,
    since: commits[1].hash,
    startedAt: "2026-10-25T09:45:00Z",
    steps: roundSteps,
    events: roundEvents,
    feedback: [
      {
        executionId: "run-58",
        round: 2,
        id: "f1",
        target: "implement",
        from: "human",
        text: "Load patches only when they scroll into view.",
        status: "fixed",
        note: "Patches load within two screens of the viewport.",
        createdAt: "2026-10-25T09:44:00Z",
      },
    ],
  },
];
const review: RunReview = {
  round: 2,
  approve: false,
  targets: [
    { id: "implement", title: "Implement", agent: "implementer" },
    { id: "docs", title: "Write the docs", agent: "writer" },
  ],
  target: "implement",
};

const viewed: ViewedFile[] = [
  { path: "docs/review.md", blob: blobFor("docs/review.md") },
  // Marked on an older version, so the file reads as not viewed.
  {
    path: "internal/app/changes.go",
    blob: blobFor("internal/app/changes.go", 1),
  },
];

// Checks: a passing lint with colours, a failing test with a long log, a
// build that keeps writing while the page is open, and one never run.
const esc = "\x1b[";
const lintLog = [
  `${esc}1mtask: [lint] golangci-lint run ./...${esc}0m`,
  `${esc}32m0 issues.${esc}0m`,
  `${esc}1mtask: [lint] npm run lint --prefix frontend${esc}0m`,
  "> eslint .",
  `${esc}2m(no problems)${esc}0m`,
].join("\n");
const testLog = [
  ...Array.from({ length: 2400 }, (_, index) =>
    index % 40 === 39
      ? `ok  \tgithub.com/inkuna-ai/inkuna/internal/pkg${index / 40}\t0.${index % 9}12s`
      : `=== RUN   TestCase${index}\n--- PASS: TestCase${index} (0.00s)`,
  ),
  "=== RUN   TestPlanStaffsEveryStage",
  '    planner_test.go:88: staffed 3 of 4 stages: "verify" has no agent',
  `${esc}31m--- FAIL: TestPlanStaffsEveryStage (0.01s)${esc}0m`,
  `${esc}31mFAIL${esc}0m\tgithub.com/inkuna-ai/inkuna/internal/planner\t0.214s`,
  `${esc}31mFAIL${esc}0m`,
].join("\n");
const started = Date.now();
const buildLines = () =>
  Array.from(
    { length: Math.min(400, Math.floor((Date.now() - started) / 250)) },
    (_, index) => `[build] compiling package ${index + 1} of 400`,
  ).join("\n") + "\n";
const checkFixtures = (): Check[] => {
  const base = {
    executionId: "run-58",
    stepId: "verify",
    stepRunId: "run-58-verify",
    stepTitle: "Run the checks",
    logBytes: 0,
  };
  const building = Math.floor((Date.now() - started) / 250) < 400;
  return [
    {
      ...base,
      id: "run-58-verify-0",
      position: 0,
      argv: ["task", "lint"],
      status: "passed",
      exitCode: 0,
      startedAt: "2026-10-26T09:00:00Z",
      finishedAt: "2026-10-26T09:00:12Z",
      durationMs: 12_400,
      logDigest: "a".repeat(64),
      logBytes: lintLog.length,
    },
    {
      ...base,
      id: "run-58-verify-1",
      position: 1,
      argv: ["task", "test"],
      status: "failed",
      exitCode: 1,
      startedAt: "2026-10-26T09:00:12Z",
      finishedAt: "2026-10-26T09:01:00Z",
      durationMs: 48_000,
      logDigest: "b".repeat(64),
      logBytes: 3_400_000,
      logCut: true,
    },
    {
      ...base,
      id: "run-58-verify-2",
      position: 2,
      argv: ["task", "build"],
      status: building ? "running" : "passed",
      exitCode: building ? undefined : 0,
      startedAt: new Date(started).toISOString(),
      finishedAt: building
        ? undefined
        : new Date(started + 100_000).toISOString(),
      durationMs: building ? 0 : 100_000,
      logDigest: building ? undefined : "c".repeat(64),
      logBytes: 0,
    },
    {
      ...base,
      id: "run-58-verify-3",
      position: 3,
      argv: ["task", "package"],
      status: building ? "queued" : "skipped",
      durationMs: 0,
      logBytes: 0,
    },
  ];
};

/** The bindings the run detail calls, answered from the fixtures. */
export const mockService = {
  RunRounds: async () => later(rounds),
  RunReview: async () => later(review),
  // The mock never starts a round; it says what Go would have been sent.
  RequestChanges: async (_run: string, request: FeedbackRequest) =>
    Promise.reject(
      new Error(
        `The mock sends nothing: ${request.items.length} item(s) for round ${request.round + 1}.`,
      ),
    ),
  RunChecks: async () => later(checkFixtures()),
  CheckLog: async (checkID: string, from: number): Promise<CheckLog> => {
    const whole = (text: string, fields: Partial<CheckLog> = {}) =>
      later({
        text,
        offset: 0,
        end: text.length,
        reset: true,
        running: false,
        cut: false,
        bytes: text.length,
        ...fields,
      });
    if (checkID.endsWith("-0")) return whole(lintLog);
    if (checkID.endsWith("-1"))
      return whole(testLog, { cut: true, bytes: 3_400_000 });
    // The mock's logs are ASCII, so their lengths are their bytes.
    const text = buildLines();
    const running = checkFixtures()[2].status === "running";
    const fromStart = from < 0 || from > text.length;
    return later(
      {
        text: fromStart ? text : text.slice(from),
        offset: fromStart ? 0 : from,
        end: text.length,
        reset: fromStart,
        running,
        cut: false,
        bytes: text.length,
      },
      60,
    );
  },
  SaveCheckLog: async () =>
    later("/Users/reviewer/Downloads/verify-check-2.log"),
  RunCommits: async () => later(listed),
  RunViewed: async () => later([...viewed]),
  SetFileViewed: async (
    _run: string,
    path: string,
    blob: string,
    on: boolean,
  ) => {
    const index = viewed.findIndex(
      (mark) => mark.path === path && mark.blob === blob,
    );
    if (on && index < 0) viewed.push({ path, blob });
    if (!on && index >= 0) viewed.splice(index, 1);
    return later(undefined, 40);
  },
  RunChanges: async (_run: string, scope: DiffScope): Promise<DiffChange> => {
    const { from, to, list } = scoped(scope);
    return later({
      from,
      to,
      files: list.map((entry) => entry.file),
      additions: list.reduce((sum, entry) => sum + entry.file.additions, 0),
      deletions: list.reduce((sum, entry) => sum + entry.file.deletions, 0),
    });
  },
  RunFilePatch: async (
    _run: string,
    scope: DiffScope,
    path: string,
    anyway: boolean,
  ): Promise<DiffFilePatch> => {
    const { from, to } = scoped(scope);
    const entry = fixtures.find((candidate) => candidate.file.path === path)!;
    const lines = entry.hunks.reduce((sum, hunk) => sum + hunk.lines.length, 0);
    const base = {
      file: entry.file,
      from,
      to,
      bytes: lines * 60,
      lines,
      newTotal: entry.updated.length,
    };
    if (entry.tooLarge && !anyway)
      return later({
        ...base,
        hunks: [],
        tooLarge: true,
        loadable: true,
        reason:
          "This patch is over 1 MB or 20000 lines, so it is not loaded until asked.",
      });
    return later(
      { ...base, hunks: entry.hunks, language: "Go" },
      200 + Math.random() * 300,
    );
  },
  RunFileExcerpt: async (
    _run: string,
    _scope: DiffScope,
    path: string,
    oldStart: number,
    newStart: number,
    count: number,
  ): Promise<DiffExcerpt> => {
    const entry = fixtures.find((candidate) => candidate.file.path === path)!;
    const lines = entry.updated
      .slice(newStart - 1, newStart - 1 + count)
      .map((text, index) => ({
        kind: "context" as const,
        old: oldStart + index,
        new: newStart + index,
        text,
        spans: highlight(text),
      }));
    return later({ lines, newTotal: entry.updated.length });
  },
};

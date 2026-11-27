import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type {
  BranchCommit,
  CommentThread,
  DiffChange,
  DiffFile,
  DiffFilePatch,
  WorkflowRun,
} from "../../../types";
import { ChangesTab } from "./ChangesTab";
import { modeKey } from "./mode";

const bridge = vi.hoisted(() => ({
  copyText: vi.fn(),
  getRunChanges: vi.fn(),
  getRunCommits: vi.fn(),
  getRunFilePatch: vi.fn(),
  getRunFileExcerpt: vi.fn(),
  getRunViewed: vi.fn(),
  setFileViewed: vi.fn(),
  getRunComments: vi.fn(),
  saveComment: vi.fn(),
  deleteComment: vi.fn(),
  resolveThread: vi.fn(),
  getCommentTarget: vi.fn(),
}));
vi.mock("../../../bridge", () => bridge);

const run: WorkflowRun = {
  id: "run-1",
  workflowId: "workflow-1",
  taskId: "task-1",
  state: "completed",
  title: "Review the diff",
  startedAt: "2026-10-25T09:00:00Z",
  branch: "feature/58-files-changed-view",
  events: [],
};

const blob = (digit: string) => digit.repeat(40);
const file = (path: string, fields: Partial<DiffFile> = {}): DiffFile => ({
  path,
  status: "modified",
  additions: 1,
  deletions: 1,
  blob: blob("a"),
  ...fields,
});
const commit = (
  hash: string,
  subject: string,
  stepId?: string,
): BranchCommit => ({
  hash,
  shortHash: hash.slice(0, 7),
  subject,
  author: "Inkuna <inkuna@localhost>",
  date: "2026-10-25T09:05:00Z",
  stepId,
  agent: stepId ? "implementer" : undefined,
  external: !stepId,
  files: 1,
  additions: 2,
  deletions: 1,
});
const tip = "c".repeat(40);
const changeOf = (files: DiffFile[], to = tip): DiffChange => ({
  from: "b".repeat(40),
  to,
  files,
  additions: files.reduce((sum, entry) => sum + entry.additions, 0),
  deletions: files.reduce((sum, entry) => sum + entry.deletions, 0),
});
const patchOf = (
  entry: DiffFile,
  fields: Partial<DiffFilePatch> = {},
): DiffFilePatch => ({
  file: entry,
  from: "b".repeat(40),
  to: tip,
  hunks: [
    {
      header: "@@ -10,3 +10,3 @@ func Example()",
      oldStart: 10,
      oldLines: 3,
      newStart: 10,
      newLines: 3,
      lines: [
        { kind: "context", old: 10, new: 10, text: "before" },
        { kind: "deleted", old: 11, text: `old ${entry.path}` },
        { kind: "added", new: 11, text: `new ${entry.path}` },
        { kind: "context", old: 12, new: 12, text: "after" },
      ],
    },
  ],
  bytes: 100,
  lines: 6,
  newTotal: 12,
  ...fields,
});

function serve(
  files: DiffFile[],
  commits = [commit(tip, "Work", "implement")],
) {
  bridge.getRunChanges.mockResolvedValue(changeOf(files));
  bridge.getRunCommits.mockResolvedValue(commits);
  bridge.getRunFilePatch.mockImplementation(
    async (_run, _scope, path: string) =>
      patchOf(files.find((entry) => entry.path === path)!),
  );
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

beforeEach(() => {
  vi.resetAllMocks();
  window.localStorage.clear();
  bridge.getRunViewed.mockResolvedValue([]);
  bridge.setFileViewed.mockResolvedValue(undefined);
  bridge.copyText.mockResolvedValue(undefined);
  bridge.getRunComments.mockResolvedValue({ threads: [], targets: [] });
});

describe("ChangesTab", () => {
  it("filters the tree and the diff to the files that match", async () => {
    serve([
      file("internal/app/changes.go"),
      file("internal/app/setup.go"),
      file("frontend/src/App.tsx"),
    ]);
    render(<ChangesTab run={run} />);
    expect(await screen.findByText("3 files changed,")).toBeInTheDocument();
    for (const path of [
      "internal/app/changes.go",
      "internal/app/setup.go",
      "frontend/src/App.tsx",
    ])
      expect(await screen.findByText(`old ${path}`)).toBeInTheDocument();
    const tree = screen.getByRole("navigation", { name: "Changed files" });
    expect(
      within(tree).getByRole("button", { name: /setup\.go/ }),
    ).toBeInTheDocument();

    fireEvent.change(
      screen.getByRole("searchbox", { name: "Filter changed files" }),
      {
        target: { value: "app chan" },
      },
    );
    expect(
      within(tree).getByRole("button", { name: /changes\.go/ }),
    ).toBeInTheDocument();
    expect(
      within(tree).queryByRole("button", { name: /setup\.go/ }),
    ).not.toBeInTheDocument();
    expect(
      within(tree).queryByRole("button", { name: /App\.tsx/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("region", { name: "internal/app/changes.go" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("region", { name: "frontend/src/App.tsx" }),
    ).not.toBeInTheDocument();

    fireEvent.change(
      screen.getByRole("searchbox", { name: "Filter changed files" }),
      {
        target: { value: "nothing" },
      },
    );
    expect(within(tree).getByText("No files match.")).toBeInTheDocument();
  });

  it("switches between unified and split and remembers the choice", async () => {
    serve([file("a.go")]);
    const { unmount } = render(<ChangesTab run={run} />);
    const region = await screen.findByRole("region", { name: "a.go" });
    const deleted = await within(region).findByText("old a.go");
    // Unified: the deleted and added lines are rows of their own.
    expect(deleted.closest(".grid")).not.toContainElement(
      within(region).getByText("new a.go"),
    );

    fireEvent.click(screen.getByRole("button", { name: "Split" }));
    expect(screen.getByRole("button", { name: "Split" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(window.localStorage.getItem(modeKey)).toBe("split");
    const paired = within(region).getByText("old a.go").closest(".grid");
    expect(paired).toContainElement(within(region).getByText("new a.go"));

    unmount();
    render(<ChangesTab run={run} />);
    expect(
      await screen.findByRole("button", { name: "Split" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("still opens when the browser refuses storage", async () => {
    serve([file("a.go")]);
    const getItem = vi
      .spyOn(Storage.prototype, "getItem")
      .mockImplementation(() => {
        throw new Error("denied");
      });
    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("denied");
      });
    try {
      render(<ChangesTab run={run} />);
      expect(await screen.findByText("old a.go")).toBeInTheDocument();
      fireEvent.click(screen.getByRole("button", { name: "Split" }));
      expect(screen.getByRole("button", { name: "Split" })).toHaveAttribute(
        "aria-pressed",
        "true",
      );
    } finally {
      getItem.mockRestore();
      setItem.mockRestore();
    }
  });

  it("keeps a file viewed only while its blob is the one that was marked", async () => {
    serve([
      file("same.go", { blob: blob("1") }),
      file("changed.go", { blob: blob("3") }),
    ]);
    bridge.getRunViewed.mockResolvedValue([
      { path: "same.go", blob: blob("1") },
      { path: "changed.go", blob: blob("2") },
    ]);
    render(<ChangesTab run={run} />);
    const same = await screen.findByRole("region", { name: "same.go" });
    const changed = screen.getByRole("region", { name: "changed.go" });
    await waitFor(() =>
      expect(
        within(same).getByRole("checkbox", { name: "Viewed" }),
      ).toBeChecked(),
    );
    expect(screen.getByText("1 of 2 viewed")).toBeInTheDocument();
    // The viewed file collapses; the one that changed since is open again.
    expect(
      within(same).getByRole("button", { name: "Expand same.go" }),
    ).toHaveAttribute("aria-expanded", "false");
    expect(within(same).queryByText("old same.go")).not.toBeInTheDocument();
    expect(
      within(changed).getByRole("checkbox", { name: "Viewed" }),
    ).not.toBeChecked();
    expect(
      await within(changed).findByText("old changed.go"),
    ).toBeInTheDocument();

    fireEvent.click(within(changed).getByRole("checkbox", { name: "Viewed" }));
    expect(bridge.setFileViewed).toHaveBeenCalledWith(
      "run-1",
      "changed.go",
      blob("3"),
      true,
    );
    expect(screen.getByText("2 of 2 viewed")).toBeInTheDocument();
    expect(
      within(changed).queryByText("old changed.go"),
    ).not.toBeInTheDocument();
  });

  it("puts a mark back and says why when Go refuses it", async () => {
    serve([file("a.go")]);
    bridge.setFileViewed.mockRejectedValue(new Error("run run-1 not found"));
    render(<ChangesTab run={run} />);
    const region = await screen.findByRole("region", { name: "a.go" });
    fireEvent.click(within(region).getByRole("checkbox", { name: "Viewed" }));
    expect(await screen.findByText(/run run-1 not found/)).toBeInTheDocument();
    expect(
      within(region).getByRole("checkbox", { name: "Viewed" }),
    ).not.toBeChecked();
  });

  it("loads a too-large file only when asked", async () => {
    const big = file("big.json", { additions: 30000, deletions: 0 });
    serve([big]);
    bridge.getRunFilePatch.mockImplementation(
      async (_run, _scope, _path, anyway: boolean) =>
        anyway
          ? patchOf(big)
          : patchOf(big, {
              hunks: [],
              tooLarge: true,
              loadable: true,
              reason:
                "This patch is over 1 MB or 20000 lines, so it is not loaded until asked.",
            }),
    );
    render(<ChangesTab run={run} />);
    const region = await screen.findByRole("region", { name: "big.json" });
    expect(
      await within(region).findByText(/not loaded until asked/),
    ).toBeInTheDocument();
    expect(bridge.getRunFilePatch).toHaveBeenCalledTimes(1);
    fireEvent.click(within(region).getByRole("button", { name: "Load diff" }));
    expect(await within(region).findByText("new big.json")).toBeInTheDocument();
    expect(bridge.getRunFilePatch).toHaveBeenLastCalledWith(
      "run-1",
      { kind: "range", from: "b".repeat(40), to: tip },
      "big.json",
      true,
    );
  });

  it("keeps a generated file collapsed until its diff is asked for", async () => {
    serve([file("go.sum", { generated: true })]);
    render(<ChangesTab run={run} />);
    const region = await screen.findByRole("region", { name: "go.sum" });
    expect(
      within(region).getByText(/Generated files are collapsed/),
    ).toBeInTheDocument();
    expect(bridge.getRunFilePatch).not.toHaveBeenCalled();
    fireEvent.click(within(region).getByRole("button", { name: "Load diff" }));
    expect(await within(region).findByText("new go.sum")).toBeInTheDocument();
  });

  it("expands hidden context from the file at the change's newer commit", async () => {
    serve([file("a.go")]);
    bridge.getRunFileExcerpt.mockResolvedValue({
      lines: Array.from({ length: 9 }, (_, index) => ({
        kind: "context",
        old: index + 1,
        new: index + 1,
        text: `top ${index + 1}`,
      })),
      newTotal: 12,
    });
    render(<ChangesTab run={run} />);
    const region = await screen.findByRole("region", { name: "a.go" });
    fireEvent.click(
      await within(region).findByRole("button", { name: "Expand 9 lines up" }),
    );
    expect(await within(region).findByText("top 1")).toBeInTheDocument();
    expect(bridge.getRunFileExcerpt).toHaveBeenCalledWith(
      "run-1",
      { kind: "range", from: "b".repeat(40), to: tip },
      "a.go",
      1,
      1,
      9,
    );
    // The gap is open, so its hunk header goes.
    expect(
      within(region).queryByText("@@ -10,3 +10,3 @@ func Example()"),
    ).not.toBeInTheDocument();
  });

  it("moves between files with j and k and marks the current one with v", async () => {
    serve([file("a.go"), file("b.go")]);
    const scrolled: string[] = [];
    const scrollIntoView = vi.fn(function (this: HTMLElement) {
      scrolled.push(this.getAttribute("aria-label") ?? "");
    });
    Element.prototype.scrollIntoView = scrollIntoView;
    try {
      render(<ChangesTab run={run} />);
      await screen.findByText("old b.go");
      await screen.findByText("old a.go");
      fireEvent.keyDown(document.body, { key: "j" });
      expect(scrolled).toEqual(["b.go"]);
      fireEvent.keyDown(document.body, { key: "v" });
      expect(bridge.setFileViewed).toHaveBeenCalledWith(
        "run-1",
        "b.go",
        blob("a"),
        true,
      );
      fireEvent.keyDown(document.body, { key: "k" });
      expect(scrolled).toEqual(["b.go", "a.go"]);
      // Typing in the filter never moves the diff.
      fireEvent.keyDown(
        screen.getByRole("searchbox", { name: "Filter changed files" }),
        { key: "j" },
      );
      expect(scrolled).toEqual(["b.go", "a.go"]);
    } finally {
      delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView;
    }
  });

  it("shows one commit's changes and its message", async () => {
    const first = "1".repeat(40);
    serve(
      [file("a.go")],
      [
        commit(first, "First step", "research"),
        commit(tip, "Second step", "implement"),
      ],
    );
    render(<ChangesTab run={run} />);
    const picker = await screen.findByRole("combobox", {
      name: "Changes to show",
    });
    await screen.findByRole("option", { name: /First step · research/ });
    fireEvent.change(picker, { target: { value: first } });
    await waitFor(() =>
      expect(bridge.getRunChanges).toHaveBeenLastCalledWith("run-1", {
        kind: "commit",
        commit: first,
      }),
    );
    expect(await screen.findByText("First step")).toBeInTheDocument();
    expect(screen.getByText(/1111111 · step research/)).toBeInTheDocument();
    // A commit's patches load by the commit, which still resolves once the
    // branch is deleted; a range would not.
    await waitFor(() =>
      expect(bridge.getRunFilePatch).toHaveBeenLastCalledWith(
        "run-1",
        { kind: "commit", commit: first },
        "a.go",
        false,
      ),
    );
  });

  it("shows what changed since a round started", async () => {
    const first = "1".repeat(40);
    serve(
      [file("a.go")],
      [
        commit(first, "First round", "implement"),
        commit(tip, "Second round", "implement"),
      ],
    );
    const round = (number: number, since?: string) => ({
      round: number,
      since,
      steps: [],
      events: [],
      hiddenEvents: 0,
      feedback: [],
    });
    render(
      <ChangesTab
        run={run}
        rounds={[round(1), round(2, first), round(3, tip)]}
      />,
    );
    const picker = await screen.findByRole("combobox", {
      name: "Changes to show",
    });
    // Round 1 is all the changes; each later round starts from the commit
    // it built on and runs to the branch's newest.
    expect(
      screen.queryByRole("option", { name: "Changes since round 1 started" }),
    ).toBeNull();
    await waitFor(() =>
      expect(
        screen.getByRole("option", { name: "Changes since round 2 started" }),
      ).toBeEnabled(),
    );
    fireEvent.change(picker, { target: { value: "round:2" } });
    await waitFor(() =>
      expect(bridge.getRunChanges).toHaveBeenLastCalledWith("run-1", {
        kind: "range",
        from: first,
        to: tip,
      }),
    );
    expect(picker).toHaveValue("round:2");

    // The branch moves on: the reader is told, and a refresh takes the
    // range to the newest commit.
    const newest = "d".repeat(40);
    bridge.getRunCommits.mockResolvedValue([
      commit(first, "First round", "implement"),
      commit(tip, "Second round", "implement"),
      commit(newest, "Third round", "implement"),
    ]);
    // The changes keep the tip they were read at until a refresh.
    bridge.getRunChanges.mockImplementation(async (_run, scope) =>
      changeOf([file("a.go")], scope.kind === "range" ? scope.to : tip),
    );
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    const notice = await screen.findByRole("status");
    expect(notice).toHaveTextContent(
      "The branch has 1 new commit since these changes loaded.",
    );
    fireEvent.click(within(notice).getByRole("button", { name: "Refresh" }));
    await waitFor(() =>
      expect(bridge.getRunChanges).toHaveBeenLastCalledWith("run-1", {
        kind: "range",
        from: first,
        to: newest,
      }),
    );
    expect(picker).toHaveValue("round:2");
    // Choosing all the changes again leaves the round.
    fireEvent.change(picker, { target: { value: "" } });
    await waitFor(() =>
      expect(bridge.getRunChanges).toHaveBeenLastCalledWith("run-1", {
        kind: "branch",
      }),
    );
  });

  it("says when the branch has moved on instead of moving the diff", async () => {
    const older = "1".repeat(40);
    serve([file("a.go")], [commit(older, "First"), commit(tip, "Second")]);
    bridge.getRunChanges.mockResolvedValueOnce(changeOf([file("a.go")], older));
    render(<ChangesTab run={run} />);
    const status = await screen.findByRole("status");
    expect(status).toHaveTextContent(
      "The branch has 1 new commit since these changes loaded.",
    );
    fireEvent.click(within(status).getByRole("button", { name: "Refresh" }));
    await waitFor(() =>
      expect(screen.queryByRole("status")).not.toBeInTheDocument(),
    );
    expect(bridge.getRunChanges).toHaveBeenCalledTimes(2);
  });

  it("loads files whose names an object would already have", async () => {
    serve([file("constructor"), file("__proto__"), file("toString")]);
    render(<ChangesTab run={run} />);
    for (const path of ["constructor", "__proto__", "toString"])
      expect(await screen.findByText(`old ${path}`)).toBeInTheDocument();
  });

  it("keeps a mark made before the stored marks arrive", async () => {
    serve([file("a.go"), file("b.go")]);
    const marks = deferred<Array<{ path: string; blob: string }>>();
    bridge.getRunViewed.mockReturnValue(marks.promise);
    render(<ChangesTab run={run} />);
    const region = await screen.findByRole("region", { name: "a.go" });
    await within(region).findByText("old a.go");
    fireEvent.click(within(region).getByRole("checkbox", { name: "Viewed" }));
    marks.resolve([{ path: "b.go", blob: blob("a") }]);
    expect(await screen.findByText("2 of 2 viewed")).toBeInTheDocument();
    expect(
      within(region).getByRole("checkbox", { name: "Viewed" }),
    ).toBeChecked();
  });

  it("opens a viewed file again once its content changes", async () => {
    serve([file("a.go", { blob: blob("1") })]);
    render(<ChangesTab run={run} />);
    let region = await screen.findByRole("region", { name: "a.go" });
    await within(region).findByText("old a.go");
    fireEvent.click(within(region).getByRole("checkbox", { name: "Viewed" }));
    expect(within(region).queryByText("old a.go")).not.toBeInTheDocument();

    bridge.getRunChanges.mockResolvedValue(
      changeOf([file("a.go", { blob: blob("2") })]),
    );
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() =>
      expect(screen.getByText("0 of 1 viewed")).toBeInTheDocument(),
    );
    region = screen.getByRole("region", { name: "a.go" });
    expect(
      within(region).getByRole("button", { name: "Collapse a.go" }),
    ).toHaveAttribute("aria-expanded", "true");
    expect(await within(region).findByText("old a.go")).toBeInTheDocument();
  });

  it("shows a filter that is still narrowing the diff once the tree is hidden", async () => {
    serve([file("app.go"), file("lib.go")]);
    render(<ChangesTab run={run} />);
    await screen.findByText("old lib.go");
    fireEvent.change(
      screen.getByRole("searchbox", { name: "Filter changed files" }),
      { target: { value: "app" } },
    );
    fireEvent.click(screen.getByRole("button", { name: "File tree" }));
    expect(
      screen.queryByRole("region", { name: "lib.go" }),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Filtered by “app”/ }));
    expect(
      await screen.findByRole("region", { name: "lib.go" }),
    ).toBeInTheDocument();
  });

  it("falls back to unified when the diff column itself is too narrow", async () => {
    serve([file("a.go")]);
    window.localStorage.setItem(modeKey, "split");
    const original = globalThis.ResizeObserver;
    globalThis.ResizeObserver = class {
      constructor(private readonly report: ResizeObserverCallback) {}
      observe() {
        this.report(
          [{ contentRect: { width: 400 } } as ResizeObserverEntry],
          this as unknown as ResizeObserver,
        );
      }
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;
    try {
      render(<ChangesTab run={run} />);
      await screen.findByText("old a.go");
      expect(screen.getByRole("button", { name: "Split" })).toBeDisabled();
      expect(screen.getByRole("button", { name: "Unified" })).toHaveAttribute(
        "aria-pressed",
        "true",
      );
    } finally {
      globalThis.ResizeObserver = original;
    }
  });

  it("stops reading patches once the view is gone", async () => {
    const files = ["a", "b", "c", "d", "e", "f"].map((name) =>
      file(`${name}.go`),
    );
    serve(files);
    const pending: Array<ReturnType<typeof deferred<DiffFilePatch>>> = [];
    bridge.getRunFilePatch.mockImplementation(() => {
      const next = deferred<DiffFilePatch>();
      pending.push(next);
      return next.promise;
    });
    const { unmount } = render(<ChangesTab run={run} />);
    await waitFor(() =>
      expect(bridge.getRunFilePatch).toHaveBeenCalledTimes(4),
    );
    unmount();
    pending.forEach((entry, index) => entry.resolve(patchOf(files[index])));
    await new Promise((settle) => setTimeout(settle, 0));
    expect(bridge.getRunFilePatch).toHaveBeenCalledTimes(4);
  });

  it("writes one mark's changes in order and lets only the latest failure undo it", async () => {
    serve([file("a.go")]);
    const writes: Array<ReturnType<typeof deferred<void>>> = [];
    bridge.setFileViewed.mockImplementation(() => {
      const next = deferred<void>();
      writes.push(next);
      return next.promise;
    });
    render(<ChangesTab run={run} />);
    const region = await screen.findByRole("region", { name: "a.go" });
    await within(region).findByText("old a.go");
    const box = within(region).getByRole("checkbox", { name: "Viewed" });
    fireEvent.click(box);
    fireEvent.click(box);
    expect(bridge.setFileViewed).toHaveBeenCalledTimes(1);
    writes[0].reject(new Error("first write failed"));
    await waitFor(() => expect(bridge.setFileViewed).toHaveBeenCalledTimes(2));
    expect(bridge.setFileViewed).toHaveBeenLastCalledWith(
      "run-1",
      "a.go",
      blob("a"),
      false,
    );
    expect(box).not.toBeChecked();
    expect(screen.queryByText(/first write failed/)).not.toBeInTheDocument();
    writes[1].reject(new Error("second write failed"));
    expect(await screen.findByText(/second write failed/)).toBeInTheDocument();
    // Neither write reached Go, so the file is shown as Go has it: not viewed.
    expect(box).not.toBeChecked();
  });

  it("puts back the mark Go last confirmed when the latest write fails", async () => {
    serve([file("a.go")]);
    const writes: Array<ReturnType<typeof deferred<void>>> = [];
    bridge.setFileViewed.mockImplementation(() => {
      const next = deferred<void>();
      writes.push(next);
      return next.promise;
    });
    render(<ChangesTab run={run} />);
    const region = await screen.findByRole("region", { name: "a.go" });
    await within(region).findByText("old a.go");
    const box = within(region).getByRole("checkbox", { name: "Viewed" });
    fireEvent.click(box);
    fireEvent.click(box);
    writes[0].resolve();
    await waitFor(() => expect(bridge.setFileViewed).toHaveBeenCalledTimes(2));
    writes[1].reject(new Error("second write failed"));
    expect(await screen.findByText(/second write failed/)).toBeInTheDocument();
    expect(box).toBeChecked();
  });

  it("lists the branch's commits by step and opens the one chosen", async () => {
    const first = "1".repeat(40);
    const byHand = "2".repeat(40);
    serve(
      [file("a.go")],
      [
        commit(first, "Research", "research"),
        commit(byHand, "Fix a typo by hand"),
        commit(tip, "Implement", "implement"),
      ],
    );
    render(<ChangesTab run={run} />);
    fireEvent.click(await screen.findByRole("tab", { name: "Commits 3" }));
    const research = screen.getByRole("region", { name: "Step research" });
    expect(within(research).getByText(/· implementer/)).toBeInTheDocument();
    expect(within(research).getByText("1 file")).toBeInTheDocument();
    const external = screen.getByRole("region", { name: "External commits" });
    expect(
      within(external).getByText("Fix a typo by hand"),
    ).toBeInTheDocument();

    fireEvent.click(
      within(external).getByRole("button", { name: /Fix a typo by hand/ }),
    );
    await waitFor(() =>
      expect(bridge.getRunChanges).toHaveBeenLastCalledWith("run-1", {
        kind: "commit",
        commit: byHand,
      }),
    );
    expect(screen.getByRole("tab", { name: /Files/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(await screen.findByText(/2222222 · external/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Commits 3" }));
    expect(
      screen.getByRole("button", { name: /Fix a typo by hand/ }),
    ).toHaveAttribute("aria-current", "true");
    fireEvent.click(screen.getByRole("button", { name: /All changes/ }));
    await waitFor(() =>
      expect(bridge.getRunChanges).toHaveBeenLastCalledWith("run-1", {
        kind: "branch",
      }),
    );
  });

  it("steps to the previous and next commit", async () => {
    const first = "1".repeat(40);
    const second = "2".repeat(40);
    serve(
      [file("a.go")],
      [
        commit(first, "First", "research"),
        commit(second, "Second", "implement"),
        commit(tip, "Third", "implement"),
      ],
    );
    render(<ChangesTab run={run} />);
    const picker = await screen.findByRole("combobox", {
      name: "Changes to show",
    });
    await screen.findByRole("option", { name: /First · research/ });
    fireEvent.change(picker, { target: { value: first } });
    const nav = await screen.findByRole("navigation", { name: "Commits" });
    expect(within(nav).getByText("Commit 1 of 3")).toBeInTheDocument();
    expect(
      within(nav).getByRole("button", { name: "← Previous" }),
    ).toBeDisabled();
    fireEvent.click(within(nav).getByRole("button", { name: "Next →" }));
    await waitFor(() =>
      expect(bridge.getRunChanges).toHaveBeenLastCalledWith("run-1", {
        kind: "commit",
        commit: second,
      }),
    );
    expect(await screen.findByText("Commit 2 of 3")).toBeInTheDocument();
  });

  it("tells commits still loading from a branch without any", async () => {
    serve([file("a.go")]);
    const list = deferred<BranchCommit[]>();
    bridge.getRunCommits.mockReturnValue(list.promise);
    render(<ChangesTab run={run} />);
    fireEvent.click(await screen.findByRole("tab", { name: "Commits" }));
    expect(screen.getByText("Loading commits…")).toBeInTheDocument();
    expect(
      screen.queryByText("No commits on the branch yet."),
    ).not.toBeInTheDocument();
    list.resolve([]);
    expect(
      await screen.findByText("No commits on the branch yet."),
    ).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Commits 0" })).toBeInTheDocument();
  });

  it("says why the commits did not load and tries again", async () => {
    serve([file("a.go")]);
    bridge.getRunCommits
      .mockRejectedValueOnce(new Error("git log failed"))
      .mockResolvedValueOnce([commit(tip, "Work", "implement")]);
    render(<ChangesTab run={run} />);
    fireEvent.click(await screen.findByRole("tab", { name: "Commits" }));
    expect(await screen.findByText(/git log failed/)).toBeInTheDocument();
    expect(
      screen.queryByText("No commits on the branch yet."),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(
      await screen.findByRole("button", { name: /Work/ }),
    ).toBeInTheDocument();
    expect(screen.queryByText(/git log failed/)).not.toBeInTheDocument();
    expect(bridge.getRunCommits).toHaveBeenCalledTimes(2);
  });

  it("copies a commit's full hash", async () => {
    serve([file("a.go")], [commit(tip, "Work", "implement")]);
    render(<ChangesTab run={run} />);
    fireEvent.click(await screen.findByRole("tab", { name: "Commits 1" }));
    fireEvent.click(screen.getByRole("button", { name: `Copy ${tip}` }));
    expect(bridge.copyText).toHaveBeenCalledWith(tip);
    expect(await screen.findByText("Copied")).toBeInTheDocument();
  });

  it("explains a run that has not written anything yet", () => {
    render(<ChangesTab run={{ ...run, branch: undefined }} />);
    expect(screen.getByText("No changes yet")).toBeInTheDocument();
    expect(bridge.getRunChanges).not.toHaveBeenCalled();
  });
});

const targets = [{ id: "implement", title: "Implement", agent: "implementer" }];
const thread = (
  id: string,
  fields: Partial<CommentThread> = {},
): CommentThread => ({
  id,
  path: "a.go",
  side: "new",
  line: 11,
  endLine: 11,
  round: 1,
  state: "open",
  comments: [
    {
      id: `${id}-comment`,
      threadId: id,
      body: `Comment ${id}`,
      target: "implement",
      state: "sent",
      round: 2,
      item: "c1",
      createdAt: "2026-10-27T09:00:00Z",
      updatedAt: "2026-10-27T09:00:00Z",
    },
  ],
  ...fields,
});

describe("ChangesTab comments", () => {
  it("shows a thread under its line with the target's reply, and replies or resolves it", async () => {
    serve([file("a.go")]);
    const declined = thread("thread-1");
    declined.comments[0] = {
      ...declined.comments[0],
      status: "declined",
      note: "Callers rely on it.",
    };
    bridge.getRunComments.mockResolvedValue({
      threads: [declined],
      targets,
      to: tip,
    });
    bridge.saveComment.mockResolvedValue(undefined);
    bridge.resolveThread.mockResolvedValue(undefined);
    render(<ChangesTab run={run} />);
    const box = await screen.findByRole("region", {
      name: "Comments on a.go line 11",
    });
    expect(within(box).getByText("Comment thread-1")).toBeInTheDocument();
    expect(within(box).getByText("declined")).toBeInTheDocument();
    expect(box).toHaveTextContent("implement declined: Callers rely on it.");
    // The thread sits right after its line.
    expect(
      screen.getByText("new a.go").closest("div")!.nextElementSibling,
    ).toBe(box);
    expect(screen.getByText("1 comment")).toBeInTheDocument();

    fireEvent.click(within(box).getByRole("button", { name: "Reply" }));
    fireEvent.change(within(box).getByRole("textbox", { name: "Comment" }), {
      target: { value: "Nothing calls it now." },
    });
    expect(
      within(box).getByRole("combobox", { name: "Stage for the comment" }),
    ).toHaveValue("implement");
    fireEvent.click(within(box).getByRole("button", { name: "Add reply" }));
    await waitFor(() =>
      expect(bridge.saveComment).toHaveBeenCalledWith("run-1", {
        threadId: "thread-1",
        body: "Nothing calls it now.",
        target: "implement",
      }),
    );
    fireEvent.click(within(box).getByRole("button", { name: "Resolve" }));
    await waitFor(() =>
      expect(bridge.resolveThread).toHaveBeenCalledWith(
        "run-1",
        "thread-1",
        true,
      ),
    );
    // Each change reads the threads again.
    await waitFor(() => expect(bridge.getRunComments).toHaveBeenCalledTimes(3));
    expect(bridge.getRunComments).toHaveBeenCalledWith("run-1", tip);
  });

  it("lists outdated threads and threads on other files with the diff they were sent with", async () => {
    serve([file("a.go")]);
    const outdated = thread("thread-1", {
      outdated: true,
      outdatedRound: 2,
      state: "resolved",
      resolvedBy: "agent",
    });
    outdated.comments[0] = {
      ...outdated.comments[0],
      status: "fixed",
      excerpt: "> 11   + return name",
    };
    bridge.getRunComments.mockResolvedValue({
      threads: [outdated, thread("thread-2", { path: "gone.go" })],
      targets,
      to: tip,
    });
    render(<ChangesTab run={run} />);
    const list = await screen.findByRole("button", {
      name: "▸ 2 comments not on these lines (1 outdated)",
    });
    expect(
      screen.queryByRole("region", { name: "Comments on a.go line 11" }),
    ).not.toBeInTheDocument();
    fireEvent.click(list);
    const box = screen.getByRole("region", {
      name: "Comments on a.go line 11",
    });
    expect(within(box).getByText("outdated after round 2")).toBeInTheDocument();
    expect(within(box).getByText("resolved by implement")).toBeInTheDocument();
    // A resolved thread folds until it is opened.
    fireEvent.click(
      within(box).getByRole("button", { name: "Show 1 comment" }),
    );
    expect(within(box).getByText(/^> 11 +\+ return name$/)).toBeInTheDocument();
    expect(within(box).queryByRole("button", { name: "Reply" })).toBeNull();
    expect(
      screen.getByRole("region", { name: "Comments on gone.go line 11" }),
    ).toBeInTheDocument();
  });

  it("drags over line numbers to comment on a range, with the stage that wrote them", async () => {
    serve([file("a.go")]);
    bridge.getRunComments.mockResolvedValue({ threads: [], targets, to: tip });
    bridge.getCommentTarget.mockResolvedValue("implement");
    bridge.saveComment.mockResolvedValue(undefined);
    render(<ChangesTab run={run} />);
    const first = await screen.findByRole("button", {
      name: "Comment on line 10",
    });
    // Removed lines are on the other side, so the drag passes over them.
    fireEvent.mouseDown(first, { button: 0 });
    fireEvent.mouseEnter(
      screen.getByRole("button", { name: "Comment on removed line 11" })
        .parentElement!,
    );
    fireEvent.mouseEnter(
      screen.getByRole("button", { name: "Comment on line 12" }).parentElement!,
    );
    fireEvent.mouseUp(document);
    expect(
      await screen.findByText(/Comment on lines 10–12\./),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(
        screen.getByRole("combobox", { name: "Stage for the comment" }),
      ).toHaveValue("implement"),
    );
    expect(bridge.getCommentTarget).toHaveBeenCalledWith(
      "run-1",
      "a.go",
      "new",
      12,
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Comment" }), {
      target: { value: "Name these better." },
    });
    fireEvent.click(screen.getByRole("button", { name: "Add comment" }));
    await waitFor(() =>
      expect(bridge.saveComment).toHaveBeenCalledWith("run-1", {
        path: "a.go",
        side: "new",
        line: 10,
        endLine: 12,
        commit: tip,
        body: "Name these better.",
        target: "implement",
      }),
    );
    await waitFor(() =>
      expect(screen.queryByText(/Comment on lines 10–12/)).toBeNull(),
    );
  });

  it("keeps a comment begun before the branch moved off the new lines until they are picked again", async () => {
    serve([file("a.go")]);
    bridge.getRunComments.mockResolvedValue({ threads: [], targets, to: tip });
    bridge.getCommentTarget.mockResolvedValue("implement");
    bridge.saveComment.mockResolvedValue(undefined);
    const first = render(<ChangesTab run={{ ...run, id: "run-moved" }} />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Comment on line 10" }),
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Comment" }), {
      target: { value: "Rename this." },
    });
    first.unmount();

    // A round moved the branch while the tab was closed.
    const moved = "e".repeat(40);
    bridge.getRunChanges.mockResolvedValue(changeOf([file("a.go")], moved));
    bridge.getRunComments.mockResolvedValue({
      threads: [],
      targets,
      to: moved,
    });
    render(<ChangesTab run={{ ...run, id: "run-moved" }} />);
    expect(
      await screen.findByText(
        /You were writing a comment on a\.go line 10 before the branch moved/,
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("Rename this.")).toBeInTheDocument();
    expect(
      screen.queryByRole("form", { name: "New comment" }),
    ).not.toBeInTheDocument();
    // Picking lines again carries the text over, against the new commit.
    fireEvent.click(
      await screen.findByRole("button", { name: "Comment on line 12" }),
    );
    expect(screen.getByRole("textbox", { name: "Comment" })).toHaveValue(
      "Rename this.",
    );
    expect(screen.queryByText(/before the branch moved/)).toBeNull();
    await waitFor(() =>
      expect(
        screen.getByRole("combobox", { name: "Stage for the comment" }),
      ).toHaveValue("implement"),
    );
    fireEvent.click(screen.getByRole("button", { name: "Add comment" }));
    await waitFor(() =>
      expect(bridge.saveComment).toHaveBeenCalledWith("run-moved", {
        path: "a.go",
        side: "new",
        line: 12,
        endLine: 12,
        commit: moved,
        body: "Rename this.",
        target: "implement",
      }),
    );
  });

  it("takes no comments while the run cannot be sent back, and says why", async () => {
    serve([file("a.go")]);
    bridge.getRunComments.mockResolvedValue({
      threads: [],
      targets: [],
      refusal: "this run is still running",
      to: tip,
    });
    render(<ChangesTab run={{ ...run, state: "running" }} />);
    expect(
      await screen.findByText(
        "Comments are written while the run can be sent back for changes: this run is still running",
      ),
    ).toBeInTheDocument();
    expect(await screen.findByText("new a.go")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /^Comment on/ }),
    ).not.toBeInTheDocument();
  });

  it("shows no comments on a single commit's changes", async () => {
    const hash = "d".repeat(40);
    serve([file("a.go")], [commit(hash, "First", "implement")]);
    bridge.getRunComments.mockResolvedValue({
      threads: [thread("thread-1")],
      targets,
      to: tip,
    });
    render(<ChangesTab run={run} />);
    expect(
      await screen.findByRole("region", { name: "Comments on a.go line 11" }),
    ).toBeInTheDocument();
    fireEvent.change(
      await screen.findByRole("combobox", { name: "Changes to show" }),
      { target: { value: hash } },
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("region", { name: "Comments on a.go line 11" }),
      ).not.toBeInTheDocument(),
    );
    expect(
      screen.queryByRole("button", { name: /^Comment on/ }),
    ).not.toBeInTheDocument();
  });
});

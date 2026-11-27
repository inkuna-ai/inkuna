import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type {
  DeleteReport,
  Project,
  TaskDeletePreview,
  WorkflowRun,
} from "../types";
import { TasksView } from "./TasksView";

const listWorkflowRuns = vi.hoisted(() => vi.fn());
const getWorkflowRun = vi.hoisted(() => vi.fn());
const previewDeleteTasks = vi.hoisted(() => vi.fn());
const previewDeleteRun = vi.hoisted(() => vi.fn());
const deleteTasks = vi.hoisted(() => vi.fn());
const deleteRun = vi.hoisted(() => vi.fn());
const checkPullRequests = vi.hoisted(() => vi.fn());
vi.mock("../bridge", () => ({
  checkPullRequests,
  listWorkflowRuns,
  getWorkflowRun,
  previewDeleteTasks,
  previewDeleteRun,
  deleteTasks,
  deleteRun,
  cancelWorkflowRun: vi.fn(),
  removeRunWorktree: vi.fn(),
}));

const projects: Project[] = [
  {
    id: "inkuna",
    name: "Inkuna",
    repository: "/src/inkuna",
    defaultRuntime: "claude",
    createdAt: "2026-09-01T00:00:00Z",
  },
  {
    id: "docs",
    name: "Docs site",
    repository: "/src/docs",
    defaultRuntime: "codex",
    createdAt: "2026-09-01T00:00:00Z",
  },
];

// Relative to the clock, so the age filter sees them as recent whenever the
// test runs.
const hoursAgo = (hours: number) =>
  new Date(Date.now() - hours * 60 * 60 * 1000).toISOString();

const run = (fields: Partial<WorkflowRun>): WorkflowRun => ({
  id: "run",
  workflowId: "workflow",
  taskId: "task",
  projectId: "inkuna",
  state: "completed",
  title: "Run",
  startedAt: hoursAgo(3),
  finishedAt: hoursAgo(2),
  events: [],
  ...fields,
});

const refusal =
  "task task-live cannot be deleted: run run-live is running; delete it once the run has ended";
const runs = [
  run({
    id: "run-live",
    taskId: "task-live",
    state: "running",
    title: "Fix the logout crash",
    finishedAt: undefined,
    deleteRefused: refusal,
  }),
  run({
    id: "run-done",
    taskId: "task-done",
    title: "Fix the login crash",
    pullRequest: {
      number: 12,
      title: "Fix the login crash",
      url: "https://github.com/acme/app/pull/12",
      state: "MERGED",
      mergedAt: hoursAgo(1),
      checkedAt: hoursAgo(1),
    },
  }),
  run({
    id: "run-failed",
    taskId: "task-failed",
    state: "failed",
    title: "Fix the signup crash",
    pullRequest: {
      number: 13,
      title: "Fix the signup crash",
      url: "https://github.com/acme/app/pull/13",
      state: "OPEN",
      checkedAt: hoursAgo(1),
    },
  }),
  run({
    id: "run-docs",
    taskId: "task-docs",
    projectId: "docs",
    state: "cancelled",
    title: "Rewrite the docs index",
  }),
];

const preview = (taskId: string, title: string): TaskDeletePreview => ({
  taskId,
  projectId: "inkuna",
  title,
  runs: [
    {
      runId: `run-${taskId}`,
      title,
      state: "completed",
      worktree: {
        path: `/src/inkuna-worktrees/${taskId}`,
        bytes: 3 * 1024 * 1024,
        removable: true,
      },
      branch: {
        projectId: "inkuna",
        name: `bug/${taskId}`,
        commit: "abc123def4567890",
        unique: 2,
        deletable: true,
      },
    },
  ],
});

const report: DeleteReport = {
  tasks: [
    {
      taskId: "task-done",
      rows: [
        { table: "events", rows: 40 },
        { table: "tasks", rows: 1 },
      ],
      blobs: 6,
      blobBytes: 2048,
      keptBlobs: 1,
      worktrees: [
        {
          runId: "run-done",
          path: "/src/inkuna-worktrees/task-done",
          removed: false,
          reason: "Not asked to remove it.",
        },
      ],
      branches: [
        {
          runId: "run-done",
          name: "bug/task-done",
          commit: "abc123def4567890",
          unique: 2,
          deleted: false,
          reason: "Not asked to delete it.",
        },
      ],
      links: [{ tracker: "github", id: "42" }],
    },
    {
      taskId: "task-failed",
      rows: [{ table: "tasks", rows: 1 }],
      blobs: 0,
      blobBytes: 0,
      keptBlobs: 0,
      worktrees: [],
      branches: [],
      links: [],
    },
  ],
};

function renderTasks() {
  return render(
    <TasksView
      selectedRunID={null}
      onSelectRun={vi.fn()}
      projects={projects}
    />,
  );
}

const listed = () =>
  within(screen.getByRole("group", { name: "Task runs" }))
    .getAllByRole("button")
    .map((button) => button.querySelector("b")?.textContent);

const nothingChecked = { checked: 0, linked: 0, removed: [], problems: [] };

beforeEach(() => {
  vi.clearAllMocks();
  listWorkflowRuns.mockResolvedValue(runs);
  checkPullRequests.mockResolvedValue(nothingChecked);
});

const row = (title: string) =>
  within(screen.getByRole("group", { name: "Task runs" }))
    .getByText(title)
    .closest("button") as HTMLElement;

describe("TasksView pull requests", () => {
  it("checks them when it opens and when asked, and shows each task's", async () => {
    renderTasks();
    await waitFor(() => expect(listed()).toHaveLength(4));
    expect(checkPullRequests).toHaveBeenCalledWith(false);
    expect(row("Fix the login crash")).toHaveTextContent("PR #12 merged");
    expect(row("Fix the signup crash")).toHaveTextContent("PR #13 open");
    expect(row("Fix the logout crash")).not.toHaveTextContent("PR #");

    checkPullRequests.mockResolvedValueOnce({
      checked: 1,
      linked: 2,
      removed: ["/src/inkuna-worktrees/bug/12-login"],
      problems: [
        "Kept /src/inkuna-worktrees/bug/13-signup after pull request #13 merged: it has uncommitted changes",
      ],
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Check pull requests" }),
    );
    expect(checkPullRequests).toHaveBeenLastCalledWith(true);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "after pull request #13 merged: it has uncommitted changes",
    );
    expect(
      screen.getByText(
        "Removed 1 worktree whose pull request merged; the branches are kept.",
      ),
    ).toBeInTheDocument();
  });

  it("filters to merged tasks, which Select all finished then takes", async () => {
    renderTasks();
    await waitFor(() => expect(listed()).toHaveLength(4));
    fireEvent.change(screen.getByLabelText("Filter by state"), {
      target: { value: "merged" },
    });
    expect(listed()).toEqual(["Fix the login crash"]);
    fireEvent.click(screen.getByRole("button", { name: "Select" }));
    fireEvent.click(
      screen.getByRole("button", { name: "Select all finished" }),
    );
    expect(screen.getByText("1 selected")).toBeInTheDocument();
    previewDeleteTasks.mockResolvedValue([
      preview("task-done", "Fix the login crash"),
    ]);
    fireEvent.click(screen.getByRole("button", { name: "Delete…" }));
    await screen.findByRole("dialog");
    expect(previewDeleteTasks).toHaveBeenCalledWith(["task-done"]);
  });
});

describe("TasksView history", () => {
  it("filters the list by state, project, and age", async () => {
    renderTasks();
    await waitFor(() => expect(listed()).toHaveLength(4));

    fireEvent.change(screen.getByLabelText("Filter by state"), {
      target: { value: "failed" },
    });
    expect(listed()).toEqual(["Fix the signup crash"]);

    fireEvent.change(screen.getByLabelText("Filter by state"), {
      target: { value: "all" },
    });
    fireEvent.change(screen.getByLabelText("Filter by project"), {
      target: { value: "docs" },
    });
    expect(listed()).toEqual(["Rewrite the docs index"]);

    // Every run here ended hours ago.
    fireEvent.change(screen.getByLabelText("Filter by age"), {
      target: { value: "90d" },
    });
    expect(screen.getByText("No tasks match")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Show every task" }));
    expect(listed()).toHaveLength(4);
  });

  it("selects all finished tasks in the filter and skips running ones", async () => {
    renderTasks();
    await waitFor(() => expect(listed()).toHaveLength(4));
    fireEvent.click(screen.getByRole("button", { name: "Select" }));

    // A task Go would refuse has no checkbox, and says why.
    expect(
      screen.queryByRole("checkbox", {
        name: "Select task Fix the logout crash",
      }),
    ).not.toBeInTheDocument();
    expect(screen.getByLabelText(`Cannot delete: ${refusal}`)).toHaveAttribute(
      "title",
      refusal,
    );

    fireEvent.change(screen.getByLabelText("Filter by project"), {
      target: { value: "inkuna" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Select all finished" }),
    );
    expect(
      screen.getByRole("checkbox", { name: "Select task Fix the login crash" }),
    ).toBeChecked();
    expect(
      screen.getByRole("checkbox", {
        name: "Select task Fix the signup crash",
      }),
    ).toBeChecked();
    expect(screen.getByText("2 selected")).toBeInTheDocument();

    // A task the filter hides is never part of the delete.
    fireEvent.change(screen.getByLabelText("Filter by project"), {
      target: { value: "" },
    });
    expect(
      screen.getByRole("checkbox", {
        name: "Select task Rewrite the docs index",
      }),
    ).not.toBeChecked();

    previewDeleteTasks.mockResolvedValue([
      preview("task-done", "Fix the login crash"),
      preview("task-failed", "Fix the signup crash"),
    ]);
    fireEvent.click(screen.getByRole("button", { name: "Delete…" }));
    await screen.findByRole("dialog");
    expect(previewDeleteTasks).toHaveBeenCalledWith([
      "task-done",
      "task-failed",
    ]);
  });

  it("confirms with both clean-ups unticked and then shows Go's report", async () => {
    renderTasks();
    await waitFor(() => expect(listed()).toHaveLength(4));
    fireEvent.click(screen.getByRole("button", { name: "Select" }));
    fireEvent.click(
      screen.getByRole("checkbox", { name: "Select task Fix the login crash" }),
    );
    fireEvent.click(
      screen.getByRole("checkbox", {
        name: "Select task Fix the signup crash",
      }),
    );
    previewDeleteTasks.mockResolvedValue([
      preview("task-done", "Fix the login crash"),
      preview("task-failed", "Fix the signup crash"),
    ]);
    deleteTasks.mockImplementation(async () => {
      listWorkflowRuns.mockResolvedValue([runs[0], runs[3]]);
      return report;
    });
    fireEvent.click(screen.getByRole("button", { name: "Delete…" }));

    const dialog = await screen.findByRole("dialog", {
      name: "Delete 2 tasks?",
    });
    await within(dialog).findByLabelText("What goes");
    expect(within(dialog).getByLabelText("What goes")).toHaveTextContent(
      "Removes 2 tasks and 2 runs",
    );
    expect(dialog).toHaveTextContent("Worktrees · 2 worktrees · 6.0 MB");
    expect(within(dialog).getByLabelText("Branches")).toHaveTextContent(
      "bug/task-done2 commits only on this branch",
    );
    expect(
      within(dialog).getByRole("checkbox", { name: /Also remove worktrees/ }),
    ).not.toBeChecked();
    expect(
      within(dialog).getByRole("checkbox", { name: /Also delete branches/ }),
    ).not.toBeChecked();

    fireEvent.click(
      within(dialog).getByRole("button", { name: "Delete 2 tasks" }),
    );
    await waitFor(() =>
      expect(deleteTasks).toHaveBeenCalledWith(["task-done", "task-failed"], {
        removeWorktrees: false,
      }),
    );

    const shown = await within(dialog).findByLabelText("Delete report");
    expect(shown).toHaveTextContent("Deleted 2 tasks: 42 rows of history.");
    expect(shown).toHaveTextContent(
      "Freed 6 artifacts (2.0 KB). 1 artifact another task still uses stayed.",
    );
    expect(shown).toHaveTextContent(
      "/src/inkuna-worktrees/task-doneKept: Not asked to remove it.",
    );
    expect(shown).toHaveTextContent(
      "bug/task-doneKept: Not asked to delete it.",
    );
    expect(shown).toHaveTextContent(
      "Inkuna no longer links github 42; nothing changed there.",
    );
    // The deleted tasks leave the list at once.
    expect(listed()).toEqual([
      "Fix the logout crash",
      "Rewrite the docs index",
    ]);
    fireEvent.click(within(dialog).getByRole("button", { name: "Done" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});

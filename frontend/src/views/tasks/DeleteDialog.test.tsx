import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type { DeleteReport, Project, TaskDeletePreview } from "../../types";
import { DeleteDialog, type DeleteTarget } from "./DeleteDialog";

const previewDeleteTasks = vi.hoisted(() => vi.fn());
const previewDeleteRun = vi.hoisted(() => vi.fn());
const deleteTasks = vi.hoisted(() => vi.fn());
const deleteRun = vi.hoisted(() => vi.fn());
vi.mock("../../bridge", () => ({
  previewDeleteTasks,
  previewDeleteRun,
  deleteTasks,
  deleteRun,
}));

const task: TaskDeletePreview = {
  taskId: "task-1",
  projectId: "inkuna",
  title: "Fix the login crash",
  runs: [
    {
      runId: "run-1",
      title: "Fix the login crash",
      state: "completed",
      worktree: {
        path: "/src/wt/bug/42-login",
        bytes: 1024,
        removable: true,
      },
      branch: {
        projectId: "inkuna",
        name: "bug/42-login",
        commit: "1111111aaaaaaa",
        unique: 0,
        deletable: true,
      },
    },
    {
      runId: "run-2",
      title: "Fix the login crash again",
      state: "failed",
      worktree: {
        path: "/src/wt/bug/42-login-2",
        bytes: 2048,
        removable: false,
        reason: "It has uncommitted changes, so it would be kept.",
      },
      branch: {
        projectId: "inkuna",
        name: "bug/42-login-2",
        commit: "2222222bbbbbbb",
        unique: 3,
        deletable: true,
      },
    },
    {
      runId: "run-3",
      title: "Fix the login crash on main",
      state: "completed",
      branch: {
        projectId: "inkuna",
        name: "main-fix",
        commit: "3333333ccccccc",
        unique: 1,
        deletable: false,
        reason:
          "It tracks origin/main-fix; Inkuna deletes only branches without an upstream.",
      },
    },
  ],
};

const empty: DeleteReport = { tasks: [] };

function open(target: DeleteTarget, projects?: Project[]) {
  const onDeleted = vi.fn();
  const onClose = vi.fn();
  render(
    <DeleteDialog
      target={target}
      projects={projects}
      onClose={onClose}
      onDeleted={onDeleted}
    />,
  );
  return { onDeleted, onClose };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("DeleteDialog", () => {
  it("names branches with unmerged commits and deletes one only when ticked", async () => {
    previewDeleteTasks.mockResolvedValue([task]);
    deleteTasks.mockResolvedValue(empty);
    const { onDeleted } = open({ kind: "tasks", taskIds: ["task-1"] });
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByLabelText("Branches");
    const branches = within(dialog).getByLabelText("Branches");
    expect(branches).toHaveTextContent(
      "bug/42-login-23 commits only on this branch",
    );
    expect(branches).toHaveTextContent(
      "main-fix1 commit only on this branch · stays: It tracks origin/main-fix",
    );
    expect(within(dialog).getByLabelText("Worktrees")).toHaveTextContent(
      "stays: It has uncommitted changes, so it would be kept.",
    );

    // Ticking the option takes only the branch whose commits are elsewhere;
    // one with unmerged commits needs its own tick.
    fireEvent.click(
      within(dialog).getByRole("checkbox", { name: /Also delete branches/ }),
    );
    const unmerged = within(dialog).getByLabelText(
      "Branches with unmerged commits",
    );
    const lose = within(unmerged).getByRole("checkbox", {
      name: "Delete bug/42-login-2 and lose 3 commits",
    });
    expect(lose).not.toBeChecked();
    fireEvent.click(lose);
    fireEvent.click(
      within(dialog).getByRole("checkbox", { name: /Also remove worktrees/ }),
    );
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Delete 1 task" }),
    );
    await waitFor(() =>
      expect(deleteTasks).toHaveBeenCalledWith(["task-1"], {
        removeWorktrees: true,
        deleteBranches: {
          inkuna: {
            "bug/42-login": "1111111aaaaaaa",
            "bug/42-login-2": "2222222bbbbbbb",
          },
        },
      }),
    );
    await waitFor(() => expect(onDeleted).toHaveBeenCalledWith(empty));
  });

  it("lists and counts a worktree two tasks share once", async () => {
    const sharing = (taskId: string): TaskDeletePreview => ({
      ...task,
      taskId,
      runs: [
        {
          runId: `run-${taskId}`,
          title: "Fix the login crash",
          state: "completed",
          worktree: { path: "/src/wt/shared", bytes: 1024, removable: true },
        },
      ],
    });
    previewDeleteTasks.mockResolvedValue([sharing("a"), sharing("b")]);
    open({ kind: "tasks", taskIds: ["a", "b"] });
    const dialog = await screen.findByRole("dialog");
    const worktrees = await within(dialog).findByLabelText("Worktrees");
    expect(within(worktrees).getAllByRole("listitem")).toHaveLength(1);
    expect(dialog).toHaveTextContent("Worktrees · 1 worktree · 1.0 KB");
    expect(
      within(dialog).getByRole("checkbox", { name: /Also remove worktrees/ }),
    ).toHaveAccessibleName(
      "Also remove worktrees: 1 worktree without uncommitted changes, 1.0 KB",
    );
  });

  it("confirms a branch under its own project when two share its name", async () => {
    const onBranch = (
      taskId: string,
      projectId: string,
      commit: string,
    ): TaskDeletePreview => ({
      taskId,
      projectId,
      title: `Set up ${projectId}`,
      runs: [
        {
          runId: `run-${taskId}`,
          title: `Set up ${projectId}`,
          state: "completed",
          branch: {
            projectId,
            name: "feature/1-setup",
            commit,
            unique: 2,
            deletable: true,
          },
        },
      ],
    });
    previewDeleteTasks.mockResolvedValue([
      onBranch("a", "inkuna", "aaaaaaa1111111"),
      onBranch("b", "docs", "bbbbbbb2222222"),
    ]);
    deleteTasks.mockResolvedValue(empty);
    open({ kind: "tasks", taskIds: ["a", "b"] }, [
      {
        id: "inkuna",
        name: "Inkuna",
        repository: "/src/inkuna",
        defaultRuntime: "claude",
        createdAt: "2026-09-01T00:00:00Z",
      },
    ]);
    const dialog = await screen.findByRole("dialog");
    const branches = await within(dialog).findByLabelText("Branches");
    const rows = within(branches).getAllByRole("listitem");
    expect(rows).toHaveLength(2);
    // A project the view does not know is named by its id.
    expect(rows[0]).toHaveTextContent("Inkuna · 2 commits only on this branch");
    expect(rows[1]).toHaveTextContent("docs · 2 commits only on this branch");
    fireEvent.click(
      within(dialog).getByRole("checkbox", { name: /Also delete branches/ }),
    );
    const unmerged = within(dialog).getByLabelText(
      "Branches with unmerged commits",
    );
    const inkuna = within(unmerged).getByRole("checkbox", {
      name: "Delete feature/1-setup in Inkuna and lose 2 commits",
    });
    const docs = within(unmerged).getByRole("checkbox", {
      name: "Delete feature/1-setup in docs and lose 2 commits",
    });
    fireEvent.click(inkuna);
    expect(inkuna).toBeChecked();
    expect(docs).not.toBeChecked();
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Delete 2 tasks" }),
    );
    await waitFor(() =>
      expect(deleteTasks).toHaveBeenCalledWith(["a", "b"], {
        removeWorktrees: false,
        deleteBranches: { inkuna: { "feature/1-setup": "aaaaaaa1111111" } },
      }),
    );
  });

  it("deletes nothing while Go refuses, and says why", async () => {
    previewDeleteTasks.mockResolvedValue([
      {
        ...task,
        refused:
          "task task-1 cannot be deleted: run run-2 is paused for review; approve, send back, or cancel it first",
      },
    ]);
    open({ kind: "tasks", taskIds: ["task-1"] });
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "run run-2 is paused for review",
    );
    expect(
      within(dialog).getByRole("button", { name: "Delete 1 task" }),
    ).toBeDisabled();
  });

  it("deletes only the run from its detail unless the whole task is chosen", async () => {
    previewDeleteTasks.mockResolvedValue([task]);
    previewDeleteRun.mockResolvedValue({ ...task, runs: [task.runs[1]] });
    deleteRun.mockResolvedValue(empty);
    deleteTasks.mockResolvedValue(empty);
    open({ kind: "run", runId: "run-2", taskId: "task-1" });
    const dialog = await screen.findByRole("dialog", {
      name: "Delete this run?",
    });
    const only = await within(dialog).findByRole("radio", {
      name: "Only this run; the task and its other 2 runs stay",
    });
    expect(only).toBeChecked();
    expect(within(dialog).getByLabelText("What goes")).toHaveTextContent(
      "Removes 1 run from Inkuna's history; its task, plan, and other runs stay.",
    );
    expect(within(dialog).getByLabelText("Worktrees")).toHaveTextContent(
      "/src/wt/bug/42-login-2",
    );
    expect(within(dialog).getByLabelText("Worktrees")).not.toHaveTextContent(
      "/src/wt/bug/42-login1",
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete run" }));
    await waitFor(() =>
      expect(deleteRun).toHaveBeenCalledWith("run-2", {
        removeWorktrees: false,
      }),
    );
    expect(deleteTasks).not.toHaveBeenCalled();
  });

  it("deletes the task when the run is its only one", async () => {
    const single = { ...task, runs: [task.runs[0]] };
    previewDeleteTasks.mockResolvedValue([single]);
    previewDeleteRun.mockResolvedValue({
      ...single,
      refused:
        "task task-1 cannot be deleted: run run-1 is the task's only run; delete the task instead",
    });
    deleteTasks.mockResolvedValue(empty);
    open({ kind: "run", runId: "run-1", taskId: "task-1" });
    const dialog = await screen.findByRole("dialog", {
      name: "Delete this task?",
    });
    await within(dialog).findByLabelText("What goes");
    expect(within(dialog).queryByRole("radio")).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Delete 1 task" }),
    );
    await waitFor(() =>
      expect(deleteTasks).toHaveBeenCalledWith(["task-1"], {
        removeWorktrees: false,
      }),
    );
  });

  it("shows Go's reason when the delete itself is refused", async () => {
    previewDeleteTasks.mockResolvedValue([task]);
    deleteTasks.mockRejectedValue(
      new Error("task task-1 cannot be deleted: run run-1 is running"),
    );
    const { onDeleted } = open({ kind: "tasks", taskIds: ["task-1"] });
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByLabelText("What goes");
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Delete 1 task" }),
    );
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "run run-1 is running",
    );
    expect(onDeleted).not.toHaveBeenCalled();
  });
});

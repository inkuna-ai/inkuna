import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type { WorkflowRun } from "../../types";
import { RunWorkspace } from "./RunWorkspace";

const openExternal = vi.hoisted(() => vi.fn());
vi.mock("../../bridge", () => ({ openExternal }));

const finished: WorkflowRun = {
  id: "run-1",
  workflowId: "workflow-1",
  taskId: "task-1",
  state: "completed",
  title: "Docs index",
  worktree: "/src/inkuna-worktrees/feature/23-docs-index",
  branch: "feature/23-docs-index",
  cleanup: "manual",
  startedAt: "2026-10-16T10:00:00Z",
  events: [],
};

describe("RunWorkspace", () => {
  it("links the pull request its branch was opened in", () => {
    render(
      <RunWorkspace
        run={{
          ...finished,
          cleanup: "merged",
          pullRequest: {
            number: 12,
            title: "Docs index",
            url: "https://github.com/acme/app/pull/12",
            state: "OPEN",
            checkedAt: "2026-10-17T10:00:00Z",
          },
        }}
        onRemoveWorktree={vi.fn()}
      />,
    );
    const workspace = screen.getByLabelText("Run workspace");
    expect(workspace).toHaveTextContent("removed once its pull request merges");
    expect(screen.getByLabelText("Pull request")).toHaveTextContent(
      "Pull request #12 open",
    );
    fireEvent.click(screen.getByRole("button", { name: "Open #12" }));
    expect(openExternal).toHaveBeenCalledWith(
      "https://github.com/acme/app/pull/12",
    );
  });

  it("removes a finished run's worktree only after confirmation", async () => {
    const remove = vi.fn().mockResolvedValue(undefined);
    render(<RunWorkspace run={finished} onRemoveWorktree={remove} />);
    expect(screen.getByLabelText("Run workspace")).toHaveTextContent(
      "kept until you remove it",
    );
    fireEvent.click(screen.getByRole("button", { name: "Remove worktree" }));
    expect(remove).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveTextContent(
      "The branch feature/23-docs-index and its commits stay.",
    );
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Remove worktree" }),
    );
    await waitFor(() => expect(remove).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });

  it("shows Go's reason when removal is refused", async () => {
    const remove = vi
      .fn()
      .mockRejectedValue(
        new Error(
          "/src/wt has uncommitted changes; commit or discard them first",
        ),
      );
    render(<RunWorkspace run={finished} onRemoveWorktree={remove} />);
    fireEvent.click(screen.getByRole("button", { name: "Remove worktree" }));
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Remove worktree",
      }),
    );
    expect(
      await screen.findByText(/has uncommitted changes; commit or discard/),
    ).toBeInTheDocument();
  });

  it("offers no removal for active, removed, or checkout runs", () => {
    const remove = vi.fn();
    const { rerender } = render(
      <RunWorkspace
        run={{ ...finished, state: "running" }}
        onRemoveWorktree={remove}
      />,
    );
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    rerender(
      <RunWorkspace
        run={{
          ...finished,
          worktreeRemovedAt: "2026-10-16T11:00:00Z",
          workspaceNotes: [
            "Pruned 1 finished run worktree(s); their branches are kept",
          ],
        }}
        onRemoveWorktree={remove}
      />,
    );
    expect(screen.getByLabelText("Run workspace")).toHaveTextContent(
      /removed .*, branch kept/,
    );
    expect(screen.getByText(/Pruned 1 finished run/)).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    rerender(
      <RunWorkspace
        run={{
          ...finished,
          worktree: "/src/inkuna",
          branch: "work",
          cleanup: "checkout",
        }}
        onRemoveWorktree={remove}
      />,
    );
    expect(screen.getByLabelText("Run workspace")).toHaveTextContent(
      "in the checkout",
    );
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});

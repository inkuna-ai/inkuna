import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { WorkflowRun } from "../../types";
import { RunStory } from "./RunStory";

const bridge = vi.hoisted(() => ({
  markStoryDone: vi.fn(),
  openExternal: vi.fn(),
}));
vi.mock("../../bridge", () => bridge);

const ended = (markDone: boolean): WorkflowRun => ({
  id: "run-1",
  workflowId: "workflow-1",
  taskId: "task-1",
  state: "completed",
  title: "Fix login",
  branch: "feature/12-login",
  startedAt: "2026-11-02T09:00:00Z",
  events: [],
  story: { tracker: "github", id: "12", markDone },
});

describe("RunStory", () => {
  beforeEach(() => vi.resetAllMocks());

  it("shows what Mark done did once the pull request merged", async () => {
    bridge.markStoryDone.mockResolvedValue({
      merged: true,
      summary: "Pull request #40 merged.",
      notes: [
        "Wrote to GitHub issue 12: status → Done (and closed #12 as completed)",
      ],
      pullRequest: {
        number: 40,
        title: "Fix login",
        url: "https://github.com/acme/app/pull/40",
        state: "MERGED",
      },
    });
    render(<RunStory run={ended(true)} />);
    expect(screen.getByLabelText("Run story")).toHaveTextContent("#12");
    fireEvent.click(screen.getByRole("button", { name: "Mark done" }));
    await waitFor(() =>
      expect(screen.getByRole("status")).toHaveTextContent(
        "Pull request #40 merged.",
      ),
    );
    expect(bridge.markStoryDone).toHaveBeenCalledWith("run-1");
    expect(screen.getByRole("status")).toHaveTextContent(
      "status → Done (and closed #12 as completed)",
    );
    fireEvent.click(screen.getByRole("button", { name: "Open #40" }));
    expect(bridge.openExternal).toHaveBeenCalledWith(
      "https://github.com/acme/app/pull/40",
    );
  });

  it("shows Go's reason when Mark done is refused", async () => {
    bridge.markStoryDone.mockRejectedValue(
      new Error("inkuna.yaml turns updates.done off for this repository"),
    );
    render(<RunStory run={ended(true)} />);
    fireEvent.click(screen.getByRole("button", { name: "Mark done" }));
    expect(
      await screen.findByText(/turns updates\.done off/),
    ).toBeInTheDocument();
  });

  it("offers Mark done only where Go does", () => {
    const { rerender } = render(<RunStory run={ended(false)} />);
    expect(screen.getByLabelText("Run story")).toHaveTextContent("#12");
    expect(
      screen.queryByRole("button", { name: "Mark done" }),
    ).not.toBeInTheDocument();
    rerender(<RunStory run={{ ...ended(false), story: undefined }} />);
    expect(screen.queryByLabelText("Run story")).not.toBeInTheDocument();
  });
});

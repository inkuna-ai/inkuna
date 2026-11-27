import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type { RunResume, WorkflowRun } from "../../types";
import { ResumeBar } from "./ResumeBar";

const bridge = vi.hoisted(() => ({
  getRunResume: vi.fn(),
  resumeWorkflowRun: vi.fn(),
}));
vi.mock("../../bridge", () => bridge);

const run = (state: string, round = 1): WorkflowRun => ({
  id: "run-1",
  workflowId: "workflow-1",
  taskId: "task-1",
  state,
  round,
  title: "Fix the login crash",
  startedAt: "2026-11-03T09:00:00Z",
  events: [],
});

function offer(resume: Partial<RunResume>) {
  bridge.getRunResume.mockResolvedValue({
    round: 1,
    stopped: true,
    rerun: [
      { id: "fix", title: "Implement the fix", state: "failed" },
      { id: "verify", title: "verify", state: "pending" },
    ],
    kept: [
      { id: "profile", title: "profile", state: "completed" },
      { id: "reproduce", title: "Reproduce", state: "completed" },
    ],
    workspace: "/work/run-1",
    changes: [],
    ...resume,
  });
}

describe("ResumeBar", () => {
  afterEach(() => {
    // Unmount before the mocks reset, so a check still pending when a test
    // ends never counts toward the next one.
    cleanup();
    bridge.getRunResume.mockReset();
    bridge.resumeWorkflowRun.mockReset();
  });

  it("lists the steps a resume runs again, and keeps what is uncommitted by default", async () => {
    offer({ changes: [{ status: "??", path: "HALF.md" }] });
    const resumed = run("queued", 2);
    bridge.resumeWorkflowRun.mockResolvedValue(resumed);
    const onResumed = vi.fn();
    render(<ResumeBar run={run("failed")} onResumed={onResumed} />);
    expect(
      await screen.findByText(
        "Runs fix and verify again on the same branch, keeping what profile and reproduce did.",
      ),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Resume…" }));
    const form = await screen.findByRole("form", { name: "Confirm resume" });
    expect(within(form).getByText("fix · Implement the fix")).toBeVisible();
    expect(within(form).getByText("HALF.md")).toBeVisible();
    expect(
      within(form).getByRole("radio", { name: /Keep them/ }),
    ).toBeChecked();
    fireEvent.click(within(form).getByRole("button", { name: "Resume" }));
    await waitFor(() => expect(onResumed).toHaveBeenCalledWith(resumed));
    expect(bridge.resumeWorkflowRun).toHaveBeenCalledWith("run-1", {
      round: 1,
      discard: false,
      changes: ["HALF.md"],
    });
  });

  it("discards what is uncommitted only when the person picks it", async () => {
    offer({
      changes: [
        { status: "??", path: "HALF.md" },
        { status: " M", path: "login.go" },
      ],
    });
    bridge.resumeWorkflowRun.mockResolvedValue(run("queued", 2));
    render(<ResumeBar run={run("cancelled")} onResumed={() => {}} />);
    fireEvent.click(await screen.findByRole("button", { name: "Resume…" }));
    const form = await screen.findByRole("form", { name: "Confirm resume" });
    fireEvent.click(within(form).getByRole("radio", { name: /Discard them/ }));
    fireEvent.click(
      within(form).getByRole("button", { name: "Discard and resume" }),
    );
    await waitFor(() =>
      expect(bridge.resumeWorkflowRun).toHaveBeenCalledWith("run-1", {
        round: 1,
        discard: true,
        changes: ["HALF.md", "login.go"],
      }),
    );
  });

  it("shows Go's reason instead of the action when the run cannot be resumed", async () => {
    offer({
      refusal:
        "the run's worktree was removed, so there is nothing to build on; plan the task again",
    });
    render(<ResumeBar run={run("failed")} onResumed={() => {}} />);
    expect(
      await screen.findByText(
        "This run cannot be resumed: the run's worktree was removed, so there is nothing to build on; plan the task again",
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Resume…" }),
    ).not.toBeInTheDocument();
  });

  it("shows Go's reason when it refuses the resume, and asks again", async () => {
    offer({ changes: [{ status: "??", path: "HALF.md" }] });
    bridge.resumeWorkflowRun.mockRejectedValue(
      "what is uncommitted in the run's worktree changed since you looked; check it again before discarding it",
    );
    render(<ResumeBar run={run("failed")} onResumed={() => {}} />);
    fireEvent.click(await screen.findByRole("button", { name: "Resume…" }));
    const form = await screen.findByRole("form", { name: "Confirm resume" });
    const asked = bridge.getRunResume.mock.calls.length;
    fireEvent.click(within(form).getByRole("button", { name: "Resume" }));
    expect(
      await screen.findByText(/changed since you looked/),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(bridge.getRunResume.mock.calls.length).toBeGreaterThan(asked),
    );
  });

  it("shows nothing for a run that did not stop", async () => {
    offer({ stopped: false, refusal: "this run completed" });
    const { container } = render(
      <ResumeBar run={run("completed")} onResumed={() => {}} />,
    );
    await waitFor(() => expect(bridge.getRunResume).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });
});

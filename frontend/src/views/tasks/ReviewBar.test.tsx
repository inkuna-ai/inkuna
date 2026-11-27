import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type { RunReview, WorkflowRun } from "../../types";
import { ReviewBar } from "./ReviewBar";

const bridge = vi.hoisted(() => ({
  getRunReview: vi.fn(),
  approveReview: vi.fn(),
  requestChanges: vi.fn(),
  getGateArtifacts: vi.fn(),
  readArtifact: vi.fn(),
  retryTrackerChange: vi.fn(),
  openExternal: vi.fn(),
}));
vi.mock("../../bridge", () => bridge);

const run = (state: string, round = 1): WorkflowRun => ({
  id: "run-1",
  workflowId: "workflow-1",
  taskId: "task-1",
  state,
  round,
  title: "Write the export",
  startedAt: "2026-10-27T09:00:00Z",
  events: [],
});

const targets = [
  { id: "draft", title: "Draft", agent: "writer" },
  { id: "polish", title: "Polish", agent: "editor" },
];

function offer(review: Partial<RunReview>) {
  bridge.getRunReview.mockResolvedValue({
    round: 1,
    approve: false,
    targets,
    target: "draft",
    ...review,
  });
}

describe("ReviewBar", () => {
  afterEach(() => {
    // Unmount before the mocks reset: Testing Library's own cleanup runs
    // after this hook, and a re-check still pending when a test ends would
    // otherwise call a reset mock and count toward the next test.
    cleanup();
    bridge.getRunReview.mockReset();
    bridge.approveReview.mockReset();
    bridge.requestChanges.mockReset();
  });

  it("sends each item with the stage it is for, and the round it reviewed", async () => {
    offer({ round: 2, approve: true });
    const sent = { ...run("queued", 3) };
    bridge.requestChanges.mockResolvedValue(sent);
    const onChanged = vi.fn();
    render(<ReviewBar run={run("reviewing", 2)} onChanged={onChanged} />);
    const first = await screen.findByRole("textbox", { name: "Item 1" });
    // The picker starts on Go's default, and offers only Go's stages.
    const stage = screen.getByRole("combobox", { name: "Stage for item 1" });
    expect(stage).toHaveValue("draft");
    expect(
      Array.from(
        (stage as HTMLSelectElement).options,
        (option) => option.value,
      ),
    ).toEqual(["draft", "polish"]);
    const submit = screen.getByRole("button", { name: "Request changes" });
    expect(submit).toBeDisabled();

    fireEvent.change(first, { target: { value: "  Shorter intro.  " } });
    fireEvent.click(screen.getByRole("button", { name: "+ Add another item" }));
    fireEvent.click(screen.getByRole("button", { name: "+ Add another item" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Item 2" }), {
      target: { value: "Name the export." },
    });
    fireEvent.change(
      screen.getByRole("combobox", { name: "Stage for item 3" }),
      {
        target: { value: "polish" },
      },
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Item 3" }), {
      target: { value: "Fix the headings." },
    });
    fireEvent.click(submit);
    await waitFor(() => expect(onChanged).toHaveBeenCalledWith(sent));
    // The same stage can take several items; each is its own.
    expect(bridge.requestChanges).toHaveBeenCalledWith("run-1", {
      round: 2,
      items: [
        { target: "draft", text: "Shorter intro." },
        { target: "draft", text: "Name the export." },
        { target: "polish", text: "Fix the headings." },
      ],
    });
  });

  it("drops a removed item and leaves the others as written", async () => {
    offer({ approve: true });
    bridge.requestChanges.mockResolvedValue(run("queued", 2));
    render(<ReviewBar run={run("reviewing")} onChanged={() => {}} />);
    fireEvent.change(await screen.findByRole("textbox", { name: "Item 1" }), {
      target: { value: "First." },
    });
    fireEvent.click(screen.getByRole("button", { name: "+ Add another item" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Item 2" }), {
      target: { value: "Second." },
    });
    fireEvent.click(screen.getByRole("button", { name: "Remove item 1" }));
    expect(screen.getByRole("textbox", { name: "Item 1" })).toHaveValue(
      "Second.",
    );
    fireEvent.click(screen.getByRole("button", { name: "Request changes" }));
    await waitFor(() =>
      expect(bridge.requestChanges).toHaveBeenCalledWith("run-1", {
        round: 1,
        items: [{ target: "draft", text: "Second." }],
      }),
    );
  });

  it("links what each paused gate follows beside Approve", async () => {
    offer({ approve: true });
    bridge.getGateArtifacts.mockResolvedValue([
      {
        stepId: "plan",
        title: "Plan the approach",
        agent: "researcher",
        round: 1,
        artifacts: [
          {
            digest: "d1",
            name: "plan.md",
            kind: "document",
            mediaType: "text/markdown",
            size: 40,
          },
        ],
      },
    ]);
    const paused = {
      ...run("reviewing"),
      steps: [
        { id: "plan", title: "Plan", kind: "agent", state: "completed" },
        {
          id: "approval",
          title: "Approval",
          kind: "approval",
          state: "reviewing",
        },
      ],
    };
    render(<ReviewBar run={paused} onChanged={vi.fn()} />);
    const read = await screen.findByRole("region", {
      name: "Read before approving",
    });
    expect(bridge.getGateArtifacts).toHaveBeenCalledWith("run-1", "approval");
    expect(bridge.getGateArtifacts).toHaveBeenCalledTimes(1);
    expect(read).toHaveTextContent("Plan the approach · researcher · round 1");
    expect(
      within(read).getByRole("button", { name: /plan\.md/ }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeInTheDocument();
  });

  it("lists the tracker changes Approve makes beside it", async () => {
    offer({
      approve: true,
      trackerChanges: [
        {
          id: "c1",
          round: 1,
          stepId: "draft",
          tracker: "linear",
          kind: "comment",
          issue: "MAD-12",
          summary: "comment: The export is ready for review.",
          change: { kind: "comment", body: "The export is ready for review." },
          state: "pending",
          updatedAt: "2026-10-27T09:10:00Z",
        },
      ],
    });
    render(<ReviewBar run={run("reviewing")} onChanged={vi.fn()} />);
    const changes = await screen.findByRole("region", {
      name: "Approve also makes these tracker changes",
    });
    expect(changes).toHaveTextContent(
      "pendingdraftMAD-12 comment: The export is ready for review.",
    );
    expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled();
    // Nothing waits once the run has moved on.
    cleanup();
    render(<ReviewBar run={run("completed")} onChanged={vi.fn()} />);
    await screen.findByRole("button", { name: "Send back for changes" });
    expect(
      screen.queryByRole("region", {
        name: "Approve also makes these tracker changes",
      }),
    ).toBeNull();
  });

  it("offers Approve only at a gate", async () => {
    offer({ approve: true });
    const approved = run("completed");
    bridge.approveReview.mockResolvedValue(approved);
    const onChanged = vi.fn();
    const view = render(
      <ReviewBar run={run("reviewing")} onChanged={onChanged} />,
    );
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    await waitFor(() => expect(onChanged).toHaveBeenCalledWith(approved));
    expect(bridge.approveReview).toHaveBeenCalledWith("run-1");

    // An ended run can be sent back, from a button, but never approved.
    offer({ approve: false });
    view.rerender(<ReviewBar run={run("completed")} onChanged={onChanged} />);
    const send = await screen.findByRole("button", {
      name: "Send back for changes",
    });
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
    expect(screen.queryByRole("form", { name: "Request changes" })).toBeNull();
    fireEvent.click(send);
    expect(
      screen.getByRole("form", { name: "Request changes" }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("form", { name: "Request changes" })).toBeNull();
  });

  it("shows Go's reason in place of what it would refuse", async () => {
    offer({
      approve: true,
      targets: [],
      target: "",
      refusal: "no stage before this gate writes, so it can only be approved",
    });
    const view = render(
      <ReviewBar run={run("reviewing")} onChanged={() => {}} />,
    );
    expect(
      await screen.findByText(
        "no stage before this gate writes, so it can only be approved",
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled();
    expect(screen.queryByRole("form", { name: "Request changes" })).toBeNull();

    offer({
      targets: [],
      refusal:
        "/work/tree has uncommitted changes; commit or discard them first",
    });
    view.rerender(<ReviewBar run={run("failed")} onChanged={() => {}} />);
    expect(
      await screen.findByText(
        /Changes cannot be sent: \/work\/tree has uncommitted changes/,
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Send back for changes" }),
    ).toBeNull();
  });

  it("shows Go's reason when it refuses what was sent, and keeps the text", async () => {
    offer({ approve: true });
    bridge.requestChanges.mockRejectedValue(
      new Error(
        "this run is in round 2, and the changes were written for round 1; reload it",
      ),
    );
    const onChanged = vi.fn();
    render(<ReviewBar run={run("reviewing")} onChanged={onChanged} />);
    const item = await screen.findByRole("textbox", { name: "Item 1" });
    fireEvent.change(item, { target: { value: "Shorter intro." } });
    fireEvent.click(screen.getByRole("button", { name: "Request changes" }));
    expect(
      await screen.findByText(/the changes were written for round 1/),
    ).toBeInTheDocument();
    expect(item).toHaveValue("Shorter intro.");
    expect(onChanged).not.toHaveBeenCalled();
    // A refusal asks Go again what the bar may offer.
    await waitFor(() => expect(bridge.getRunReview).toHaveBeenCalledTimes(2));
  });

  it("asks Go again when the form opens, and on request after a refusal", async () => {
    offer({ approve: false });
    render(<ReviewBar run={run("completed")} onChanged={() => {}} />);
    const send = await screen.findByRole("button", {
      name: "Send back for changes",
    });
    expect(bridge.getRunReview).toHaveBeenCalledTimes(1);

    // The worktree was removed since the bar asked: opening the form asks
    // again, and Go's reason takes the form's place.
    offer({
      targets: [],
      refusal:
        "the run's worktree was removed, so there is nothing to build on; plan the task again",
    });
    fireEvent.click(send);
    expect(
      await screen.findByText(/the run's worktree was removed/),
    ).toBeInTheDocument();
    expect(bridge.getRunReview).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole("form", { name: "Request changes" })).toBeNull();

    // Once the reason is gone, checking again brings the form back.
    offer({ approve: false });
    fireEvent.click(screen.getByRole("button", { name: "Check again" }));
    expect(
      await screen.findByRole("form", { name: "Request changes" }),
    ).toBeInTheDocument();
    expect(screen.queryByText(/worktree was removed/)).toBeNull();
  });
});

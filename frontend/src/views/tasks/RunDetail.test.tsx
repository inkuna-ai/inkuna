import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { useState } from "react";
import type {
  CommentDraft,
  CommentThread,
  RunRound,
  RunUsage,
  WorkflowRun,
} from "../../types";
import { RunDetail } from "./RunDetail";
import type { Tab } from "./run";

const bridge = vi.hoisted(() => ({
  getRunUsage: vi.fn(),
  getRunRounds: vi.fn(),
  getRunReview: vi.fn(),
  getRunResume: vi.fn(),
  getRunFailure: vi.fn(),
  resumeWorkflowRun: vi.fn(),
  requestChanges: vi.fn(),
  approveReview: vi.fn(),
  copyText: vi.fn(),
  getRunChanges: vi.fn(),
  getRunCommits: vi.fn(),
  getRunFilePatch: vi.fn(),
  getRunFileExcerpt: vi.fn(),
  getRunViewed: vi.fn(),
  setFileViewed: vi.fn(),
  getRunComments: vi.fn(),
  getRunTask: vi.fn(),
  saveComment: vi.fn(),
  deleteComment: vi.fn(),
  resolveThread: vi.fn(),
  getCommentTarget: vi.fn(),
  getGateArtifacts: vi.fn(),
}));
vi.mock("../../bridge", () => bridge);
const { getRunUsage } = bridge;

const usage: RunUsage = {
  calls: [
    {
      id: 1,
      taskId: "task-1",
      executionId: "run-1",
      stepId: "worker",
      callId: "run-1-worker",
      turn: 1,
      kind: "worker",
      agent: "implementer",
      runtime: "claude",
      usage: { inputTokens: 10, outputTokens: 5 },
      promptBytes: 100,
      outcome: "completed",
      startedAt: "2026-10-18T09:00:00Z",
      finishedAt: "2026-10-18T09:01:00Z",
    },
  ],
  total: { inputTokens: 10, outputTokens: 5 },
  cost: { reportedCents: 0, estimatedCents: 0, unknownCalls: 1 },
};

// A running run as each poll returns it: the same run, one event longer.
const polled = (events: number): WorkflowRun => ({
  id: "run-1",
  workflowId: "workflow-1",
  taskId: "task-1",
  state: "running",
  live: true,
  title: "Update the docs",
  worktree: "/src/inkuna-worktrees/feature/update-the-docs",
  branch: "feature/update-the-docs",
  cleanup: "manual",
  startedAt: "2026-10-18T09:00:00Z",
  events: Array.from({ length: events }, (_, index) => ({
    version: 1,
    runId: "run-1",
    sequence: index + 1,
    timestamp: "2026-10-18T09:00:00Z",
    kind: "message",
    summary: `event ${index + 1}`,
  })),
});

describe("RunDetail", () => {
  beforeEach(() => {
    getRunUsage.mockResolvedValue(usage);
    bridge.getRunFailure.mockResolvedValue({
      round: 1,
      steps: [],
      suggestions: [],
    });
    bridge.getRunRounds.mockResolvedValue([]);
    bridge.getGateArtifacts.mockResolvedValue([]);
    bridge.getRunReview.mockResolvedValue({
      round: 2,
      approve: true,
      targets: [{ id: "implement", title: "Implement", agent: "implementer" }],
      target: "implement",
    });
    bridge.getRunResume.mockResolvedValue({
      round: 1,
      stopped: false,
      rerun: [],
      kept: [],
      changes: [],
    });
  });
  afterEach(() => {
    // Unmount before the mocks reset, so an effect still pending when a test
    // ends never calls a reset mock or counts toward the next test.
    cleanup();
    getRunUsage.mockReset();
    bridge.getRunRounds.mockReset();
    bridge.getRunReview.mockReset();
    bridge.getRunResume.mockReset();
  });

  it("shows one branch panel and one usage panel however often the run refreshes", async () => {
    const props = {
      tab: "timeline" as const,
      onTab: () => {},
      onCancel: () => {},
      onRemoveWorktree: async () => {},
    };
    const view = render(<RunDetail run={polled(1)} {...props} />);
    for (let events = 2; events <= 8; events++) {
      view.rerender(<RunDetail run={polled(events)} {...props} />);
    }
    await waitFor(() =>
      expect(screen.getAllByLabelText("Token usage")).toHaveLength(1),
    );
    expect(screen.getAllByLabelText("Run workspace")).toHaveLength(1);
  });

  it("points a run paused for review at its thread, and can cancel it after a restart", async () => {
    const onOpenThread = vi.fn();
    const onCancel = vi.fn();
    const props = {
      tab: "timeline" as const,
      onTab: () => {},
      onCancel,
      onRemoveWorktree: async () => {},
      onOpenThread,
    };
    const reviewing: WorkflowRun = {
      ...polled(1),
      state: "reviewing",
      round: 2,
      steps: [
        {
          id: "approval",
          title: "Approval",
          kind: "approval",
          state: "reviewing",
          a2aTaskId: "a2a-gate",
        },
      ],
    };
    const view = render(<RunDetail run={reviewing} {...props} />);
    expect(
      await screen.findByText("Waiting for your review of round 2"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Open in Messages" }));
    expect(onOpenThread).toHaveBeenCalledWith("a2a-gate");

    // After a restart the run comes from storage: not live, its gate's
    // thread unknown, and still cancellable.
    view.rerender(
      <RunDetail run={{ ...reviewing, live: false, steps: [] }} {...props} />,
    );
    expect(
      screen.queryByRole("button", { name: "Open in Messages" }),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Cancel run" }));
    expect(onCancel).toHaveBeenCalled();
  });

  it("marks a step planned from a shared definition, as the approved plan recorded it", async () => {
    const run: WorkflowRun = {
      ...polled(1),
      steps: [
        {
          id: "worker",
          title: "Update the docs",
          kind: "agent",
          state: "completed",
        },
        {
          id: "verify",
          title: "Verify",
          kind: "evaluation",
          state: "running",
          from: {
            stage: "verify",
            scope: "repository",
            file: "/src/inkuna/.inkuna/rules.yaml",
            overrides: ["commands"],
          },
        },
      ],
    };
    render(
      <RunDetail
        run={run}
        tab="timeline"
        onTab={() => {}}
        onCancel={() => {}}
        onRemoveWorktree={async () => {}}
      />,
    );
    const table = await screen.findByRole("table", { name: "Workflow steps" });
    const rows = within(table).getAllByRole("row");
    expect(within(rows[1]).queryByText(/^shared:/)).toBeNull();
    fireEvent.click(within(rows[2]).getByText("shared: verify (repository)"));
    expect(
      within(rows[2]).getByRole("group", { name: "Where Verify came from" }),
    ).toHaveTextContent("Overridden by this flow: commands.");
  });

  it("shows what the run's task has cost so far, and opens the breakdown from it", async () => {
    const onTab = vi.fn();
    render(
      <RunDetail
        run={{
          ...polled(1),
          taskCost: {
            usage: { inputTokens: 10, outputTokens: 5 },
            cost: { reportedCents: 40, estimatedCents: 3, unknownCalls: 1 },
            bestCents: 43,
            subscriptionCents: 40,
            calls: 4,
            durationMs: 1000,
          },
        }}
        tab="timeline"
        onTab={onTab}
        onCancel={() => {}}
        onRemoveWorktree={async () => {}}
      />,
    );
    const figure = screen.getByRole("button", { name: /Task cost/ });
    expect(figure).toHaveTextContent("Task cost ≈$0.43 + 1 unpriced");
    expect(figure.getAttribute("title")).toContain(
      "$0.40 of it was reported under a subscription login",
    );
    fireEvent.click(figure);
    expect(onTab).toHaveBeenCalledWith("cost");
    await waitFor(() => expect(getRunUsage).toHaveBeenCalled());
  });

  it("shows the current round until another is picked, and every round on request", async () => {
    const event = (runId: string, sequence: number, summary: string) => ({
      version: 1,
      runId,
      sequence,
      timestamp: "2026-10-18T09:00:00Z",
      kind: "message",
      summary,
    });
    const step = (id: string, state: string, round: number) => ({
      id,
      title: id,
      kind: "agent",
      state,
      round,
    });
    const rounds: RunRound[] = [
      {
        round: 1,
        steps: [
          step("plan", "completed", 1),
          step("implement", "completed", 1),
        ],
        events: [event("run-1-implement", 1, "Round one wrote the flag")],
        feedback: [],
      },
      {
        round: 2,
        since: "abc123",
        steps: [step("plan", "completed", 1), step("implement", "running", 2)],
        events: [],
        // A failed check started round 2.
        feedback: [
          {
            executionId: "run-1",
            round: 2,
            id: "k1",
            target: "implement",
            from: "check",
            text: "go test ./... exited with code 1",
            status: "open",
            createdAt: "2026-10-18T09:00:00Z",
            checkId: "run-1-verify-0",
            tail: "--- FAIL: TestName",
          },
        ],
      },
    ];
    bridge.getRunRounds.mockResolvedValue(rounds);
    const run: WorkflowRun = {
      ...polled(0),
      round: 2,
      steps: [step("plan", "completed", 1), step("implement", "running", 2)],
      events: [event("run-1.r2.implement", 1, "Round two fixes the name")],
    };
    render(
      <RunDetail
        run={run}
        tab="timeline"
        onTab={() => {}}
        onCancel={() => {}}
        onRemoveWorktree={async () => {}}
      />,
    );
    const picker = await screen.findByRole("combobox", { name: "Round" });
    expect(picker).toHaveValue("2");
    // A round a failed check started says so.
    expect(
      within(picker).getByRole("option", { name: "Round 2 · check (current)" }),
    ).toBeInTheDocument();
    expect(
      within(picker).getByRole("option", { name: "Round 1" }),
    ).toBeInTheDocument();
    // The current round shows the live run's events, and marks the step it
    // kept from round 1.
    expect(screen.getByText("Round two fixes the name")).toBeInTheDocument();
    expect(screen.queryByText("Round one wrote the flag")).toBeNull();
    const table = screen.getByRole("table", { name: "Round 2 steps" });
    expect(within(table).getByText("kept from round 1")).toBeInTheDocument();

    fireEvent.change(picker, { target: { value: "1" } });
    expect(screen.getByText("Round one wrote the flag")).toBeInTheDocument();
    expect(screen.queryByText("Round two fixes the name")).toBeNull();
    expect(screen.getByRole("table", { name: "Round 1 steps" })).toBeVisible();
    expect(screen.queryByText("kept from round 1")).toBeNull();

    fireEvent.change(picker, { target: { value: "all" } });
    expect(
      screen.getByRole("region", { name: "Round 1 events" }),
    ).toHaveTextContent("Round one wrote the flag");
    expect(
      screen.getByRole("region", { name: "Round 2 events" }),
    ).toHaveTextContent("Round two fixes the name");
    // Every round lists only the steps it ran.
    expect(
      within(screen.getByRole("table", { name: "Round 2 steps" })).queryByText(
        "plan",
      ),
    ).toBeNull();
  });

  it("asks Go about review only once a run has ended or paused, and shows why not", async () => {
    const props = {
      tab: "timeline" as const,
      onTab: () => {},
      onCancel: () => {},
      onRemoveWorktree: async () => {},
    };
    const view = render(<RunDetail run={polled(1)} {...props} />);
    await waitFor(() => expect(bridge.getRunRounds).toHaveBeenCalled());
    expect(screen.queryByRole("region", { name: "Review" })).toBeNull();
    expect(bridge.getRunReview).not.toHaveBeenCalled();
    expect(bridge.getRunResume).not.toHaveBeenCalled();

    bridge.getRunReview.mockResolvedValue({
      round: 1,
      approve: false,
      targets: [],
      refusal:
        "a cancelled run cannot be sent back; resume it, or plan the task again",
    });
    bridge.getRunResume.mockResolvedValue({
      round: 1,
      stopped: true,
      rerun: [{ id: "worker", title: "Implement", state: "cancelled" }],
      kept: [],
      changes: [],
    });
    view.rerender(
      <RunDetail run={{ ...polled(1), state: "cancelled" }} {...props} />,
    );
    const review = await screen.findByRole("region", { name: "Review" });
    expect(review).toHaveTextContent(
      "Changes cannot be sent: a cancelled run cannot be sent back",
    );
    expect(
      within(review).queryByRole("button", { name: "Send back for changes" }),
    ).toBeNull();
    // A stopped run offers Resume instead.
    const resume = await screen.findByRole("region", { name: "Resume" });
    expect(
      within(resume).getByRole("button", { name: "Resume…" }),
    ).toBeVisible();
  });

  it("keeps draft comments across tabs and sends them with Request changes", async () => {
    const tip = "c".repeat(40);
    const implement = {
      id: "implement",
      title: "Implement",
      agent: "implementer",
    };
    // Go's side, as far as the view can tell: drafts are kept until sent.
    const threads: CommentThread[] = [];
    bridge.getRunComments.mockImplementation(async () => ({
      threads: structuredClone(threads),
      targets: [implement],
      to: tip,
    }));
    bridge.saveComment.mockImplementation(
      async (_run: string, draft: CommentDraft) => {
        const id = `thread-${threads.length + 1}`;
        threads.push({
          id,
          path: draft.path!,
          side: draft.side!,
          line: draft.line!,
          endLine: draft.endLine!,
          round: 1,
          state: "open",
          comments: [
            {
              id: `${id}-comment`,
              threadId: id,
              body: draft.body,
              target: draft.target,
              state: "draft",
              createdAt: "2026-10-27T09:00:00Z",
              updatedAt: "2026-10-27T09:00:00Z",
            },
          ],
        });
      },
    );
    bridge.getCommentTarget.mockResolvedValue("implement");
    bridge.getRunReview.mockImplementation(async () => ({
      round: 1,
      approve: false,
      targets: [implement],
      target: "implement",
      drafts: threads.length,
    }));
    bridge.getRunViewed.mockResolvedValue([]);
    bridge.getRunCommits.mockResolvedValue([]);
    const file = {
      path: "name.go",
      status: "modified" as const,
      additions: 1,
      deletions: 1,
      blob: "a".repeat(40),
    };
    bridge.getRunChanges.mockResolvedValue({
      from: "b".repeat(40),
      to: tip,
      files: [file],
      additions: 1,
      deletions: 1,
    });
    bridge.getRunFilePatch.mockResolvedValue({
      file,
      from: "b".repeat(40),
      to: tip,
      hunks: [
        {
          header: "@@ -1,2 +1,2 @@",
          oldStart: 1,
          oldLines: 2,
          newStart: 1,
          newLines: 2,
          lines: [
            { kind: "context", old: 1, new: 1, text: "package name" },
            { kind: "deleted", old: 2, text: "func Name() {}" },
            { kind: "added", new: 2, text: "func Name(name string) {}" },
          ],
        },
      ],
      bytes: 80,
      lines: 4,
      newTotal: 2,
    });
    const ended: WorkflowRun = {
      ...polled(0),
      state: "completed",
      live: false,
      round: 1,
    };
    const sent = { ...ended, state: "queued", round: 2 };
    bridge.requestChanges.mockResolvedValue(sent);
    const onReviewed = vi.fn();
    function Detail() {
      const [tab, setTab] = useState<Tab>("changes");
      return (
        <RunDetail
          run={ended}
          tab={tab}
          onTab={setTab}
          onCancel={() => {}}
          onRemoveWorktree={async () => {}}
          onReviewed={onReviewed}
        />
      );
    }
    render(<Detail />);

    fireEvent.click(
      await screen.findByRole("button", { name: "Comment on line 2" }),
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Comment" }), {
      target: { value: "Reject an empty name." },
    });
    await waitFor(() =>
      expect(
        screen.getByRole("combobox", { name: "Stage for the comment" }),
      ).toHaveValue("implement"),
    );
    fireEvent.click(screen.getByRole("button", { name: "Add comment" }));
    expect(
      await screen.findByRole("article", { name: "Draft comment" }),
    ).toHaveTextContent("Reject an empty name.");
    // A comment still being written is kept too.
    fireEvent.click(screen.getByRole("button", { name: "Comment on line 1" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Comment" }), {
      target: { value: "Half written" },
    });

    fireEvent.click(screen.getByRole("tab", { name: "Timeline" }));
    await waitFor(() =>
      expect(
        screen.queryByRole("article", { name: "Draft comment" }),
      ).not.toBeInTheDocument(),
    );
    fireEvent.click(screen.getByRole("tab", { name: "Changes" }));
    expect(
      await screen.findByRole("article", { name: "Draft comment" }),
    ).toHaveTextContent("Reject an empty name.");
    expect(screen.getByRole("textbox", { name: "Comment" })).toHaveValue(
      "Half written",
    );

    // The review bar counts the draft, and sends it with no other text.
    fireEvent.click(
      await screen.findByRole("button", { name: "Send back for changes" }),
    );
    expect(
      await screen.findByText(
        /Your 1 draft comment on the changes goes with this request/,
      ),
    ).toBeInTheDocument();
    const review = screen.getByRole("form", { name: "Request changes" });
    const submit = within(review).getByRole("button", {
      name: "Request changes",
    });
    expect(submit).toBeEnabled();
    fireEvent.click(submit);
    await waitFor(() =>
      expect(bridge.requestChanges).toHaveBeenCalledWith("run-1", {
        round: 1,
        items: [],
      }),
    );
    expect(onReviewed).toHaveBeenCalledWith(sent);
  });

  it("shows the run's title with its task's details folded under it", async () => {
    bridge.getRunTask.mockResolvedValue({
      version: 1,
      id: "task-1",
      kind: "implementation",
      title: "Update the docs",
      objective: "The run docs still describe the old Commit tab.",
      source: { kind: "request" },
      acceptanceCriteria: ["Mentions the Changes tab"],
      constraints: ["Only docs/"],
      repository: "/src/inkuna",
      priority: 1,
      budget: { wallTimeSeconds: 60 },
    });
    render(
      <RunDetail
        run={{ ...polled(1), title: "Update the docs" }}
        tab="timeline"
        onTab={() => {}}
        onCancel={() => {}}
        onRemoveWorktree={async () => {}}
      />,
    );
    expect(
      screen.getByRole("heading", { name: "Update the docs" }),
    ).toBeInTheDocument();
    // Nothing is read until the details are opened.
    expect(bridge.getRunTask).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "▸ Details" }));
    expect(
      await screen.findByText(
        "The run docs still describe the old Commit tab.",
      ),
    ).toBeVisible();
    expect(bridge.getRunTask).toHaveBeenCalledWith("run-1");
    expect(screen.getByText("Mentions the Changes tab")).toBeInTheDocument();
    expect(screen.getByText("Only docs/")).toBeInTheDocument();
  });

  it("offers Delete only once the run has ended", async () => {
    const onDelete = vi.fn();
    const props = {
      tab: "timeline" as const,
      onTab: () => {},
      onCancel: () => {},
      onRemoveWorktree: async () => {},
      onDelete,
    };
    const view = render(<RunDetail run={polled(1)} {...props} />);
    expect(
      screen.queryByRole("button", { name: "Delete…" }),
    ).not.toBeInTheDocument();
    view.rerender(
      <RunDetail
        run={{ ...polled(1), state: "failed", live: false }}
        {...props}
      />,
    );
    fireEvent.click(await screen.findByRole("button", { name: "Delete…" }));
    expect(onDelete).toHaveBeenCalledTimes(1);
  });
});

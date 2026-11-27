import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type { RunUsage, UsageRecord, WorkflowRun } from "../../types";
import { UsagePanel } from "./UsagePanel";

const getRunUsage = vi.hoisted(() => vi.fn());
vi.mock("../../bridge", () => ({ getRunUsage }));

const run: WorkflowRun = {
  id: "run-1",
  workflowId: "workflow-1",
  taskId: "task-1",
  state: "completed",
  title: "Record usage",
  startedAt: "2026-10-18T09:00:00Z",
  events: [],
};

const call = (fields: Partial<UsageRecord>): UsageRecord => ({
  id: 1,
  taskId: "task-1",
  executionId: "run-1",
  stepId: "worker",
  callId: "run-1-worker",
  turn: 1,
  kind: "worker",
  agent: "implementer",
  runtime: "claude",
  usage: {},
  promptBytes: 0,
  outcome: "completed",
  startedAt: "2026-10-18T09:00:00Z",
  finishedAt: "2026-10-18T09:01:00Z",
  ...fields,
});

describe("UsagePanel", () => {
  afterEach(() => {
    // Unmount before the mocks reset, so an effect still pending when a test
    // ends never calls a reset mock or counts toward the next test.
    cleanup();
    getRunUsage.mockReset();
  });

  it("shows Go's run totals and a row per call, each cost with its source", async () => {
    const usage: RunUsage = {
      calls: [
        call({
          id: 1,
          executionId: undefined,
          stepId: "staffing",
          kind: "orchestrator",
          agent: "orchestrator",
          model: "haiku",
          modelUsed: "claude-haiku-5-5",
          authMode: "subscription",
          usage: {
            inputTokens: 6,
            cacheWriteTokens: 12000,
            outputTokens: 400,
            reportedCents: 2,
          },
          cost: { cents: 2, source: "reported", estimatedCents: 1.9 },
          promptBytes: 18000,
        }),
        call({
          id: 2,
          runtime: "codex",
          model: "gpt-5.6-terra",
          usage: {
            inputTokens: 10,
            cacheWriteTokens: 20000,
            outputTokens: 300,
          },
          cost: { cents: 9, source: "estimated", pricingVersion: 1 },
          promptBytes: 40000,
          outcome: "timed-out",
        }),
        call({
          id: 3,
          turn: 2,
          usage: { inputTokens: 12, cacheReadTokens: 28000, outputTokens: 500 },
          cost: { cents: 0, source: "unknown" },
          promptBytes: 41000,
        }),
      ],
      total: {
        inputTokens: 28,
        cacheWriteTokens: 32000,
        cacheReadTokens: 28000,
        outputTokens: 1200,
        reportedCents: 2,
      },
      cost: { reportedCents: 2, estimatedCents: 9, unknownCalls: 1 },
    };
    getRunUsage.mockResolvedValue(usage);
    render(<UsagePanel run={run} />);
    const panel = await screen.findByLabelText("Token usage");
    expect(getRunUsage).toHaveBeenCalledWith("run-1");
    expect(panel).toHaveTextContent("60kInput tokens");
    expect(panel).toHaveTextContent("47%Input read from cache");
    expect(panel).toHaveTextContent("1.2kOutput tokens");
    // Reported and estimated figures add up as the best available, marked
    // as an estimate, with the unpriced call counted, not valued.
    expect(panel).toHaveTextContent("≈$0.11 + 1 unpricedCost");
    const rows = within(panel).getAllByRole("row");
    // A heading, three calls, and the total.
    expect(rows).toHaveLength(5);
    // The model the stream named wins over the alias Inkuna asked for.
    expect(rows[1]).toHaveTextContent(
      /staffing.*orchestrator · orchestrator.*claude · claude-haiku-5-5.*completed.*6.*12,000.*0.*400.*\$0\.02.*18,000/,
    );
    expect(within(rows[1]).getByText("$0.02")).toHaveAttribute(
      "title",
      expect.stringContaining("signed in to a subscription"),
    );
    expect(rows[2]).toHaveTextContent(
      /worker.*1.*worker · implementer.*codex · gpt-5\.6-terra.*timed-out.*≈\$0\.09/,
    );
    expect(rows[3]).toHaveTextContent(/2.*28,000.*500.*unknown.*41,000/);
    expect(rows[4]).toHaveTextContent(
      /Run total.*28.*32,000.*28,000.*1,200.*≈\$0\.11 \+ 1 unpriced/,
    );
    expect(panel).toHaveTextContent(/≈ marks a cost estimated/);
    expect(panel).toHaveTextContent(
      /signed in to a subscription: its reported cost is what the call would cost through the API/,
    );
  });

  it("shows nothing for a run without model calls, and says why when loading fails", async () => {
    getRunUsage.mockResolvedValue({ calls: [], total: {} });
    const view = render(<UsagePanel run={run} />);
    await waitFor(() => expect(getRunUsage).toHaveBeenCalled());
    expect(screen.queryByLabelText("Token usage")).toBeNull();
    getRunUsage.mockRejectedValue(new Error('run "run-2" was not found'));
    view.rerender(<UsagePanel run={{ ...run, id: "run-2" }} />);
    expect(
      await screen.findByText(/run "run-2" was not found/),
    ).toBeInTheDocument();
  });

  it("refreshes when a live run's latest event changes, even at the event cap", async () => {
    getRunUsage.mockResolvedValue({ calls: [], total: {} });
    const event = (sequence: number) => ({
      version: 1,
      runId: "run-1-worker",
      sequence,
      timestamp: `2026-10-18T09:00:0${sequence}Z`,
      kind: "message",
      summary: "working",
    });
    const live = { ...run, state: "running", live: true };
    const view = render(<UsagePanel run={{ ...live, events: [event(1)] }} />);
    await waitFor(() => expect(getRunUsage).toHaveBeenCalledTimes(1));
    // The oldest event drops out as a new one arrives: same count.
    view.rerender(<UsagePanel run={{ ...live, events: [event(2)] }} />);
    await waitFor(() => expect(getRunUsage).toHaveBeenCalledTimes(2));
  });

  it("reads usage again when Go stores a row for the run after it ended", async () => {
    // Go's inkuna:usage event names the run whose usage gained a row.
    const listeners: Array<(data?: unknown) => void> = [];
    window.runtime = {
      EventsOnMultiple: (name: string, callback: (data?: unknown) => void) => {
        if (name === "inkuna:usage") listeners.push(callback);
        return () => {};
      },
    };
    try {
      getRunUsage.mockResolvedValue({ calls: [], total: {} });
      render(<UsagePanel run={{ ...run, state: "cancelled" }} />);
      await waitFor(() => expect(getRunUsage).toHaveBeenCalledTimes(1));
      getRunUsage.mockResolvedValue({
        calls: [call({ outcome: "cancelled", usage: { outputTokens: 700 } })],
        total: { outputTokens: 700 },
      });
      act(() => listeners.forEach((listener) => listener("run-other")));
      expect(getRunUsage).toHaveBeenCalledTimes(1);
      act(() => listeners.forEach((listener) => listener("run-1")));
      const panel = await screen.findByLabelText("Token usage");
      expect(getRunUsage).toHaveBeenCalledTimes(2);
      expect(panel).toHaveTextContent("cancelled");
    } finally {
      delete window.runtime;
    }
  });

  it("says which round each call was in once a run has been sent back", async () => {
    getRunUsage.mockResolvedValue({
      calls: [
        call({ id: 1, round: 1 }),
        call({ id: 2, round: 2, callId: "run-1.r2.worker" }),
      ],
      total: {},
      cost: { reportedCents: 0, estimatedCents: 0, unknownCalls: 2 },
    });
    render(<UsagePanel run={run} />);
    const panel = await screen.findByLabelText("Token usage");
    const rows = within(panel).getAllByRole("row");
    expect(rows[0]).toHaveTextContent(/Step\s*Round\s*Turn/);
    expect(within(rows[1]).getAllByRole("cell")[1]).toHaveTextContent("1");
    expect(within(rows[2]).getAllByRole("cell")[1]).toHaveTextContent("2");
    expect(within(rows[3]).getAllByRole("cell")[0]).toHaveAttribute(
      "colspan",
      "6",
    );
  });
});

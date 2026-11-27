import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type {
  CallTotals,
  TaskCost,
  UsageRecord,
  WorkflowRun,
} from "../../types";
import { CostTab } from "./CostTab";

const getTaskCost = vi.hoisted(() => vi.fn());
vi.mock("../../bridge", () => ({ getTaskCost }));

const run: WorkflowRun = {
  id: "run-1",
  workflowId: "workflow-1",
  taskId: "task-1",
  state: "completed",
  title: "Add a cost view",
  startedAt: "2026-10-19T09:05:00Z",
  events: [],
};

const totals = (
  calls: number,
  reported: number,
  estimated: number,
  unknown: number,
  fields: Partial<CallTotals> = {},
): CallTotals => ({
  usage: {},
  cost: {
    reportedCents: reported,
    estimatedCents: estimated,
    unknownCalls: unknown,
  },
  bestCents: reported + estimated,
  subscriptionCents: 0,
  calls,
  durationMs: 0,
  ...fields,
});

const call = (fields: Partial<UsageRecord>): UsageRecord => ({
  id: 1,
  taskId: "task-1",
  executionId: "run-1",
  stepId: "research",
  callId: "call",
  turn: 1,
  kind: "worker",
  agent: "researcher",
  runtime: "claude",
  model: "haiku",
  usage: {},
  cost: { cents: 0, source: "unknown" },
  promptBytes: 0,
  outcome: "completed",
  startedAt: "2026-10-19T09:06:00Z",
  finishedAt: "2026-10-19T09:07:00Z",
  ...fields,
});

const staffing = call({
  id: 1,
  executionId: undefined,
  stepId: "staffing",
  kind: "orchestrator",
  agent: "orchestrator",
  cost: { cents: 2, source: "reported" },
});
const researched = call({
  id: 2,
  cost: { cents: 4, source: "reported" },
  authMode: "subscription",
  usage: { inputTokens: 10, outputTokens: 100 },
});
const reviewed = call({
  id: 3,
  kind: "peer",
  agent: "reviewer",
  runtime: "copilot",
  model: undefined,
});
const implemented = call({
  id: 4,
  stepId: "implement",
  agent: "implementer",
  runtime: "codex",
  model: "gpt-5.5",
  cost: { cents: 2.5, source: "estimated" },
});
const retried = call({
  id: 5,
  executionId: "run-2",
  cost: { cents: 1.6, source: "reported" },
  outcome: "failed",
});

const cost: TaskCost = {
  taskId: "task-1",
  total: totals(5, 7.6, 2.5, 1, {
    subscriptionCents: 6,
    usage: { inputTokens: 331, cacheReadTokens: 1000, outputTokens: 620 },
    durationMs: 149000,
  }),
  planning: {
    total: totals(1, 2, 0, 0, { subscriptionCents: 2 }),
    calls: [staffing],
  },
  runs: [
    {
      executionId: "run-1",
      state: "completed",
      startedAt: "2026-10-19T09:05:00Z",
      finishedAt: "2026-10-19T09:15:00Z",
      total: totals(3, 4, 2.5, 1),
      steps: [
        {
          stepId: "research",
          agent: "researcher",
          runtime: "claude",
          model: "haiku",
          startedAt: "2026-10-19T09:06:00Z",
          total: totals(2, 4, 0, 1),
          calls: [researched, reviewed],
        },
        {
          stepId: "implement",
          agent: "implementer",
          runtime: "codex",
          model: "gpt-5.5",
          startedAt: "2026-10-19T09:08:00Z",
          total: totals(1, 0, 2.5, 0),
          calls: [implemented],
        },
      ],
    },
    {
      executionId: "run-2",
      state: "failed",
      startedAt: "2026-10-19T09:30:00Z",
      finishedAt: "2026-10-19T09:40:00Z",
      total: totals(1, 1.6, 0, 0),
      steps: [
        {
          stepId: "research",
          agent: "researcher",
          runtime: "claude",
          model: "haiku",
          startedAt: "2026-10-19T09:31:00Z",
          total: totals(1, 1.6, 0, 0),
          calls: [retried],
        },
      ],
    },
  ],
  byAgent: [
    { key: "researcher", total: totals(2, 5.6, 0, 0) },
    { key: "implementer", total: totals(1, 0, 2.5, 0) },
    { key: "orchestrator", total: totals(1, 2, 0, 0) },
    { key: "reviewer", total: totals(1, 0, 0, 1) },
  ],
  byRuntime: [
    { key: "claude", total: totals(3, 7.6, 0, 0) },
    { key: "codex", total: totals(1, 0, 2.5, 0) },
    { key: "copilot", total: totals(1, 0, 0, 1) },
  ],
  byModel: [
    { key: "haiku", total: totals(3, 7.6, 0, 0) },
    { key: "gpt-5.5", total: totals(1, 0, 2.5, 0) },
    { key: "", total: totals(1, 0, 0, 1) },
  ],
  byStep: [
    { key: "research", total: totals(3, 5.6, 0, 1) },
    { key: "implement", total: totals(1, 0, 2.5, 0) },
  ],
};

const breakdown = () => screen.getByRole("table", { name: /Task cost by/ });
const firstCells = () =>
  within(breakdown())
    .getAllByRole("row")
    .slice(1)
    .map((row) =>
      within(row).getAllByRole("cell")[0].textContent?.replace(/^[▸▾]/, ""),
    );
const costs = () =>
  within(breakdown())
    .getAllByRole("row")
    .slice(1)
    .map((row) => within(row).getAllByRole("cell").at(-1)?.textContent);

describe("CostTab", () => {
  // A block body: a function returned from beforeEach is run as its cleanup.
  beforeEach(() => {
    getTaskCost.mockResolvedValue(cost);
  });
  afterEach(() => {
    // Unmount before the mocks reset, so an effect still pending when a test
    // ends never calls a reset mock or counts toward the next test.
    cleanup();
    getTaskCost.mockReset();
  });

  it("shows the task's totals and planning apart from its runs, with each figure's markers", async () => {
    render(<CostTab run={run} />);
    const total = await screen.findByText("Task cost");
    // An estimate in the total marks it; the call nothing priced is counted.
    expect(total.previousSibling?.textContent).toBe("≈$0.10 + 1 unpriced");
    expect(
      screen.getByText("Input read from cache").previousSibling?.textContent,
    ).toBe("75%");
    expect(screen.getByText("Time in calls").previousSibling?.textContent).toBe(
      "2m 29s",
    );
    expect(getTaskCost).toHaveBeenCalledWith("task-1");
    expect(
      screen.getByText(
        /Across 2 runs of this task and its planning: \$0\.08 reported, ≈\$0\.03 estimated, 1 call without a price/,
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Planning", { selector: "b" }).parentElement,
    ).toHaveTextContent("Planning $0.02 · 1 orchestrator call");
    expect(
      screen.getByText(/≈ marks a cost that includes estimates/),
    ).toHaveTextContent(
      "$0.06 of it was reported under a subscription login: what those calls would cost through the API, not a bill.",
    );
    // A bar per run, split by step on one scale.
    expect(
      within(screen.getByRole("group", { name: "Run 1 by step" }))
        .getAllByRole("img")
        .map((segment) => segment.getAttribute("aria-label")),
    ).toEqual(["research: $0.04 + 1 unpriced", "implement: ≈$0.03"]);
    expect(
      within(screen.getByRole("group", { name: "Run 2 by step" })).getAllByRole(
        "img",
      ),
    ).toHaveLength(1);
    fireEvent.focus(
      within(screen.getByRole("group", { name: "Run 2 by step" })).getByRole(
        "img",
      ),
    );
    expect(screen.getByText(/research · 1 call/)).toHaveTextContent(
      "$0.02 research · 1 call",
    );
  });

  it("switches the breakdown between step, agent, runtime, and model", async () => {
    render(<CostTab run={run} />);
    await screen.findByText("Task cost");
    // Steps leave planning out, so it has its own row and the rows add up.
    expect(firstCells()).toEqual([
      "research",
      "implement",
      "Planning",
      "Task total",
    ]);
    expect(costs()).toEqual([
      "$0.06 + 1 unpriced",
      "≈$0.03",
      "$0.02",
      "≈$0.10 + 1 unpriced",
    ]);
    fireEvent.click(screen.getByRole("tab", { name: "Agent" }));
    expect(firstCells()).toEqual([
      "researcher",
      "implementer",
      "orchestrator",
      "reviewer",
      "Task total",
    ]);
    // A group nothing priced is counted, never shown as free.
    expect(costs()[3]).toBe("1 unpriced");
    fireEvent.click(screen.getByRole("tab", { name: "Runtime" }));
    expect(firstCells()).toEqual(["claude", "codex", "copilot", "Task total"]);
    fireEvent.click(screen.getByRole("tab", { name: "Model" }));
    expect(firstCells()).toEqual(["haiku", "gpt-5.5", "default", "Task total"]);
  });

  it("opens a row onto its turns and peer rounds", async () => {
    render(<CostTab run={run} />);
    await screen.findByText("Task cost");
    const research = screen.getByRole("button", { name: /research/ });
    expect(research).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(research);
    expect(research).toHaveAttribute("aria-expanded", "true");
    const calls = within(
      screen.getAllByRole("table", { name: "Calls" }).at(-1)!,
    );
    const rows = calls
      .getAllByRole("row")
      .slice(1)
      .map((row) =>
        within(row)
          .getAllByRole("cell")
          .map((cell) => cell.textContent),
      );
    expect(rows.map((row) => row.slice(0, 2))).toEqual([
      ["Turn 1 · researcher", "Run 1"],
      ["Turn 1 · peer reviewer", "Run 1"],
      ["Turn 1 · researcher", "Run 2"],
    ]);
    expect(rows.map((row) => row.at(-1))).toEqual([
      "$0.04",
      "unknown",
      "$0.02",
    ]);
    expect(calls.getAllByRole("row")[1].lastElementChild).toHaveAttribute(
      "title",
      expect.stringContaining("signed in to a subscription"),
    );
    // Changing the grouping closes what was open.
    fireEvent.click(screen.getByRole("tab", { name: "Agent" }));
    expect(screen.getByRole("button", { name: /researcher/ })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
  });

  it("reads the task again when its totals change, as when another run stores calls", async () => {
    const view = render(<CostTab run={{ ...run, taskCost: cost.total }} />);
    await screen.findByText("Task cost");
    expect(getTaskCost).toHaveBeenCalledTimes(1);
    // This run is finished and sends nothing more, but a rerun of the task
    // stored a call, so the task's totals on the run moved.
    view.rerender(
      <CostTab
        run={{
          ...run,
          taskCost: { ...cost.total, calls: cost.total.calls + 1 },
        }}
      />,
    );
    await waitFor(() => expect(getTaskCost).toHaveBeenCalledTimes(2));
  });

  it("says so when nothing has been recorded", async () => {
    getTaskCost.mockResolvedValue({
      ...cost,
      total: totals(0, 0, 0, 0),
      runs: [],
      byStep: [],
      byAgent: [],
      byRuntime: [],
      byModel: [],
      planning: { total: totals(0, 0, 0, 0), calls: [] },
    });
    render(<CostTab run={run} />);
    expect(
      await screen.findByText(
        "No model call has been recorded for this task yet.",
      ),
    ).toBeInTheDocument();
  });
});

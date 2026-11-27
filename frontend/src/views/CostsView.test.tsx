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
  PeriodCost,
  Project,
  TaskCostSummary,
} from "../types";
import { CostsView } from "./CostsView";

const getTaskCosts = vi.hoisted(() => vi.fn());
const saveCSV = vi.hoisted(() => vi.fn());
vi.mock("../bridge", () => ({ getTaskCosts, saveCSV }));

const projects: Project[] = [
  {
    id: "project",
    name: "Inkuna",
    repository: "/src/inkuna",
    defaultRuntime: "claude",
    createdAt: "2026-09-01T00:00:00Z",
  },
  {
    id: "other",
    name: "Website",
    repository: "/src/site",
    defaultRuntime: "codex",
    createdAt: "2026-09-01T00:00:00Z",
  },
];

const totals = (fields: Partial<CallTotals>): CallTotals => ({
  usage: {},
  cost: { reportedCents: 0, estimatedCents: 0, unknownCalls: 0 },
  bestCents: 0,
  subscriptionCents: 0,
  calls: 1,
  durationMs: 0,
  ...fields,
});

const task = (fields: Partial<TaskCostSummary>): TaskCostSummary => ({
  taskId: "task-1",
  projectId: "project",
  objective: "Add a cost view",
  kind: "implementation",
  state: "approved",
  latestRunId: "run-2",
  lastCallAt: "2026-10-19T09:40:00Z",
  total: totals({}),
  ...fields,
});

// Go's answer, the most expensive task first.
const period: PeriodCost = {
  tasks: [
    task({
      total: totals({
        usage: {
          inputTokens: 331,
          cacheWriteTokens: 12000,
          cacheReadTokens: 1000,
          outputTokens: 620,
        },
        cost: { reportedCents: 7.6, estimatedCents: 2.5, unknownCalls: 1 },
        bestCents: 10.1,
        subscriptionCents: 6,
        calls: 5,
        durationMs: 149000,
      }),
    }),
    task({
      taskId: "task-2",
      projectId: "other",
      objective: "Fix the footer, again",
      state: "planned",
      latestRunId: undefined,
      lastCallAt: "2026-10-02T09:01:00Z",
      total: totals({
        usage: { inputTokens: 5, outputTokens: 5 },
        cost: { reportedCents: 0, estimatedCents: 0, unknownCalls: 1 },
      }),
    }),
  ],
  total: totals({
    usage: {
      inputTokens: 336,
      cacheWriteTokens: 12000,
      cacheReadTokens: 1000,
      outputTokens: 625,
    },
    cost: { reportedCents: 7.6, estimatedCents: 2.5, unknownCalls: 2 },
    bestCents: 10.1,
    subscriptionCents: 6,
    calls: 6,
    durationMs: 149000,
  }),
};

const rows = () =>
  within(screen.getByRole("table"))
    .getAllByRole("row")
    .slice(1)
    .map((row) =>
      within(row)
        .getAllByRole("cell")
        .map((cell) => cell.textContent),
    );

const localDayStart = (day: string) =>
  new Date(`${day}T00:00:00`).toISOString();

describe("CostsView", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(2026, 9, 19, 15, 0, 0));
    getTaskCosts.mockResolvedValue(period);
  });
  afterEach(() => {
    // Unmount before the mocks reset, so an effect still pending when a test
    // ends never calls a reset mock or counts toward the next test.
    cleanup();
    vi.useRealTimers();
    getTaskCosts.mockReset();
    saveCSV.mockReset();
  });

  it("lists Go's rows for the last 30 days with a total row and each figure's markers", async () => {
    render(<CostsView projects={projects} onOpenRun={() => {}} />);
    await screen.findByText("Add a cost view");
    // Every project, from the start of the day 29 days ago to the end of today.
    expect(getTaskCosts).toHaveBeenCalledWith(
      "",
      localDayStart("2026-09-20"),
      localDayStart("2026-10-20"),
    );
    const [first, second, total] = rows();
    // The cost comes right after the task, so it stays in view when the
    // table scrolls.
    expect(first.slice(0, 5)).toEqual([
      "Add a cost viewtask-1",
      "≈$0.10 + 1 unpriced",
      "5",
      "13.3k",
      "620",
    ]);
    expect(first.slice(6, 8)).toEqual(["Inkuna", "approved"]);
    // A task nothing priced shows as unpriced, never as free.
    expect(second[1]).toBe("1 unpriced");
    expect(second.slice(6, 8)).toEqual(["Website", "planned"]);
    expect(total.slice(0, 5)).toEqual([
      "Total · 2 tasks",
      "≈$0.10 + 2 unpriced",
      "6",
      "13.3k",
      "625",
    ]);
    expect(
      screen.getByText(/≈ marks a cost that includes estimates/),
    ).toHaveTextContent(
      "Of the total, $0.06 of it was reported under a subscription login: what those calls would cost through the API, not a bill.",
    );
  });

  it("asks Go again for another project or another range", async () => {
    render(<CostsView projects={projects} onOpenRun={() => {}} />);
    await screen.findByText("Add a cost view");
    fireEvent.change(screen.getByRole("combobox", { name: "Project" }), {
      target: { value: "other" },
    });
    await waitFor(() =>
      expect(getTaskCosts).toHaveBeenLastCalledWith(
        "other",
        localDayStart("2026-09-20"),
        localDayStart("2026-10-20"),
      ),
    );
    fireEvent.change(screen.getByLabelText("From"), {
      target: { value: "2026-10-01" },
    });
    fireEvent.change(screen.getByLabelText("To"), {
      target: { value: "2026-10-05" },
    });
    await waitFor(() =>
      expect(getTaskCosts).toHaveBeenLastCalledWith(
        "other",
        localDayStart("2026-10-01"),
        localDayStart("2026-10-06"),
      ),
    );
  });

  it("exports the listed rows as CSV", async () => {
    saveCSV.mockResolvedValue("/Users/me/inkuna-costs.csv");
    render(<CostsView projects={projects} onOpenRun={() => {}} />);
    await screen.findByText("Add a cost view");
    fireEvent.click(screen.getByRole("button", { name: "Export CSV" }));
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Saved 2 rows to /Users/me/inkuna-costs.csv",
    );
    expect(saveCSV).toHaveBeenCalledWith(
      "inkuna-costs-2026-09-20-to-2026-10-19.csv",
      [
        "task,project,objective,kind,state,last_call,calls,input_tokens,cache_write_tokens,cache_read_tokens,output_tokens,duration_ms,reported_usd,estimated_usd,unknown_calls,best_usd,subscription_usd",
        "task-1,Inkuna,Add a cost view,implementation,approved,2026-10-19T09:40:00Z,5,331,12000,1000,620,149000,0.076000,0.025000,1,0.101000,0.060000",
        'task-2,Website,"Fix the footer, again",implementation,planned,2026-10-02T09:01:00Z,1,5,0,0,5,0,0.000000,0.000000,1,0.000000,0.000000',
        "",
      ].join("\n"),
    );
  });

  it("says nothing was saved when the export is cancelled, and opens a task's latest run", async () => {
    saveCSV.mockResolvedValue("");
    const onOpenRun = vi.fn();
    render(<CostsView projects={projects} onOpenRun={onOpenRun} />);
    await screen.findByText("Add a cost view");
    fireEvent.click(screen.getByRole("button", { name: "Export CSV" }));
    await waitFor(() => expect(saveCSV).toHaveBeenCalled());
    expect(screen.queryByRole("status")).toBeNull();
    // Only a task that has run has a run to open.
    expect(
      screen.getAllByRole("button", { name: /Open the latest run/ }),
    ).toHaveLength(1);
    fireEvent.click(
      screen.getByRole("button", {
        name: "Open the latest run of Add a cost view",
      }),
    );
    expect(onOpenRun).toHaveBeenCalledWith("run-2");
  });

  it("never shows or exports rows read for other filters", async () => {
    saveCSV.mockResolvedValue("/Users/me/inkuna-costs.csv");
    render(<CostsView projects={projects} onOpenRun={() => {}} />);
    await screen.findByText("Add a cost view");
    fireEvent.click(screen.getByRole("button", { name: "Export CSV" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Saved 2 rows");
    // Reading the new project fails: the old project's rows, total, and
    // export notice must not stay under it, and there is nothing to export.
    getTaskCosts.mockRejectedValueOnce("the database is locked");
    fireEvent.change(screen.getByRole("combobox", { name: "Project" }), {
      target: { value: "other" },
    });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "the database is locked",
    );
    expect(screen.queryByText("Add a cost view")).toBeNull();
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.queryByRole("status")).toBeNull();
    expect(screen.getByRole("button", { name: "Export CSV" })).toBeDisabled();
    // Back on the filters the rows were read for, they show again once read.
    fireEvent.change(screen.getByRole("combobox", { name: "Project" }), {
      target: { value: "" },
    });
    expect(await screen.findByText("Add a cost view")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Export CSV" })).toBeEnabled();
  });

  it("shows Go's reason for a range it refuses, and an empty range as empty", async () => {
    getTaskCosts.mockRejectedValueOnce(
      new Error("the period must end after it starts"),
    );
    getTaskCosts.mockResolvedValue({ tasks: [], total: totals({ calls: 0 }) });
    render(<CostsView projects={projects} onOpenRun={() => {}} />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "the period must end after it starts",
    );
    fireEvent.change(screen.getByLabelText("From"), {
      target: { value: "2026-10-01" },
    });
    expect(
      await screen.findByText("No costs in this range"),
    ).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByRole("button", { name: "Export CSV" })).toBeDisabled();
  });
});

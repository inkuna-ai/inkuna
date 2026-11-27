import type { CallTotals, TaskCost, TaskCostSummary } from "../types";
import {
  barSegments,
  bestCostLabel,
  callTime,
  costBreakdown,
  costsCSV,
  stepColors,
  subscriptionNote,
} from "./cost";

const totals = (fields: Partial<CallTotals> = {}): CallTotals => ({
  usage: {},
  cost: { reportedCents: 0, estimatedCents: 0, unknownCalls: 0 },
  bestCents: 0,
  subscriptionCents: 0,
  calls: 1,
  durationMs: 0,
  ...fields,
});

describe("bestCostLabel", () => {
  it("shows Go's best figure, marks estimates, and counts what nothing priced", () => {
    expect(
      bestCostLabel(
        totals({
          cost: { reportedCents: 40, estimatedCents: 0, unknownCalls: 0 },
          bestCents: 40,
        }),
      ),
    ).toBe("$0.40");
    expect(
      bestCostLabel(
        totals({
          cost: { reportedCents: 40, estimatedCents: 3, unknownCalls: 0 },
          bestCents: 43,
        }),
      ),
    ).toBe("≈$0.43");
    expect(
      bestCostLabel(
        totals({
          cost: { reportedCents: 0, estimatedCents: 0.5, unknownCalls: 2 },
          bestCents: 0.5,
        }),
      ),
    ).toBe("≈$0.0050 + 2 unpriced");
    // Unknown is never shown as free.
    expect(
      bestCostLabel(
        totals({
          cost: { reportedCents: 0, estimatedCents: 0, unknownCalls: 3 },
        }),
      ),
    ).toBe("3 unpriced");
    expect(bestCostLabel(totals({ calls: 0 }))).toBe("—");
  });

  it("notes the part a subscription login reported as API-equivalent", () => {
    const total = totals({
      cost: { reportedCents: 40, estimatedCents: 2.5, unknownCalls: 1 },
      bestCents: 42.5,
      subscriptionCents: 30,
    });
    expect(subscriptionNote(total)).toBe(
      "$0.30 of it was reported under a subscription login: what those calls would cost through the API, not a bill",
    );
    expect(costBreakdown(total)).toBe(
      "$0.40 reported, ≈$0.03 estimated, 1 call without a price. $0.30 of it was reported under a subscription login: what those calls would cost through the API, not a bill",
    );
    expect(subscriptionNote(totals())).toBe("");
  });

  it("shows time in calls to the second", () => {
    expect(callTime(45_400)).toBe("45s");
    expect(callTime(149_000)).toBe("2m 29s");
    expect(callTime(3_840_000)).toBe("1h 4m");
  });
});

describe("costsCSV", () => {
  it("writes the listed rows in order, with inkunactl's columns", () => {
    const task = (fields: Partial<TaskCostSummary>): TaskCostSummary => ({
      taskId: "task-1",
      projectId: "project",
      objective: "Add a cost view",
      kind: "implementation",
      state: "approved",
      lastCallAt: "2026-10-19T09:40:00Z",
      total: totals(),
      ...fields,
    });
    const csv = costsCSV(
      [
        task({
          total: totals({
            usage: {
              inputTokens: 331,
              cacheWriteTokens: 12000,
              cacheReadTokens: 1000,
              outputTokens: 620,
            },
            cost: { reportedCents: 7.5, estimatedCents: 2.5, unknownCalls: 1 },
            bestCents: 10,
            subscriptionCents: 6,
            calls: 5,
            durationMs: 149000,
          }),
        }),
        // Commas, quotes, and line breaks stay inside their field.
        task({
          taskId: "task-2",
          objective: 'Fix "the list", then\nship',
          projectId: "other",
        }),
      ],
      (id) => (id === "project" ? "Inkuna" : id),
    );
    expect(csv).toBe(
      [
        "task,project,objective,kind,state,last_call,calls,input_tokens,cache_write_tokens,cache_read_tokens,output_tokens,duration_ms,reported_usd,estimated_usd,unknown_calls,best_usd,subscription_usd",
        "task-1,Inkuna,Add a cost view,implementation,approved,2026-10-19T09:40:00Z,5,331,12000,1000,620,149000,0.075000,0.025000,1,0.100000,0.060000",
        'task-2,other,"Fix ""the list"", then\nship",implementation,approved,2026-10-19T09:40:00Z,1,0,0,0,0,0,0.000000,0.000000,0,0.000000,0.000000',
        "",
      ].join("\n"),
    );
  });
});

describe("costsCSV formulas", () => {
  it("keeps text that a spreadsheet would run as a formula as text, and leaves numbers alone", () => {
    const csv = costsCSV(
      [
        {
          taskId: "task-1",
          projectId: "project",
          objective: '=HYPERLINK("https://example.com")',
          kind: "implementation",
          state: "completed",
          lastCallAt: "2026-10-19T09:40:00Z",
          total: totals({ calls: 2 }),
        },
      ],
      () => "@team",
    );
    expect(csv.split("\n")[1]).toBe(
      `task-1,'@team,"'=HYPERLINK(""https://example.com"")",implementation,completed,2026-10-19T09:40:00Z,2,0,0,0,0,0,0.000000,0.000000,0,0.000000,0.000000`,
    );
    for (const lead of ["+1", "-1", "\tcmd", "\rcmd", "\n=1+1", "＝1+1"])
      expect(
        costsCSV(
          [
            {
              taskId: "t",
              projectId: "p",
              objective: lead,
              kind: "k",
              state: "s",
              lastCallAt: "",
              total: totals(),
            },
          ],
          () => "p",
        ),
      ).toContain(`'${lead}`);
  });
});

describe("barSegments", () => {
  it("places each step at its exact share, so many small steps are not inflated", () => {
    // Ten steps of 1% each draw a bar a tenth as long as the largest run.
    const segments = barSegments(Array(10).fill(1), 100);
    expect(segments.map((segment) => segment.width)).toEqual(Array(10).fill(1));
    expect(segments.map((segment) => segment.start)).toEqual([
      0, 1, 2, 3, 4, 5, 6, 7, 8, 9,
    ]);
    expect(barSegments([3, 1], 4)).toEqual([
      { start: 0, width: 75 },
      { start: 75, width: 25 },
    ]);
    // A task nothing was spent on draws nothing.
    expect(barSegments([0, 0], 0)).toEqual([
      { start: 0, width: 0 },
      { start: 0, width: 0 },
    ]);
  });
});

describe("stepColors", () => {
  it("keeps a step's color across runs and folds steps past eight into the neutral", () => {
    const step = (stepId: string) => ({
      stepId,
      agent: "researcher",
      runtime: "claude",
      model: "haiku",
      startedAt: "2026-10-19T09:00:00Z",
      total: totals(),
      calls: [],
    });
    const run = (id: string, steps: string[]) => ({
      executionId: id,
      state: "completed",
      startedAt: "2026-10-19T09:00:00Z",
      finishedAt: "2026-10-19T09:10:00Z",
      total: totals(),
      steps: steps.map(step),
    });
    const cost: TaskCost = {
      taskId: "task-1",
      total: totals(),
      planning: { total: totals({ calls: 0 }), calls: [] },
      runs: [
        run("run-1", ["research", "implement"]),
        // A rerun that skipped research and added more steps.
        run("run-2", [
          "implement",
          "review",
          "s4",
          "s5",
          "s6",
          "s7",
          "s8",
          "s9",
        ]),
      ],
      byAgent: [],
      byRuntime: [],
      byModel: [],
      byStep: [],
    };
    const colors = stepColors(cost);
    expect(colors.get("research")).toBe("var(--color-series-1)");
    expect(colors.get("implement")).toBe("var(--color-series-2)");
    expect(colors.get("review")).toBe("var(--color-series-3)");
    expect(colors.get("s8")).toBe("var(--color-series-8)");
    expect(colors.get("s9")).toBe("var(--color-series-other)");
  });
});

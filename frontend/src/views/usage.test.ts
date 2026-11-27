import {
  approxTokens,
  cacheReadShare,
  callLabel,
  compactTokens,
  costLabel,
  costSource,
  dollars,
  sectionLabel,
  totalCostLabel,
  totalInput,
  truncatedPartLabel,
  usageLine,
} from "./usage";
import type { UsageRecord } from "../types";

describe("usage formatting", () => {
  it("totals input across fresh, cache-write, and cache-read tokens", () => {
    const usage = {
      inputTokens: 10,
      cacheWriteTokens: 2000,
      cacheReadTokens: 6000,
      outputTokens: 450,
    };
    expect(totalInput(usage)).toBe(8010);
    expect(cacheReadShare(usage)).toBe(75);
    expect(cacheReadShare({ outputTokens: 4 })).toBeNull();
    expect(usageLine(usage)).toBe("8k in · 75% cached · 450 out");
    expect(usageLine({ outputTokens: 4 })).toBe("0 in · 4 out");
  });

  it("compacts counts", () => {
    expect(compactTokens(950)).toBe("950");
    expect(compactTokens(12_345)).toBe("12.3k");
    expect(compactTokens(123_456)).toBe("123k");
    expect(compactTokens(4_100_000)).toBe("4.1M");
    expect(approxTokens(4001)).toBe(1000);
  });

  it("says where each cost came from and never prices the unknown at zero", () => {
    expect(dollars(125)).toBe("$1.25");
    // A call under a cent keeps hundredths of one.
    expect(dollars(0.604093)).toBe("$0.0060");
    expect(dollars(0)).toBe("$0");
    expect(dollars(0.004)).toBe("<$0.0001");
    expect(costLabel({ cents: 46.1, source: "reported" })).toBe("$0.46");
    expect(costLabel({ cents: 33.8, source: "estimated" })).toBe("≈$0.34");
    expect(costLabel({ cents: 0, source: "unknown" })).toBe("unknown");
    // A call still running has no cost yet.
    expect(costLabel(undefined)).toBe("unknown");
    expect(
      totalCostLabel({ reportedCents: 40, estimatedCents: 0, unknownCalls: 0 }),
    ).toBe("$0.40");
    expect(
      totalCostLabel({ reportedCents: 40, estimatedCents: 3, unknownCalls: 2 }),
    ).toBe("≈$0.43 + 2 unpriced");
    expect(
      totalCostLabel({ reportedCents: 0, estimatedCents: 0, unknownCalls: 3 }),
    ).toBe("3 unpriced");
  });

  it("explains a cost's source", () => {
    const base: UsageRecord = {
      id: 1,
      taskId: "task-1",
      stepId: "worker",
      callId: "call",
      turn: 1,
      kind: "worker",
      agent: "implementer",
      runtime: "claude",
      usage: {},
      promptBytes: 0,
      outcome: "completed",
      startedAt: "",
      finishedAt: "",
    };
    expect(
      costSource({
        ...base,
        authMode: "subscription",
        cost: { cents: 46, source: "reported", estimatedCents: 45 },
      }),
    ).toBe(
      "Reported by claude; the pricing table estimates $0.45; signed in to a subscription, so this is what the call would cost through the API, not a bill",
    );
    expect(
      costSource({
        ...base,
        runtime: "copilot",
        usage: { premiumRequests: 0.33 },
        cost: { cents: 1.32, source: "estimated" },
      }),
    ).toBe("Estimated from 0.33 premium requests at the price in Settings");
    expect(
      costSource({
        ...base,
        runtime: "codex",
        cost: { cents: 3, source: "estimated", pricingVersion: 1 },
      }),
    ).toBe("Estimated with pricing table v1");
    expect(
      costSource({ ...base, cost: { cents: 0, source: "unknown" } }),
    ).toMatch(/^Nothing priced this call/);
  });

  it("names sections and calls", () => {
    expect(
      sectionLabel({ section: "skill", name: "conventions", bytes: 1 }),
    ).toBe("Skill · conventions");
    expect(
      sectionLabel({
        section: "instructions",
        name: "agentInstructions",
        bytes: 1,
      }),
    ).toBe("Agent instructions · envelope copy");
    expect(sectionLabel({ section: "docs", bytes: 1 })).toBe("Doc index");
    expect(sectionLabel({ section: "outputs", bytes: 1 })).toBe(
      "Dependency outputs",
    );
    expect(sectionLabel({ section: "future", bytes: 1 })).toBe("future");
    expect(sectionLabel({ section: "skillIndex", bytes: 1 })).toBe(
      "Skills to open when needed",
    );
    expect(truncatedPartLabel("skill:conventions")).toBe("Skill · conventions");
    expect(truncatedPartLabel("humanResponses")).toBe("Human responses");
    expect(truncatedPartLabel("acceptanceCriteria")).toBe(
      "Acceptance criteria",
    );
    const call = {
      turn: 2,
      agent: "reviewer",
      stepId: "review",
    } as UsageRecord;
    expect(callLabel({ ...call, kind: "worker" })).toBe("Turn 2 · reviewer");
    expect(callLabel({ ...call, kind: "peer" })).toBe("Turn 2 · peer reviewer");
    // Each round's turns count from 1, so a later round's calls say which.
    expect(callLabel({ ...call, kind: "worker", round: 1 })).toBe(
      "Turn 2 · reviewer",
    );
    expect(callLabel({ ...call, kind: "worker", round: 3 })).toBe(
      "Round 3 · Turn 2 · reviewer",
    );
    expect(callLabel({ ...call, kind: "peer", round: 2 })).toBe(
      "Round 2 · Turn 2 · peer reviewer",
    );
    expect(
      callLabel({
        ...call,
        kind: "orchestrator",
        stepId: "staffing",
        agent: "orchestrator",
      }),
    ).toBe("staffing · orchestrator");
  });
});

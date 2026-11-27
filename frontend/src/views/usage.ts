import type {
  Cost,
  CostTotal,
  PromptSection,
  Usage,
  UsageRecord,
} from "../types";

/** Every prompt token the model read: fresh, written to cache, and from cache. */
export function totalInput(usage: Usage): number {
  return (
    (usage.inputTokens ?? 0) +
    (usage.cacheWriteTokens ?? 0) +
    (usage.cacheReadTokens ?? 0)
  );
}

/** The whole percent of input served from the prompt cache, or null with none. */
export function cacheReadShare(usage: Usage): number | null {
  const input = totalInput(usage);
  if (input === 0) return null;
  return Math.round((100 * (usage.cacheReadTokens ?? 0)) / input);
}

/** A token count at a glance: 950, 12.3k, 4.1M. */
export function compactTokens(value = 0): string {
  if (value < 1000) return String(value);
  if (value < 1_000_000) return `${trim(value / 1000)}k`;
  return `${trim(value / 1_000_000)}M`;
}

function trim(value: number) {
  return value >= 100 ? value.toFixed(0) : value.toFixed(1).replace(/\.0$/, "");
}

/** An exact count with thousands separators, for tables meant to be compared. */
export function exact(value = 0): string {
  return value.toLocaleString("en-US");
}

/**
 * Dollars from cents, to the cent; a cost under a cent, which one call often
 * is, keeps hundredths of a cent so it does not read as nothing, and one
 * smaller still says so.
 */
export function dollars(cents: number): string {
  if (cents === 0) return "$0";
  if (cents > 0 && cents < 0.01) return "<$0.0001";
  return `$${(cents / 100).toFixed(Math.abs(cents) < 1 ? 4 : 2)}`;
}

/**
 * A call's cost as Go priced it: a reported figure as is, an estimate marked
 * with ≈, and a call nothing priced as unknown, never as zero.
 */
export function costLabel(cost?: Cost): string {
  switch (cost?.source) {
    case "reported":
      return dollars(cost.cents);
    case "estimated":
      return `≈${dollars(cost.cents)}`;
    default:
      return "unknown";
  }
}

/** Where a call's cost came from, for the hint on its figure. */
export function costSource(call: UsageRecord): string {
  const cost = call.cost;
  switch (cost?.source) {
    case "reported": {
      const parts = [`Reported by ${call.runtime}`];
      if (cost.estimatedCents)
        parts.push(
          `the pricing table estimates ${dollars(cost.estimatedCents)}`,
        );
      if (call.authMode === "subscription")
        parts.push(
          "signed in to a subscription, so this is what the call would cost through the API, not a bill",
        );
      return parts.join("; ");
    }
    case "estimated":
      if (call.usage.premiumRequests)
        return `Estimated from ${call.usage.premiumRequests} premium requests at the price in Settings`;
      return `Estimated with pricing table v${cost.pricingVersion ?? 0}`;
    default:
      return "Nothing priced this call: add the model's price, or a Copilot premium request price, under Model prices in Settings";
  }
}

/**
 * A total of each call's best figure, marked as an estimate when any part
 * is one, with the calls nothing priced counted beside it rather than
 * valued at zero.
 */
export function totalCostLabel(total?: CostTotal): string {
  if (!total) return "unknown";
  const priced = total.reportedCents + total.estimatedCents;
  const unpriced = `${total.unknownCalls} unpriced`;
  if (priced === 0 && total.unknownCalls > 0) return unpriced;
  const label = `${total.estimatedCents > 0 ? "≈" : ""}${dollars(priced)}`;
  return total.unknownCalls > 0 ? `${label} + ${unpriced}` : label;
}

/** Tokens a prompt of this many bytes is roughly worth, at four bytes each. */
export function approxTokens(bytes: number): number {
  return Math.round(bytes / 4);
}

const sectionNames: Record<string, string> = {
  schema: "Output schema",
  instructions: "Agent instructions",
  protocol: "Inkuna protocol",
  skill: "Skill",
  skillIndex: "Skills to open when needed",
  objective: "Objective",
  stage: "Stage",
  constraints: "Constraints",
  // Cut with the parts no other section claims; named in metadata.truncated.
  acceptanceCriteria: "Acceptance criteria",
  dependencies: "Dependency summaries",
  outputs: "Dependency outputs",
  decisions: "Decisions",
  risks: "Risks",
  humanResponses: "Human responses",
  peerMessages: "Peer messages",
  previousTurn: "Previous turn",
  feedback: "Feedback",
  previousRound: "Previous round",
  resumed: "Resumed",
  docs: "Doc index",
  // Acceptance criteria, allowed paths, and commands; in calls made before
  // the prompt was markdown, the rest of the envelope's JSON.
  envelope: "Other envelope fields",
  // An orchestrator prompt's request, field by field.
  task: "Task",
  repository: "Repository profile",
  stages: "Stages",
  // Agent briefs, and in calls made before them, A2A cards.
  agents: "Agents",
  skills: "Skill briefs",
  missing: "Missing keys",
  evidence: "Branch evidence",
  allowed: "Allowed values",
  request: "Other request fields",
};

/**
 * How a part named in an envelope's metadata.truncated reads: the section
 * name, with a skill's id after it ("skill:conventions").
 */
export function truncatedPartLabel(part: string): string {
  const [section, ...name] = part.split(":");
  return sectionLabel({ section, name: name.join(":") || undefined, bytes: 0 });
}

/** How the breakdown names a section. */
export function sectionLabel(section: PromptSection): string {
  const name = sectionNames[section.section] ?? section.section;
  // Calls made before the prompt was markdown sent the instructions twice.
  if (section.name === "agentInstructions") return `${name} · envelope copy`;
  return section.name ? `${name} · ${section.name}` : name;
}

/**
 * How a call is named where a step's calls are listed. A call after the
 * first round names its round, since each round's turns count from 1.
 */
export function callLabel(call: UsageRecord): string {
  if (call.kind === "orchestrator") return `${call.stepId} · ${call.agent}`;
  const round = (call.round ?? 1) > 1 ? `Round ${call.round} · ` : "";
  if (call.kind === "peer")
    return `${round}Turn ${call.turn} · peer ${call.agent}`;
  return `${round}Turn ${call.turn} · ${call.agent}`;
}

/** "12.3k in · 75% cached · 400 out" for one call or a sum. */
export function usageLine(usage: Usage): string {
  const share = cacheReadShare(usage);
  return [
    `${compactTokens(totalInput(usage))} in`,
    share === null ? "" : `${share}% cached`,
    `${compactTokens(usage.outputTokens)} out`,
  ]
    .filter(Boolean)
    .join(" · ");
}
